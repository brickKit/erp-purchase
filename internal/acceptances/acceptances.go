package acceptances

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

	"github.com/brickKit/be-sdk-go/v3/bizdate"
	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/idem"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/jackc/pgx/v5"
)

// Service acceptances (PO08): an approved order's service lines accepted by quantity or amount, within the ordered
// quantity and amount and the policy's over-receipt percent, in one transaction with their acceptance facts (the
// match basis of services and the period-end accrual, protocol 05).

// Line is an acceptance line; its ID is its fact's.
type Line struct {
	ID          string `json:"id"`
	OrderLineID string `json:"order_line_id"`
	Quantity    string `json:"quantity"`
	Amount      string `json:"amount"`
}

// Acceptance is the contract's Acceptance.
type Acceptance struct {
	ID            string `json:"id"`
	Number        string `json:"number"`
	OrderID       string `json:"order_id"`
	LegalEntityID string `json:"legal_entity_id"`
	BusinessDate  string `json:"business_date"`
	AcceptedBy    string `json:"accepted_by"`
	Lines         []Line `json:"lines"`

	OwnerID        string  `json:"-"`
	DepartmentPath *string `json:"-"`
}

type lineIn struct {
	OrderLineID string  `json:"order_line_id"`
	Quantity    *string `json:"quantity"`
	Amount      *string `json:"amount"`
}

type acceptanceIn struct {
	BusinessDate *string  `json:"business_date"`
	Lines        []lineIn `json:"lines"`
}

const maxLines = 200

// plan checks the lines against the order outside the transaction: its service lines, once each, a quantity or an
// amount (the amount omitted: quantity × unit price, rounded to the currency).
func plan(o orders.Order, in *acceptanceIn, scale int32) ([]Line, error) {
	if len(in.Lines) == 0 || len(in.Lines) > maxLines {
		return nil, core.Invalid("lines", fmt.Sprintf("an acceptance has 1-%d lines", maxLines))
	}
	byID := map[string]orders.Line{}
	for _, l := range o.Lines {
		byID[l.ID] = l
	}
	seen := map[string]bool{}
	var out []Line
	for i, l := range in.Lines {
		field := fmt.Sprintf("lines[%d]", i)
		ol, ok := byID[l.OrderLineID]
		if !ok || ol.Kind != "service" || seen[l.OrderLineID] {
			return nil, core.Invalid(field+".order_line_id", "an acceptance takes the order's service lines, once; goods are received")
		}
		seen[l.OrderLineID] = true
		if l.Quantity == nil && l.Amount == nil {
			return nil, core.Invalid(field, "a line accepts a quantity or an amount")
		}
		q, a := "0", ""
		var err error
		if l.Quantity != nil {
			if q, err = core.Decimal(field+".quantity", *l.Quantity, core.QuantityScale, false); err != nil {
				return nil, err
			}
		}
		if l.Amount != nil {
			if a, err = core.Decimal(field+".amount", *l.Amount, scale, false); err != nil {
				return nil, err
			}
		} else {
			a = core.Per(core.Times(q, ol.UnitPrice, 12), "1", scale)
		}
		if q == "0" && a == "0" {
			return nil, core.Invalid(field, "a line accepts a quantity or an amount above zero")
		}
		out = append(out, Line{ID: ids.New().String(), OrderLineID: l.OrderLineID, Quantity: q, Amount: a})
	}
	return out, nil
}

// CreateAcceptance accepts service lines of an approved order (PO08).
func CreateAcceptance(m *core.Base, c *httpx.Context) error {
	req, err := idem.FromHTTP(c)
	if err != nil {
		return err
	}
	var in acceptanceIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.BusinessDate != nil && !core.DateForm.MatchString(*in.BusinessDate) {
		return core.Invalid("business_date", "business_date is YYYY-MM-DD")
	}
	o, err := orders.Read(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, actor, err := core.Check(c, core.OpReceiptCreate, orders.OrderFacts(o))
	if err != nil {
		return err
	}
	scale, err := masterdata.CurrencyScale(m, ctx, o.Currency)
	if err != nil {
		return err
	}
	lines, err := plan(o, &in, scale)
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
	number, err := core.Number(m, ctx, "SA", le.ID, le.Code, today)
	if err != nil {
		return err
	}
	out, _, err := idem.Do(ctx, m.RT.DB, req, func(ctx context.Context, tx db.Tx) (idem.Outcome, error) {
		cur, err := orders.Get(ctx, tx, o.ID, true)
		if err != nil {
			return idem.Outcome{}, err
		}
		if d := orders.Lifecycle.Decide(cur.State, orders.Accept); d.Effect != core.Proceed {
			return idem.Outcome{}, core.Refused(d, cur.State)
		}
		p, err := policies.Of(ctx, tx, cur.LegalEntityID)
		if err != nil {
			return idem.Outcome{}, err
		}
		id := ids.New().String()
		if _, err := tx.Exec(ctx, `INSERT INTO acceptance (id, number, order_id, legal_entity_id, business_date,
			accepted_by, owner_id, department_id, department_path) SELECT $1, $2, id, legal_entity_id, $3::date, $4,
			owner_id, department_id, department_path FROM purchase_order WHERE id = $5`, id, number, date, actor, cur.ID); err != nil {
			return idem.Outcome{}, err
		}
		for i, l := range lines {
			// PO08: within the ordered quantity and amount and the over-receipt percent, both measures
			tag, err := tx.Exec(ctx, `UPDATE order_line SET accepted_qty = accepted_qty + $2::numeric,
				accepted_amt = accepted_amt + $3::numeric WHERE id = $1
				AND accepted_qty + $2::numeric <= quantity * (1 + $4::numeric / 100)
				AND accepted_amt + $3::numeric <= amount * (1 + $4::numeric / 100)`,
				l.OrderLineID, l.Quantity, l.Amount, p.OverReceiptPercent)
			if err != nil {
				return idem.Outcome{}, err
			}
			if tag.RowsAffected() != 1 {
				return idem.Outcome{}, core.ErrOverReceipt.New(fmt.Sprintf("line %s: %s, %s more is over the %s%% allowed",
					l.OrderLineID, l.Quantity, l.Amount, p.OverReceiptPercent))
			}
			if _, err := tx.Exec(ctx, `INSERT INTO acceptance_line (id, acceptance_id, line_no, order_line_id, quantity,
				amount) VALUES ($1, $2, $3, $4, $5::numeric, $6::numeric)`, l.ID, id, i+1, l.OrderLineID, l.Quantity, l.Amount); err != nil {
				return idem.Outcome{}, err
			}
			if err := core.Fact(m, ctx, tx, core.AcceptanceRecorded, l.ID, cur.LegalEntityID, map[string]any{
				"fact_id": l.ID, "acceptance_id": id, "order_id": cur.ID, "order_line_id": l.OrderLineID,
				"legal_entity_id": cur.LegalEntityID, "quantity": l.Quantity, "amount": l.Amount, "currency": cur.Currency,
				"business_date": date, "recorded_at": canon.Time(time.Now())}); err != nil {
				return idem.Outcome{}, err
			}
		}
		if _, err := orders.Progressed(m, ctx, tx, cur.ID, actor); err != nil {
			return idem.Outcome{}, err
		}
		a, err := get(ctx, tx, id)
		if err != nil {
			return idem.Outcome{}, err
		}
		return core.IDResult(id), core.Change(m, ctx, tx, actor, core.OpReceiptCreate, core.Receipts, id, a)
	})
	if err != nil {
		return err
	}
	a, err := read(m, ctx, core.ResultID(out))
	if err != nil {
		return err
	}
	return c.JSON(200, a)
}

func GetAcceptance(m *core.Base, c *httpx.Context) error {
	a, err := read(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	if _, _, err := core.Check(c, core.OpReceiptRead, core.DocFacts(a.LegalEntityID, a.DepartmentPath, a.OwnerID)); err != nil {
		return err
	}
	return c.JSON(200, a)
}

func get(ctx context.Context, tx db.Tx, id string) (Acceptance, error) {
	var a Acceptance
	var lines []byte
	err := tx.QueryRow(ctx, `SELECT a.id::text, a.number, a.order_id::text, a.legal_entity_id::text,
		a.business_date::text, a.accepted_by, a.owner_id, a.department_path,
		COALESCE((SELECT jsonb_agg(jsonb_build_object('id', l.id::text, 'order_line_id', l.order_line_id::text,
			'quantity', trim_scale(l.quantity)::text, 'amount', trim_scale(l.amount)::text) ORDER BY l.line_no)
			FROM acceptance_line l WHERE l.acceptance_id = a.id), '[]'::jsonb)
		FROM acceptance a WHERE a.id = $1::uuid`, id).Scan(&a.ID, &a.Number, &a.OrderID, &a.LegalEntityID, &a.BusinessDate,
		&a.AcceptedBy, &a.OwnerID, &a.DepartmentPath, &lines)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, core.NotFound("acceptance")
	}
	if err != nil {
		return a, err
	}
	return a, json.Unmarshal(lines, &a.Lines)
}

func read(m *core.Base, ctx context.Context, id string) (Acceptance, error) {
	var a Acceptance
	if !core.UUIDForm.MatchString(id) {
		return a, core.NotFound("acceptance")
	}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		a, err = get(ctx, tx, id)
		return err
	})
	return a, err
}
