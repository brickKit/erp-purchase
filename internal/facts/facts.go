package facts

import (
	"context"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	purchasev3 "github.com/brickKit/erp-purchase/contract/v3/gen/go/be/erp/purchase/v3"
)

// Read answers this component's incremental facts by identity (protocol 05, common rules: a consumer that missed one
// reads it back): each as its event carried it, in the order asked; one not known has an empty type. Every fact is
// kept by core.Fact in the transaction that published it.
func Read(m *core.Base, ctx context.Context, in *purchasev3.ReadRequest) (*purchasev3.ReadResponse, error) {
	if len(in.GetFactIds()) > core.MaxReadIDs {
		return nil, errs.New(errs.ReasonRequestInvalid, "at most 500 facts a call")
	}
	var asked []string
	for _, id := range in.GetFactIds() {
		if core.UUIDForm.MatchString(id) {
			asked = append(asked, id)
		}
	}
	known := map[string]*purchasev3.Fact{}
	err := m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		rows, err := tx.Query(ctx, `SELECT fact_id::text, type, data::text FROM purchase_fact WHERE fact_id = ANY($1::uuid[])`, asked)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f := &purchasev3.Fact{}
			var data string
			if err := rows.Scan(&f.FactId, &f.Type, &data); err != nil {
				return err
			}
			f.Data = []byte(data)
			known[f.FactId] = f
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	out := &purchasev3.ReadResponse{}
	for _, id := range in.GetFactIds() {
		if f, ok := known[id]; ok {
			out.Facts = append(out.Facts, f)
		} else {
			out.Facts = append(out.Facts, &purchasev3.Fact{FactId: id})
		}
	}
	return out, nil
}
