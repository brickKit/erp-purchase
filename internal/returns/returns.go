package returns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core "github.com/brickKit/erp-purchase/v3/internal/core"
	masterdata "github.com/brickKit/erp-purchase/v3/internal/masterdata"
	orders "github.com/brickKit/erp-purchase/v3/internal/orders"

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
	"github.com/jackc/pgx/v5"
)

// Purchase returns (PO09; docs/design.md, "收货、验收与退货"): each line names a receipt movement of the order; in the
// return's transaction every line takes what it returns from what its movement has left (more is refused before
// anything is made: RETURN_EXCEEDS_RECEIPT). Then erp/inventory issues each line on its own, outside any
// transaction: its answer posts the line (the received quantity falls, the return fact published), its refusal fails
// it (the quantity given back to the movement), an unknown answer leaves it for the work item.

// JobReturn sends a return's lines still returning until erp/inventory answers (protocol 01, idempotent by each
// line's ID).
const JobReturn = "return"

// RefKind is the reference kind of a purchase return's movements.
const RefKind = "purchase_return"

type lineIn struct {
	OriginMovementID string   `json:"origin_movement_id"`
	BaseQuantity     string   `json:"base_quantity"`
	Serials          []string `json:"serials"`
}

type returnIn struct {
	Reason       *string  `json:"reason"`
	BusinessDate *string  `json:"business_date"`
	Lines        []lineIn `json:"lines"`
}

const maxLines = 200

func checkIn(in *returnIn) error {
	switch {
	case in.BusinessDate != nil && !core.DateForm.MatchString(*in.BusinessDate):
		return core.Invalid("business_date", "business_date is YYYY-MM-DD")
	case len(in.Lines) == 0 || len(in.Lines) > maxLines:
		return core.Invalid("lines", fmt.Sprintf("a return has 1-%d lines", maxLines))
	}
	for i := range in.Lines {
		l := &in.Lines[i]
		field := fmt.Sprintf("lines[%d]", i)
		if !core.UUIDForm.MatchString(l.OriginMovementID) {
			return core.Invalid(field+".origin_movement_id", "origin_movement_id is a receipt movement's ID")
		}
		var err error
		if l.BaseQuantity, err = core.Decimal(field+".base_quantity", l.BaseQuantity, core.QuantityScale, true); err != nil {
			return err
		}
		if l.Serials == nil {
			l.Serials = []string{}
		}
	}
	return nil
}

// CreateReturn returns goods of an order from its receipt movements (PO09): 200 when every line is posted or failed,
// 202 while a line is still returning.
func CreateReturn(m *core.Base, c *httpx.Context) error {
	req, err := idem.FromHTTP(c)
	if err != nil {
		return err
	}
	var in returnIn
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
	ctx, actor, err := core.Check(c, core.OpReturnEdit, orders.OrderFacts(o))
	if err != nil {
		return err
	}
	if _, err := masterdata.Conn(m, core.Inventory); err != nil {
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
	number, err := core.Number(m, ctx, "RT", le.ID, le.Code, today)
	if err != nil {
		return err
	}
	out, replayed, err := idem.Do(ctx, m.RT.DB, req, func(ctx context.Context, tx db.Tx) (idem.Outcome, error) {
		cur, err := orders.Get(ctx, tx, o.ID, true)
		if err != nil {
			return idem.Outcome{}, err
		}
		if d := orders.Lifecycle.Decide(cur.State, orders.Return); d.Effect != core.Proceed {
			return idem.Outcome{}, core.Refused(d, cur.State)
		}
		id := ids.New().String()
		if _, err := tx.Exec(ctx, `INSERT INTO purchase_return (id, number, order_id, legal_entity_id, reason,
			business_date, owner_id, department_id, department_path, created_by) SELECT $1, $2, id, legal_entity_id, $3,
			$4::date, owner_id, department_id, department_path, $5 FROM purchase_order WHERE id = $6`,
			id, number, in.Reason, date, actor, cur.ID); err != nil {
			return idem.Outcome{}, err
		}
		for i, l := range in.Lines {
			field := fmt.Sprintf("lines[%d].origin_movement_id", i)
			var receiptLine, orderLine, product string
			err := tx.QueryRow(ctx, `SELECT mv.receipt_line_id::text, mv.order_line_id::text, ol.product_id::text
				FROM receipt_movement mv JOIN order_line ol ON ol.id = mv.order_line_id
				WHERE mv.movement_id = $1::uuid AND ol.order_id = $2`, l.OriginMovementID, cur.ID).Scan(&receiptLine, &orderLine, &product)
			if errors.Is(err, pgx.ErrNoRows) {
				return idem.Outcome{}, core.Invalid(field, "origin_movement_id is a receipt movement of this order")
			}
			if err != nil {
				return idem.Outcome{}, err
			}
			// PO09: at most what the movement received and has not returned (or is returning)
			tag, err := tx.Exec(ctx, `UPDATE receipt_movement SET returned_base_qty = returned_base_qty + $2::numeric
				WHERE movement_id = $1 AND returned_base_qty + $2::numeric <= base_quantity`, l.OriginMovementID, l.BaseQuantity)
			if err != nil {
				return idem.Outcome{}, err
			}
			if tag.RowsAffected() != 1 {
				return idem.Outcome{}, core.ErrReturnExceeds.New(fmt.Sprintf("movement %s has less than %s left to return",
					l.OriginMovementID, l.BaseQuantity))
			}
			if _, err := tx.Exec(ctx, `INSERT INTO return_line (id, return_id, line_no, order_line_id, receipt_line_id,
				origin_movement_id, product_id, base_quantity, serials) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::numeric, $9)`,
				ids.New().String(), id, i+1, orderLine, receiptLine, l.OriginMovementID, product, l.BaseQuantity, l.Serials); err != nil {
				return idem.Outcome{}, err
			}
		}
		if _, err := m.RT.Work.Enqueue(ctx, tx, JobReturn, returnArgs{ID: id}); err != nil {
			return idem.Outcome{}, err
		}
		r, err := emit(m, ctx, tx, id, false)
		if err != nil {
			return idem.Outcome{}, err
		}
		return core.IDResult(id), core.Change(m, ctx, tx, actor, core.OpReturnEdit, core.Returns, id, r)
	})
	if err != nil {
		return err
	}
	id := core.ResultID(out)
	if !replayed {
		sync, cancel := context.WithTimeout(ctx, core.SyncBudget)
		_ = issue(m, sync, id) // what is left is the work item's
		cancel()
	}
	r, err := read(m, ctx, id)
	if err != nil {
		return err
	}
	if r.settled() {
		return c.JSON(200, r)
	}
	return c.JSON(202, r)
}

type returnArgs struct {
	ID string `json:"return_id"`
}

var errUnknown = errors.New("erp/inventory's answer is not known")

// refused says whether erp/inventory's error is its answer (the line refused for good), not a failure to answer.
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

// issue asks erp/inventory to issue each line still returning, one by one, and records each answer.
func issue(m *core.Base, ctx context.Context, id string) error {
	r, err := read(m, ctx, id)
	if err != nil {
		return err
	}
	conn, err := masterdata.Conn(m, core.Inventory)
	if err != nil {
		return err
	}
	var unknown error
	for _, l := range r.Lines {
		if l.State != "returning" {
			continue
		}
		res, err := inventoryv3.NewStockServiceClient(conn).Return(ctx, &inventoryv3.ReturnRequest{ReturnId: l.ID,
			OriginMovementId: l.OriginMovementID, Quantity: l.BaseQuantity, Serials: l.Serials,
			Reference: &inventoryv3.Reference{Kind: RefKind, Id: r.ID}, LineId: l.ID, BusinessDate: r.BusinessDate},
			calls.Idempotent())
		switch {
		case err == nil:
			err = settle(m, ctx, r, l, res.GetMovementId(), "")
		default:
			if e, ok := refused(err); ok {
				err = settle(m, ctx, r, l, "", e.Reason+": "+e.Detail)
			} else {
				unknown = fmt.Errorf("%w: %v", errUnknown, err)
				continue
			}
		}
		if err != nil {
			return err
		}
	}
	return unknown
}

// settle records one line's answer: posted (the movement, the received quantity falls, the return fact) or failed
// (the problem, the quantity given back to its movement); a line already settled stays as it is.
func settle(m *core.Base, ctx context.Context, r Return, l Line, movement, problem string) error {
	return m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		if _, err := orders.Get(ctx, tx, r.OrderID, true); err != nil { // the order's lock: every change of its progress
			return err
		}
		state := "posted"
		if movement == "" {
			state = "failed"
		}
		var mv, why *string
		if movement != "" {
			mv = &movement
		} else {
			why = &problem
		}
		tag, err := tx.Exec(ctx, `UPDATE return_line SET state = $2, movement_id = $3, problem = $4
			WHERE id = $1 AND state = 'returning'`, l.ID, state, mv, why)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if state == "posted" {
			if _, err := tx.Exec(ctx, `UPDATE order_line SET received_base_qty = received_base_qty - $2::numeric
				WHERE id = $1`, l.OrderLineID, l.BaseQuantity); err != nil {
				return err
			}
			if err := core.Fact(m, ctx, tx, core.ReturnPosted, l.ID, r.LegalEntityID, map[string]any{
				"fact_id": l.ID, "return_id": r.ID, "order_id": r.OrderID, "order_line_id": l.OrderLineID,
				"legal_entity_id": r.LegalEntityID, "origin_movement_id": l.OriginMovementID, "movement_id": movement,
				"product_id": l.ProductID, "base_quantity": l.BaseQuantity, "business_date": r.BusinessDate,
				"recorded_at": canon.Time(time.Now())}); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE receipt_movement SET returned_base_qty = returned_base_qty - $2::numeric
			WHERE movement_id = $1`, l.OriginMovementID, l.BaseQuantity); err != nil {
			return err
		}
		after, err := emit(m, ctx, tx, r.ID, true)
		if err != nil {
			return err
		}
		if _, err := orders.Progressed(m, ctx, tx, r.OrderID, r.CreatedBy); err != nil {
			return err
		}
		if after.settled() { // the outcome, audited in the name of who returned
			return core.Change(m, ctx, tx, r.CreatedBy, core.OpReturnEdit, core.Returns, r.ID, after)
		}
		return nil
	})
}

// ReturnJob is the recovery of a return with lines still returning (protocol 01, idempotent_external by each line's
// ID): it waits while the request's synchronous path may still have it.
func ReturnJob(m *core.Base, ctx context.Context, r *work.Run) error {
	var a returnArgs
	if err := json.Unmarshal(r.Args, &a); err != nil {
		return work.Permanent(err)
	}
	var young bool
	err := m.RT.DB.BackgroundTx(ctx, func(ctx context.Context, tx db.Tx) error {
		return tx.QueryRow(ctx, `SELECT created_at > now() - make_interval(secs => $2) FROM purchase_return WHERE id = $1`,
			a.ID, core.SyncBudget.Seconds()).Scan(&young)
	})
	if err != nil {
		return err
	}
	if young {
		return work.Wait(errors.New("the request's synchronous path still has the return"))
	}
	return issue(m, ctx, a.ID)
}

func GetReturn(m *core.Base, c *httpx.Context) error {
	r, err := read(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	if _, _, err := core.Check(c, core.OpReturnRead, core.DocFacts(r.LegalEntityID, r.DepartmentPath, r.OwnerID)); err != nil {
		return err
	}
	return c.JSON(200, r)
}

var listSpec = list.Spec{Operation: core.OpReturnRead, Alias: "r", IDColumn: "id", IDKind: list.UUID,
	Columns: []list.Column{
		{Field: "created_at", Column: "created_at", Kind: list.Time, Sortable: true},
		{Field: "order_id", Column: "order_id", Kind: list.UUID, Filterable: true},
		{Field: "legal_entity_id", Column: "legal_entity_id", Kind: list.UUID, Filterable: true}}}

// ListReturns is one page, newest first, by order and legal entity.
func ListReturns(m *core.Base, c *httpx.Context) error {
	var f []list.Filter
	for _, p := range []string{"order_id", "legal_entity_id"} {
		v := c.Query(p)
		if v == "" {
			continue
		}
		if !core.UUIDForm.MatchString(v) {
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
	out, next, err := list.Page(ctx, m.RT.DB, q, "purchase_return", cols, scan,
		func(r Return) ([]string, string) { return []string{r.CreatedAt}, r.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"returns": out, "next_cursor": next})
}
