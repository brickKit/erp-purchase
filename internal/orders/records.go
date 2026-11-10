package orders

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

// Supplier is the order's supplier as it was when the order was made.
type Supplier struct {
	Name  string  `json:"name"`
	TaxID *string `json:"tax_id"`
}

// Product is a line's product as it was when the line was made.
type Product struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Unit string `json:"unit"` // its base unit
}

// Line is an order line with its progress.
type Line struct {
	ID                string  `json:"id"`
	Kind              string  `json:"kind"`
	ProductID         string  `json:"product_id"`
	Product           Product `json:"product"`
	Quantity          string  `json:"quantity"`
	Unit              string  `json:"unit"`
	BaseQuantity      string  `json:"base_quantity"`
	UnitPrice         string  `json:"unit_price"`
	Amount            string  `json:"amount"`
	WarehouseID       *string `json:"warehouse_id"`
	ExpectedDate      *string `json:"expected_date"`
	RequisitionLineID *string `json:"requisition_line_id"`
	ProjectRef        *string `json:"project_ref"`
	Receiving         string  `json:"receiving_base_quantity"`
	Received          string  `json:"received_base_quantity"`
	AcceptedQuantity  string  `json:"accepted_quantity"`
	AcceptedAmount    string  `json:"accepted_amount"`
}

// Order is an order's full state (erp.purchase_order.changed.v1).
type Order struct {
	ID            string   `json:"id"`
	Number        string   `json:"number"`
	LegalEntityID string   `json:"legal_entity_id"`
	SupplierID    string   `json:"supplier_id"`
	Supplier      Supplier `json:"supplier"`
	Currency      string   `json:"currency"`
	BusinessDate  string   `json:"business_date"`
	OwnerID       string   `json:"owner_id"`
	DepartmentID  *string  `json:"department_id"`
	State         string   `json:"state"`
	Revision      int      `json:"revision"`
	Total         string   `json:"total"`
	SubmittedBy   *string  `json:"submitted_by"`
	ApprovedBy    *string  `json:"approved_by"`
	Reason        *string  `json:"reason"`
	Lines         []Line   `json:"lines"`
	Version       int64    `json:"version"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`

	DepartmentPath *string `json:"-"`
}

const cols = `o.id::text, o.number, o.legal_entity_id::text, o.supplier_id::text, o.supplier_name, o.supplier_tax_id,
	o.currency, o.business_date::text, o.owner_id, o.department_id::text, o.department_path, o.state, o.revision,
	trim_scale(o.total)::text, o.submitted_by, o.approved_by, o.reason, o.version, o.created_at, o.updated_at,
	COALESCE((SELECT jsonb_agg(jsonb_build_object('id', l.id::text, 'kind', l.kind, 'product_id', l.product_id::text,
		'product', jsonb_build_object('code', l.product_code, 'name', l.product_name, 'unit', l.base_unit),
		'quantity', trim_scale(l.quantity)::text, 'unit', l.unit, 'base_quantity', trim_scale(l.base_quantity)::text,
		'unit_price', trim_scale(l.unit_price)::text, 'amount', trim_scale(l.amount)::text,
		'warehouse_id', l.warehouse_id::text, 'expected_date', l.expected_date::text,
		'requisition_line_id', l.requisition_line_id::text, 'project_ref', l.project_ref,
		'receiving_base_quantity', trim_scale(l.receiving_base_qty)::text,
		'received_base_quantity', trim_scale(l.received_base_qty)::text,
		'accepted_quantity', trim_scale(l.accepted_qty)::text, 'accepted_amount', trim_scale(l.accepted_amt)::text)
		ORDER BY l.line_no) FROM order_line l WHERE l.order_id = o.id), '[]'::jsonb)`

func scan(row pgx.Row) (Order, error) {
	var o Order
	var created, updated time.Time
	var lines []byte
	err := row.Scan(&o.ID, &o.Number, &o.LegalEntityID, &o.SupplierID, &o.Supplier.Name, &o.Supplier.TaxID, &o.Currency,
		&o.BusinessDate, &o.OwnerID, &o.DepartmentID, &o.DepartmentPath, &o.State, &o.Revision, &o.Total, &o.SubmittedBy,
		&o.ApprovedBy, &o.Reason, &o.Version, &created, &updated, &lines)
	if err != nil {
		return o, err
	}
	o.CreatedAt, o.UpdatedAt = canon.Time(created), canon.Time(updated)
	return o, json.Unmarshal(lines, &o.Lines)
}

// Get reads an order in tx; lock takes its row's lock, the one every change of the order and its lines' progress
// takes.
func Get(ctx context.Context, tx db.Tx, id string, lock bool) (Order, error) {
	if lock {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM purchase_order WHERE id = $1::uuid FOR UPDATE`, id); err != nil {
			return Order{}, err
		}
	}
	o, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM purchase_order o WHERE o.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return o, core.NotFound("order")
	}
	return o, err
}

// Read reads an order in its own transaction; an ID of the wrong form is not found.
func Read(m *core.Base, ctx context.Context, id string) (Order, error) {
	var o Order
	if !core.UUIDForm.MatchString(id) {
		return o, core.NotFound("order")
	}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var err error
		o, err = Get(ctx, tx, id, false)
		return err
	})
	return o, err
}

// Emit publishes the order's state after its rows changed.
func Emit(m *core.Base, ctx context.Context, tx db.Tx, id string) (Order, error) {
	o, err := Get(ctx, tx, id, false)
	if err != nil {
		return o, err
	}
	return o, core.Emit(m, ctx, tx, core.OrderChanged, o.ID, o.Version, o.LegalEntityID, o)
}
