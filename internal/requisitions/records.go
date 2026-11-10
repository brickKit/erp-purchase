package requisitions

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

// Product is a line's product as it was when the line was made.
type Product struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Unit string `json:"unit"` // its base unit
}

// Line is a requisition line; Ordered is what was turned into order lines (PR06), in the line's unit.
type Line struct {
	ID         string  `json:"id"`
	ProductID  string  `json:"product_id"`
	Product    Product `json:"product"`
	Quantity   string  `json:"quantity"`
	Unit       string  `json:"unit"`
	NeededDate *string `json:"needed_date"`
	Ordered    string  `json:"ordered_quantity"`
}

// Requisition is a requisition's full state (erp.purchase_requisition.changed.v1).
type Requisition struct {
	ID            string  `json:"id"`
	Number        string  `json:"number"`
	LegalEntityID string  `json:"legal_entity_id"`
	DepartmentID  *string `json:"department_id"`
	RequesterID   string  `json:"requester_id"`
	State         string  `json:"state"`
	Reason        *string `json:"reason"`
	Lines         []Line  `json:"lines"`
	Version       int64   `json:"version"`
	UpdatedAt     string  `json:"updated_at"`

	DepartmentPath *string `json:"-"`
	SubmittedBy    *string `json:"-"`
	CreatedAt      string  `json:"-"`
}

const cols = `r.id::text, r.number, r.legal_entity_id::text, r.department_id::text, r.department_path, r.requester_id,
	r.state, r.reason, r.submitted_by, r.version, r.created_at, r.updated_at,
	COALESCE((SELECT jsonb_agg(jsonb_build_object('id', l.id::text, 'product_id', l.product_id::text,
		'product', jsonb_build_object('code', l.product_code, 'name', l.product_name, 'unit', l.base_unit),
		'quantity', trim_scale(l.quantity)::text, 'unit', l.unit, 'needed_date', l.needed_date::text,
		'ordered_quantity', trim_scale(l.ordered_qty)::text) ORDER BY l.line_no)
		FROM requisition_line l WHERE l.requisition_id = r.id), '[]'::jsonb)`

func scan(row pgx.Row) (Requisition, error) {
	var r Requisition
	var created, updated time.Time
	var lines []byte
	err := row.Scan(&r.ID, &r.Number, &r.LegalEntityID, &r.DepartmentID, &r.DepartmentPath, &r.RequesterID, &r.State,
		&r.Reason, &r.SubmittedBy, &r.Version, &created, &updated, &lines)
	if err != nil {
		return r, err
	}
	r.CreatedAt, r.UpdatedAt = canon.Time(created), canon.Time(updated)
	return r, json.Unmarshal(lines, &r.Lines)
}

// get reads a requisition in tx; lock takes its row's lock, the one every change of it takes.
func get(ctx context.Context, tx db.Tx, id string, lock bool) (Requisition, error) {
	if lock {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM requisition WHERE id = $1::uuid FOR UPDATE`, id); err != nil {
			return Requisition{}, err
		}
	}
	r, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM requisition r WHERE r.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, core.NotFound("requisition")
	}
	return r, err
}

// read reads a requisition in its own transaction; an ID of the wrong form is not found.
func read(m *core.Base, ctx context.Context, id string) (Requisition, error) {
	var r Requisition
	if !core.UUIDForm.MatchString(id) {
		return r, core.NotFound("requisition")
	}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		r, err = get(ctx, tx, id, false)
		return err
	})
	return r, err
}

// emit publishes the requisition's state after its rows changed.
func emit(m *core.Base, ctx context.Context, tx db.Tx, id string) (Requisition, error) {
	r, err := get(ctx, tx, id, false)
	if err != nil {
		return r, err
	}
	return r, core.Emit(m, ctx, tx, core.RequisitionChanged, r.ID, r.Version, r.LegalEntityID, r)
}
