//go:build integration

package itest

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// purchaseOrders: an order's life up to its approval, its revision, closing and cancelling (docs/design.md, "订单的状态";
// PO01-PO06, PO10, PO11), and requisition lines turned into order lines (PR06). Approved orders stay in fx.orders for
// the receipts.
func (fx *fixture) purchaseOrders() {
	t := fx.t
	// PO01: a draft; the supplier's and each product's eligibility to purchase taken outside the transaction
	o := fx.order(fx.le, &fx.d1, fx.goods("10", "120"), fx.svc("3", "100.5"))
	assert.Equal(t, "draft", o["state"])
	assert.Regexp(t, `^PO-SH-`+fx.year+`-\d{6}$`, o["number"])
	assert.Equal(t, float64(1), o["revision"])
	assert.Equal(t, map[string]any{"name": "上海供应商", "tax_id": "91310000-" + fx.sup[:4]}, o["supplier"])
	assert.Equal(t, sectestAlice, o["owner_id"])
	assert.Equal(t, fx.d1, o["department_id"])
	assert.Equal(t, "goods", at(o, "lines", 0, "kind"))
	assert.Equal(t, "120", at(o, "lines", 0, "base_quantity")) // 10 boxes of 12
	assert.Equal(t, "1200", at(o, "lines", 0, "amount"))
	assert.Equal(t, "service", at(o, "lines", 1, "kind"))
	assert.Nil(t, at(o, "lines", 1, "warehouse_id"))
	assert.Equal(t, "301.5", at(o, "lines", 1, "amount"))
	assert.Equal(t, "1501.5", o["total"])
	assert.Equal(t, "0", at(o, "lines", 0, "received_base_quantity"))
	assert.Equal(t, o, fx.get("/api/purchase/orders/"+o["id"].(string)))
	code, out := fx.carol.do("GET", "/api/purchase/orders/"+o["id"].(string), nil)
	assert.Equal(t, 404, code, out)

	// PO01: a suspended supplier, an unknown currency are refused; a goods line needs its warehouse
	for _, c := range []struct {
		body   map[string]any
		code   int
		reason string
	}{
		{fx.orderBody(fx.le, nil, fx.blocked, "CNY", fx.goods("1", "1")), 409, "NOT_ELIGIBLE"},
		{fx.orderBody(fx.le, nil, fx.sup, "XXX", fx.goods("1", "1")), 409, "CURRENCY_NOT_USABLE"},
		{fx.orderBody(fx.le, nil, fx.sup, "CNY", map[string]any{"product_id": fx.screw, "quantity": "1", "unit": "BX",
			"unit_price": "1"}), 400, "REQUEST_INVALID"},
	} {
		code, out := fx.alice.do("POST", "/api/purchase/orders", c.body)
		assert.Equal(t, c.code, code, out)
		assert.Equal(t, c.reason, out["reason"], out)
	}

	// PO02: a draft changes by version, its lines replaced whole; PO03: submitted, then it no longer changes
	id := o["id"].(string)
	code, out = fx.alice.do("PATCH", "/api/purchase/orders/"+id, fx.with(fx.orderBody(fx.le, &fx.d1, fx.sup, "CNY",
		fx.goods("10", "110"), fx.svc("3", "100.5")), "version", 1))
	require.Equal(t, 200, code, out)
	assert.Equal(t, "1401.5", out["total"])
	code, out = fx.alice.do("PATCH", "/api/purchase/orders/"+id, fx.with(fx.orderBody(fx.le, &fx.d1, fx.sup, "CNY",
		fx.goods("10", "100")), "version", 1))
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "VERSION_CONFLICT", out["reason"])
	o = fx.orderAction(fx.alice, id, "submit", nil, 200)
	assert.Equal(t, "submitted", o["state"])
	assert.Equal(t, sectestAlice, o["submitted_by"])
	fx.orderRefused(fx.alice, "PATCH", id, "", fx.orderBody(fx.le, nil, fx.sup, "CNY", fx.goods("1", "1")))
	fx.orderRefused(fx.alice, "POST", id, "submit", nil)
	fx.orderRefused(fx.alice, "POST", id, "revise", fx.orderBody(fx.le, nil, fx.sup, "CNY", fx.goods("1", "1")))

	// PO05: rejected back to draft with its reason; PO04: approved, not by its submitter; again answers it as it is
	code, out = fx.bob.do("POST", "/api/purchase/orders/"+id+"/reject", map[string]any{})
	assert.Equal(t, 400, code, out)
	o = fx.orderAction(fx.bob, id, "reject", map[string]any{"reason": "price too high"}, 200)
	assert.Equal(t, "draft", o["state"])
	assert.Equal(t, "price too high", o["reason"])
	fx.orderRefused(fx.bob, "POST", id, "reject", map[string]any{"reason": "again"})
	fx.orderRefused(fx.bob, "POST", id, "approve", nil)
	fx.orderRefused(fx.alice, "POST", id, "close", nil)
	fx.orderAction(fx.alice, id, "submit", nil, 200)
	code, out = fx.alice.do("POST", "/api/purchase/orders/"+id+"/approve", nil)
	assert.Equal(t, 403, code, out) // the flow kernel's check by person (P17.10; 3.0.7, before: APPROVER_IS_SUBMITTER)
	assert.Equal(t, "SELF_APPROVAL", out["reason"])
	o = fx.orderAction(fx.bob, id, "approve", nil, 200)
	assert.Equal(t, "approved", o["state"])
	assert.Equal(t, "0190f3a1-0000-7000-8000-0000000000b2", o["approved_by"])
	assert.Equal(t, o["version"], fx.orderAction(fx.bob, id, "approve", nil, 200)["version"])
	fx.orderRefused(fx.bob, "POST", id, "reject", map[string]any{"reason": "late"})
	fx.orderRefused(fx.alice, "PATCH", id, "", fx.orderBody(fx.le, nil, fx.sup, "CNY", fx.goods("1", "1")))
	fx.orderRefused(fx.alice, "POST", id, "submit", nil)

	// PO06: a revision keeps a line by its ID and drops one with no progress; back to submitted, revision 2; the
	// supplier, the currency and a kept line's product stay
	screwLine := at(o, "lines", 0, "id").(string)
	kept := fx.goods("12", "100")
	kept["id"] = screwLine
	code, out = fx.alice.do("POST", "/api/purchase/orders/"+id+"/revise", fx.orderBody(fx.le, &fx.d1, fx.blocked, "CNY", kept))
	assert.Equal(t, 400, code, out)
	moved := fx.svc("1", "1")
	moved["id"] = screwLine
	code, out = fx.alice.do("POST", "/api/purchase/orders/"+id+"/revise", fx.orderBody(fx.le, &fx.d1, fx.sup, "CNY", moved))
	assert.Equal(t, 400, code, out)
	o = fx.orderAction(fx.alice, id, "revise", fx.with(fx.orderBody(fx.le, &fx.d1, fx.sup, "CNY", kept), "version", o["version"]), 200)
	assert.Equal(t, "submitted", o["state"])
	assert.Equal(t, float64(2), o["revision"])
	require.Len(t, o["lines"], 1)
	assert.Equal(t, screwLine, at(o, "lines", 0, "id"))
	assert.Equal(t, "144", at(o, "lines", 0, "base_quantity"))
	assert.Equal(t, "1200", o["total"])
	o = fx.orderAction(fx.bob, id, "approve", nil, 200)
	fx.orders["A"] = o

	// PO03: Beijing's policy needs no approval: submitting approves it, its submitter the approver
	b := fx.order(fx.le2, nil, fx.goods("5", "10"))
	b = fx.orderAction(fx.alice, b["id"].(string), "submit", nil, 200)
	assert.Equal(t, "approved", b["state"])
	assert.Equal(t, sectestAlice, b["approved_by"])
	assert.Regexp(t, `^PO-BJ-`, b["number"])
	fx.orders["B"] = b

	// PO10: an approved order is closed; again answers it as it is; then nothing changes it
	c := fx.approved(fx.le, fx.svc("2", "50"))
	cid := c["id"].(string)
	c = fx.orderAction(fx.alice, cid, "close", nil, 200)
	assert.Equal(t, "closed", c["state"])
	fx.orderAction(fx.alice, cid, "close", nil, 200)
	fx.orderRefused(fx.alice, "POST", cid, "cancel", nil)
	fx.orderRefused(fx.alice, "POST", cid, "revise", fx.orderBody(fx.le, nil, fx.sup, "CNY", fx.svc("2", "50")))
	fx.orderRefused(fx.bob, "POST", cid, "approve", nil)

	// PO11: a draft, a submitted and an approved order with nothing received are cancelled; again answers it
	for _, path := range [][]string{{}, {"submit"}, {"submit", "approve"}} {
		d := fx.order(fx.le, nil, fx.svc("1", "1"))
		did := d["id"].(string)
		for _, a := range path {
			who := fx.alice
			if a == "approve" {
				who = fx.bob
			}
			fx.orderAction(who, did, a, nil, 200)
		}
		d = fx.orderAction(fx.alice, did, "cancel", nil, 200)
		assert.Equal(t, "cancelled", d["state"], path)
		fx.orderAction(fx.alice, did, "cancel", nil, 200)
		fx.orderRefused(fx.alice, "POST", did, "submit", nil)
		fx.orderRefused(fx.alice, "POST", did, "close", nil)
		fx.orderRefused(fx.alice, "PATCH", did, "", fx.orderBody(fx.le, nil, fx.sup, "CNY", fx.svc("1", "1")))
	}

	// lists: by supplier and state, by number
	l := fx.get("/api/purchase/orders?state=closed&supplier_id=" + fx.sup)
	require.Len(t, l["orders"], 1, l)
	assert.Equal(t, cid, at(l, "orders", 0, "id"))
	l = fx.get("/api/purchase/orders?supplier_id=" + fx.blocked)
	assert.Empty(t, l["orders"], "no order of the suspended supplier")
	l = fx.get("/api/purchase/orders?q=" + b["number"].(string))
	require.Len(t, l["orders"], 1, l)
	assert.Equal(t, b["id"], at(l, "orders", 0, "id"))
}

// fromRequisitions: approved requisition lines are turned into order lines (PR06): a line may go into several orders,
// never more than it asks; all ordered closes the requisition, a cancelled order or a dropped line gives back
// (PR05: an approved requisition something was ordered from is not cancelled).
func (fx *fixture) fromRequisitions() {
	t := fx.t
	r := fx.reqs["A"] // 5 boxes of screws, approved
	rid, line := r["id"].(string), at(r, "lines", 0, "id").(string)
	from := func(qty string) map[string]any {
		g := fx.goods(qty, "100")
		g["requisition_line_id"] = line
		return g
	}
	first := fx.order(fx.le, nil, from("3"))
	assert.Equal(t, line, at(first, "lines", 0, "requisition_line_id"))
	r = fx.get("/api/purchase/requisitions/" + rid)
	assert.Equal(t, "3", at(r, "lines", 0, "ordered_quantity"))
	assert.Equal(t, "approved", r["state"])
	fx.reqRefused(fx.alice, "POST", rid, "cancel", nil)

	// a draft order's change gives back what it took and takes again
	code, out := fx.alice.do("PATCH", "/api/purchase/orders/"+first["id"].(string), fx.orderBody(fx.le, nil, fx.sup, "CNY", from("4")))
	require.Equal(t, 200, code, out)
	assert.Equal(t, "4", at(fx.get("/api/purchase/requisitions/"+rid), "lines", 0, "ordered_quantity"))

	// more than is left is refused; the rest closes the requisition
	code, out = fx.alice.do("POST", "/api/purchase/orders", fx.orderBody(fx.le, nil, fx.sup, "CNY", from("2")))
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "REQUISITION_LINE_NOT_USABLE", out["reason"])
	second := fx.order(fx.le, nil, from("1"))
	r = fx.get("/api/purchase/requisitions/" + rid)
	assert.Equal(t, "closed", r["state"])
	assert.Equal(t, "5", at(r, "lines", 0, "ordered_quantity"))
	fx.reqRefused(fx.alice, "POST", rid, "cancel", nil)

	// a cancelled order gives back: the requisition is approved again
	fx.orderAction(fx.alice, second["id"].(string), "cancel", nil, 200)
	r = fx.get("/api/purchase/requisitions/" + rid)
	assert.Equal(t, "approved", r["state"])
	assert.Equal(t, "4", at(r, "lines", 0, "ordered_quantity"))

	// a line of a requisition not approved, or of another legal entity, or another product, is refused
	draft := fx.requisition(fx.le, nil, fx.screwLine("1"))
	bj := fx.reqs["D"]
	for _, g := range []map[string]any{
		fx.with(fx.goods("1", "1"), "requisition_line_id", at(draft, "lines", 0, "id")),
		fx.with(fx.goods("1", "1"), "requisition_line_id", at(bj, "lines", 0, "id")),
		fx.with(fx.svc("1", "1"), "requisition_line_id", line),
		fx.with(fx.with(fx.goods("1", "1"), "product_id", fx.bolt), "requisition_line_id", line), // same unit, another product
	} {
		code, out := fx.alice.do("POST", "/api/purchase/orders", fx.orderBody(fx.le, nil, fx.sup, "CNY", g))
		assert.Equal(t, 409, code, out)
		assert.Equal(t, "REQUISITION_LINE_NOT_USABLE", out["reason"], g)
	}
}

func (fx *fixture) goods(boxes, price string) map[string]any {
	return map[string]any{"product_id": fx.screw, "quantity": boxes, "unit": "BX", "unit_price": price, "warehouse_id": fx.wh}
}

func (fx *fixture) svc(hours, price string) map[string]any {
	return map[string]any{"product_id": fx.install, "quantity": hours, "unit": "HUR", "unit_price": price}
}

func (fx *fixture) with(m map[string]any, k string, v any) map[string]any {
	out := map[string]any{k: v}
	for kk, vv := range m {
		if kk != k {
			out[kk] = vv
		}
	}
	return out
}

func (fx *fixture) orderBody(le string, dept *string, supplier, currency string, lines ...map[string]any) map[string]any {
	ls := make([]any, len(lines))
	for i, l := range lines {
		ls[i] = l
	}
	b := map[string]any{"legal_entity_id": le, "supplier_id": supplier, "currency": currency, "lines": ls}
	if dept != nil {
		b["department_id"] = *dept
	}
	return b
}

// order creates a draft order of the supplier in CNY.
func (fx *fixture) order(le string, dept *string, lines ...map[string]any) map[string]any {
	fx.t.Helper()
	code, o := fx.alice.do("POST", "/api/purchase/orders", fx.orderBody(le, dept, fx.sup, "CNY", lines...))
	require.Equal(fx.t, 200, code, o)
	return o
}

// approved creates an order Alice submits and Bob approves.
func (fx *fixture) approved(le string, lines ...map[string]any) map[string]any {
	fx.t.Helper()
	o := fx.order(le, nil, lines...)
	fx.orderAction(fx.alice, o["id"].(string), "submit", nil, 200)
	return fx.orderAction(fx.bob, o["id"].(string), "approve", nil, 200)
}

func (fx *fixture) orderAction(c client, id, action string, body map[string]any, want int) map[string]any {
	fx.t.Helper()
	code, out := c.do("POST", "/api/purchase/orders/"+id+"/"+action, body)
	require.Equal(fx.t, want, code, out)
	return out
}

func (fx *fixture) orderRefused(c client, method, id, action string, body map[string]any) {
	fx.t.Helper()
	path := "/api/purchase/orders/" + id
	if action != "" {
		path += "/" + action
	}
	code, out := c.do(method, path, body)
	assert.Equal(fx.t, 409, code, "%s %s: %v", method, path, out)
	assert.Equal(fx.t, "STATE_CONFLICT", out["reason"], "%s %s", method, path)
}
