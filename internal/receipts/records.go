package receipts

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

// Line is a receipt line; its ID is the receipt line reference.
type Line struct {
	ID           string   `json:"id"`
	OrderLineID  string   `json:"order_line_id"`
	Quantity     string   `json:"quantity"`
	BaseQuantity string   `json:"base_quantity"`
	Batch        *string  `json:"batch"`
	Serials      []string `json:"serials"`
	MovementIDs  []string `json:"movement_ids"`

	ProductID string `json:"-"`
}

// Receipt is a receipt's full state (erp.purchase_receipt.changed.v1).
type Receipt struct {
	ID            string  `json:"id"`
	Number        string  `json:"number"`
	OrderID       string  `json:"order_id"`
	OrderRevision int     `json:"order_revision"`
	LegalEntityID string  `json:"legal_entity_id"`
	WarehouseID   string  `json:"warehouse_id"`
	State         string  `json:"state"`
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

const cols = `r.id::text, r.number, r.order_id::text, r.order_revision, r.legal_entity_id::text, r.warehouse_id::text,
	r.state, r.reason, r.business_date::text, r.owner_id, r.department_path, r.created_by, r.version, r.created_at,
	r.updated_at,
	COALESCE((SELECT jsonb_agg(jsonb_build_object('id', l.id::text, 'order_line_id', l.order_line_id::text,
		'quantity', trim_scale(l.quantity)::text, 'base_quantity', trim_scale(l.base_quantity)::text, 'batch', l.batch,
		'serials', to_jsonb(l.serials), 'product_id', l.product_id::text,
		'movement_ids', COALESCE((SELECT jsonb_agg(mv.movement_id::text ORDER BY mv.recorded_at, mv.movement_id)
			FROM receipt_movement mv WHERE mv.receipt_line_id = l.id), '[]'::jsonb)) ORDER BY l.line_no)
		FROM receipt_line l WHERE l.receipt_id = r.id), '[]'::jsonb)`

// lineRow is a line as cols reads it (with its product, not in the event).
type lineRow struct {
	Line
	ProductID string `json:"product_id"`
}

func scan(row pgx.Row) (Receipt, error) {
	var r Receipt
	var created, updated time.Time
	var lines []byte
	err := row.Scan(&r.ID, &r.Number, &r.OrderID, &r.OrderRevision, &r.LegalEntityID, &r.WarehouseID, &r.State, &r.Reason,
		&r.BusinessDate, &r.OwnerID, &r.DepartmentPath, &r.CreatedBy, &r.Version, &created, &updated, &lines)
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

func get(ctx context.Context, tx db.Tx, id string) (Receipt, error) {
	r, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM receipt r WHERE r.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, core.NotFound("receipt")
	}
	return r, err
}

func read(m *core.Base, ctx context.Context, id string) (Receipt, error) {
	var r Receipt
	if !core.UUIDForm.MatchString(id) {
		return r, core.NotFound("receipt")
	}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		r, err = get(ctx, tx, id)
		return err
	})
	return r, err
}

func emit(m *core.Base, ctx context.Context, tx db.Tx, id string) (Receipt, error) {
	r, err := get(ctx, tx, id)
	if err != nil {
		return r, err
	}
	return r, core.Emit(m, ctx, tx, core.ReceiptChanged, r.ID, r.Version, r.LegalEntityID, r)
}
