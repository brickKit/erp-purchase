package requisitions

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	core "github.com/brickKit/erp-purchase/v3/internal/core"
	policies "github.com/brickKit/erp-purchase/v3/internal/policies"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/bizdate"
	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/flow"
	"github.com/brickKit/be-sdk-go/v3/httpx"
)

// The requisition's approval on the SDK's flow kernel (be-protocol chapter 17; docs/design.md PR02-PR06): a
// submission starts an instance in its transaction, a decision on the task changes the requisition through FlowEnded,
// cancelling withdraws or supersedes it, and order lines are taken only from a released requisition.

// keyRevision is a requisition's key revision: it never changes once submitted (a rejected one is not submitted
// again, PR04), so one revision.
const keyRevision = 1

// Flow is the requisition's approval: one step, any of its candidates, the policy's position or principal; a
// rejection ends the requisition rejected and says why.
func Flow() flow.Definition {
	return flow.Definition{Key: core.RequisitionFlow, Version: 1, TaskType: core.RequisitionTaskType,
		DocumentType: core.RequisitionDocument, Operation: core.OpReqApprove, ReadOperation: core.OpReqRead,
		OnReject: flow.Rejected, RejectComment: true, Steps: []flow.Step{{Form: flow.Any, Rule: flow.PositionOrPerson}}}
}

// Document is the requisition as the kernel refers to it.
func Document(r Requisition) flow.Document {
	return flow.Document{Type: core.RequisitionDocument, ID: r.ID, Number: r.Number}
}

// basis is the digest of the requisition's key fields: each line's product, quantity and unit.
func basis(r Requisition) (string, error) {
	lines := make([]any, len(r.Lines))
	for i, l := range r.Lines {
		lines[i] = map[string]any{"product_id": l.ProductID, "quantity": l.Quantity, "unit": l.Unit}
	}
	return canon.Digest("erp.purchase.requisition.approval_basis", 1, map[string]any{"lines": lines})
}

// FlowDocument is the kernel's Document hook for a requisition: locked, its key revision, its legal entity's business
// date and its three dimensions.
func FlowDocument(ctx context.Context, tx db.Tx, d flow.Document) (int64, string, sdkauthz.Facts, error) {
	var tz, le, requester string
	var path *string
	if err := tx.QueryRow(ctx, `SELECT timezone, legal_entity_id::text, department_path, requester_id FROM requisition
		WHERE id = $1::uuid FOR UPDATE`, d.ID).Scan(&tz, &le, &path, &requester); err != nil {
		return 0, "", nil, err
	}
	date, err := bizdate.Today(tz)
	return keyRevision, date, core.DocFacts(le, path, requester), err
}

// FlowEnded is the kernel's end hook for a requisition (be-protocol P17.6): approved, or rejected with the decider's
// comment as the reason.
func FlowEnded(m *core.Base, ctx context.Context, tx db.Tx, i flow.Instance) error {
	r, err := get(ctx, tx, i.Document.ID, true)
	if err != nil {
		return err
	}
	if i.Ending == nil {
		return fmt.Errorf("requisitions: the approval of %s ended %s without a decision", r.ID, i.State)
	}
	command, set := Approve, Set{}
	if i.State == flow.Rejected {
		command, set = Reject, Set{Reason: i.Ending.Comment}
	}
	d := Lifecycle.Decide(r.State, command)
	if d.Effect != core.Move {
		return fmt.Errorf("requisitions: the approval of %s ended %s on a %s requisition", r.ID, i.State, r.State)
	}
	_, err = Transition(m, ctx, tx, r, d, set, i.Ending.By)
	return err
}

// startApproval starts the requisition's approval in tx (PR02): approved at once when the policy needs no approval
// (d moves to approved), else a task for the policy's approver.
func startApproval(m *core.Base, ctx context.Context, tx db.Tx, r Requisition, d *core.Decision, actor string) error {
	p, err := policies.Of(ctx, tx, r.LegalEntityID)
	if err != nil {
		return err
	}
	digest, err := basis(r)
	if err != nil {
		return err
	}
	s := flow.Start{Flow: core.RequisitionFlow, Document: Document(r), LegalEntity: r.LegalEntityID, KeyRevision: keyRevision,
		Digests: []string{digest}, Initiator: actor, Summary: flow.Summary{Title: "Purchase requisition " + r.Number,
			Fields: []flow.SummaryField{{Key: "number", Label: "Number", Value: r.Number},
				{Key: "lines", Label: "Lines", Value: strconv.Itoa(len(r.Lines))}}}}
	if p.NeedsApproval() {
		b, err := p.Approver()
		if err != nil {
			return err
		}
		s.Bindings = []flow.Binding{b}
	} else {
		s.Automatic, d.To = true, Approved
	}
	_, err = m.Flow.StartIn(ctx, tx, s)
	return err
}

// released is the release check (be-protocol P17.7) before order lines are taken from a requisition (PR06).
func released(m *core.Base, ctx context.Context, tx db.Tx, r Requisition) error {
	digest, err := basis(r)
	if err != nil {
		return err
	}
	err = m.Flow.ReleaseIn(ctx, tx, Document(r), keyRevision, []string{digest})
	var nr flow.ErrNotReleased
	if errors.As(err, &nr) {
		return core.ErrNotReleased.New(r.Number + ": " + nr.Why)
	}
	return err
}

// decide is the approve and reject routes (PR03, PR04): the caller's decision on the requisition's approval task
// through the kernel; an approved requisition approved again, a rejected one rejected again, answer it as it is.
func decide(m *core.Base, c *httpx.Context, ctx context.Context, cur Requisition, command string, in actionIn) error {
	d := Lifecycle.Decide(cur.State, command)
	switch d.Effect {
	case core.Ignore:
		return c.JSON(200, cur)
	case core.Refuse:
		return core.Refused(d, cur.State)
	}
	if in.Version != nil && *in.Version != cur.Version {
		return core.ErrVersion.New(fmt.Sprintf("the requisition is at version %d", cur.Version))
	}
	choice := flow.Approve
	if command == Reject {
		choice = flow.Reject
	}
	subj, _ := sdkauthz.From(ctx)
	if _, err := m.Flow.DecideDocument(ctx, subj, c.Header(calls.HeaderIdempotencyKey), Document(cur), choice, in.Reason); err != nil {
		return err
	}
	r, err := read(m, ctx, cur.ID)
	if err != nil {
		return err
	}
	return c.JSON(200, r)
}
