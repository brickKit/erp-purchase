package purchase

import (
	"context"
	"time"

	acceptances "github.com/brickKit/erp-purchase/v3/internal/acceptances"
	attachments "github.com/brickKit/erp-purchase/v3/internal/attachments"
	core "github.com/brickKit/erp-purchase/v3/internal/core"
	orders "github.com/brickKit/erp-purchase/v3/internal/orders"
	orgunits "github.com/brickKit/erp-purchase/v3/internal/orgunits"
	policies "github.com/brickKit/erp-purchase/v3/internal/policies"
	receipts "github.com/brickKit/erp-purchase/v3/internal/receipts"
	requisitions "github.com/brickKit/erp-purchase/v3/internal/requisitions"
	returns "github.com/brickKit/erp-purchase/v3/internal/returns"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/flow"
	"github.com/brickKit/be-sdk-go/v3/grpcx"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/runtime"
	"github.com/brickKit/be-sdk-go/v3/work"
	purchasev3 "github.com/brickKit/erp-purchase/contract/v3/gen/go/be/erp/purchase/v3"
)

// Module is the purchase component as the launcher and the shells run it: it only assembles the business modules
// under internal/ (their map is internal/AGENTS.md).
type Module struct {
	purchasev3.UnimplementedMatchServiceServer
	purchasev3.UnimplementedFactServiceServer
	b         *core.Base
	employees *flow.Employees // nil without mdm/employee
}

// New builds the module from its runtime: the flow kernel of the requisitions' and orders' approvals (be-protocol
// chapter 17), its hooks the two modules', its directory the employee projection when the deployment has mdm/employee.
func New(rt *runtime.Runtime) (*Module, error) {
	m := &Module{b: &core.Base{RT: rt}}
	k := &flow.Kernel{Component: core.ID, Stream: core.Stream, TaskEvent: core.TaskChanged, HeartbeatEvent: core.TaskHeartbeat,
		Operation: core.OpFlowDecide, ManageOperation: core.OpFlowManage,
		Definitions: []flow.Definition{orders.Flow(), requisitions.Flow()},
		Hooks: flow.Hooks{
			Document: func(ctx context.Context, tx db.Tx, d flow.Document) (int64, string, sdkauthz.Facts, error) {
				if d.Type == core.RequisitionDocument {
					return requisitions.FlowDocument(ctx, tx, d)
				}
				return orders.FlowDocument(ctx, tx, d)
			},
			Ended: func(ctx context.Context, tx db.Tx, i flow.Instance) error {
				if i.Document.Type == core.RequisitionDocument {
					return requisitions.FlowEnded(m.b, ctx, tx, i)
				}
				return orders.FlowEnded(m.b, ctx, tx, i)
			}},
		DB: rt.DB, Log: rt.Logger}
	if rt.Events != nil {
		k.Events = rt.Events
	}
	if rt.Audit != nil {
		k.Audit = rt.Audit
	}
	if e, ok := flow.NewEmployees(rt); ok {
		m.employees, k.Directory = e, e
	}
	if err := k.Check(); err != nil {
		return nil, err
	}
	m.b.Flow = k
	return m, nil
}

// FlowKernel gives the host the kernel, whose standard routes it registers after the module's (be-protocol P17.14).
func (m *Module) FlowKernel() *flow.Kernel { return m.b.Flow }

// Start runs the employee projection the approvals resolve against (be-protocol P17.8).
func (m *Module) Start(ctx context.Context) error {
	if m.employees == nil {
		return nil
	}
	return m.employees.Start(ctx)
}

// Routes registers the user surface (contract/openapi.yaml).
func (m *Module) Routes(r *httpx.Routes) error {
	add := func(method, path, op string, h httpx.Handler) {
		r.Add(httpx.Route{Method: method, Path: path, Operation: op, Handle: h})
	}
	h := m.handler
	add("GET", "/api/purchase/requisitions", core.OpReqRead, h(requisitions.ListRequisitions))
	add("POST", "/api/purchase/requisitions", core.OpReqEdit, h(requisitions.CreateRequisition))
	add("GET", "/api/purchase/requisitions/:id", core.OpReqRead, h(requisitions.GetRequisition))
	add("PATCH", "/api/purchase/requisitions/:id", core.OpReqEdit, h(requisitions.UpdateRequisition))
	add("POST", "/api/purchase/requisitions/:id/submit", core.OpReqEdit, requisitions.Action(m.b, requisitions.Submit))
	add("POST", "/api/purchase/requisitions/:id/approve", core.OpReqApprove, requisitions.Action(m.b, requisitions.Approve))
	add("POST", "/api/purchase/requisitions/:id/reject", core.OpReqApprove, requisitions.Action(m.b, requisitions.Reject))
	add("POST", "/api/purchase/requisitions/:id/cancel", core.OpReqEdit, requisitions.Action(m.b, requisitions.Cancel))
	add("GET", "/api/purchase/orders", core.OpOrderRead, h(orders.ListOrders))
	add("POST", "/api/purchase/orders", core.OpOrderEdit, h(orders.CreateOrder))
	add("GET", "/api/purchase/orders/:id", core.OpOrderRead, h(orders.GetOrder))
	add("PATCH", "/api/purchase/orders/:id", core.OpOrderEdit, h(orders.UpdateOrder))
	add("POST", "/api/purchase/orders/:id/submit", core.OpOrderEdit, orders.Action(m.b, orders.Submit))
	add("POST", "/api/purchase/orders/:id/approve", core.OpOrderApprove, orders.Action(m.b, orders.Approve))
	add("POST", "/api/purchase/orders/:id/reject", core.OpOrderApprove, orders.Action(m.b, orders.Reject))
	add("POST", "/api/purchase/orders/:id/revise", core.OpOrderEdit, h(orders.ReviseOrder))
	add("POST", "/api/purchase/orders/:id/close", core.OpOrderEdit, orders.Action(m.b, orders.Close))
	add("POST", "/api/purchase/orders/:id/cancel", core.OpOrderEdit, orders.Action(m.b, orders.Cancel))
	// the orders' attachments, kept by infra/attachment (assembly protocol 12)
	add("POST", "/api/purchase/orders/:id/attachments", core.OpOrderEdit, h(attachments.Upload))
	add("GET", "/api/purchase/orders/:id/attachments", core.OpOrderRead, h(attachments.List))
	add("POST", "/api/purchase/orders/:id/attachments/:aid/download", core.OpOrderRead, h(attachments.Download))
	add("DELETE", "/api/purchase/orders/:id/attachments/:aid", core.OpOrderEdit, h(attachments.Remove))
	r.Add(httpx.Route{Method: "POST", Path: "/api/purchase/orders/:id/receipts", Operation: core.OpReceiptCreate,
		Orchestration: true, Handle: h(receipts.CreateReceipt)})
	add("GET", "/api/purchase/receipts", core.OpReceiptRead, h(receipts.ListReceipts))
	add("GET", "/api/purchase/receipts/:id", core.OpReceiptRead, h(receipts.GetReceipt))
	add("POST", "/api/purchase/orders/:id/acceptances", core.OpReceiptCreate, h(acceptances.CreateAcceptance))
	add("GET", "/api/purchase/acceptances/:id", core.OpReceiptRead, h(acceptances.GetAcceptance))
	r.Add(httpx.Route{Method: "POST", Path: "/api/purchase/orders/:id/returns", Operation: core.OpReturnEdit,
		Orchestration: true, Handle: h(returns.CreateReturn)})
	add("GET", "/api/purchase/returns", core.OpReturnRead, h(returns.ListReturns))
	add("GET", "/api/purchase/returns/:id", core.OpReturnRead, h(returns.GetReturn))
	add("GET", "/api/purchase/policies/:legal_entity_id", core.OpPolicyRead, h(policies.GetPolicy))
	add("PUT", "/api/purchase/policies/:legal_entity_id", core.OpPolicyManage, h(policies.PutPolicy))
	// in the contract, not in this version (D75; docs/design.md, "实现状态")
	add("POST", "/api/purchase/orders/:id/prepayments", core.OpOrderEdit, core.Unsupported("a prepayment request"))
	add("GET", "/api/purchase/price-lists", core.OpOrderRead, core.Unsupported("supplier price lists"))
	return nil
}

// Services registers the system surface: the match basis for erp/finance, the facts by identity for any component.
func (m *Module) Services(s *grpcx.Services) error {
	s.Register(&purchasev3.MatchService_ServiceDesc, m, map[string]grpcx.Method{
		"Basis": {Operation: core.OpSysRead, Callers: []string{core.Finance}}})
	s.Register(&purchasev3.FactService_ServiceDesc, m, map[string]grpcx.Method{
		"Read": {Operation: core.OpSysRead, Callers: []string{grpcx.AnyComponent}}})
	return nil
}

// Subscriptions declares the department projection's input (protocol 02) and erp/inventory's movements, the only
// source of the received quantities.
func (m *Module) Subscriptions(s *events.Subscriptions) error {
	s.Add(events.Sub{Name: "org-unit-events", Streams: []string{core.OrgStream}, Types: []string{core.OrgUnitEvents},
		Handle: m.received(orgunits.OnOrgUnit)})
	s.Add(events.Sub{Name: "inventory-movements", Streams: []string{core.InventoryStream}, Types: []string{core.MovementRecorded},
		Handle: m.received(receipts.OnMovement)})
	if m.employees != nil {
		s.Add(m.employees.Sub())
	}
	return nil
}

// Jobs declares the background work (protocol 01).
func (m *Module) Jobs(j *work.Jobs) error {
	// a receipt is sent until erp/inventory answers: only it knows whether the goods came in
	j.Add(work.Job{Name: receipts.JobReceive, Kind: work.Queue, Effect: work.IdempotentExternal, MaxAttempts: work.Unlimited,
		Handle: m.job(receipts.ReceiveJob)})
	j.Add(work.Job{Name: returns.JobReturn, Kind: work.Queue, Effect: work.IdempotentExternal, MaxAttempts: work.Unlimited,
		Handle: m.job(returns.ReturnJob)})
	j.Add(work.Job{Name: "align-org-units", Kind: work.Singleton, Effect: work.Local, Interval: time.Hour,
		MaxAttempts: work.Unlimited, Handle: m.job(orgunits.AlignOrgUnits)})
	j.Add(work.Job{Name: orgunits.JobRepairOrgUnits, Kind: work.Queue, Effect: work.IdempotentExternal,
		MaxAttempts: work.Unlimited, Handle: m.job(orgunits.RepairOrgUnits)})
	j.Add(work.Job{Name: orgunits.JobRealignOrgUnits, Kind: work.Queue, Effect: work.IdempotentExternal,
		MaxAttempts: work.Unlimited, Handle: m.job(orgunits.RealignOrgUnits)})
	// an order's end commanded to infra/attachment once it commits (D68)
	j.Add(work.Job{Name: core.JobOrderEnded, Kind: work.Queue, Effect: work.IdempotentExternal, MaxAttempts: work.Unlimited,
		Handle: m.job(attachments.Ended)})
	m.b.Flow.Jobs(j) // paused approval tasks resolved again; the heartbeat
	return nil
}

// handler, job and received adapt a module's function, which takes the shared base first, to what the SDK registers.
func (m *Module) handler(f func(*core.Base, *httpx.Context) error) httpx.Handler {
	return func(c *httpx.Context) error { return f(m.b, c) }
}

func (m *Module) job(f func(*core.Base, context.Context, *work.Run) error) func(context.Context, *work.Run) error {
	return func(ctx context.Context, r *work.Run) error { return f(m.b, ctx, r) }
}

func (m *Module) received(f func(*core.Base, context.Context, db.Tx, events.Received) error) func(context.Context, db.Tx, events.Received) error {
	return func(ctx context.Context, tx db.Tx, e events.Received) error { return f(m.b, ctx, tx, e) }
}
