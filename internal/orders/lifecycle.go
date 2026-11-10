package orders

import (
	"context"
	"fmt"
	"regexp"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
)

// The order's lifecycle in one table (docs/design.md, "订单的状态"; rules PO01-PO12). Every command on an order asks
// Lifecycle.Decide what it may do in the order's state, and every change of the state goes through Transition.
// Receipts, acceptances and returns are progress on the lines, not states: their commands only ask whether the state
// allows them. A new state or command is a new phase or command here: the unit tests fail until each of its cells is
// decided and docs/design.md says the same.

const (
	Draft     = "draft"
	Submitted = "submitted"
	Approved  = "approved"
	Completed = "completed"
	Closed    = "closed"
	Cancelled = "cancelled"
)

const (
	Edit     = "edit"
	Submit   = "submit"
	Approve  = "approve"
	Reject   = "reject"
	Revise   = "revise"
	Receive  = "receive"
	Accept   = "accept"
	Return   = "return"
	Close    = "close"
	Cancel   = "cancel"
	Complete = "complete" // every line received or accepted whole (net)
)

const (
	// GuardPolicy: a legal entity whose policy needs no approval has a submitted order (and a revision) approved at
	// once, its submitter the approver.
	GuardPolicy = "the policy needs no approval: approved at once"
	// GuardEvidence: an order is submitted only as it was when its evidence was taken (its version).
	GuardEvidence = "the order is as when its evidence was taken"
	// GuardNotSubmitter: the approver is not the one who submitted (by account).
	GuardNotSubmitter = "not by the one who submitted it"
	// GuardProgress: a revision keeps each line at least what it received, is receiving and accepted, and drops only
	// lines without progress.
	GuardProgress = "no line below its progress"
	// GuardNothingReceiving: an order is closed only with no receipt under way.
	GuardNothingReceiving = "no receipt under way"
	// GuardNothingStarted: an approved order is cancelled only with nothing received, receiving or accepted.
	GuardNothingStarted = "nothing received, receiving or accepted"
	// GuardAllDone: an order completes when every line is received or accepted whole, net of returns.
	GuardAllDone = "every line received or accepted whole"
)

const (
	notDraft = "only a draft order changes; this one is {state} (an approved one is revised)"
)

func refuse(rule, words string) core.Decision { return core.RefuseCell(rule, words) }
func onlyApproved(rule, verb string) core.Decision {
	return refuse(rule, "only an approved order is "+verb+"; this one is {state}")
}

// Lifecycle is the table.
var Lifecycle = core.Table{
	Rule:     regexp.MustCompile(`^PO\d\d$`),
	Phases:   []string{Draft, Submitted, Approved, Completed, Closed, Cancelled},
	Commands: []string{Edit, Submit, Approve, Reject, Revise, Receive, Accept, Return, Close, Cancel, Complete},
	Cells: map[string]map[string]core.Decision{
		Edit: {
			Draft:     core.ProceedCell("PO02"),
			Submitted: refuse("PO02", notDraft),
			Approved:  refuse("PO02", notDraft),
			Completed: refuse("PO02", notDraft),
			Closed:    refuse("PO02", notDraft),
			Cancelled: refuse("PO02", notDraft),
		},
		Submit: {
			Draft:     {Rule: "PO03", Effect: core.Move, To: Submitted, Op: core.OpOrderEdit, Guard: GuardPolicy},
			Submitted: refuse("PO03", "only a draft order is submitted; this one is {state}"),
			Approved:  refuse("PO03", "only a draft order is submitted; this one is {state}"),
			Completed: refuse("PO03", "only a draft order is submitted; this one is {state}"),
			Closed:    refuse("PO03", "only a draft order is submitted; this one is {state}"),
			Cancelled: refuse("PO03", "only a draft order is submitted; this one is {state}"),
		},
		Approve: {
			Draft:     refuse("PO04", "only a submitted order is approved; this one is {state}"),
			Submitted: {Rule: "PO04", Effect: core.Move, To: Approved, Op: core.OpOrderApprove, Guard: GuardNotSubmitter},
			Approved:  core.IgnoreCell("PO04"),
			Completed: refuse("PO04", "only a submitted order is approved; this one is {state}"),
			Closed:    refuse("PO04", "only a submitted order is approved; this one is {state}"),
			Cancelled: refuse("PO04", "only a submitted order is approved; this one is {state}"),
		},
		Reject: {
			Draft:     refuse("PO05", "only a submitted order is rejected; this one is {state}"),
			Submitted: core.MoveCell("PO05", Draft, core.OpOrderApprove),
			Approved:  refuse("PO05", "only a submitted order is rejected; this one is {state}"),
			Completed: refuse("PO05", "only a submitted order is rejected; this one is {state}"),
			Closed:    refuse("PO05", "only a submitted order is rejected; this one is {state}"),
			Cancelled: refuse("PO05", "only a submitted order is rejected; this one is {state}"),
		},
		Revise: {
			Draft:     refuse("PO06", "a draft order is changed, not revised"),
			Submitted: onlyApproved("PO06", "revised"),
			Approved:  {Rule: "PO06", Effect: core.Move, To: Submitted, Op: core.OpOrderEdit, Guard: GuardProgress},
			Completed: onlyApproved("PO06", "revised"),
			Closed:    onlyApproved("PO06", "revised"),
			Cancelled: onlyApproved("PO06", "revised"),
		},
		Receive: {
			Draft:     onlyApproved("PO07", "received"),
			Submitted: onlyApproved("PO07", "received"),
			Approved:  core.ProceedCell("PO07"),
			Completed: onlyApproved("PO07", "received"),
			Closed:    onlyApproved("PO07", "received"),
			Cancelled: onlyApproved("PO07", "received"),
		},
		Accept: {
			Draft:     onlyApproved("PO08", "accepted"),
			Submitted: onlyApproved("PO08", "accepted"),
			Approved:  core.ProceedCell("PO08"),
			Completed: onlyApproved("PO08", "accepted"),
			Closed:    onlyApproved("PO08", "accepted"),
			Cancelled: onlyApproved("PO08", "accepted"),
		},
		Return: {
			Draft:     refuse("PO09", "nothing was received on a {state} order"),
			Submitted: refuse("PO09", "nothing was received on a {state} order"),
			Approved:  core.ProceedCell("PO09"),
			Completed: core.ProceedCell("PO09"),
			Closed:    core.ProceedCell("PO09"),
			Cancelled: refuse("PO09", "nothing was received on a {state} order"),
		},
		Close: {
			Draft:     onlyApproved("PO10", "closed"),
			Submitted: onlyApproved("PO10", "closed"),
			Approved:  {Rule: "PO10", Effect: core.Move, To: Closed, Op: core.OpOrderEdit, Guard: GuardNothingReceiving},
			Completed: core.IgnoreCell("PO10"),
			Closed:    core.IgnoreCell("PO10"),
			Cancelled: onlyApproved("PO10", "closed"),
		},
		Cancel: {
			Draft:     core.MoveCell("PO11", Cancelled, core.OpOrderEdit),
			Submitted: core.MoveCell("PO11", Cancelled, core.OpOrderEdit),
			Approved:  {Rule: "PO11", Effect: core.Move, To: Cancelled, Op: core.OpOrderEdit, Guard: GuardNothingStarted},
			Completed: refuse("PO11", "a completed order is not cancelled"),
			Closed:    refuse("PO11", "a closed order is not cancelled"),
			Cancelled: core.IgnoreCell("PO11"),
		},
		Complete: {
			Draft:     core.IgnoreCell("PO12"),
			Submitted: core.IgnoreCell("PO12"),
			Approved:  {Rule: "PO12", Effect: core.Move, To: Completed, Guard: GuardAllDone},
			Completed: core.IgnoreCell("PO12"),
			Closed:    core.IgnoreCell("PO12"),
			Cancelled: core.IgnoreCell("PO12"),
		},
	},
}

// Set is what a transition writes beside the state.
type Set struct {
	Reason      *string // a rejection's reason; nil keeps the last one
	SubmittedBy string  // who submitted, "" keeps it
	ApprovedBy  string  // who approved, "" keeps it
	Unapprove   bool    // a submission or a revision clears the last approver
}

// Transition is the one writer of the order's state: d is a Move decided for o, read under its lock in tx. It
// publishes the order's state and audits the change with the cell's operation, when it names one.
func Transition(m *core.Base, ctx context.Context, tx db.Tx, o Order, d core.Decision, set Set, actor string) (Order, error) {
	if d.Effect != core.Move {
		return o, fmt.Errorf("orders: transition of %s on a %s decision", o.ID, d.Effect)
	}
	nullable := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	tag, err := tx.Exec(ctx, `UPDATE purchase_order SET state = $2, reason = coalesce($3, reason),
		submitted_by = coalesce($4, submitted_by), approved_by = CASE WHEN $6 THEN $5 ELSE coalesce($5, approved_by) END,
		version = version + 1, updated_at = now() WHERE id = $1 AND state = $7`,
		o.ID, d.To, set.Reason, nullable(set.SubmittedBy), nullable(set.ApprovedBy), set.Unapprove, o.State)
	if err != nil {
		return o, err
	}
	if tag.RowsAffected() != 1 {
		return o, fmt.Errorf("orders: %s is no longer %s", o.ID, o.State)
	}
	after, err := Emit(m, ctx, tx, o.ID)
	if err != nil || d.Op == "" {
		return after, err
	}
	return after, core.Change(m, ctx, tx, actor, d.Op, core.Orders, o.ID, after)
}
