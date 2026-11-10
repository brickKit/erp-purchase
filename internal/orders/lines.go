package orders

import (
	"context"
	"fmt"
	"strings"
	"time"

	core "github.com/brickKit/erp-purchase/v3/internal/core"
	masterdata "github.com/brickKit/erp-purchase/v3/internal/masterdata"
	orgunits "github.com/brickKit/erp-purchase/v3/internal/orgunits"
	requisitions "github.com/brickKit/erp-purchase/v3/internal/requisitions"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/ids"
)

// LineIn is a line as the user surface takes it (the contract's OrderIn lines); ID is a revision's kept line.
type LineIn struct {
	ID                *string `json:"id"`
	ProductID         string  `json:"product_id"`
	Quantity          string  `json:"quantity"`
	Unit              string  `json:"unit"`
	UnitPrice         string  `json:"unit_price"`
	WarehouseID       *string `json:"warehouse_id"`
	ExpectedDate      *string `json:"expected_date"`
	RequisitionLineID *string `json:"requisition_line_id"`
	ProjectRef        *string `json:"project_ref"`
}

// OrderIn is the contract's OrderIn: a new draft, a draft's change or a revision.
type OrderIn struct {
	LegalEntityID string   `json:"legal_entity_id"`
	SupplierID    string   `json:"supplier_id"`
	Currency      string   `json:"currency"`
	BusinessDate  *string  `json:"business_date"`
	OwnerID       *string  `json:"owner_id"`
	DepartmentID  *string  `json:"department_id"`
	Version       *int64   `json:"version"`
	Lines         []LineIn `json:"lines"`
}

const maxLines = 200

func checkIn(in *OrderIn) error {
	switch {
	case !core.UUIDForm.MatchString(in.LegalEntityID):
		return core.Invalid("legal_entity_id", "legal_entity_id is a legal entity's ID")
	case !core.UUIDForm.MatchString(in.SupplierID):
		return core.Invalid("supplier_id", "supplier_id is a partner's ID")
	case !core.CurrencyForm.MatchString(in.Currency):
		return core.Invalid("currency", "currency is an ISO 4217 code")
	case in.BusinessDate != nil && !core.DateForm.MatchString(*in.BusinessDate):
		return core.Invalid("business_date", "business_date is YYYY-MM-DD")
	case in.OwnerID != nil && strings.TrimSpace(*in.OwnerID) == "":
		return core.Invalid("owner_id", "owner_id is a principal")
	case len(in.Lines) == 0 || len(in.Lines) > maxLines:
		return core.Invalid("lines", fmt.Sprintf("an order has 1-%d lines", maxLines))
	}
	for i := range in.Lines {
		l := &in.Lines[i]
		field := fmt.Sprintf("lines[%d]", i)
		for name, v := range map[string]*string{"id": l.ID, "warehouse_id": l.WarehouseID, "requisition_line_id": l.RequisitionLineID} {
			if v != nil && !core.UUIDForm.MatchString(*v) {
				return core.Invalid(field+"."+name, name+" is an ID")
			}
		}
		if !core.UUIDForm.MatchString(l.ProductID) {
			return core.Invalid(field+".product_id", "product_id is a product's ID")
		}
		if strings.TrimSpace(l.Unit) == "" {
			return core.Invalid(field+".unit", "unit is required")
		}
		if l.ExpectedDate != nil && !core.DateForm.MatchString(*l.ExpectedDate) {
			return core.Invalid(field+".expected_date", "expected_date is YYYY-MM-DD")
		}
		var err error
		if l.Quantity, err = core.Decimal(field+".quantity", l.Quantity, core.QuantityScale, true); err != nil {
			return err
		}
		if l.UnitPrice, err = core.Decimal(field+".unit_price", l.UnitPrice, core.PriceScale, false); err != nil {
			return err
		}
	}
	return nil
}

// built is what a line takes from the master data and makes of it.
type built struct {
	kind, code, name, baseUnit string
	base, factor, amount       string
}

// evidence is an order's header and lines with what the master data said, taken outside the transaction (PO01,
// PO03), and when it was asked: it is evidence for core.EvidenceW.
type evidence struct {
	asked    time.Time
	le       masterdata.LegalEntity
	supplier masterdata.Supplier
	scale    int32
	deptID   *string
	deptPath *string
	owner    string
	lines    []built
}

// prepare checks an order's input and takes its evidence: the department, the legal entity, the currency, the
// supplier's and each product's eligibility to purchase (stocked: a goods line, with its warehouse), each line's base
// quantity and amount. owner is the order's owner when the input names none.
func prepare(m *core.Base, ctx context.Context, in *OrderIn, owner string) (evidence, error) {
	ev := evidence{asked: time.Now(), owner: owner}
	if err := checkIn(in); err != nil {
		return ev, err
	}
	if in.OwnerID != nil {
		ev.owner = *in.OwnerID
	}
	var err error
	if ev.deptID, ev.deptPath, err = orgunits.Usable(m, ctx, in.DepartmentID, in.LegalEntityID); err != nil {
		return ev, err
	}
	if ev.le, err = masterdata.LegalEntityOf(m, ctx, in.LegalEntityID); err != nil {
		return ev, err
	}
	if ev.scale, err = masterdata.CurrencyScale(m, ctx, in.Currency); err != nil {
		return ev, err
	}
	if ev.supplier, err = masterdata.SupplierOf(m, ctx, in.SupplierID, in.LegalEntityID); err != nil {
		return ev, err
	}
	pids := make([]string, len(in.Lines))
	for i, l := range in.Lines {
		pids[i] = l.ProductID
	}
	ps, err := masterdata.ProductsOf(m, ctx, pids, in.LegalEntityID)
	if err != nil {
		return ev, err
	}
	for i, l := range in.Lines {
		p, field := ps[l.ProductID], fmt.Sprintf("lines[%d].warehouse_id", i)
		b := built{kind: "service", code: p.Code, name: p.Name, baseUnit: p.BaseUnit}
		if p.Stocked {
			b.kind = "goods"
			if l.WarehouseID == nil {
				return ev, core.Invalid(field, "a stocked product's line names the warehouse it is received into")
			}
		} else if l.WarehouseID != nil {
			return ev, core.Invalid(field, "a service or non-stocked product's line has no warehouse")
		}
		if b.base, b.factor, err = masterdata.Convert(m, ctx, l.ProductID, l.Quantity, l.Unit, p.BaseUnit); err != nil {
			return ev, err
		}
		b.amount = core.Times(l.Quantity, l.UnitPrice, ev.scale)
		ev.lines = append(ev.lines, b)
	}
	return ev, nil
}

// fresh refuses evidence older than core.EvidenceW at commit (protocol 07): the master data answered too slowly.
func (ev evidence) fresh() error {
	if time.Since(ev.asked) > core.EvidenceW {
		return errs.New(errs.ReasonDependencyUnavailable, "the master data answered too slowly: try again")
	}
	return nil
}

func insertLine(ctx context.Context, tx db.Tx, orderID string, no int, l LineIn, b built) error {
	id := ids.New().String()
	if l.ID != nil {
		id = *l.ID
	}
	_, err := tx.Exec(ctx, `INSERT INTO order_line (id, order_id, line_no, kind, product_id, product_code, product_name,
		base_unit, quantity, unit, conversion_factor, base_quantity, unit_price, amount, warehouse_id, expected_date,
		requisition_line_id, project_ref) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::numeric, $10, $11::numeric,
		$12::numeric, $13::numeric, $14::numeric, $15, $16::date, $17, $18)`, id, orderID, no, b.kind, l.ProductID, b.code,
		b.name, b.baseUnit, l.Quantity, l.Unit, b.factor, b.base, l.UnitPrice, b.amount, l.WarehouseID, l.ExpectedDate,
		l.RequisitionLineID, l.ProjectRef)
	return err
}

// replaceLines replaces a draft's lines whole.
func replaceLines(ctx context.Context, tx db.Tx, orderID string, in []LineIn, ev evidence) error {
	if _, err := tx.Exec(ctx, `DELETE FROM order_line WHERE order_id = $1`, orderID); err != nil {
		return err
	}
	for i, l := range in {
		l.ID = nil
		if err := insertLine(ctx, tx, orderID, i+1, l, ev.lines[i]); err != nil {
			return err
		}
	}
	return nil
}

// reviseLines makes a revision's lines (PO06): a kept line (by its ID) keeps its product, its requisition line and
// its progress, and is not made smaller than that progress; a new line is added; a line left out is dropped only
// when it has no progress.
func reviseLines(ctx context.Context, tx db.Tx, o Order, in []LineIn, ev evidence) error {
	old := map[string]Line{}
	for _, l := range o.Lines {
		old[l.ID] = l
	}
	kept := map[string]bool{}
	for i, l := range in {
		field := fmt.Sprintf("lines[%d]", i)
		if l.ID == nil {
			if err := insertLine(ctx, tx, o.ID, i+1, l, ev.lines[i]); err != nil {
				return err
			}
			continue
		}
		was, ok := old[*l.ID]
		switch {
		case !ok || kept[*l.ID]:
			return core.Invalid(field+".id", "id is a line of this order, kept once")
		case was.ProductID != l.ProductID:
			return core.Invalid(field+".product_id", "a kept line keeps its product")
		case !samePtr(was.RequisitionLineID, l.RequisitionLineID):
			return core.Invalid(field+".requisition_line_id", "a kept line keeps its requisition line")
		}
		kept[*l.ID] = true
		b := ev.lines[i]
		tag, err := tx.Exec(ctx, `UPDATE order_line SET line_no = $2, quantity = $3::numeric, unit = $4,
			conversion_factor = $5::numeric, base_quantity = $6::numeric, unit_price = $7::numeric, amount = $8::numeric,
			warehouse_id = $9, expected_date = $10::date, project_ref = $11
			WHERE id = $1 AND $6::numeric >= receiving_base_qty + received_base_qty AND $3::numeric >= accepted_qty
			AND $8::numeric >= accepted_amt`, *l.ID, i+1, l.Quantity, l.Unit, b.factor, b.base, l.UnitPrice, b.amount,
			l.WarehouseID, l.ExpectedDate, l.ProjectRef)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return core.ErrState.New("line " + *l.ID + " would be below what it received, is receiving or accepted")
		}
	}
	for id, l := range old {
		if kept[id] {
			continue
		}
		if l.Receiving != "0" || l.Received != "0" || l.AcceptedQuantity != "0" || l.AcceptedAmount != "0" {
			return core.ErrState.New("line " + id + " has progress: it stays in the revision")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM order_line WHERE id = $1`, id); err != nil {
			return err
		}
	}
	return nil
}

func samePtr(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// takes are the net changes of what an order's lines take from requisition lines (PR06), old → new.
func takes(old []Line, in []LineIn) []requisitions.Take {
	type key struct{ line, product, unit string }
	net := map[string]string{}
	of := map[string]key{}
	var order []string
	add := func(k key, q string) {
		if _, ok := net[k.line]; !ok {
			net[k.line], of[k.line] = "0", k
			order = append(order, k.line)
		}
		net[k.line] = core.Minus(net[k.line], q)
	}
	for _, l := range old {
		if l.RequisitionLineID != nil {
			add(key{*l.RequisitionLineID, l.ProductID, l.Unit}, l.Quantity)
		}
	}
	for _, l := range in {
		if l.RequisitionLineID != nil {
			add(key{*l.RequisitionLineID, l.ProductID, l.Unit}, "-"+l.Quantity)
		}
	}
	var out []requisitions.Take
	for _, line := range order {
		if q := net[line]; q != "0" {
			k := of[line]
			out = append(out, requisitions.Take{LineID: k.line, ProductID: k.product, Unit: k.unit, Quantity: q})
		}
	}
	return out
}

// total sums the order's line amounts.
func total(ctx context.Context, tx db.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE purchase_order SET total = coalesce((SELECT sum(amount) FROM order_line
		WHERE order_id = $1), 0) WHERE id = $1`, id)
	return err
}
