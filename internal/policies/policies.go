package policies

import (
	"context"
	"errors"
	"fmt"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/jackc/pgx/v5"
)

// The purchase policy of a legal entity: whether requisitions and orders need an approval (PR02, PO03) and how much
// over the ordered quantity a receipt or an acceptance may go (PO07, PO08). A legal entity never set has the starting
// values, version 0.

// Policy is a legal entity's policy (the contract's Policy).
type Policy struct {
	LegalEntityID      string `json:"legal_entity_id"`
	Approval           string `json:"approval"`
	OverReceiptPercent string `json:"over_receipt_percent"`
	Version            int64  `json:"version"`
}

// NeedsApproval says whether a submitted requisition or order waits for an approver.
func (p Policy) NeedsApproval() bool { return p.Approval != "none" }

// Of reads the policy of a legal entity in tx.
func Of(ctx context.Context, tx db.Tx, legalEntity string) (Policy, error) {
	p := Policy{LegalEntityID: legalEntity, Approval: "required", OverReceiptPercent: "0"}
	err := tx.QueryRow(ctx, `SELECT approval, trim_scale(over_receipt_percent)::text, version FROM purchase_policy
		WHERE legal_entity_id = $1`, legalEntity).Scan(&p.Approval, &p.OverReceiptPercent, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	return p, err
}

func GetPolicy(m *core.Base, c *httpx.Context) error {
	le := c.Param("legal_entity_id")
	if !core.UUIDForm.MatchString(le) {
		return core.NotFound("policy")
	}
	ctx, _, err := core.Check(c, core.OpPolicyRead, core.EntityFacts(le))
	if err != nil {
		return err
	}
	var p Policy
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		p, err = Of(ctx, tx, le)
		return err
	})
	if err != nil {
		return err
	}
	return c.JSON(200, p)
}

type policyIn struct {
	Approval           string `json:"approval"`
	OverReceiptPercent string `json:"over_receipt_percent"`
	Version            *int64 `json:"version"`
}

// PutPolicy sets a legal entity's policy; a version given is the one the change is made on (0: never set).
func PutPolicy(m *core.Base, c *httpx.Context) error {
	le := c.Param("legal_entity_id")
	if !core.UUIDForm.MatchString(le) {
		return core.NotFound("policy")
	}
	var in policyIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Approval != "required" && in.Approval != "none" {
		return core.Invalid("approval", "approval is required or none")
	}
	pct, err := core.Decimal("over_receipt_percent", in.OverReceiptPercent, 4, false)
	if err != nil {
		return err
	}
	ctx, actor, err := core.Check(c, core.OpPolicyManage, core.EntityFacts(le))
	if err != nil {
		return err
	}
	var out Policy
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		// the version decides in the statement itself: two changes on the same version, one wins
		q := `INSERT INTO purchase_policy (legal_entity_id, approval, over_receipt_percent) VALUES ($1, $2, $3::numeric)
			ON CONFLICT (legal_entity_id) DO UPDATE SET approval = $2, over_receipt_percent = $3::numeric,
			version = purchase_policy.version + 1, updated_at = now()`
		args := []any{le, in.Approval, pct}
		switch {
		case in.Version != nil && *in.Version == 0:
			q = `INSERT INTO purchase_policy (legal_entity_id, approval, over_receipt_percent)
				VALUES ($1, $2, $3::numeric) ON CONFLICT (legal_entity_id) DO NOTHING`
		case in.Version != nil:
			q = `UPDATE purchase_policy SET approval = $2, over_receipt_percent = $3::numeric, version = version + 1,
				updated_at = now() WHERE legal_entity_id = $1 AND version = $4`
			args = append(args, *in.Version)
		}
		tag, err := tx.Exec(ctx, q, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			cur, err := Of(ctx, tx, le)
			if err != nil {
				return err
			}
			return core.ErrVersion.New(fmt.Sprintf("the policy is at version %d", cur.Version))
		}
		if out, err = Of(ctx, tx, le); err != nil {
			return err
		}
		return core.Change(m, ctx, tx, actor, core.OpPolicyManage, core.Policies, le, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
