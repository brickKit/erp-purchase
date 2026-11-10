//go:build integration

package itest

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acceptances: service lines accepted by quantity or amount (PO08), within the over-receipt percent; the acceptance
// fact for the match basis and the period-end accrual (protocol 05); an order accepted whole completes (PO12).
func (fx *fixture) acceptances() {
	t := fx.t
	c := fx.approved(fx.le, fx.svc("2", "50"), fx.goods("1", "10")) // Shanghai, 10% over allowed
	cid, svcLine, goodsLine := c["id"].(string), at(c, "lines", 0, "id").(string), at(c, "lines", 1, "id").(string)

	// PO08: by quantity, the amount at the unit price; then by amount alone
	code, a := fx.accept(cid, map[string]any{"order_line_id": svcLine, "quantity": "1"})
	require.Equal(t, 200, code, a)
	assert.Regexp(t, `^SA-SH-`+fx.year+`-\d{6}$`, a["number"])
	assert.Equal(t, sectestAlice, a["accepted_by"])
	assert.Equal(t, "50", at(a, "lines", 0, "amount"))
	f := fx.fact(at(a, "lines", 0, "id").(string), "erp.purchase_acceptance.recorded.v1")
	assert.Equal(t, "1", f["quantity"])
	assert.Equal(t, "50", f["amount"])
	assert.Equal(t, "CNY", f["currency"])
	code, a = fx.accept(cid, map[string]any{"order_line_id": svcLine, "amount": "30"})
	require.Equal(t, 200, code, a)
	assert.Equal(t, "0", at(a, "lines", 0, "quantity"))
	o := fx.get("/api/purchase/orders/" + cid)
	assert.Equal(t, "1", at(o, "lines", 0, "accepted_quantity"))
	assert.Equal(t, "80", at(o, "lines", 0, "accepted_amount"))
	assert.Equal(t, a, fx.get("/api/purchase/acceptances/"+a["id"].(string)))
	code, out := fx.carol.do("GET", "/api/purchase/acceptances/"+a["id"].(string), nil)
	assert.Equal(t, 404, code, out)

	// PO08: within 10% over, by quantity (1 + 1.5 > 2.2, the amount within) and by amount (80 + 40 > 110); a goods line
	// is received, a line needs a quantity or an amount
	for _, l := range []map[string]any{{"order_line_id": svcLine, "quantity": "1.5", "amount": "1"},
		{"order_line_id": svcLine, "amount": "40"}} {
		code, out := fx.accept(cid, l)
		assert.Equal(t, 409, code, out)
		assert.Equal(t, "OVER_RECEIPT", out["reason"], l)
	}
	for _, l := range []map[string]any{{"order_line_id": goodsLine, "quantity": "1"}, {"order_line_id": svcLine}} {
		code, out := fx.accept(cid, l)
		assert.Equal(t, 400, code, "%v: %v", l, out)
	}
	// PO11: an order with an acceptance is closed, not cancelled
	fx.orderRefused(fx.alice, "POST", cid, "cancel", nil)

	// PO08: only an approved order; PO12: a service order accepted whole completes, then accepts no more
	d := fx.order(fx.le, nil, fx.svc("1", "1"))
	code, out = fx.accept(d["id"].(string), map[string]any{"order_line_id": at(d, "lines", 0, "id"), "quantity": "1"})
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "STATE_CONFLICT", out["reason"])
	e := fx.approved(fx.le, fx.svc("3", "20"))
	eid, eLine := e["id"].(string), at(e, "lines", 0, "id").(string)
	code, out = fx.accept(eid, map[string]any{"order_line_id": eLine, "quantity": "3"})
	require.Equal(t, 200, code, out)
	assert.Equal(t, "completed", fx.get("/api/purchase/orders/" + eid)["state"])
	code, out = fx.accept(eid, map[string]any{"order_line_id": eLine, "quantity": "0.1"})
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "STATE_CONFLICT", out["reason"])
	fx.orders["C"] = fx.get("/api/purchase/orders/" + cid)
}

func (fx *fixture) accept(order string, lines ...map[string]any) (int, map[string]any) {
	ls := make([]any, len(lines))
	for i, l := range lines {
		ls[i] = l
	}
	return fx.alice.do("POST", "/api/purchase/orders/"+order+"/acceptances", map[string]any{"lines": ls})
}
