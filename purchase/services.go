package purchase

import (
	"context"

	facts "github.com/brickKit/erp-purchase/v3/internal/facts"
	matchbasis "github.com/brickKit/erp-purchase/v3/internal/matchbasis"

	purchasev3 "github.com/brickKit/erp-purchase/contract/v3/gen/go/be/erp/purchase/v3"
)

// The system surface's methods the SDK calls on the module, each answered by the business module that owns it
// (internal/matchbasis: the match basis; internal/facts: the incremental facts by identity).

func (m *Module) Basis(ctx context.Context, in *purchasev3.BasisRequest) (*purchasev3.BasisResponse, error) {
	return matchbasis.Basis(m.b, ctx, in)
}

func (m *Module) Read(ctx context.Context, in *purchasev3.ReadRequest) (*purchasev3.ReadResponse, error) {
	return facts.Read(m.b, ctx, in)
}
