package requisitions

import (
	"context"
	"fmt"
	"regexp"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
)

// The requisition's lifecycle in one table (docs/design.md, "申请的状态"; rules PR01-PR06). Every command asks
// Lifecycle.Decide what it may do in the requisition's state, and every change of the state goes through Transition.
// A new state or command is a new phase or command here: the unit tests fail until each of its cells is decided and
// docs/design.md says the same.

const (
	Draft     = "draft"
	Submitted = "submitted"
	Approved  = "approved"
	Rejected  = "rejected"
	Cancelled = "cancelled"
	Closed    = "closed"
)

const (
	Edit    = "edit"
	Submit  = "submit"
	Approve = "approve"
	Reject  = "reject"
	Cancel  = "cancel"
	Order   = "order"  // order lines are made from its lines (PR06)
	Close   = "close"  // everything was ordered
	Reopen  = "reopen" // an order gave back what it took
)

const (
	// GuardPolicy: a legal entity whose policy needs no approval has a submitted requisition approved at once.
	GuardPolicy = "the policy needs no approval: approved at once"
	// GuardFlow: approving and rejecting are decisions on the requisition's approval task: the flow kernel checks the
	// candidate, the submitter's person, the operation and the qualification, and FlowEnded moves the requisition.
	GuardFlow = "a decision on the approval task"
	// GuardNothingOrdered: an approved requisition is cancelled only when nothing was ordered from it.
	GuardNothingOrdered = "nothing was ordered from it"
	// GuardAllOrdered: every line was ordered whole; GuardNotAllOrdered: a line has some left again.
	GuardAllOrdered    = "every line ordered whole"
	GuardNotAllOrdered = "a line has some left"
)

const notApproved = "order lines are made only from an approved requisition; this one is {state}"

const notDraft = "only a draft requisition changes; this one is {state}"

func refuse(rule, words string) core.Decision { return core.RefuseCell(rule, words) }

// Lifecycle is the table.
var Lifecycle = core.Table{
	Rule:     regexp.MustCompile(`^PR\d\d$`),
	Phases:   []string{Draft, Submitted, Approved, Rejected, Cancelled, Closed},
	Commands: []string{Edit, Submit, Approve, Reject, Cancel, Order, Close, Reopen},
	Cells: map[string]map[string]core.Decision{
		Edit: {
			Draft:     core.ProceedCell("PR01"),
			Submitted: refuse("PR01", notDraft),
			Approved:  refuse("PR01", notDraft),
			Rejected:  refuse("PR04", "a rejected requisition does not change: make a new one"),
			Cancelled: refuse("PR01", notDraft),
			Closed:    refuse("PR01", notDraft),
		},
		Submit: {
			Draft:     {Rule: "PR02", Effect: core.Move, To: Submitted, Op: core.OpReqEdit, Guard: GuardPolicy},
			Submitted: refuse("PR02", "only a draft requisition is submitted; this one is {state}"),
			Approved:  refuse("PR02", "only a draft requisition is submitted; this one is {state}"),
			Rejected:  refuse("PR04", "a rejected requisition is not submitted again: make a new one"),
			Cancelled: refuse("PR02", "only a draft requisition is submitted; this one is {state}"),
			Closed:    refuse("PR02", "only a draft requisition is submitted; this one is {state}"),
		},
		Approve: {
			Draft:     refuse("PR03", "only a submitted requisition is approved; this one is {state}"),
			Submitted: {Rule: "PR03", Effect: core.Move, To: Approved, Op: core.OpReqApprove, Guard: GuardFlow},
			Approved:  core.IgnoreCell("PR03"),
			Rejected:  refuse("PR03", "only a submitted requisition is approved; this one is {state}"),
			Cancelled: refuse("PR03", "only a submitted requisition is approved; this one is {state}"),
			Closed:    refuse("PR03", "only a submitted requisition is approved; this one is {state}"),
		},
		Reject: {
			Draft:     refuse("PR04", "only a submitted requisition is rejected; this one is {state}"),
			Submitted: {Rule: "PR04", Effect: core.Move, To: Rejected, Op: core.OpReqApprove, Guard: GuardFlow},
			Approved:  refuse("PR04", "only a submitted requisition is rejected; this one is {state}"),
			Rejected:  core.IgnoreCell("PR04"),
			Cancelled: refuse("PR04", "only a submitted requisition is rejected; this one is {state}"),
			Closed:    refuse("PR04", "only a submitted requisition is rejected; this one is {state}"),
		},
		Cancel: {
			Draft:     core.MoveCell("PR05", Cancelled, core.OpReqEdit),
			Submitted: core.MoveCell("PR05", Cancelled, core.OpReqEdit),
			Approved:  {Rule: "PR05", Effect: core.Move, To: Cancelled, Op: core.OpReqEdit, Guard: GuardNothingOrdered},
			Rejected:  refuse("PR05", "a rejected requisition is not cancelled; it is already ended"),
			Cancelled: core.IgnoreCell("PR05"),
			Closed:    refuse("PR05", "a closed requisition is not cancelled: everything was ordered"),
		},
		Order: {
			Draft:     refuse("PR06", notApproved),
			Submitted: refuse("PR06", notApproved),
			Approved:  core.ProceedCell("PR06"),
			Rejected:  refuse("PR06", notApproved),
			Cancelled: refuse("PR06", notApproved),
			Closed:    refuse("PR06", "everything of the requisition was ordered"),
		},
		Close: {
			Draft:     core.IgnoreCell("PR06"),
			Submitted: core.IgnoreCell("PR06"),
			Approved:  {Rule: "PR06", Effect: core.Move, To: Closed, Guard: GuardAllOrdered},
			Rejected:  core.IgnoreCell("PR06"),
			Cancelled: core.IgnoreCell("PR06"),
			Closed:    core.IgnoreCell("PR06"),
		},
		Reopen: {
			Draft:     core.IgnoreCell("PR06"),
			Submitted: core.IgnoreCell("PR06"),
			Approved:  core.IgnoreCell("PR06"),
			Rejected:  core.IgnoreCell("PR06"),
			Cancelled: core.IgnoreCell("PR06"),
			Closed:    {Rule: "PR06", Effect: core.Move, To: Approved, Guard: GuardNotAllOrdered},
		},
	},
}

// Set is what a transition writes beside the state.
type Set struct {
	Reason      *string // the rejection's reason; nil clears it
	SubmittedBy string  // who submitted, "" keeps it
}

// Transition is the one writer of the requisition's state: d is a Move decided for r, read under its lock in tx. It
// publishes the requisition's state and audits the change with the cell's operation.
func Transition(m *core.Base, ctx context.Context, tx db.Tx, r Requisition, d core.Decision, set Set, actor string) (Requisition, error) {
	if d.Effect != core.Move {
		return r, fmt.Errorf("requisitions: transition of %s on a %s decision", r.ID, d.Effect)
	}
	var by *string
	if set.SubmittedBy != "" {
		by = &set.SubmittedBy
	}
	tag, err := tx.Exec(ctx, `UPDATE requisition SET state = $2, reason = $3, submitted_by = coalesce($4, submitted_by),
		version = version + 1, updated_at = now() WHERE id = $1 AND state = $5`, r.ID, d.To, set.Reason, by, r.State)
	if err != nil {
		return r, err
	}
	if tag.RowsAffected() != 1 {
		return r, fmt.Errorf("requisitions: %s is no longer %s", r.ID, r.State)
	}
	after, err := emit(m, ctx, tx, r.ID)
	if err != nil || d.Op == "" {
		return after, err
	}
	return after, core.Change(m, ctx, tx, actor, d.Op, core.Requisitions, r.ID, after)
}
