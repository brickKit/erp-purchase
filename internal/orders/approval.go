package orders

import (
	"context"
	"errors"
	"fmt"

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

// The order's approval on the SDK's flow kernel (be-protocol chapter 17; docs/design.md PO03-PO08, PO11): a submission
// or a revision starts an instance in its transaction, a decision on the task changes the order through FlowEnded,
// cancelling withdraws or supersedes it, and receipts and acceptances start only on a released order.

// Flow is the order's approval: one step, any of its candidates, the policy's position or principal; a rejection
// returns the order to draft and says why.
func Flow() flow.Definition {
	return flow.Definition{Key: core.OrderFlow, Version: 1, TaskType: core.OrderTaskType, DocumentType: core.OrderDocument,
		Operation: core.OpOrderApprove, ReadOperation: core.OpOrderRead, OnReject: flow.Returned, RejectComment: true,
		Steps: []flow.Step{{Form: flow.Any, Rule: flow.PositionOrPerson}}}
}

// Document is the order as the kernel refers to it.
func Document(o Order) flow.Document {
	return flow.Document{Type: core.OrderDocument, ID: o.ID, Number: o.Number}
}

// basis is the digest of the order's key fields (be-protocol P17.4): the supplier, the currency, each line's product,
// quantity, unit and unit price, the total. Its key revision is the order's revision.
func basis(o Order) (string, error) {
	lines := make([]any, len(o.Lines))
	for i, l := range o.Lines {
		lines[i] = map[string]any{"product_id": l.ProductID, "quantity": l.Quantity, "unit": l.Unit, "unit_price": l.UnitPrice}
	}
	return canon.Digest("erp.purchase.order.approval_basis", 1, map[string]any{"supplier_id": o.SupplierID,
		"currency": o.Currency, "lines": lines, "total": o.Total})
}

// FlowDocument is the kernel's Document hook for an order: locked, its revision, its legal entity's business date
// and its three dimensions.
func FlowDocument(ctx context.Context, tx db.Tx, d flow.Document) (int64, string, sdkauthz.Facts, error) {
	var revision int64
	var tz, le, owner string
	var path *string
	if err := tx.QueryRow(ctx, `SELECT revision, timezone, legal_entity_id::text, department_path, owner_id
		FROM purchase_order WHERE id = $1::uuid FOR UPDATE`, d.ID).Scan(&revision, &tz, &le, &path, &owner); err != nil {
		return 0, "", nil, err
	}
	date, err := bizdate.Today(tz)
	return revision, date, core.DocFacts(le, path, owner), err
}

// FlowEnded is the kernel's end hook for an order (be-protocol P17.6): approved, the decider its approver; returned,
// back to draft with the decider's comment as the reason.
func FlowEnded(m *core.Base, ctx context.Context, tx db.Tx, i flow.Instance) error {
	o, err := Get(ctx, tx, i.Document.ID, true)
	if err != nil {
		return err
	}
	if i.Ending == nil {
		return fmt.Errorf("orders: the approval of %s ended %s without a decision", o.ID, i.State)
	}
	command, set := Approve, Set{ApprovedBy: i.Ending.By}
	if i.State == flow.Returned {
		command, set = Reject, Set{Reason: i.Ending.Comment}
	}
	d := Lifecycle.Decide(o.State, command)
	if d.Effect != core.Move {
		return fmt.Errorf("orders: the approval of %s ended %s on a %s order", o.ID, i.State, o.State)
	}
	_, err = Transition(m, ctx, tx, o, d, set, i.Ending.By)
	return err
}

// startApproval starts the order's approval as it is now in tx (PO03, PO06), after its lines and header were written:
// approved at once when the policy needs no approval (d moves to approved, the submitter its approver), else a task
// for the policy's approver.
func startApproval(m *core.Base, ctx context.Context, tx db.Tx, id string, d *core.Decision, actor string) (Set, error) {
	set := Set{SubmittedBy: actor, Unapprove: true}
	o, err := Get(ctx, tx, id, false)
	if err != nil {
		return set, err
	}
	p, err := policies.Of(ctx, tx, o.LegalEntityID)
	if err != nil {
		return set, err
	}
	digest, err := basis(o)
	if err != nil {
		return set, err
	}
	s := flow.Start{Flow: core.OrderFlow, Document: Document(o), LegalEntity: o.LegalEntityID, KeyRevision: int64(o.Revision),
		Digests: []string{digest}, Initiator: actor, Summary: flow.Summary{Title: "Purchase order " + o.Number,
			Fields: []flow.SummaryField{{Key: "number", Label: "Number", Value: o.Number},
				{Key: "supplier", Label: "Supplier", Value: o.Supplier.Name},
				{Key: "total", Label: "Total", Value: o.Total + " " + o.Currency}}}}
	if p.NeedsApproval() {
		b, err := p.Approver()
		if err != nil {
			return set, err
		}
		s.Bindings = []flow.Binding{b}
	} else {
		s.Automatic, d.To, set.ApprovedBy = true, Approved, actor
	}
	_, err = m.Flow.StartIn(ctx, tx, s)
	return set, err
}

// Released is the release check (be-protocol P17.7) in the transaction that starts a receipt or an acceptance (PO07,
// PO08): an approved instance of the order's revision and key fields as they are now.
func Released(m *core.Base, ctx context.Context, tx db.Tx, o Order) error {
	digest, err := basis(o)
	if err != nil {
		return err
	}
	err = m.Flow.ReleaseIn(ctx, tx, Document(o), int64(o.Revision), []string{digest})
	var nr flow.ErrNotReleased
	if errors.As(err, &nr) {
		return core.ErrNotReleased.New(o.Number + ": " + nr.Why)
	}
	return err
}

// decide is the approve and reject routes (PO04, PO05): the caller's decision on the order's approval task through
// the kernel, which changes the order when the instance ends; an approved order approved again answers it as it is.
func decide(m *core.Base, c *httpx.Context, ctx context.Context, cur Order, command string, in actionIn) error {
	d := Lifecycle.Decide(cur.State, command)
	switch d.Effect {
	case core.Ignore:
		return c.JSON(200, cur)
	case core.Refuse:
		return core.Refused(d, cur.State)
	}
	if in.Version != nil && *in.Version != cur.Version {
		return core.ErrVersion.New(fmt.Sprintf("the order is at version %d", cur.Version))
	}
	choice := flow.Approve
	if command == Reject {
		choice = flow.Reject
	}
	subj, _ := sdkauthz.From(ctx)
	if _, err := m.Flow.DecideDocument(ctx, subj, c.Header(calls.HeaderIdempotencyKey), Document(cur), choice, in.Reason); err != nil {
		return err
	}
	o, err := Read(m, ctx, cur.ID)
	if err != nil {
		return err
	}
	return c.JSON(200, o)
}
