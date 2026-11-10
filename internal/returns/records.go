package returns

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/canon"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/jackc/pgx/v5"
)

// Line is a return line; its ID is the return's idempotency key at erp/inventory and its fact's.
type Line struct {
	ID               string   `json:"id"`
	OrderLineID      string   `json:"order_line_id"`
	ReceiptLineID    string   `json:"receipt_line_id"`
	OriginMovementID string   `json:"origin_movement_id"`
	BaseQuantity     string   `json:"base_quantity"`
	Serials          []string `json:"serials"`
	State            string   `json:"state"`
	MovementID       *string  `json:"movement_id"`
	Problem          *string  `json:"problem"`

	ProductID string `json:"-"`
}

// Return is a return's full state (erp.purchase_return.changed.v1).
type Return struct {
	ID            string  `json:"id"`
	Number        string  `json:"number"`
	OrderID       string  `json:"order_id"`
	LegalEntityID string  `json:"legal_entity_id"`
	Reason        *string `json:"reason"`
	BusinessDate  string  `json:"business_date"`
	Lines         []Line  `json:"lines"`
	Version       int64   `json:"version"`
	UpdatedAt     string  `json:"updated_at"`

	OwnerID        string  `json:"-"`
	DepartmentPath *string `json:"-"`
	CreatedBy      string  `json:"-"`
	CreatedAt      string  `json:"-"`
}

// settled says whether every line is posted or failed.
func (r Return) settled() bool {
	for _, l := range r.Lines {
		if l.State == "returning" {
			return false
		}
	}
	return true
}

const cols = `r.id::text, r.number, r.order_id::text, r.legal_entity_id::text, r.reason, r.business_date::text, r.owner_id,
	r.department_path, r.created_by, r.version, r.created_at, r.updated_at,
	COALESCE((SELECT jsonb_agg(jsonb_build_object('id', l.id::text, 'order_line_id', l.order_line_id::text,
		'receipt_line_id', l.receipt_line_id::text, 'origin_movement_id', l.origin_movement_id::text,
		'base_quantity', trim_scale(l.base_quantity)::text, 'serials', to_jsonb(l.serials), 'state', l.state,
		'movement_id', l.movement_id::text, 'problem', l.problem, 'product_id', l.product_id::text) ORDER BY l.line_no)
		FROM return_line l WHERE l.return_id = r.id), '[]'::jsonb)`

type lineRow struct {
	Line
	ProductID string `json:"product_id"`
}

func scan(row pgx.Row) (Return, error) {
	var r Return
	var created, updated time.Time
	var lines []byte
	err := row.Scan(&r.ID, &r.Number, &r.OrderID, &r.LegalEntityID, &r.Reason, &r.BusinessDate, &r.OwnerID,
		&r.DepartmentPath, &r.CreatedBy, &r.Version, &created, &updated, &lines)
	if err != nil {
		return r, err
	}
	r.CreatedAt, r.UpdatedAt = canon.Time(created), canon.Time(updated)
	var rows []lineRow
	if err := json.Unmarshal(lines, &rows); err != nil {
		return r, err
	}
	r.Lines = make([]Line, len(rows))
	for i, l := range rows {
		r.Lines[i] = l.Line
		r.Lines[i].ProductID = l.ProductID
	}
	return r, nil
}

func get(ctx context.Context, tx db.Tx, id string) (Return, error) {
	r, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM purchase_return r WHERE r.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, core.NotFound("return")
	}
	return r, err
}

func read(m *core.Base, ctx context.Context, id string) (Return, error) {
	var r Return
	if !core.UUIDForm.MatchString(id) {
		return r, core.NotFound("return")
	}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		r, err = get(ctx, tx, id)
		return err
	})
	return r, err
}

// emit publishes the return's state; changed counts one more version (a line settled), the new return has its first.
func emit(m *core.Base, ctx context.Context, tx db.Tx, id string, changed bool) (Return, error) {
	if changed {
		if _, err := tx.Exec(ctx, `UPDATE purchase_return SET version = version + 1, updated_at = now() WHERE id = $1`, id); err != nil {
			return Return{}, err
		}
	}
	r, err := get(ctx, tx, id)
	if err != nil {
		return r, err
	}
	return r, core.Emit(m, ctx, tx, core.ReturnChanged, r.ID, r.Version, r.LegalEntityID, r)
}
