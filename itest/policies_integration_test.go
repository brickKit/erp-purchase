//go:build integration

package itest

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policies: a legal entity's purchase policy starts as "approval required, no over-receipt" (version 0); setting it is
// by version. Beijing's is set to need no approval, which the requisitions and orders after rely on (PR02, PO03).
func (fx *fixture) policies() {
	t := fx.t
	p := fx.get("/api/purchase/policies/" + fx.le)
	assert.Equal(t, map[string]any{"legal_entity_id": fx.le, "approval": "required", "over_receipt_percent": "0",
		"version": float64(0)}, p)

	code, p := fx.alice.do("PUT", "/api/purchase/policies/"+fx.le, map[string]any{"approval": "required",
		"over_receipt_percent": "10", "version": 0})
	require.Equal(t, 200, code, p)
	assert.Equal(t, "10", p["over_receipt_percent"])
	assert.Equal(t, float64(1), p["version"])
	assert.Equal(t, p, fx.get("/api/purchase/policies/"+fx.le))

	code, p = fx.alice.do("PUT", "/api/purchase/policies/"+fx.le, map[string]any{"approval": "required",
		"over_receipt_percent": "5", "version": 0})
	assert.Equal(t, 409, code, p)
	assert.Equal(t, "VERSION_CONFLICT", p["reason"])
	code, p = fx.alice.do("PUT", "/api/purchase/policies/"+fx.le, map[string]any{"approval": "required",
		"over_receipt_percent": "5", "version": 7})
	assert.Equal(t, 409, code, p)
	assert.Equal(t, "VERSION_CONFLICT", p["reason"])
	assert.Equal(t, "10", fx.get("/api/purchase/policies/" + fx.le)["over_receipt_percent"])
	for _, bad := range []map[string]any{{"approval": "sometimes", "over_receipt_percent": "0"},
		{"approval": "none", "over_receipt_percent": "-1"}, {"approval": "none", "over_receipt_percent": "1.23456"}} {
		code, p = fx.alice.do("PUT", "/api/purchase/policies/"+fx.le, bad)
		assert.Equal(t, 400, code, "%v: %v", bad, p)
	}
	code, p = fx.carol.do("GET", "/api/purchase/policies/"+fx.le, nil)
	assert.Equal(t, 404, code, p)

	code, p = fx.alice.do("PUT", "/api/purchase/policies/"+fx.le2, map[string]any{"approval": "none",
		"over_receipt_percent": "0"})
	require.Equal(t, 200, code, p)
	assert.Equal(t, "none", p["approval"])
}
