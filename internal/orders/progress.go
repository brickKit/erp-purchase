package orders

import (
	"context"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
)

// Progressed publishes the order after its lines' progress changed (a receipt, an acceptance, a return), in that
// change's transaction under the order's lock, and completes it when every line is received or accepted whole, net of
// returns (PO12): a goods line its base quantity, a service line its quantity or its amount.
func Progressed(m *core.Base, ctx context.Context, tx db.Tx, id, actor string) (Order, error) {
	o, err := Get(ctx, tx, id, false)
	if err != nil {
		return o, err
	}
	if d := Lifecycle.Decide(o.State, Complete); d.Effect == core.Move {
		var done bool
		if err := tx.QueryRow(ctx, `SELECT bool_and(CASE WHEN kind = 'goods' THEN received_base_qty >= base_quantity
			ELSE accepted_qty >= quantity OR accepted_amt >= amount END) FROM order_line WHERE order_id = $1`, id).Scan(&done); err != nil {
			return o, err
		}
		if done { // GuardAllDone
			return Transition(m, ctx, tx, o, d, Set{}, actor)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_order SET version = version + 1, updated_at = now() WHERE id = $1`, id); err != nil {
		return o, err
	}
	return Emit(m, ctx, tx, id)
}
