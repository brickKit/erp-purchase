//go:build integration

package itest

import (
	"maps"
	"time"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/events"
	core "github.com/brickKit/erp-purchase/v3/internal/core"
	assert "github.com/stretchr/testify/assert"
	require "github.com/stretchr/testify/require"
)

// departments: Dave reads the orders and requisitions of department 1 and below; department 2 moves under it and its
// documents follow (security 07: the copied department path, rewritten by the department's event).
func (fx *fixture) departments() {
	t := fx.t
	inD1 := fx.order(fx.le, &fx.d1, fx.svc("1", "1"))["id"]
	inD2 := fx.order(fx.le, &fx.d2, fx.svc("1", "1"))["id"]
	reqD2 := fx.requisition(fx.le, &fx.d2, fx.serviceLine("1"))["id"]
	tree := map[string]sdkauthz.Atom{core.DimLegalEntity: {Kind: "all"}, core.DimOwner: {Kind: "all"},
		core.DimDepartment: {Kind: "tree", Items: []sdkauthz.TreeItem{{Path: fx.le + "/" + fx.d1, Mode: "subtree"}}}}
	fx.w.Grant(dave, sdkauthz.Segment{ResourceType: core.Orders, Operation: core.OpOrderRead, ReadFields: core.OrderFields,
		Atoms: tree, Valid: sdkauthz.Window{From: "2026-01-01T00:00:00Z"}},
		sdkauthz.Segment{ResourceType: core.Requisitions, Operation: core.OpReqRead, ReadFields: core.RequisitionFields,
			Atoms: tree, Valid: sdkauthz.Window{From: "2026-01-01T00:00:00Z"}})
	d := client{t, fx.w.Bearer(dave)}
	seen := func(path, key string) map[any]bool {
		_, page := d.do("GET", path, nil)
		out := map[any]bool{}
		listed, _ := page[key].([]any)
		for _, o := range listed {
			out[at(o, "id")] = true
		}
		return out
	}
	orders := func() map[any]bool { return seen("/api/purchase/orders", "orders") }
	reqs := func() map[any]bool { return seen("/api/purchase/requisitions", "requisitions") }
	require.Eventually(t, func() bool { return orders()[inD1] }, 30*time.Second, 200*time.Millisecond)
	assert.False(t, orders()[inD2], "department 2 is outside Dave's tree")
	assert.False(t, reqs()[reqD2], "department 2 is outside Dave's tree")
	code, _ := d.do("GET", "/api/purchase/orders/"+inD2.(string), nil)
	assert.Equal(t, 404, code)
	moved := fx.unit(fx.d2, fx.le+"/"+fx.d1+"/"+fx.d2, 2)
	moved["parent_id"] = fx.d1
	fx.org.mu.Lock()
	fx.org.units[fx.d2] = moved
	fx.org.mu.Unlock()
	fx.publishOrgUnit(moved)
	require.Eventually(t, func() bool { return orders()[inD2] && reqs()[reqD2] }, 30*time.Second, 200*time.Millisecond,
		"the department's move rewrites the copied path of orders and requisitions")
	fx.laterOrgEvents(moved)
}

// dave is a user who reads the orders and requisitions of department 1's tree.
const dave = "0190f3a1-0000-7000-8000-0000000000d4"

// laterOrgEvents: mdm/org later publishes an action this version does not know and keeps publishing its legal
// entities on the same stream (protocol P6.33, protocol 02 PJ36). The unknown action marks the department incomplete
// and fetches it, without applying a payload whose shape it does not know; the legal entity is not a department.
func (fx *fixture) laterOrgEvents(unit map[string]any) {
	t := fx.t
	renamed := maps.Clone(unit)
	renamed["name"], renamed["version"] = "改名后的部门", int64(3)
	fx.org.mu.Lock()
	fx.org.units[fx.d2] = renamed
	fx.org.mu.Unlock()
	fx.publishOrg("mdm.org_unit.renamed.v1", fx.d2, 3, events.Full, []byte(`{"unit_id":"`+fx.d2+`","new_name":"x"}`))
	fx.publishOrg("mdm.legal_entity.changed.v1", fx.le, 7, events.Full, []byte(`{"id":"`+fx.le+`","name":"法人"}`))
	row := func(id string) (state string, version int64, name string) {
		_ = fx.admin.QueryRow(fx.ctx, "SELECT state, version, coalesce(data->>'name', '') FROM "+itSchema+
			".be_projection WHERE projection = $1 AND record_id = $2", core.OrgUnitsProject, id).Scan(&state, &version, &name)
		return
	}
	require.Eventually(t, func() bool {
		state, version, name := row(fx.d2)
		return state == "complete" && version == 3 && name == "改名后的部门"
	}, 30*time.Second, 200*time.Millisecond, "the unknown action is fetched from mdm/org, not taken from its payload")
	state, _, _ := row(fx.le)
	assert.Empty(t, state, "a legal entity's event is not a department")
	var dead int
	require.NoError(t, fx.admin.QueryRow(fx.ctx, "SELECT count(*) FROM "+itSchema+".be_dead_letter").Scan(&dead))
	assert.Zero(t, dead, "no event of the stream is dead-lettered")
}
