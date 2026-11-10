package purchase

import (
	"context"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/recovery"
)

// RecoveryChecks registers what a full recovery asks this member (be-protocol chapter 14): whether the objects this
// member audits are still there, at which version. A receipt and an acceptance are both audited as receipts (the same
// resource); an acceptance never changes, its version is 1. The policy's identity is its legal entity.
func (m *Module) RecoveryChecks(c *recovery.Checks) error {
	c.Presence(core.Requisitions, recovery.TablePresence("requisition", "id", "version"))
	c.Presence(core.Orders, recovery.TablePresence("purchase_order", "id", "version"))
	c.Presence(core.Returns, recovery.TablePresence("purchase_return", "id", "version"))
	c.Presence(core.Policies, recovery.TablePresence("purchase_policy", "legal_entity_id", "version"))
	receipts := recovery.TablePresence("receipt", "id", "version")
	c.Presence(core.Receipts, func(ctx context.Context, tx db.Tx, ids []string) (map[string]int64, error) {
		out, err := receipts(ctx, tx, ids)
		if err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `SELECT id::text FROM acceptance WHERE id::text = ANY($1)`, ids)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out[id] = 1
		}
		return out, rows.Err()
	})
	return nil
}
