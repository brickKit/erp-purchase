package purchase

import (
	"context"
	"time"

	core "github.com/brickKit/erp-purchase/v3/internal/core"
	orgunits "github.com/brickKit/erp-purchase/v3/internal/orgunits"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/runtime"
	"github.com/brickKit/be-sdk-go/v3/work"
)

// Module is the purchase component as the launcher and the shells run it: it only assembles the business modules
// under internal/ (their map is internal/AGENTS.md).
type Module struct {
	b *core.Base
}

// New builds the module from its runtime.
func New(rt *runtime.Runtime) (*Module, error) {
	return &Module{b: &core.Base{RT: rt}}, nil
}

// Routes registers the user surface (contract/openapi.yaml).
func (m *Module) Routes(r *httpx.Routes) error {
	add := func(method, path, op string, h httpx.Handler) {
		r.Add(httpx.Route{Method: method, Path: path, Operation: op, Handle: h})
	}
	todo := core.Unsupported("this route")
	add("GET", "/api/purchase/requisitions", core.OpReqRead, todo)
	add("POST", "/api/purchase/requisitions", core.OpReqEdit, todo)
	add("GET", "/api/purchase/requisitions/:id", core.OpReqRead, todo)
	add("PATCH", "/api/purchase/requisitions/:id", core.OpReqEdit, todo)
	add("POST", "/api/purchase/requisitions/:id/submit", core.OpReqEdit, todo)
	add("POST", "/api/purchase/requisitions/:id/approve", core.OpReqApprove, todo)
	add("POST", "/api/purchase/requisitions/:id/reject", core.OpReqApprove, todo)
	add("POST", "/api/purchase/requisitions/:id/cancel", core.OpReqEdit, todo)
	add("GET", "/api/purchase/orders", core.OpOrderRead, todo)
	add("POST", "/api/purchase/orders", core.OpOrderEdit, todo)
	add("GET", "/api/purchase/orders/:id", core.OpOrderRead, todo)
	add("PATCH", "/api/purchase/orders/:id", core.OpOrderEdit, todo)
	add("POST", "/api/purchase/orders/:id/submit", core.OpOrderEdit, todo)
	add("POST", "/api/purchase/orders/:id/approve", core.OpOrderApprove, todo)
	add("POST", "/api/purchase/orders/:id/reject", core.OpOrderApprove, todo)
	add("POST", "/api/purchase/orders/:id/revise", core.OpOrderEdit, todo)
	add("POST", "/api/purchase/orders/:id/close", core.OpOrderEdit, todo)
	add("POST", "/api/purchase/orders/:id/cancel", core.OpOrderEdit, todo)
	add("POST", "/api/purchase/orders/:id/receipts", core.OpReceiptCreate, todo)
	add("GET", "/api/purchase/receipts", core.OpReceiptRead, todo)
	add("GET", "/api/purchase/receipts/:id", core.OpReceiptRead, todo)
	add("POST", "/api/purchase/orders/:id/acceptances", core.OpReceiptCreate, todo)
	add("GET", "/api/purchase/acceptances/:id", core.OpReceiptRead, todo)
	add("POST", "/api/purchase/orders/:id/returns", core.OpReturnEdit, todo)
	add("GET", "/api/purchase/returns", core.OpReturnRead, todo)
	add("GET", "/api/purchase/returns/:id", core.OpReturnRead, todo)
	add("GET", "/api/purchase/policies/:legal_entity_id", core.OpPolicyRead, todo)
	add("PUT", "/api/purchase/policies/:legal_entity_id", core.OpPolicyManage, todo)
	// in the contract, not in this version (D75; docs/design.md, "实现状态")
	add("POST", "/api/purchase/orders/:id/prepayments", core.OpOrderEdit, core.Unsupported("a prepayment request"))
	add("GET", "/api/purchase/price-lists", core.OpOrderRead, core.Unsupported("supplier price lists"))
	return nil
}

// Subscriptions declares the department projection's input (protocol 02).
func (m *Module) Subscriptions(s *events.Subscriptions) error {
	s.Add(events.Sub{Name: "org-unit-events", Streams: []string{core.OrgStream}, Types: []string{core.OrgUnitEvents},
		Handle: m.received(orgunits.OnOrgUnit)})
	return nil
}

// Jobs declares the background work (protocol 01).
func (m *Module) Jobs(j *work.Jobs) error {
	j.Add(work.Job{Name: "align-org-units", Kind: work.Singleton, Effect: work.Local, Interval: time.Hour,
		MaxAttempts: work.Unlimited, Handle: m.job(orgunits.AlignOrgUnits)})
	j.Add(work.Job{Name: orgunits.JobRepairOrgUnits, Kind: work.Queue, Effect: work.IdempotentExternal,
		MaxAttempts: work.Unlimited, Handle: m.job(orgunits.RepairOrgUnits)})
	j.Add(work.Job{Name: orgunits.JobRealignOrgUnits, Kind: work.Queue, Effect: work.IdempotentExternal,
		MaxAttempts: work.Unlimited, Handle: m.job(orgunits.RealignOrgUnits)})
	return nil
}

// job and received adapt a module's function, which takes the shared base first, to what the SDK registers.
func (m *Module) job(f func(*core.Base, context.Context, *work.Run) error) func(context.Context, *work.Run) error {
	return func(ctx context.Context, r *work.Run) error { return f(m.b, ctx, r) }
}

func (m *Module) received(f func(*core.Base, context.Context, db.Tx, events.Received) error) func(context.Context, db.Tx, events.Received) error {
	return func(ctx context.Context, tx db.Tx, e events.Received) error { return f(m.b, ctx, tx, e) }
}
