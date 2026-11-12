package requisitions

import (
	"context"
	"slices"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
)

// Taking (PR06): order lines are made from approved requisition lines; what an order line takes counts in the
// requisition line's ordered quantity, never more than the line asks. A cancelled order, a dropped or smaller order
// line gives back. A requisition all ordered is closed; one that gets some back is approved again.

// Take is what one order line takes from (positive) or gives back to (negative) a requisition line, in the line's
// unit; the order line's product and unit are the requisition line's.
type Take struct {
	LineID    string
	ProductID string
	Unit      string
	Quantity  string // a decimal, negative to give back
}

// Apply applies the takes of an order of the legal entity, in the order's transaction: each requisition is locked
// (in ID order, so two orders never wait on each other), checked, changed, closed or reopened, and published.
func Apply(m *core.Base, ctx context.Context, tx db.Tx, legalEntity string, takes []Take, actor string) error {
	if len(takes) == 0 {
		return nil
	}
	ids := make([]string, 0, len(takes))
	lines := make([]string, len(takes))
	for i, t := range takes {
		lines[i] = t.LineID
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT requisition_id::text FROM requisition_line WHERE id = ANY($1::uuid[])`, lines)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	slices.Sort(ids)
	byLine := map[string]Requisition{}
	for _, id := range ids {
		r, err := get(ctx, tx, id, true)
		if err != nil {
			return err
		}
		for _, l := range r.Lines {
			byLine[l.ID] = r
		}
	}
	for _, t := range takes {
		r, ok := byLine[t.LineID]
		if !ok {
			return core.ErrRequisition.New("no requisition line " + t.LineID)
		}
		var l Line
		for _, x := range r.Lines {
			if x.ID == t.LineID {
				l = x
			}
		}
		if t.Quantity[0] != '-' { // taking: from an approved requisition of the order's legal entity, same product and unit
			if d := Lifecycle.Decide(r.State, Order); d.Effect != core.Proceed {
				return core.ErrRequisition.New(r.Number + ": " + core.Refused(d, r.State).Error())
			}
			if r.LegalEntityID != legalEntity || l.ProductID != t.ProductID || l.Unit != t.Unit {
				return core.ErrRequisition.New("requisition line " + t.LineID + " is of another legal entity, product or unit")
			}
			if err := released(m, ctx, tx, r); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE requisition_line SET ordered_qty = ordered_qty + $2::numeric
			WHERE id = $1 AND ordered_qty + $2::numeric BETWEEN 0 AND quantity`, t.LineID, t.Quantity)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return core.ErrRequisition.New("requisition line " + t.LineID + " has not that much left to order")
		}
	}
	for _, id := range ids {
		if err := settle(m, ctx, tx, id, actor); err != nil {
			return err
		}
	}
	return nil
}

// settle closes a requisition all ordered, reopens one with some left, and publishes it either way.
func settle(m *core.Base, ctx context.Context, tx db.Tx, id, actor string) error {
	r, err := get(ctx, tx, id, false)
	if err != nil {
		return err
	}
	var all bool
	if err := tx.QueryRow(ctx, `SELECT bool_and(ordered_qty = quantity) FROM requisition_line WHERE requisition_id = $1`,
		id).Scan(&all); err != nil {
		return err
	}
	cmd := Reopen
	if all {
		cmd = Close
	}
	if d := Lifecycle.Decide(r.State, cmd); d.Effect == core.Move {
		_, err := Transition(m, ctx, tx, r, d, Set{Reason: r.Reason}, actor)
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE requisition SET version = version + 1, updated_at = now() WHERE id = $1`, id); err != nil {
		return err
	}
	_, err = emit(m, ctx, tx, id)
	return err
}
