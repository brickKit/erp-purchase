package matchbasis

import (
	"context"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	purchasev3 "github.com/brickKit/erp-purchase/contract/v3/gen/go/be/erp/purchase/v3"
)

// The match basis (D50, protocol 05): what each order line ordered, received net of returns (by movement) and
// accepted, as it stands now; erp/finance's three-way match and period-end accrual read it. The received quantity is
// the movements' (receipts), the returned only what erp/inventory posted (returns).

// Basis answers the lines of one order, or the lines asked (at most core.MaxReadIDs; a line not known is left out).
func Basis(m *core.Base, ctx context.Context, in *purchasev3.BasisRequest) (*purchasev3.BasisResponse, error) {
	switch {
	case in.GetOrderId() == "" && len(in.GetOrderLineIds()) == 0:
		return nil, errs.New(errs.ReasonRequestInvalid, "an order_id or order_line_ids")
	case in.GetOrderId() != "" && len(in.GetOrderLineIds()) > 0:
		return nil, errs.New(errs.ReasonRequestInvalid, "an order_id or order_line_ids, not both")
	case len(in.GetOrderLineIds()) > core.MaxReadIDs:
		return nil, errs.New(errs.ReasonRequestInvalid, "at most 500 order lines a call")
	case in.GetOrderId() != "" && !core.UUIDForm.MatchString(in.GetOrderId()):
		return nil, core.NotFound("order")
	}
	var lineIDs []string
	for _, id := range in.GetOrderLineIds() {
		if core.UUIDForm.MatchString(id) {
			lineIDs = append(lineIDs, id)
		}
	}
	out := &purchasev3.BasisResponse{}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		rows, err := tx.Query(ctx, `SELECT o.id::text, l.id::text, o.revision, o.legal_entity_id::text, o.supplier_id::text,
			l.kind, l.product_id::text, l.unit, trim_scale(l.quantity)::text,
			trim_scale(CASE WHEN l.kind = 'goods' THEN l.received_base_qty ELSE 0 END)::text,
			trim_scale(l.accepted_qty)::text, trim_scale(l.accepted_amt)::text, trim_scale(l.unit_price)::text,
			o.currency, o.state
			FROM order_line l JOIN purchase_order o ON o.id = l.order_id
			WHERE ($1::uuid IS NOT NULL AND l.order_id = $1::uuid) OR l.id = ANY($2::uuid[])
			ORDER BY o.id, l.line_no`, nullable(in.GetOrderId()), lineIDs)
		if err != nil {
			return err
		}
		byLine := map[string]*purchasev3.BasisLine{}
		var ids []string
		for rows.Next() {
			l := &purchasev3.BasisLine{}
			if err := rows.Scan(&l.OrderId, &l.OrderLineId, &l.OrderRevision, &l.LegalEntityId, &l.SupplierId, &l.Kind,
				&l.ProductId, &l.Unit, &l.OrderedQuantity, &l.ReceivedBaseQuantity, &l.AcceptedQuantity, &l.AcceptedAmount,
				&l.UnitPrice, &l.Currency, &l.OrderState); err != nil {
				rows.Close()
				return err
			}
			out.Lines = append(out.Lines, l)
			byLine[l.OrderLineId] = l
			ids = append(ids, l.OrderLineId)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if in.GetOrderId() != "" && len(out.Lines) == 0 {
			return core.NotFound("order")
		}
		mvs, err := tx.Query(ctx, `SELECT mv.order_line_id::text, mv.movement_id::text, mv.receipt_line_id::text,
			trim_scale(mv.base_quantity)::text, trim_scale(coalesce((SELECT sum(r.base_quantity) FROM return_line r
				WHERE r.origin_movement_id = mv.movement_id AND r.state = 'posted'), 0))::text
			FROM receipt_movement mv WHERE mv.order_line_id = ANY($1::uuid[]) ORDER BY mv.recorded_at, mv.movement_id`, ids)
		if err != nil {
			return err
		}
		defer mvs.Close()
		for mvs.Next() {
			var line string
			mv := &purchasev3.ReceiptMovement{}
			if err := mvs.Scan(&line, &mv.MovementId, &mv.ReceiptLineId, &mv.ReceivedBaseQuantity, &mv.ReturnedBaseQuantity); err != nil {
				return err
			}
			byLine[line].Movements = append(byLine[line].Movements, mv)
		}
		return mvs.Err()
	})
	return out, err
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
