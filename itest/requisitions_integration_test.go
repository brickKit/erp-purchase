//go:build integration

package itest

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requisitions: a requisition's life (docs/design.md, "申请的状态"; PR01-PR05); approved ones stay in fx.reqs for
// the orders (PR06).
func (fx *fixture) requisitions() {
	t := fx.t
	// PR01: a draft, the products' eligibility to purchase taken outside the transaction
	r := fx.requisition(fx.le, &fx.d1, fx.screwLine("2"), fx.serviceLine("3"))
	assert.Equal(t, "draft", r["state"])
	assert.Regexp(t, `^PR-SH-`+fx.year+`-\d{6}$`, r["number"])
	assert.Equal(t, sectestAlice, r["requester_id"])
	assert.Equal(t, fx.d1, r["department_id"])
	assert.Equal(t, map[string]any{"code": "SCREW", "name": "螺丝", "unit": "EA"}, at(r, "lines", 0, "product"))
	assert.Equal(t, "2", at(r, "lines", 0, "quantity"))
	assert.Equal(t, "BX", at(r, "lines", 0, "unit"))
	assert.Equal(t, "0", at(r, "lines", 0, "ordered_quantity"))
	assert.Equal(t, r, fx.get("/api/purchase/requisitions/"+r["id"].(string)))
	code, out := fx.carol.do("GET", "/api/purchase/requisitions/"+r["id"].(string), nil)
	assert.Equal(t, 404, code, out) // what one may not see does not exist (Carol reads Beijing's only)

	// PR01: what may not be bought, or does not convert, or the department of another legal entity, is refused
	for _, c := range []struct {
		body   map[string]any
		reason string
	}{
		{fx.reqBody(fx.le, nil, map[string]any{"product_id": fx.sup, "quantity": "1", "unit": "EA"}), "NOT_ELIGIBLE"},
		{fx.reqBody(fx.le, nil, map[string]any{"product_id": fx.screw, "quantity": "1", "unit": "KG"}), "NO_CONVERSION"},
		{fx.reqBody(fx.le2, &fx.d1, fx.screwLine("1")), "DEPARTMENT_NOT_USABLE"},
	} {
		code, out := fx.alice.do("POST", "/api/purchase/requisitions", c.body)
		assert.Equal(t, 409, code, out)
		assert.Equal(t, c.reason, out["reason"], out)
	}
	code, out = fx.alice.do("POST", "/api/purchase/requisitions", fx.reqBody(fx.le, nil,
		map[string]any{"product_id": fx.screw, "quantity": "-1", "unit": "EA"}))
	assert.Equal(t, 400, code, out)

	// PR01: a draft changes, by version; its lines are replaced whole
	id := r["id"].(string)
	code, out = fx.alice.do("PATCH", "/api/purchase/requisitions/"+id, map[string]any{"legal_entity_id": fx.le,
		"department_id": fx.d2, "version": 1, "lines": []any{fx.screwLine("5")}})
	require.Equal(t, 200, code, out)
	assert.Equal(t, float64(2), out["version"])
	assert.Equal(t, fx.d2, out["department_id"])
	assert.Len(t, out["lines"], 1)
	assert.Equal(t, "5", at(out, "lines", 0, "quantity"))
	code, out = fx.alice.do("PATCH", "/api/purchase/requisitions/"+id, map[string]any{"legal_entity_id": fx.le,
		"version": 1, "lines": []any{fx.screwLine("6")}})
	assert.Equal(t, 409, code, out)
	assert.Equal(t, "VERSION_CONFLICT", out["reason"])

	// PR02: submitted; then it no longer changes (PR01) nor is submitted again (PR02)
	r = fx.reqAction(fx.alice, id, "submit", nil, 200)
	assert.Equal(t, "submitted", r["state"])
	fx.reqRefused(fx.alice, "PATCH", id, "", map[string]any{"legal_entity_id": fx.le, "lines": []any{fx.screwLine("7")}})
	fx.reqRefused(fx.alice, "POST", id, "submit", nil)

	// PR03: not approved by its submitter; approved by another; approving again answers it as it is
	code, out = fx.alice.do("POST", "/api/purchase/requisitions/"+id+"/approve", nil)
	assert.Equal(t, 403, code, out) // the flow kernel's check by person (P17.10; 3.0.7, before: APPROVER_IS_SUBMITTER)
	assert.Equal(t, "SELF_APPROVAL", out["reason"])
	r = fx.reqAction(fx.bob, id, "approve", nil, 200)
	assert.Equal(t, "approved", r["state"])
	again := fx.reqAction(fx.bob, id, "approve", nil, 200)
	assert.Equal(t, r["version"], again["version"])
	fx.reqRefused(fx.bob, "POST", id, "reject", map[string]any{"reason": "too late"})
	fx.reqRefused(fx.alice, "PATCH", id, "", map[string]any{"legal_entity_id": fx.le, "lines": []any{fx.screwLine("7")}})
	fx.reqs["A"] = r

	// PR04: rejected with a reason; a rejected requisition is not changed, approved or cancelled
	b := fx.requisition(fx.le, nil, fx.serviceLine("1"))
	bid := b["id"].(string)
	fx.reqRefused(fx.bob, "POST", bid, "approve", nil)
	fx.reqRefused(fx.bob, "POST", bid, "reject", map[string]any{"reason": "draft"})
	fx.reqAction(fx.alice, bid, "submit", nil, 200)
	code, out = fx.bob.do("POST", "/api/purchase/requisitions/"+bid+"/reject", map[string]any{})
	assert.Equal(t, 400, code, out)
	b = fx.reqAction(fx.bob, bid, "reject", map[string]any{"reason": "not this quarter"}, 200)
	assert.Equal(t, "rejected", b["state"])
	assert.Equal(t, "not this quarter", b["reason"])
	fx.reqAction(fx.bob, bid, "reject", map[string]any{"reason": "again"}, 200) // a repeat answers it as it is
	fx.reqRefused(fx.alice, "PATCH", bid, "", map[string]any{"legal_entity_id": fx.le, "lines": []any{fx.screwLine("1")}})
	fx.reqRefused(fx.bob, "POST", bid, "approve", nil)
	fx.reqRefused(fx.alice, "POST", bid, "cancel", nil)
	fx.reqRefused(fx.alice, "POST", bid, "submit", nil)

	// PR05: a draft, a submitted and an approved requisition nothing was ordered from are cancelled; again answers it
	for _, path := range [][]string{{}, {"submit"}, {"submit", "approve"}} {
		c := fx.requisition(fx.le, nil, fx.screwLine("1"))
		cid := c["id"].(string)
		for _, a := range path {
			who := fx.alice
			if a == "approve" {
				who = fx.bob
			}
			fx.reqAction(who, cid, a, nil, 200)
		}
		c = fx.reqAction(fx.alice, cid, "cancel", nil, 200)
		assert.Equal(t, "cancelled", c["state"], path)
		fx.reqAction(fx.alice, cid, "cancel", nil, 200)
		fx.reqRefused(fx.alice, "POST", cid, "submit", nil)
		fx.reqRefused(fx.bob, "POST", cid, "approve", nil)
		fx.reqRefused(fx.alice, "PATCH", cid, "", map[string]any{"legal_entity_id": fx.le, "lines": []any{fx.screwLine("1")}})
	}

	// PR02: Beijing's policy needs no approval: submitting approves it, its submitter the approver
	d := fx.requisition(fx.le2, nil, fx.screwLine("4"))
	d = fx.reqAction(fx.alice, d["id"].(string), "submit", nil, 200)
	assert.Equal(t, "approved", d["state"])
	assert.Regexp(t, `^PR-BJ-`, d["number"])
	fx.reqs["D"] = d

	// lists: by state and by number, newest first
	l := fx.get("/api/purchase/requisitions?state=approved&legal_entity_id=" + fx.le)
	require.Len(t, l["requisitions"], 1, l)
	assert.Equal(t, id, at(l, "requisitions", 0, "id"))
	l = fx.get("/api/purchase/requisitions?q=" + d["number"].(string))
	require.Len(t, l["requisitions"], 1, l)
	assert.Equal(t, d["id"], at(l, "requisitions", 0, "id"))
	code, l = fx.carol.do("GET", "/api/purchase/requisitions", nil)
	require.Equal(t, 200, code, l)
	require.Len(t, l["requisitions"], 1, "Carol lists Beijing's only")
	assert.Equal(t, d["id"], at(l, "requisitions", 0, "id"))
	assert.Equal(t, d, fx.carolGet("/api/purchase/requisitions/"+d["id"].(string)))
}

func (fx *fixture) carolGet(path string) map[string]any {
	fx.t.Helper()
	code, out := fx.carol.do("GET", path, nil)
	require.Equal(fx.t, 200, code, out)
	return out
}

const sectestAlice = "0190f3a1-0000-7000-8000-0000000000a1"

func (fx *fixture) screwLine(boxes string) map[string]any {
	return map[string]any{"product_id": fx.screw, "quantity": boxes, "unit": "BX", "needed_date": "2026-12-01"}
}

func (fx *fixture) serviceLine(hours string) map[string]any {
	return map[string]any{"product_id": fx.install, "quantity": hours, "unit": "HUR"}
}

func (fx *fixture) reqBody(le string, dept *string, lines ...map[string]any) map[string]any {
	ls := make([]any, len(lines))
	for i, l := range lines {
		ls[i] = l
	}
	b := map[string]any{"legal_entity_id": le, "lines": ls}
	if dept != nil {
		b["department_id"] = *dept
	}
	return b
}

// requisition creates a draft requisition.
func (fx *fixture) requisition(le string, dept *string, lines ...map[string]any) map[string]any {
	fx.t.Helper()
	code, r := fx.alice.do("POST", "/api/purchase/requisitions", fx.reqBody(le, dept, lines...))
	require.Equal(fx.t, 200, code, r)
	return r
}

func (fx *fixture) reqAction(c client, id, action string, body map[string]any, want int) map[string]any {
	fx.t.Helper()
	code, out := c.do("POST", "/api/purchase/requisitions/"+id+"/"+action, body)
	require.Equal(fx.t, want, code, out)
	return out
}

// reqRefused: the command is refused by the requisition's state.
func (fx *fixture) reqRefused(c client, method, id, action string, body map[string]any) {
	fx.t.Helper()
	path := "/api/purchase/requisitions/" + id
	if action != "" {
		path += "/" + action
	}
	code, out := c.do(method, path, body)
	assert.Equal(fx.t, 409, code, "%s %s: %v", method, path, out)
	assert.Equal(fx.t, "STATE_CONFLICT", out["reason"], "%s %s", method, path)
}
