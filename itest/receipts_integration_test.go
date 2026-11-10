//go:build integration

package itest

import (
	"encoding/json"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// receipts: goods received through erp/inventory (PO07): the received quantity only from its movements; the
// over-receipt percent of the legal entity's policy (Shanghai 10%, Beijing 0%); the valuation basis published with the
// receipt and withdrawn when erp/inventory refuses it; an answer lost or not given settled by the movement events and
// the work item that sends the receipt again; an order received whole completes (PO12).
func (fx *fixture) receipts() {
	t := fx.t
	a := fx.orders["A"] // Shanghai: 12 boxes of screws (144 EA) at 100 a box
	aid, aLine := a["id"].(string), at(a, "lines", 0, "id").(string)

	// PO07: 5 boxes posted; the line's received quantity is the movement's; the valuation basis per base unit
	code, r := fx.receive(aid, aLine, "5")
	require.Equal(t, 200, code, r)
	assert.Equal(t, "posted", r["state"])
	assert.Regexp(t, `^GR-SH-`+fx.year+`-\d{6}$`, r["number"])
	assert.Equal(t, "60", at(r, "lines", 0, "base_quantity"))
	assert.Len(t, at(r, "lines", 0, "movement_ids"), 1)
	assert.Equal(t, fx.wh, r["warehouse_id"])
	o := fx.get("/api/purchase/orders/" + aid)
	assert.Equal(t, "60", at(o, "lines", 0, "received_base_quantity"))
	assert.Equal(t, "0", at(o, "lines", 0, "receiving_base_quantity"))
	assert.Equal(t, "approved", o["state"])
	basis := fx.fact(at(r, "lines", 0, "id").(string), "erp.purchase_receipt.valuation_basis.v1")
	assert.Equal(t, "8.333333", basis["unit_price"]) // 100 a box of 12, per EA
	assert.Equal(t, "60", basis["base_quantity"])
	assert.Equal(t, "CNY", basis["currency"])
	assert.Equal(t, r, fx.get("/api/purchase/receipts/"+r["id"].(string)))
	code, out := fx.carol.do("GET", "/api/purchase/receipts/"+r["id"].(string), nil)
	assert.Equal(t, 404, code, out)

	// PO07: over the 10% of Shanghai, the whole receipt is refused; within it, received; PO12: received whole, the
	// order completes, and is received no more
	code, out = fx.receive(aid, aLine, "9") // 60 + 108 > 144 × 1.1
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "OVER_RECEIPT", out["reason"])
	assert.Equal(t, "0", at(fx.get("/api/purchase/orders/"+aid), "lines", 0, "receiving_base_quantity"))
	code, r = fx.receive(aid, aLine, "8") // 60 + 96 = 156 ≤ 158.4
	require.Equal(t, 200, code, r)
	o = fx.get("/api/purchase/orders/" + aid)
	assert.Equal(t, "156", at(o, "lines", 0, "received_base_quantity"))
	assert.Equal(t, "completed", o["state"])
	code, out = fx.receive(aid, aLine, "1")
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "STATE_CONFLICT", out["reason"])
	fx.orders["A"] = o

	b := fx.orders["B"] // Beijing: 5 boxes (60 EA), no over-receipt
	bid, bLine := b["id"].(string), at(b, "lines", 0, "id").(string)

	// erp/inventory does not answer: the receipt stays receiving (202), the order is neither closed (PO10) nor
	// cancelled (PO11); the work item sends it again and it posts
	fx.inv.set("down")
	code, r = fx.receive(bid, bLine, "1")
	require.Equal(t, 202, code, r)
	assert.Equal(t, "receiving", r["state"])
	assert.Equal(t, "12", at(fx.get("/api/purchase/orders/"+bid), "lines", 0, "receiving_base_quantity"))
	fx.orderRefused(fx.alice, "POST", bid, "close", nil)
	fx.orderRefused(fx.alice, "POST", bid, "cancel", nil)
	code, out = fx.receive(bid, bLine, "5") // PO07: what is receiving counts: 12 + 60 > 60
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "OVER_RECEIPT", out["reason"])
	fx.inv.set("")
	fx.eventually(func() bool { return fx.get("/api/purchase/receipts/" + r["id"].(string))["state"] == "posted" },
		"the work item sends the receipt again")
	o = fx.get("/api/purchase/orders/" + bid)
	assert.Equal(t, "12", at(o, "lines", 0, "received_base_quantity"))
	assert.Equal(t, "0", at(o, "lines", 0, "receiving_base_quantity"))

	// the answer is lost after erp/inventory took it in: its movement event settles the receipt (the story), and
	// sending it again takes in nothing more
	fx.inv.set("lost")
	code, r = fx.receive(bid, bLine, "2")
	require.Contains(t, []int{200, 202}, code, r) // 200 when the movement event came within the synchronous wait
	rid := r["id"].(string)
	// every answer stays lost (the SDK's own retries too): only the movement event can post it
	fx.eventually(func() bool { return fx.get("/api/purchase/receipts/" + rid)["state"] == "posted" },
		"erp/inventory's movement settles the receipt")
	fx.inv.set("")
	fx.eventually(func() bool { return fx.pendingJobs("receive") == 0 }, "every receipt's work item has ended")
	o = fx.get("/api/purchase/orders/" + bid)
	assert.Equal(t, "36", at(o, "lines", 0, "received_base_quantity"), "the movement counts once")

	// erp/inventory refuses: the receipt failed, its basis withdrawn, nothing receiving
	fx.inv.set("refuse")
	code, out = fx.receive(bid, bLine, "1")
	fx.inv.set("")
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "RECEIPT_FAILED", out["reason"])
	failed := fx.get("/api/purchase/receipts/" + at(out, "metadata", "receipt_id").(string))
	assert.Equal(t, "failed", failed["state"])
	assert.Contains(t, failed["reason"], "WAREHOUSE_NOT_USABLE")
	line := at(failed, "lines", 0, "id").(string)
	fx.fact(line, "erp.purchase_receipt.valuation_basis.v1")
	withdrawn := fx.factWhere("erp.purchase_receipt.valuation_basis_withdrawn.v1", "basis_fact_id", line)
	assert.Equal(t, failed["id"], withdrawn["receipt_id"])
	o = fx.get("/api/purchase/orders/" + bid)
	assert.Equal(t, "36", at(o, "lines", 0, "received_base_quantity"))
	assert.Equal(t, "0", at(o, "lines", 0, "receiving_base_quantity"))

	// PO07: Beijing allows no over-receipt; a service line or a draft order is not received
	code, out = fx.receive(bid, bLine, "3") // 36 + 36 > 60
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "OVER_RECEIPT", out["reason"])
	c := fx.approved(fx.le, fx.svc("2", "50"), fx.goods("1", "10"))
	code, out = fx.receive(c["id"].(string), at(c, "lines", 0, "id").(string), "1")
	assert.Equal(t, 400, code, out)
	d := fx.order(fx.le, nil, fx.goods("1", "10"))
	code, out = fx.receive(d["id"].(string), at(d, "lines", 0, "id").(string), "1")
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "STATE_CONFLICT", out["reason"])

	// PO10: with nothing receiving, the order is closed; what was received stays
	b = fx.orderAction(fx.alice, bid, "close", nil, 200)
	assert.Equal(t, "closed", b["state"])
	fx.orders["B"] = b

	// lists: the receipts of an order
	l := fx.get("/api/purchase/receipts?order_id=" + bid)
	assert.Len(t, l["receipts"], 3, l)
	l = fx.get("/api/purchase/receipts?state=failed&order_id=" + bid)
	assert.Len(t, l["receipts"], 1, l)
}

func (fx *fixture) receive(order, line, boxes string) (int, map[string]any) {
	return fx.alice.do("POST", "/api/purchase/orders/"+order+"/receipts", map[string]any{
		"lines": []any{map[string]any{"order_line_id": line, "quantity": boxes}}})
}

func (fx *fixture) eventually(cond func() bool, why string) {
	fx.t.Helper()
	require.Eventually(fx.t, cond, 30*time.Second, 200*time.Millisecond, why)
}

// pendingJobs counts the work items of a job not done yet.
func (fx *fixture) pendingJobs(job string) int {
	var n int
	require.NoError(fx.t, fx.admin.QueryRow(fx.ctx, "SELECT count(*) FROM "+itSchema+".be_work_item WHERE job = $1 AND state <> 'done'",
		job).Scan(&n))
	return n
}

// fact reads the payload of the incremental fact with the ID from the outbox.
func (fx *fixture) fact(id, typ string) map[string]any {
	return fx.factWhere(typ, "fact_id", id)
}

func (fx *fixture) factWhere(typ, key, value string) map[string]any {
	fx.t.Helper()
	var payload []byte
	require.NoError(fx.t, fx.admin.QueryRow(fx.ctx, "SELECT payload FROM "+itSchema+".be_outbox WHERE type = $1 AND "+
		"convert_from(payload, 'UTF8')::jsonb->>'"+key+"' = $2", typ, value).Scan(&payload), "%s with %s %s", typ, key, value)
	var out map[string]any
	require.NoError(fx.t, json.Unmarshal(payload, &out))
	return out
}
