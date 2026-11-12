package receipts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core "github.com/brickKit/erp-purchase/v3/internal/core"
	masterdata "github.com/brickKit/erp-purchase/v3/internal/masterdata"
	orders "github.com/brickKit/erp-purchase/v3/internal/orders"
	policies "github.com/brickKit/erp-purchase/v3/internal/policies"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/bizdate"
	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/idem"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/list"
	"github.com/brickKit/be-sdk-go/v3/work"
	inventoryv3 "github.com/brickKit/erp-inventory/contract/v3/gen/go/be/erp/inventory/v3"
)

// Receipts (PO07; docs/design.md, "收货、验收与退货"): the receipt is made receiving, with each line's valuation basis,
// in one transaction with a work item; then erp/inventory takes it in, outside any transaction. Its answer posts the
// receipt, refuses it (failed, the bases withdrawn) or is not known: the work item sends the same receipt again, and
// erp/inventory's movement events settle it (movements.go). The received quantity only ever comes from the movements.

// JobReceive sends a receiving receipt to erp/inventory again until it answers (protocol 01, idempotent by the
// receipt's ID).
const JobReceive = "receive"

// RefKind is the reference kind of a purchase receipt's movements.
const RefKind = "purchase_receipt"

type lineIn struct {
	OrderLineID string   `json:"order_line_id"`
	Quantity    string   `json:"quantity"`
	Batch       *string  `json:"batch"`
	Serials     []string `json:"serials"`
}

type receiptIn struct {
	WarehouseID  *string  `json:"warehouse_id"`
	BusinessDate *string  `json:"business_date"`
	Lines        []lineIn `json:"lines"`
}

const maxLines = 200

func checkIn(in *receiptIn) error {
	switch {
	case in.WarehouseID != nil && !core.UUIDForm.MatchString(*in.WarehouseID):
		return core.Invalid("warehouse_id", "warehouse_id is a warehouse's ID")
	case in.BusinessDate != nil && !core.DateForm.MatchString(*in.BusinessDate):
		return core.Invalid("business_date", "business_date is YYYY-MM-DD")
	case len(in.Lines) == 0 || len(in.Lines) > maxLines:
		return core.Invalid("lines", fmt.Sprintf("a receipt has 1-%d lines", maxLines))
	}
	seen := map[string]bool{}
	for i := range in.Lines {
		l := &in.Lines[i]
		field := fmt.Sprintf("lines[%d]", i)
		if !core.UUIDForm.MatchString(l.OrderLineID) || seen[l.OrderLineID] {
			return core.Invalid(field+".order_line_id", "order_line_id is a line of the order, once")
		}
		seen[l.OrderLineID] = true
		var err error
		if l.Quantity, err = core.Decimal(field+".quantity", l.Quantity, core.QuantityScale, true); err != nil {
			return err
		}
		if l.Serials == nil {
			l.Serials = []string{}
		}
	}
	return nil
}

// planned is a receipt line with what it takes from its order line.
type planned struct {
	in   lineIn
	line orders.Line
	base string
}

// plan checks the receipt against the order outside the transaction: goods lines of the order, one warehouse, each
// quantity in the base unit (mdm/product's conversion).
func plan(m *core.Base, ctx context.Context, o orders.Order, in *receiptIn) ([]planned, string, error) {
	byID := map[string]orders.Line{}
	for _, l := range o.Lines {
		byID[l.ID] = l
	}
	var out []planned
	warehouse := ""
	if in.WarehouseID != nil {
		warehouse = *in.WarehouseID
	}
	for i, l := range in.Lines {
		field := fmt.Sprintf("lines[%d].order_line_id", i)
		ol, ok := byID[l.OrderLineID]
		if !ok || ol.Kind != "goods" {
			return nil, "", core.Invalid(field, "a receipt takes the order's goods lines; a service line is accepted")
		}
		if in.WarehouseID == nil {
			if warehouse != "" && warehouse != *ol.WarehouseID {
				return nil, "", core.Invalid("warehouse_id", "the lines go to different warehouses: one receipt a warehouse")
			}
			warehouse = *ol.WarehouseID
		}
		base, _, err := masterdata.Convert(m, ctx, ol.ProductID, l.Quantity, ol.Unit, ol.Product.Unit)
		if err != nil {
			return nil, "", err
		}
		out = append(out, planned{in: l, line: ol, base: base})
	}
	return out, warehouse, nil
}

// CreateReceipt receives goods lines of an approved order into one warehouse (PO07): 200 posted, 202 still receiving,
// 409 RECEIPT_FAILED (the problem's metadata.receipt_id) when erp/inventory refused it.
func CreateReceipt(m *core.Base, c *httpx.Context) error {
	req, err := idem.FromHTTP(c)
	if err != nil {
		return err
	}
	var in receiptIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if err := checkIn(&in); err != nil {
		return err
	}
	o, err := orders.Read(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, actor, err := core.Check(c, core.OpReceiptCreate, orders.OrderFacts(o))
	if err != nil {
		return err
	}
	if _, err := masterdata.Conn(m, core.Inventory); err != nil {
		return err
	}
	lines, warehouse, err := plan(m, ctx, o, &in)
	if err != nil {
		return err
	}
	le, err := masterdata.LegalEntityOf(m, ctx, o.LegalEntityID)
	if err != nil {
		return err
	}
	today, err := bizdate.Today(le.Timezone)
	if err != nil {
		return err
	}
	date := today
	if in.BusinessDate != nil {
		date = *in.BusinessDate
	}
	number, err := core.Number(m, ctx, "GR", le.ID, le.Code, today)
	if err != nil {
		return err
	}
	out, replayed, err := idem.Do(ctx, m.RT.DB, req, func(ctx context.Context, tx db.Tx) (idem.Outcome, error) {
		cur, err := orders.Get(ctx, tx, o.ID, true)
		if err != nil {
			return idem.Outcome{}, err
		}
		if d := orders.Lifecycle.Decide(cur.State, orders.Receive); d.Effect != core.Proceed {
			return idem.Outcome{}, core.Refused(d, cur.State)
		}
		if err := orders.Released(m, ctx, tx, cur); err != nil { // the approval covers the order as it is (P17.7)
			return idem.Outcome{}, err
		}
		p, err := policies.Of(ctx, tx, cur.LegalEntityID)
		if err != nil {
			return idem.Outcome{}, err
		}
		id := ids.New().String()
		if _, err := tx.Exec(ctx, `INSERT INTO receipt (id, number, order_id, order_revision, legal_entity_id,
			warehouse_id, business_date, owner_id, department_id, department_path, created_by)
			SELECT $1, $2, id, revision, legal_entity_id, $3, $4::date, owner_id, department_id, department_path, $5
			FROM purchase_order WHERE id = $6`, id, number, warehouse, date, actor, cur.ID); err != nil {
			return idem.Outcome{}, err
		}
		for i, l := range lines {
			// PO07: this receipt, what is receiving and what was received, within the ordered quantity and the
			// over-receipt percent; the whole receipt is refused otherwise
			tag, err := tx.Exec(ctx, `UPDATE order_line SET receiving_base_qty = receiving_base_qty + $2::numeric
				WHERE id = $1 AND receiving_base_qty + received_base_qty + $2::numeric
				<= base_quantity * (1 + $3::numeric / 100)`, l.line.ID, l.base, p.OverReceiptPercent)
			if err != nil {
				return idem.Outcome{}, err
			}
			if tag.RowsAffected() != 1 {
				return idem.Outcome{}, core.ErrOverReceipt.New(fmt.Sprintf("line %s: %s more is over the %s%% allowed",
					l.line.ID, l.base, p.OverReceiptPercent))
			}
			lineID := ids.New().String()
			if _, err := tx.Exec(ctx, `INSERT INTO receipt_line (id, receipt_id, line_no, order_line_id, product_id,
				quantity, base_quantity, batch, serials) VALUES ($1, $2, $3, $4, $5, $6::numeric, $7::numeric, $8, $9)`,
				lineID, id, i+1, l.line.ID, l.line.ProductID, l.in.Quantity, l.base, l.in.Batch, l.in.Serials); err != nil {
				return idem.Outcome{}, err
			}
			// protocol 05b: the line's valuation basis, net of tax per base unit
			if err := core.Fact(m, ctx, tx, core.ValuationBasis, lineID, cur.LegalEntityID, map[string]any{
				"fact_id": lineID, "receipt_id": id, "order_id": cur.ID, "order_line_id": l.line.ID,
				"legal_entity_id": cur.LegalEntityID, "product_id": l.line.ProductID, "base_quantity": l.base,
				"unit_price": core.Per(core.Times(l.line.UnitPrice, l.line.Quantity, 12), l.line.BaseQuantity, core.PriceScale),
				"currency":   cur.Currency, "price_basis": "net_of_tax", "business_date": date,
				"recorded_at": canon.Time(time.Now())}); err != nil {
				return idem.Outcome{}, err
			}
		}
		if _, err := m.RT.Work.Enqueue(ctx, tx, JobReceive, receiveArgs{ID: id}); err != nil {
			return idem.Outcome{}, err
		}
		r, err := emit(m, ctx, tx, id)
		if err != nil {
			return idem.Outcome{}, err
		}
		if _, err := orders.Progressed(m, ctx, tx, cur.ID, actor); err != nil {
			return idem.Outcome{}, err
		}
		return core.IDResult(id), core.Change(m, ctx, tx, actor, core.OpReceiptCreate, core.Receipts, id, r)
	})
	if err != nil {
		return err
	}
	id := core.ResultID(out)
	if !replayed {
		sync, cancel := context.WithTimeout(ctx, core.SyncBudget)
		_ = receive(m, sync, id) // what is left is the work item's
		cancel()
	}
	r, err := read(m, ctx, id)
	if err != nil {
		return err
	}
	switch r.State {
	case "posted":
		return c.JSON(200, r)
	case "failed":
		return core.ErrReceiptFailed.New(deref(r.Reason)).With("receipt_id", r.ID)
	}
	return c.JSON(202, r)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type receiveArgs struct {
	ID string `json:"receipt_id"`
}

// errUnknown is erp/inventory's answer not known: the receipt stays receiving for the work item.
var errUnknown = errors.New("erp/inventory's answer is not known")

// refused says whether erp/inventory's error is its answer (the receipt refused for good), not a failure to answer.
func refused(err error) (*errs.Error, bool) {
	e, code, ok := errs.FromGRPC(err)
	if !ok || e.Domain != core.Inventory {
		return nil, false
	}
	switch code {
	case errs.FailedPrecondition, errs.AlreadyExists, errs.InvalidArgument, errs.NotFound, errs.PermissionDenied:
		return e, true
	}
	return nil, false
}

// receive asks erp/inventory to take a receiving receipt in and records its answer.
func receive(m *core.Base, ctx context.Context, id string) error {
	r, err := read(m, ctx, id)
	if err != nil || r.State != "receiving" {
		return err
	}
	conn, err := masterdata.Conn(m, core.Inventory)
	if err != nil {
		return err
	}
	req := &inventoryv3.ReceiveRequest{ReceiptId: r.ID, LegalEntityId: r.LegalEntityID, WarehouseId: r.WarehouseID,
		BusinessDate: r.BusinessDate, Reference: &inventoryv3.Reference{Kind: RefKind, Id: r.OrderID}}
	for _, l := range r.Lines {
		req.Lines = append(req.Lines, &inventoryv3.ReceiveLine{LineId: l.ID, ProductId: l.ProductID,
			Quantity: l.BaseQuantity, Batch: deref(l.Batch), Serials: l.Serials})
	}
	res, err := inventoryv3.NewStockServiceClient(conn).Receive(ctx, req, calls.Idempotent())
	if err != nil {
		if e, ok := refused(err); ok {
			return fail(m, ctx, r.ID, e.Reason+": "+e.Detail)
		}
		return fmt.Errorf("%w: %v", errUnknown, err)
	}
	if len(res.GetMovementIds()) != len(r.Lines) {
		return fmt.Errorf("%w: %d movements for %d lines", errUnknown, len(res.GetMovementIds()), len(r.Lines))
	}
	return m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		for i, l := range r.Lines {
			if err := record(m, ctx, tx, res.GetMovementIds()[i], l.ID, l.BaseQuantity); err != nil {
				return err
			}
		}
		return nil
	})
}

// fail records erp/inventory's refusal: the receipt failed, what it was receiving given back, each line's basis
// withdrawn (protocol 05b: no movement uses it).
func fail(m *core.Base, ctx context.Context, id, why string) error {
	return m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		r, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := orders.Get(ctx, tx, r.OrderID, true); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE receipt SET state = 'failed', reason = $2, version = version + 1,
			updated_at = now() WHERE id = $1 AND state = 'receiving'`, id, why)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		for _, l := range r.Lines {
			if _, err := tx.Exec(ctx, `UPDATE order_line SET receiving_base_qty = receiving_base_qty - $2::numeric
				WHERE id = $1`, l.OrderLineID, l.BaseQuantity); err != nil {
				return err
			}
			factID := core.WithdrawalID(l.ID)
			if err := core.Fact(m, ctx, tx, core.ValuationWithdrawn, factID, r.LegalEntityID, map[string]any{
				"fact_id": factID, "basis_fact_id": l.ID, "receipt_id": id, "reason": why,
				"recorded_at": canon.Time(time.Now())}); err != nil {
				return err
			}
		}
		after, err := emit(m, ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := orders.Progressed(m, ctx, tx, r.OrderID, r.CreatedBy); err != nil {
			return err
		}
		return core.Change(m, ctx, tx, r.CreatedBy, core.OpReceiptCreate, core.Receipts, id, after)
	})
}

// ReceiveJob is the recovery of a receiving receipt (protocol 01, idempotent_external by the receipt's ID): it waits
// while the request's synchronous path may still have it, then sends it until erp/inventory answers.
func ReceiveJob(m *core.Base, ctx context.Context, r *work.Run) error {
	var a receiveArgs
	if err := json.Unmarshal(r.Args, &a); err != nil {
		return work.Permanent(err)
	}
	var young bool
	err := m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		return tx.QueryRow(ctx, `SELECT state = 'receiving' AND created_at > now() - make_interval(secs => $2)
			FROM receipt WHERE id = $1`, a.ID, core.SyncBudget.Seconds()).Scan(&young)
	})
	if err != nil {
		return err
	}
	if young {
		return work.Wait(errors.New("the request's synchronous path still has the receipt"))
	}
	return receive(m, ctx, a.ID)
}

func receiptFacts(r Receipt) sdkauthz.Facts {
	return core.DocFacts(r.LegalEntityID, r.DepartmentPath, r.OwnerID)
}

func GetReceipt(m *core.Base, c *httpx.Context) error {
	r, err := read(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	if _, _, err := core.Check(c, core.OpReceiptRead, receiptFacts(r)); err != nil {
		return err
	}
	return c.JSON(200, r)
}

var listSpec = list.Spec{Operation: core.OpReceiptRead, Alias: "r", IDColumn: "id", IDKind: list.UUID,
	Columns: []list.Column{
		{Field: "created_at", Column: "created_at", Kind: list.Time, Sortable: true},
		{Field: "order_id", Column: "order_id", Kind: list.UUID, Filterable: true},
		{Field: "legal_entity_id", Column: "legal_entity_id", Kind: list.UUID, Filterable: true},
		{Field: "state", Column: "state", Kind: list.Text, Filterable: true}}}

// ListReceipts is one page, newest first, by order, legal entity and state.
func ListReceipts(m *core.Base, c *httpx.Context) error {
	var f []list.Filter
	for _, p := range []string{"order_id", "legal_entity_id", "state"} {
		v := c.Query(p)
		if v == "" {
			continue
		}
		if p != "state" && !core.UUIDForm.MatchString(v) {
			return core.Invalid(p, p+" is an ID")
		}
		f = append(f, list.Filter{Field: p, Op: "eq", Values: []string{v}})
	}
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, listSpec, list.Request{Filters: f, Limit: limit, Cursor: c.Query("cursor"),
		Sort: []list.Order{{Field: "created_at", Desc: true}}})
	if err != nil {
		return err
	}
	out, next, err := list.Page(ctx, m.RT.DB, q, "receipt", cols, scan,
		func(r Receipt) ([]string, string) { return []string{r.CreatedAt}, r.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"receipts": out, "next_cursor": next})
}
