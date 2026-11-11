//go:build integration

package itest

import (
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// returns: goods returned from the receipt movements of the order (PO09), at most what each received and has not
// returned, checked before anything is made (RETURN_EXCEEDS_RECEIPT); each line issued by erp/inventory on its own,
// its refusal giving the quantity back, an unknown answer sent again by the work item; the received quantity falls.
func (fx *fixture) returns() {
	t := fx.t
	a := fx.orders["A"] // completed: 156 EA received in two movements, 60 and 96
	aid := a["id"].(string)
	first, second := fx.movements(aid)
	require.NotEmpty(t, first)

	// PO09: 10 of the first; a completed order is returned from and stays completed
	code, r := fx.giveBack(aid, first, "10")
	require.Equal(t, 200, code, r)
	assert.Regexp(t, `^RT-SH-`+fx.year+`-\d{6}$`, r["number"])
	assert.Equal(t, "posted", at(r, "lines", 0, "state"))
	assert.NotNil(t, at(r, "lines", 0, "movement_id"))
	o := fx.get("/api/purchase/orders/" + aid)
	assert.Equal(t, "146", at(o, "lines", 0, "received_base_quantity"))
	assert.Equal(t, "completed", o["state"])
	f := fx.fact(at(r, "lines", 0, "id").(string), "erp.purchase_return.posted.v1")
	assert.Equal(t, first, f["origin_movement_id"])
	assert.Equal(t, "10", f["base_quantity"])
	assert.Equal(t, r, fx.get("/api/purchase/returns/"+r["id"].(string)))
	code, out := fx.carol.do("GET", "/api/purchase/returns/"+r["id"].(string), nil)
	assert.Equal(t, 404, code, out)

	// PO09: more than the movement has left (50) is refused before anything is made
	before := len(fx.get("/api/purchase/returns?order_id=" + aid)["returns"].([]any))
	code, out = fx.giveBack(aid, first, "51")
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "RETURN_EXCEEDS_RECEIPT", out["reason"])
	assert.Len(t, fx.get("/api/purchase/returns?order_id=" + aid)["returns"], before)

	// erp/inventory refuses a line: failed, its quantity can be returned again (all 50 left)
	fx.inv.set("refuse")
	code, r = fx.giveBack(aid, first, "50")
	fx.inv.set("")
	require.Equal(t, 200, code, r)
	assert.Equal(t, "failed", at(r, "lines", 0, "state"))
	assert.Contains(t, at(r, "lines", 0, "problem"), "RETURN_NOT_POSSIBLE")
	assert.Equal(t, "146", at(fx.get("/api/purchase/orders/"+aid), "lines", 0, "received_base_quantity"))
	code, r = fx.giveBack(aid, first, "50")
	require.Equal(t, 200, code, r)
	assert.Equal(t, "posted", at(r, "lines", 0, "state"))

	// erp/inventory does not answer: the line stays returning (202); the work item sends it again
	fx.inv.set("down")
	code, r = fx.giveBack(aid, second, "1")
	fx.inv.set("")
	require.Equal(t, 202, code, r)
	assert.Equal(t, "returning", at(r, "lines", 0, "state"))
	rid := r["id"].(string)
	fx.eventually(func() bool { return at(fx.get("/api/purchase/returns/"+rid), "lines", 0, "state") == "posted" },
		"the work item sends the return again")
	assert.Equal(t, "95", at(fx.get("/api/purchase/orders/"+aid), "lines", 0, "received_base_quantity"))

	// PO09: a closed order is returned from; a movement of another order is not this order's; a draft has nothing
	b := fx.orders["B"]
	bFirst, _ := fx.movements(b["id"].(string))
	code, r = fx.giveBack(b["id"].(string), bFirst, "12")
	require.Equal(t, 200, code, r)
	code, out = fx.giveBack(aid, bFirst, "1")
	assert.Equal(t, 400, code, out)
	d := fx.order(fx.le, nil, fx.goods("1", "10"))
	code, out = fx.giveBack(d["id"].(string), first, "1")
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "STATE_CONFLICT", out["reason"])
}

// returnLimits: erp/costing's limit on a purchase return (PO09, protocol 05b): after systemSurface, which reads order
// A's returns as returns left them.
func (fx *fixture) returnLimits() {
	t := fx.t
	aid := fx.orders["A"]["id"].(string)
	_, second := fx.movements(aid)
	require.NotEmpty(t, second)
	// PO09 (RT01): erp/costing values by FIFO and 3 units of the second movement's layer are in stock: more is refused
	// before anything is made, two lines on the movement count together; 3 go back; the same request again is the
	// same return, whatever the limit is now; erp/costing not answering does not stop a return (it values the excess as
	// stock below zero, with a warning)
	fx.cost.limit(second, "3")
	before := len(fx.get("/api/purchase/returns?order_id=" + aid)["returns"].([]any))
	code, out := fx.giveBack(aid, second, "4")
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "RETURN_EXCEEDS_COST_LAYER", out["reason"])
	code, out = fx.alice.do("POST", "/api/purchase/orders/"+aid+"/returns", map[string]any{"lines": []any{
		map[string]any{"origin_movement_id": second, "base_quantity": "2"},
		map[string]any{"origin_movement_id": second, "base_quantity": "2"}}})
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "RETURN_EXCEEDS_COST_LAYER", out["reason"])
	assert.Len(t, fx.get("/api/purchase/returns?order_id=" + aid)["returns"], before)
	body := map[string]any{"lines": []any{map[string]any{"origin_movement_id": second, "base_quantity": "3"}}}
	key := ids.New().String()
	code, r := fx.alice.do("POST", "/api/purchase/orders/"+aid+"/returns", body, key)
	require.Equal(t, 200, code, r)
	fx.cost.limit(second, "0")
	code, again := fx.alice.do("POST", "/api/purchase/orders/"+aid+"/returns", body, key)
	require.Equal(t, 200, code, again)
	assert.Equal(t, r["id"], again["id"])
	fx.cost.setDown(true)
	code, r = fx.giveBack(aid, second, "1")
	fx.cost.setDown(false)
	require.Equal(t, 200, code, r)
	assert.Equal(t, "posted", at(r, "lines", 0, "state"))
	assert.Equal(t, "91", at(fx.get("/api/purchase/orders/"+aid), "lines", 0, "received_base_quantity"))

}

// movements are the receipt movements of an order's posted receipts, oldest first (first and second).
func (fx *fixture) movements(order string) (string, string) {
	fx.t.Helper()
	l := fx.get("/api/purchase/receipts?state=posted&order_id=" + order)
	var mvs []string
	rs := l["receipts"].([]any)
	for i := len(rs) - 1; i >= 0; i-- {
		for _, mv := range at(rs[i], "lines", 0, "movement_ids").([]any) {
			mvs = append(mvs, mv.(string))
		}
	}
	mvs = append(mvs, "", "")
	return mvs[0], mvs[1]
}

func (fx *fixture) giveBack(order, movement, qty string) (int, map[string]any) {
	return fx.alice.do("POST", "/api/purchase/orders/"+order+"/returns", map[string]any{"reason": "damaged",
		"lines": []any{map[string]any{"origin_movement_id": movement, "base_quantity": qty}}})
}
