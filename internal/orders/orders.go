package orders

import (
	"context"
	"fmt"
	"strings"
	"time"

	core "github.com/brickKit/erp-purchase/v3/internal/core"
	masterdata "github.com/brickKit/erp-purchase/v3/internal/masterdata"
	policies "github.com/brickKit/erp-purchase/v3/internal/policies"
	requisitions "github.com/brickKit/erp-purchase/v3/internal/requisitions"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/bizdate"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/idem"
	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/list"
	"github.com/brickKit/be-sdk-go/v3/search"
)

// Orders: drafts and their change, submission, approval or rejection, revision, closing, cancelling, the list.

// OrderFacts are the order's three dimensions (security 07).
func OrderFacts(o Order) sdkauthz.Facts {
	return core.DocFacts(o.LegalEntityID, o.DepartmentPath, o.OwnerID)
}

func subject(c *httpx.Context) string {
	u, _ := identity.UserFrom(c.Ctx())
	return u.Subject
}

// CreateOrder makes a draft (PO01), its lines possibly made from requisition lines (PR06).
func CreateOrder(m *core.Base, c *httpx.Context) error {
	req, err := idem.FromHTTP(c)
	if err != nil {
		return err
	}
	var in OrderIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Version != nil {
		return core.Invalid("version", "a new order has no version")
	}
	for i, l := range in.Lines {
		if l.ID != nil {
			return core.Invalid(fmt.Sprintf("lines[%d].id", i), "a new order's lines have no ID")
		}
	}
	ev, err := prepare(m, c.Ctx(), &in, subject(c))
	if err != nil {
		return err
	}
	ctx, actor, err := core.Check(c, core.OpOrderEdit, core.DocFacts(in.LegalEntityID, ev.deptPath, ev.owner))
	if err != nil {
		return err
	}
	today, err := bizdate.Today(ev.le.Timezone)
	if err != nil {
		return err
	}
	date := today
	if in.BusinessDate != nil {
		date = *in.BusinessDate
	}
	number, err := core.Number(m, ctx, "PO", ev.le.ID, ev.le.Code, today)
	if err != nil {
		return err
	}
	out, _, err := idem.Do(ctx, m.RT.DB, req, func(ctx context.Context, tx db.Tx) (idem.Outcome, error) {
		if err := ev.fresh(); err != nil {
			return idem.Outcome{}, err
		}
		id := ids.New().String()
		if _, err := tx.Exec(ctx, `INSERT INTO purchase_order (id, number, legal_entity_id, supplier_id, supplier_name,
			supplier_tax_id, currency, business_date, owner_id, department_id, department_path, search, search_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::date, $9, $10, $11, $12, $13)`, id, number, ev.le.ID, in.SupplierID,
			ev.supplier.Name, ev.supplier.TaxID, in.Currency, date, ev.owner, ev.deptID, ev.deptPath,
			search.Text(number, ev.supplier.Name), search.Version); err != nil {
			return idem.Outcome{}, err
		}
		if err := replaceLines(ctx, tx, id, in.Lines, ev); err != nil {
			return idem.Outcome{}, err
		}
		if err := total(ctx, tx, id); err != nil {
			return idem.Outcome{}, err
		}
		if err := requisitions.Apply(m, ctx, tx, ev.le.ID, takes(nil, in.Lines), actor); err != nil {
			return idem.Outcome{}, err
		}
		o, err := Emit(m, ctx, tx, id)
		if err != nil {
			return idem.Outcome{}, err
		}
		return core.IDResult(id), core.Change(m, ctx, tx, actor, core.OpOrderEdit, core.Orders, id, o)
	})
	if err != nil {
		return err
	}
	o, err := Read(m, ctx, core.ResultID(out))
	if err != nil {
		return err
	}
	return c.JSON(200, o)
}

func GetOrder(m *core.Base, c *httpx.Context) error {
	o, err := Read(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	if _, _, err := core.Check(c, core.OpOrderRead, OrderFacts(o)); err != nil {
		return err
	}
	return c.JSON(200, o)
}

// change is a draft's change (PO02) or a revision (PO06): the input checked against the order, the evidence taken
// again, op decided on the order's facts as they are and as they will be.
func change(m *core.Base, c *httpx.Context, command string) (context.Context, string, Order, OrderIn, evidence, error) {
	var in OrderIn
	var ev evidence
	if err := c.BindJSON(&in); err != nil {
		return nil, "", Order{}, in, ev, err
	}
	cur, err := Read(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return nil, "", cur, in, ev, err
	}
	ctx, actor, err := core.Check(c, core.OpOrderEdit, OrderFacts(cur))
	if err != nil {
		return nil, "", cur, in, ev, err
	}
	if d := Lifecycle.Decide(cur.State, command); d.Effect == core.Refuse { // before the evidence; decided again below
		return nil, "", cur, in, ev, core.Refused(d, cur.State)
	}
	switch {
	case in.LegalEntityID != cur.LegalEntityID:
		return nil, "", cur, in, ev, core.Invalid("legal_entity_id", "an order's legal entity does not change")
	case command == Revise && (in.SupplierID != cur.SupplierID || in.Currency != cur.Currency):
		return nil, "", cur, in, ev, core.Invalid("supplier_id", "a revision keeps the supplier and the currency")
	case command == Edit:
		for i, l := range in.Lines {
			if l.ID != nil {
				return nil, "", cur, in, ev, core.Invalid(fmt.Sprintf("lines[%d].id", i), "a draft's lines are replaced whole")
			}
		}
	}
	if ev, err = prepare(m, ctx, &in, cur.OwnerID); err != nil {
		return nil, "", cur, in, ev, err
	}
	if _, _, err := core.Check(c, core.OpOrderEdit, core.DocFacts(cur.LegalEntityID, ev.deptPath, ev.owner)); err != nil {
		return nil, "", cur, in, ev, err
	}
	return ctx, actor, cur, in, ev, nil
}

// header writes the order's header from a change or a revision (a revision counts one more).
func header(ctx context.Context, tx db.Tx, o Order, in OrderIn, ev evidence, revision bool) error {
	_, err := tx.Exec(ctx, `UPDATE purchase_order SET supplier_id = $2, supplier_name = $3, supplier_tax_id = $4,
		currency = $5, business_date = coalesce($6::date, business_date), owner_id = $7, department_id = $8,
		department_path = $9, revision = revision + CASE WHEN $10 THEN 1 ELSE 0 END, search = $11, search_version = $12,
		updated_at = now() WHERE id = $1`, o.ID, in.SupplierID, ev.supplier.Name, ev.supplier.TaxID, in.Currency,
		in.BusinessDate, ev.owner, ev.deptID, ev.deptPath, revision, search.Text(o.Number, ev.supplier.Name), search.Version)
	return err
}

// UpdateOrder changes a draft (PO02): its supplier, currency, owner, department and whole lines, by version.
func UpdateOrder(m *core.Base, c *httpx.Context) error {
	ctx, actor, cur, in, ev, err := change(m, c, Edit)
	if err != nil {
		return err
	}
	var out Order
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		o, err := Get(ctx, tx, cur.ID, true)
		if err != nil {
			return err
		}
		if d := Lifecycle.Decide(o.State, Edit); d.Effect == core.Refuse {
			return core.Refused(d, o.State)
		}
		if in.Version != nil && *in.Version != o.Version {
			return core.ErrVersion.New(fmt.Sprintf("the order is at version %d", o.Version))
		}
		if err := ev.fresh(); err != nil {
			return err
		}
		if err := replaceLines(ctx, tx, o.ID, in.Lines, ev); err != nil {
			return err
		}
		if err := header(ctx, tx, o, in, ev, false); err != nil {
			return err
		}
		if err := finish(m, ctx, tx, o, in.Lines, actor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE purchase_order SET version = version + 1 WHERE id = $1`, o.ID); err != nil {
			return err
		}
		if out, err = Emit(m, ctx, tx, o.ID); err != nil {
			return err
		}
		return core.Change(m, ctx, tx, actor, core.OpOrderEdit, core.Orders, o.ID, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// finish sums the lines and applies what they take from requisitions, old lines to new.
func finish(m *core.Base, ctx context.Context, tx db.Tx, o Order, in []LineIn, actor string) error {
	if err := total(ctx, tx, o.ID); err != nil {
		return err
	}
	return requisitions.Apply(m, ctx, tx, o.LegalEntityID, takes(o.Lines, in), actor)
}

// ReviseOrder makes a new revision of an approved order (PO06): back to submitted, or approved at once when the
// policy needs no approval.
func ReviseOrder(m *core.Base, c *httpx.Context) error {
	ctx, actor, cur, in, ev, err := change(m, c, Revise)
	if err != nil {
		return err
	}
	var out Order
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		o, err := Get(ctx, tx, cur.ID, true)
		if err != nil {
			return err
		}
		d := Lifecycle.Decide(o.State, Revise)
		if d.Effect == core.Refuse {
			return core.Refused(d, o.State)
		}
		if in.Version != nil && *in.Version != o.Version {
			return core.ErrVersion.New(fmt.Sprintf("the order is at version %d", o.Version))
		}
		if err := ev.fresh(); err != nil {
			return err
		}
		if err := reviseLines(ctx, tx, o, in.Lines, ev); err != nil {
			return err
		}
		if err := header(ctx, tx, o, in, ev, true); err != nil {
			return err
		}
		if err := finish(m, ctx, tx, o, in.Lines, actor); err != nil {
			return err
		}
		set, err := submitted(ctx, tx, o, &d, actor)
		if err != nil {
			return err
		}
		out, err = Transition(m, ctx, tx, o, d, set, actor)
		return err
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// submitted is what a submission or a revision writes (GuardPolicy): its submitter, and when the legal entity's
// policy needs no approval, the order approved at once by its submitter.
func submitted(ctx context.Context, tx db.Tx, o Order, d *core.Decision, actor string) (Set, error) {
	set := Set{SubmittedBy: actor, Unapprove: true}
	p, err := policies.Of(ctx, tx, o.LegalEntityID)
	if err != nil {
		return set, err
	}
	if !p.NeedsApproval() {
		d.To, set.ApprovedBy = Approved, actor
	}
	return set, nil
}

type actionIn struct {
	Version *int64  `json:"version"`
	Reason  *string `json:"reason"`
}

// Action is one of the commands submit, approve, reject, close and cancel, as the table decides it in the order's
// state; an ignored command answers the order as it is.
func Action(m *core.Base, command string) httpx.Handler {
	op := core.OpOrderEdit
	if command == Approve || command == Reject {
		op = core.OpOrderApprove
	}
	return func(c *httpx.Context) error {
		var in actionIn
		if err := core.BindOptional(c, &in); err != nil {
			return err
		}
		cur, err := Read(m, c.Ctx(), c.Param("id"))
		if err != nil {
			return err
		}
		ctx, actor, err := core.Check(c, op, OrderFacts(cur))
		if err != nil {
			return err
		}
		d := Lifecycle.Decide(cur.State, command)
		set := Set{}
		if command == Reject {
			if d.Effect == core.Move && (in.Reason == nil || strings.TrimSpace(*in.Reason) == "") {
				return core.Invalid("reason", "a rejection has its reason")
			}
			set.Reason = in.Reason
		}
		var ev evidence
		if d.Guard == GuardPolicy { // PO03: the evidence taken again on submitting
			if ev, err = again(m, ctx, cur); err != nil {
				return err
			}
		}
		var out Order
		err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
			o, err := Get(ctx, tx, cur.ID, true)
			if err != nil {
				return err
			}
			out = o
			d := Lifecycle.Decide(o.State, command)
			switch d.Effect {
			case core.Ignore:
				return nil
			case core.Refuse:
				return core.Refused(d, o.State)
			}
			if in.Version != nil && *in.Version != o.Version {
				return core.ErrVersion.New(fmt.Sprintf("the order is at version %d", o.Version))
			}
			switch d.Guard {
			case GuardPolicy:
				if o.Version != cur.Version { // GuardEvidence
					return core.ErrVersion.New("the order changed while its evidence was taken: submit it again")
				}
				if err := ev.fresh(); err != nil {
					return err
				}
				if set, err = submitted(ctx, tx, o, &d, actor); err != nil {
					return err
				}
			case GuardNotSubmitter:
				if o.SubmittedBy != nil && *o.SubmittedBy == actor {
					return core.ErrApprover.New("the order was submitted by " + actor + ": another approves it")
				}
				set.ApprovedBy = actor
			case GuardNothingReceiving:
				for _, l := range o.Lines {
					if l.Receiving != "0" {
						return core.ErrState.New("a receipt of the order is under way: it is closed when the receipt ends")
					}
				}
			case GuardNothingStarted:
				for _, l := range o.Lines {
					if l.Receiving != "0" || l.Received != "0" || l.AcceptedQuantity != "0" || l.AcceptedAmount != "0" {
						return core.ErrState.New("the order has receipts or acceptances: it is closed, not cancelled")
					}
				}
			}
			if command == Cancel { // a cancelled order gives back what it took from requisitions (PR06)
				if err := requisitions.Apply(m, ctx, tx, o.LegalEntityID, takes(o.Lines, nil), actor); err != nil {
					return err
				}
			}
			out, err = Transition(m, ctx, tx, o, d, set, actor)
			return err
		})
		if err != nil {
			return err
		}
		return c.JSON(200, out)
	}
}

// again takes a draft's evidence again on submitting (PO03): the legal entity, the supplier, the products.
func again(m *core.Base, ctx context.Context, o Order) (evidence, error) {
	ev := evidence{asked: time.Now()}
	var err error
	if ev.le, err = masterdata.LegalEntityOf(m, ctx, o.LegalEntityID); err != nil {
		return ev, err
	}
	if ev.supplier, err = masterdata.SupplierOf(m, ctx, o.SupplierID, o.LegalEntityID); err != nil {
		return ev, err
	}
	pids := make([]string, len(o.Lines))
	for i, l := range o.Lines {
		pids[i] = l.ProductID
	}
	_, err = masterdata.ProductsOf(m, ctx, pids, o.LegalEntityID)
	return ev, err
}

var listSpec = list.Spec{Operation: core.OpOrderRead, Alias: "o", IDColumn: "id", IDKind: list.UUID,
	Columns: []list.Column{
		{Field: "created_at", Column: "created_at", Kind: list.Time, Sortable: true},
		{Field: "legal_entity_id", Column: "legal_entity_id", Kind: list.UUID, Filterable: true},
		{Field: "supplier_id", Column: "supplier_id", Kind: list.UUID, Filterable: true},
		{Field: "owner_id", Column: "owner_id", Kind: list.Text, Filterable: true},
		{Field: "department_id", Column: "department_id", Kind: list.UUID, Filterable: true},
		{Field: "state", Column: "state", Kind: list.Text, Filterable: true}},
	Search: []list.SearchColumn{{Name: "number_supplier", Column: "search", Fields: []string{"number", "supplier"}}}}

// ListOrders is one page, newest first, by legal entity, supplier, owner, department, state, and number or supplier.
func ListOrders(m *core.Base, c *httpx.Context) error {
	var f []list.Filter
	for _, p := range []string{"legal_entity_id", "supplier_id", "owner_id", "department_id", "state"} {
		v := c.Query(p)
		if v == "" {
			continue
		}
		if p != "state" && p != "owner_id" && !core.UUIDForm.MatchString(v) {
			return core.Invalid(p, p+" is an ID")
		}
		f = append(f, list.Filter{Field: p, Op: "eq", Values: []string{v}})
	}
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, listSpec, list.Request{Filters: f, Q: c.Query("q"), Limit: limit,
		Cursor: c.Query("cursor"), Sort: []list.Order{{Field: "created_at", Desc: true}}})
	if err != nil {
		return err
	}
	out, next, err := list.Page(ctx, m.RT.DB, q, "purchase_order", cols, scan,
		func(o Order) ([]string, string) { return []string{o.CreatedAt}, o.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"orders": out, "next_cursor": next})
}
