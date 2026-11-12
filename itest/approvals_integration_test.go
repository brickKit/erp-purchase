//go:build integration

package itest

import (
	"encoding/json"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/sectest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// approvals (PO07, PR06 too): requisitions and orders are approved on the SDK's flow kernel (be-protocol chapter 17; docs/design.md
// PO03-PO08, PO11, PR02-PR06): a policy that wants an approval names an approver (NO_APPROVER_CONFIGURED); the task
// is published by the shared task contract with the policy's principal as its candidate; the standard routes decide
// and read it (a rejection says why, a stale version and a stranger are refused, the same key replays); a position
// without the employee component pauses the task; cancelling withdraws, revising supersedes; receipts and acceptances
// start only on a released order (NOT_RELEASED).
func (fx *fixture) approvals() {
	t := fx.t
	policy := func(body map[string]any) {
		t.Helper()
		code, p := fx.alice.do("PUT", "/api/purchase/policies/"+fx.le, body)
		require.Equal(t, 200, code, p)
	}
	bobApproves := map[string]any{"approval": "required", "approver_principal": sectest.Bob, "over_receipt_percent": "10"}

	// PO03, PR02: no approver named: submitting is refused, nothing changes
	policy(map[string]any{"approval": "required", "over_receipt_percent": "10"})
	o := fx.order(fx.le, nil, fx.svc("2", "50"))
	id := o["id"].(string)
	code, out := fx.alice.do("POST", "/api/purchase/orders/"+id+"/submit", nil)
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "NO_APPROVER_CONFIGURED", out["reason"])
	assert.Equal(t, "draft", fx.get("/api/purchase/orders/" + id)["state"])
	code, r := fx.alice.do("POST", "/api/purchase/requisitions", fx.reqBody(fx.le, &fx.d1, fx.screwLine("1")))
	require.Equal(t, 200, code, r)
	code, out = fx.alice.do("POST", "/api/purchase/requisitions/"+r["id"].(string)+"/submit", nil)
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "NO_APPROVER_CONFIGURED", out["reason"])
	assert.Empty(t, fx.tasks(id), "no instance, no task")

	// PO04: the task by the shared task contract, Bob its candidate; who may see it
	policy(bobApproves)
	fx.orderAction(fx.alice, id, "submit", nil, 200)
	task := fx.task(id)
	assert.Equal(t, core.OrderTaskType, task["task_type"])
	assert.Equal(t, "pending", task["state"])
	assert.Equal(t, []any{sectest.Bob}, task["candidates"])
	assert.Equal(t, core.ID, task["component"])
	assert.Equal(t, o["number"], at(task, "document", "number"))
	assert.Equal(t, "100 CNY", at(task, "summary", "fields", 2, "value"))
	tid := task["id"].(string)
	code, got := fx.bob.do("GET", "/be/flow/tasks/"+tid, nil)
	require.Equal(t, 200, code, got)
	assert.Equal(t, tid, got["id"])
	code, out = fx.carol.do("GET", "/be/flow/tasks/"+tid, nil)
	assert.Equal(t, 404, code, out)

	// PO05: a rejection on the standard route says why; a stale version is refused; the order goes back to draft
	decide := func(c client, task string, version any, decision string, comment any, key ...string) (int, map[string]any) {
		body := map[string]any{"version": version, "decision": decision}
		if comment != nil {
			body["comment"] = comment
		}
		return c.do("POST", "/be/flow/tasks/"+task+"/decisions", body, key...)
	}
	code, out = decide(fx.bob, tid, task["version"], "reject", nil)
	assert.Equal(t, 400, code, out)
	assert.Equal(t, "REQUEST_INVALID", out["reason"])
	code, out = decide(fx.bob, tid, task["version"].(float64)+1, "reject", "late")
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "VERSION_CONFLICT", out["reason"])
	code, out = decide(fx.alice, tid, task["version"], "approve", nil)
	assert.Equal(t, 403, code, out)
	assert.Equal(t, "SELF_APPROVAL", out["reason"])
	code, out = decide(fx.bob, tid, task["version"], "reject", "the price is wrong")
	require.Equal(t, 200, code, out)
	assert.Equal(t, "decided", out["state"])
	assert.Equal(t, "returned", at(out, "instance", "state"))
	back := fx.get("/api/purchase/orders/" + id)
	assert.Equal(t, "draft", back["state"])
	assert.Equal(t, "the price is wrong", back["reason"])

	// PO04: submitted again, a new instance; approved on the standard route; the same key replays, the old route too
	fx.orderAction(fx.alice, id, "submit", nil, 200)
	task = fx.task(id)
	require.NotEqual(t, tid, task["id"], "a new instance, a new task")
	tid = task["id"].(string)
	key := ids.New().String()
	code, first := decide(fx.bob, tid, task["version"], "approve", nil, key)
	require.Equal(t, 200, code, first)
	code, again := decide(fx.bob, tid, task["version"], "approve", nil, key)
	require.Equal(t, 200, code, again)
	assert.Equal(t, first["version"], again["version"], "the same key answers the first result (P17.11)")
	approved := fx.get("/api/purchase/orders/" + id)
	assert.Equal(t, "approved", approved["state"])
	assert.Equal(t, sectest.Bob, approved["approved_by"])
	assert.Equal(t, approved["version"], fx.orderAction(fx.bob, id, "approve", nil, 200)["version"])
	code, out = fx.bob.do("POST", "/api/purchase/orders/"+id+"/reject", map[string]any{"reason": "x", "version": 1})
	assert.Equal(t, 409, code, out) // an approved order is not rejected: the state answers first
	stale := fx.order(fx.le, nil, fx.svc("1", "10"))
	fx.orderAction(fx.alice, stale["id"].(string), "submit", nil, 200)
	code, out = fx.bob.do("POST", "/api/purchase/orders/"+stale["id"].(string)+"/approve", map[string]any{"version": 1})
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "VERSION_CONFLICT", out["reason"], "the old route approves the order's version it names")

	// PO08, P17.7: an approved order is released; changed behind its approval it is not
	line := at(approved, "lines", 0, "id").(string)
	_, err := fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".purchase_order SET revision = revision + 1 WHERE id = $1", id)
	require.NoError(t, err)
	code, out = fx.accept(id, map[string]any{"order_line_id": line, "quantity": "1"})
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "NOT_RELEASED", out["reason"])
	_, err = fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".purchase_order SET revision = revision - 1 WHERE id = $1", id)
	require.NoError(t, err)
	_, err = fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".order_line SET unit_price = unit_price + 1 WHERE id = $1", line)
	require.NoError(t, err)
	code, out = fx.accept(id, map[string]any{"order_line_id": line, "quantity": "1"})
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "NOT_RELEASED", out["reason"], "a unit price is a key field")
	_, err = fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".order_line SET unit_price = unit_price - 1 WHERE id = $1", line)
	require.NoError(t, err)
	code, out = fx.accept(id, map[string]any{"order_line_id": line, "quantity": "1"})
	assert.Equal(t, 200, code, out)

	// PO07, P17.7: a receipt too
	g := fx.approved(fx.le, fx.goods("1", "10"))
	gid := g["id"].(string)
	_, err = fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".purchase_order SET revision = revision + 1 WHERE id = $1", gid)
	require.NoError(t, err)
	code, out = fx.receive(gid, at(g, "lines", 0, "id").(string), "1")
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "NOT_RELEASED", out["reason"])
	_, err = fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".purchase_order SET revision = revision - 1 WHERE id = $1", gid)
	require.NoError(t, err)

	// PR06, P17.7: order lines are taken only from a requisition its approval covers
	code, q := fx.alice.do("POST", "/api/purchase/requisitions", fx.reqBody(fx.le, &fx.d1, fx.screwLine("2")))
	require.Equal(t, 200, code, q)
	fx.reqAction(fx.alice, q["id"].(string), "submit", nil, 200)
	q = fx.reqAction(fx.bob, q["id"].(string), "approve", nil, 200)
	qline := at(q, "lines", 0, "id").(string)
	_, err = fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".requisition_line SET quantity = quantity + 1 WHERE id = $1", qline)
	require.NoError(t, err)
	code, out = fx.alice.do("POST", "/api/purchase/orders", fx.orderBody(fx.le, nil, fx.sup, "CNY",
		fx.with(fx.goods("1", "1"), "requisition_line_id", qline)))
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "NOT_RELEASED", out["reason"])
	_, err = fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".requisition_line SET quantity = quantity - 1 WHERE id = $1", qline)
	require.NoError(t, err)

	// PO11: cancelling a submitted order withdraws its approval: the task is superseded
	c := fx.order(fx.le, nil, fx.svc("1", "10"))
	cid := c["id"].(string)
	fx.orderAction(fx.alice, cid, "submit", nil, 200)
	fx.orderAction(fx.alice, cid, "cancel", nil, 200)
	ct := fx.task(cid)
	assert.Equal(t, "superseded", ct["state"])
	assert.Equal(t, "withdrawn", at(ct, "instance", "state"))

	// P17.3, P17.8: a position without mdm/employee in the deployment: the task waits, the manager sees why
	policy(map[string]any{"approval": "required", "approver_position_id": ids.New().String(), "over_receipt_percent": "10"})
	p := fx.order(fx.le, nil, fx.svc("1", "10"))
	fx.orderAction(fx.alice, p["id"].(string), "submit", nil, 200)
	pt := fx.task(p["id"].(string))
	assert.Equal(t, "paused", pt["state"])
	assert.Equal(t, "no_assignee", pt["paused_reason"])
	code, paused := fx.alice.do("GET", "/be/flow/tasks?state=paused", nil)
	require.Equal(t, 200, code, paused)
	var found bool
	for _, x := range paused["tasks"].([]any) {
		found = found || x.(map[string]any)["id"] == pt["id"]
	}
	assert.True(t, found, "the exception entry lists it")
	code, out = fx.carol.do("GET", "/be/flow/tasks?state=paused", nil)
	assert.Equal(t, 404, code, out, "one who may not manage the flows sees nothing")
	policy(bobApproves)

	// PR03, PR04: a requisition's task; a rejection on the old route carries its reason into the instance's end
	code, r = fx.alice.do("POST", "/api/purchase/requisitions", fx.reqBody(fx.le, &fx.d1, fx.screwLine("1")))
	require.Equal(t, 200, code, r)
	rid := r["id"].(string)
	fx.reqAction(fx.alice, rid, "submit", nil, 200)
	rt := fx.task(rid)
	assert.Equal(t, core.RequisitionTaskType, rt["task_type"])
	rejected := fx.reqAction(fx.bob, rid, "reject", map[string]any{"reason": "not now"}, 200)
	assert.Equal(t, "rejected", rejected["state"])
	assert.Equal(t, "not now", rejected["reason"])
	assert.Equal(t, "rejected", at(fx.task(rid), "instance", "state"))
}

// tasks are the approval tasks published for a document, newest version first (the outbox in the member's schema).
func (fx *fixture) tasks(document string) []map[string]any {
	fx.t.Helper()
	rows, err := fx.admin.Query(fx.ctx, "SELECT payload FROM "+itSchema+".be_outbox WHERE type = $1 "+
		"AND convert_from(payload, 'UTF8')::jsonb->'document'->>'id' = $2 ORDER BY ord DESC", core.TaskChanged, document)
	require.NoError(fx.t, err)
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var payload []byte
		require.NoError(fx.t, rows.Scan(&payload))
		var m map[string]any
		require.NoError(fx.t, json.Unmarshal(payload, &m))
		out = append(out, m)
	}
	require.NoError(fx.t, rows.Err())
	return out
}

// task is the latest state of the document's newest task.
func (fx *fixture) task(document string) map[string]any {
	fx.t.Helper()
	ts := fx.tasks(document)
	require.NotEmpty(fx.t, ts, "a task of %s", document)
	return ts[0]
}
