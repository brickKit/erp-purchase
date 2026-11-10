//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/recoverypb"
	purchasev3 "github.com/brickKit/erp-purchase/contract/v3/gen/go/be/erp/purchase/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// systemSurface: the match basis for erp/finance (D50, protocol 05) and the incremental facts read back by identity
// (protocol 05, common rules), over the system surface with the callers' service identities.
func (fx *fixture) systemSurface() {
	t, ctx := fx.t, context.Background()
	finance := purchasev3.NewMatchServiceClient(fx.dial(core.Finance))

	// order A: completed, 95 EA received net: its first movement (60) all returned, its second (96) 1 returned
	a := fx.orders["A"]
	res, err := finance.Basis(ctx, &purchasev3.BasisRequest{OrderId: a["id"].(string)})
	require.NoError(t, err)
	require.Len(t, res.GetLines(), 1)
	l := res.GetLines()[0]
	assert.Equal(t, "goods", l.GetKind())
	assert.Equal(t, "95", l.GetReceivedBaseQuantity())
	assert.Equal(t, "12", l.GetOrderedQuantity())
	assert.Equal(t, "BX", l.GetUnit())
	assert.Equal(t, "100", l.GetUnitPrice())
	assert.Equal(t, "CNY", l.GetCurrency())
	assert.Equal(t, int64(2), l.GetOrderRevision())
	assert.Equal(t, "completed", l.GetOrderState())
	assert.Equal(t, fx.sup, l.GetSupplierId())
	require.Len(t, l.GetMovements(), 2)
	assert.Equal(t, []string{"60", "60"}, []string{l.GetMovements()[0].GetReceivedBaseQuantity(), l.GetMovements()[0].GetReturnedBaseQuantity()})
	assert.Equal(t, []string{"96", "1"}, []string{l.GetMovements()[1].GetReceivedBaseQuantity(), l.GetMovements()[1].GetReturnedBaseQuantity()})

	// order C: a service line accepted 1 and 80, by its line's ID; a goods line nothing received
	c := fx.orders["C"]
	res, err = finance.Basis(ctx, &purchasev3.BasisRequest{OrderLineIds: []string{at(c, "lines", 0, "id").(string), ids.New().String()}})
	require.NoError(t, err)
	require.Len(t, res.GetLines(), 1, "a line not known is left out")
	assert.Equal(t, "service", res.GetLines()[0].GetKind())
	assert.Equal(t, "1", res.GetLines()[0].GetAcceptedQuantity())
	assert.Equal(t, "80", res.GetLines()[0].GetAcceptedAmount())
	_, err = finance.Basis(ctx, &purchasev3.BasisRequest{OrderId: ids.New().String()})
	assert.Equal(t, codes.NotFound, status.Code(err), "%v", err)
	_, err = finance.Basis(ctx, &purchasev3.BasisRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
	_, err = purchasev3.NewMatchServiceClient(fx.dial(core.Partner)).Basis(ctx, &purchasev3.BasisRequest{OrderId: a["id"].(string)})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "erp/finance only: %v", err)

	// the facts by identity, for any component; one not known has no type
	basisID := fx.factWhere(core.ValuationBasis, "order_id", a["id"].(string))["fact_id"].(string)
	facts, err := purchasev3.NewFactServiceClient(fx.dial("erp/costing")).Read(ctx, &purchasev3.ReadRequest{
		FactIds: []string{basisID, ids.New().String()}})
	require.NoError(t, err)
	require.Len(t, facts.GetFacts(), 2)
	assert.Equal(t, core.ValuationBasis, facts.GetFacts()[0].GetType())
	var data map[string]any
	require.NoError(t, json.Unmarshal(facts.GetFacts()[0].GetData(), &data))
	assert.Equal(t, basisID, data["fact_id"])
	assert.Empty(t, facts.GetFacts()[1].GetType())
}

// outbox: every event fits its schema, and every type was published.
func (fx *fixture) outbox() {
	t := fx.t
	rows, err := fx.admin.Query(fx.ctx, "SELECT type, payload FROM "+itSchema+".be_outbox ORDER BY ord")
	require.NoError(t, err)
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var typ string
		var payload []byte
		require.NoError(t, rows.Scan(&typ, &payload))
		counts[typ]++
		s, err := jsonschema.NewCompiler().Compile("../contract/events/" + typ + ".schema.json")
		require.NoError(t, err, typ)
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(payload))
		require.NoError(t, err)
		assert.NoError(t, s.Validate(doc), "%s: %s", typ, payload)
	}
	for _, typ := range []string{core.RequisitionChanged, core.OrderChanged, core.ReceiptChanged, core.ReturnChanged,
		core.ValuationBasis, core.ValuationWithdrawn, core.AcceptanceRecorded, core.ReturnPosted} {
		assert.NotZero(t, counts[typ], typ)
	}
}

// recoveryChecks: a full recovery asks this member, as the recovery tool only, whether the objects its audit names
// are still there at their versions (be-protocol chapter 14, P14.3): each type it audits.
func (fx *fixture) recoveryChecks() {
	t, ctx := fx.t, context.Background()
	_, err := recoverypb.NewCheckServiceClient(fx.dial(core.Partner)).Check(ctx, &recoverypb.CheckRequest{Recovery: "r"})
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "the recovery tool only: %v", err)
	cs := recoverypb.NewCheckServiceClient(fx.dial("ops/recover"))
	_, err = cs.Check(ctx, &recoverypb.CheckRequest{Recovery: "r"})
	require.NoError(t, err)
	for _, typ := range []string{core.Requisitions, core.Orders, core.Receipts, core.Returns, core.Policies} {
		var id, version string
		require.NoError(t, fx.admin.QueryRow(ctx, "SELECT target->>'id', target->>'version' FROM "+itSchema+".be_audit_log "+
			"WHERE target->>'type' = $1 ORDER BY ord DESC LIMIT 1", typ).Scan(&id, &version), "%s is audited with its version", typ)
		v, err := strconv.ParseInt(version, 10, 64)
		require.NoError(t, err, typ)
		present, err := cs.Present(ctx, &recoverypb.PresentRequest{Recovery: "r", Objects: []*recoverypb.ObjectRef{
			{Type: typ, Id: id, Version: version}, {Type: typ, Id: id, Version: strconv.FormatInt(v+1, 10)},
			{Type: typ, Id: ids.New().String(), Version: "1"}}})
		require.NoError(t, err, typ)
		var answers []string
		for _, a := range present.GetAnswers() {
			answers = append(answers, a.GetAnswer())
		}
		assert.Equal(t, []string{"present", "older", "absent"}, answers, typ)
	}
}

func (fx *fixture) dial(as string) *grpc.ClientConn {
	fx.t.Helper()
	conn, err := grpc.NewClient(itSystem, grpc.WithTransportCredentials(credentials.NewTLS(fx.w.TLS(fx.t, as).ClientConfig(core.ID))))
	require.NoError(fx.t, err)
	fx.t.Cleanup(func() { conn.Close() })
	return conn
}
