package receipts

import (
	"context"
	"encoding/json"
	"errors"

	core "github.com/brickKit/erp-purchase/v3/internal/core"
	orders "github.com/brickKit/erp-purchase/v3/internal/orders"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/jackc/pgx/v5"
)

// The received quantity comes from erp/inventory's movements only: the answer to Receive and the movement events
// both land in record, deduplicated by the movement's ID (its fact_id). So a lost answer, a crash after the call, or
// an event before the answer all end the same: each movement counted once.

// movement is what this component reads of erp.inventory_movement.recorded.v1.
type movement struct {
	FactID    string `json:"fact_id"`
	Kind      string `json:"kind"`
	Quantity  string `json:"quantity"`
	Reference *struct {
		Kind   string  `json:"kind"`
		LineID *string `json:"line_id"`
	} `json:"reference"`
}

// OnMovement receives erp/inventory's movement events: a receipt movement of a purchase receipt line of this
// component is recorded; every other movement is not this component's.
func OnMovement(m *core.Base, ctx context.Context, tx db.Tx, e events.Received) error {
	if e.Type != core.MovementRecorded {
		return nil
	}
	var mv movement
	if err := json.Unmarshal(e.Data, &mv); err != nil {
		return err
	}
	if mv.Kind != "receipt" || mv.Reference == nil || mv.Reference.Kind != RefKind || mv.Reference.LineID == nil {
		return nil
	}
	return record(m, ctx, tx, mv.FactID, *mv.Reference.LineID, mv.Quantity)
}

// record counts one movement of a receipt line, once: the order line's received quantity grows, what the line was
// receiving is given back, and the receipt posts when every line has its movement. A movement of a line this
// component does not have (another deployment's, a later purchase) is not its.
func record(m *core.Base, ctx context.Context, tx db.Tx, movementID, lineID, qty string) error {
	var receiptID, orderID, orderLineID, base string
	err := tx.QueryRow(ctx, `SELECT l.receipt_id::text, r.order_id::text, l.order_line_id::text,
		trim_scale(l.base_quantity)::text FROM receipt_line l JOIN receipt r ON r.id = l.receipt_id WHERE l.id = $1::uuid`,
		lineID).Scan(&receiptID, &orderID, &orderLineID, &base)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := orders.Get(ctx, tx, orderID, true); err != nil { // the order's lock: every change of its progress
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO receipt_movement (movement_id, receipt_line_id, order_line_id, base_quantity)
		VALUES ($1, $2, $3, $4::numeric) ON CONFLICT (movement_id) DO NOTHING`, movementID, lineID, orderLineID, qty)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM receipt WHERE id = $1 FOR UPDATE`, receiptID).Scan(&state); err != nil {
		return err
	}
	// a failed receipt gave its receiving back already; the movement still counts (the fact wins)
	receiving := "0"
	if state == "receiving" {
		receiving = base
	}
	if _, err := tx.Exec(ctx, `UPDATE order_line SET received_base_qty = received_base_qty + $2::numeric,
		receiving_base_qty = receiving_base_qty - $3::numeric WHERE id = $1`, orderLineID, qty, receiving); err != nil {
		return err
	}
	var createdBy string
	tag, err = tx.Exec(ctx, `UPDATE receipt SET state = 'posted', reason = NULL, version = version + 1, updated_at = now()
		WHERE id = $1 AND state = 'receiving' AND NOT EXISTS (SELECT 1 FROM receipt_line l WHERE l.receipt_id = $1
		AND NOT EXISTS (SELECT 1 FROM receipt_movement mv WHERE mv.receipt_line_id = l.id))`, receiptID)
	if err != nil {
		return err
	}
	posted := tag.RowsAffected() == 1
	if !posted {
		if _, err := tx.Exec(ctx, `UPDATE receipt SET version = version + 1, updated_at = now() WHERE id = $1`, receiptID); err != nil {
			return err
		}
	}
	r, err := emit(m, ctx, tx, receiptID)
	if err != nil {
		return err
	}
	createdBy = r.CreatedBy
	if _, err := orders.Progressed(m, ctx, tx, orderID, createdBy); err != nil {
		return err
	}
	if posted { // the outcome, audited in the name of who received
		return core.Change(m, ctx, tx, createdBy, core.OpReceiptCreate, core.Receipts, receiptID, r)
	}
	return nil
}
