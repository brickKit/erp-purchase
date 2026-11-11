package masterdata

import (
	"context"
	"encoding/json"
	"time"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/errs"
	costingv3 "github.com/brickKit/erp-costing/contract/v3/gen/go/be/erp/costing/v3"
	currencyv3 "github.com/brickKit/mdm-currency/contract/v3/gen/go/be/mdm/currency/v3"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	partnerv3 "github.com/brickKit/mdm-partner/contract/v3/gen/go/be/mdm/partner/v3"
	productv3 "github.com/brickKit/mdm-product/contract/v3/gen/go/be/mdm/product/v3"
	"google.golang.org/grpc"
)

// Every call here runs outside a transaction (protocol P4.20): evidence first, then the transaction. Unreachable is
// never "does not exist" (protocol 07): it answers DEPENDENCY_UNAVAILABLE.

func Conn(m *core.Base, id string) (*grpc.ClientConn, error) {
	c, ok := m.RT.GRPC(id)
	if !ok {
		return nil, errs.New(errs.ReasonDependencyUnavailable, id+" is not reachable")
	}
	return c, nil
}

// LegalEntity is what purchase needs of a legal entity.
type LegalEntity struct {
	ID           string `json:"id"`
	Code         string `json:"code"`
	State        string `json:"state"`
	Timezone     string `json:"timezone"`
	BaseCurrency string `json:"base_currency"`
}

func LegalEntityOf(m *core.Base, ctx context.Context, id string) (LegalEntity, error) {
	c, err := Conn(m, core.Org)
	if err != nil {
		return LegalEntity{}, err
	}
	res, err := orgv3.NewSourceServiceClient(c).Read(ctx, &orgv3.ReadRequest{
		Aggregate: orgv3.Aggregate_AGGREGATE_LEGAL_ENTITY, Ids: []string{id}}, calls.Idempotent())
	if err != nil {
		return LegalEntity{}, err
	}
	var le LegalEntity
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != orgv3.ReadState_READ_STATE_EXISTS {
		return le, core.ErrLegalEntity.New("no legal entity " + id)
	}
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &le); err != nil {
		return le, err
	}
	if le.State != "active" {
		return le, core.ErrLegalEntity.New("the legal entity is " + le.State)
	}
	return le, nil
}

// OrgUnit is a department as the projection keeps it (mdm.org_unit.changed.v1).
type OrgUnit struct {
	ID            string `json:"id"`
	LegalEntityID string `json:"legal_entity_id"`
	Path          string `json:"path"`
	State         string `json:"state"`
}

// OrgUnitRead reads one department from mdm/org's source method, with its version and the source's identity.
func OrgUnitRead(m *core.Base, ctx context.Context, id string) (OrgUnit, int64, string, string, bool, error) {
	c, err := Conn(m, core.Org)
	if err != nil {
		return OrgUnit{}, 0, "", "", false, err
	}
	res, err := orgv3.NewSourceServiceClient(c).Read(ctx, &orgv3.ReadRequest{
		Aggregate: orgv3.Aggregate_AGGREGATE_ORG_UNIT, Ids: []string{id}}, calls.Idempotent())
	if err != nil {
		return OrgUnit{}, 0, "", "", false, err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != orgv3.ReadState_READ_STATE_EXISTS {
		return OrgUnit{}, 0, res.GetStamp().GetInstance(), res.GetStamp().GetEpoch(), false, nil
	}
	var u OrgUnit
	r := res.GetResults()[0]
	return u, r.GetVersion(), res.GetStamp().GetInstance(), res.GetStamp().GetEpoch(), true, json.Unmarshal(r.GetData(), &u)
}

// Supplier is the evidence and the snapshot of a supplier for an order.
type Supplier struct {
	ID          string
	Name        string
	TaxID       *string
	EvaluatedAt time.Time
}

// SupplierOf asks mdm/partner whether the partner may be bought from in the legal entity now (its supplier role), and
// reads its name and tax ID for the order's snapshot (protocol 07).
func SupplierOf(m *core.Base, ctx context.Context, partner, legalEntity string) (Supplier, error) {
	c, err := Conn(m, core.Partner)
	if err != nil {
		return Supplier{}, err
	}
	asked := time.Now()
	e, err := partnerv3.NewPartnerServiceClient(c).CheckEligibility(ctx, &partnerv3.CheckEligibilityRequest{
		PartnerId: partner, LegalEntityId: legalEntity, Purpose: partnerv3.Purpose_PURPOSE_PURCHASE}, calls.Idempotent())
	if err != nil {
		return Supplier{}, err
	}
	if !e.GetEligible() {
		return Supplier{}, core.ErrIneligible.New("the supplier is not eligible: " + e.GetReason())
	}
	res, err := partnerv3.NewSourceServiceClient(c).Read(ctx, &partnerv3.ReadRequest{
		Aggregate: partnerv3.Aggregate_AGGREGATE_PARTNER, Ids: []string{partner}}, calls.Idempotent())
	if err != nil {
		return Supplier{}, err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != partnerv3.ReadState_READ_STATE_EXISTS {
		return Supplier{}, core.ErrIneligible.New("no partner " + partner)
	}
	var p struct {
		Name  string  `json:"name"`
		TaxID *string `json:"tax_id"`
	}
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &p); err != nil {
		return Supplier{}, err
	}
	return Supplier{ID: partner, Name: p.Name, TaxID: p.TaxID, EvaluatedAt: asked}, nil
}

// ProductInfo is the evidence and the snapshot of a product for a document line.
type ProductInfo struct {
	ID          string
	Code        string
	Name        string
	BaseUnit    string
	Stocked     bool
	EvaluatedAt time.Time
}

// ProductsOf asks mdm/product for each product's eligibility to purchase in the legal entity (the answer says whether
// it is stocked: a goods line or a service line) and reads its code, name and base unit.
func ProductsOf(m *core.Base, ctx context.Context, ids []string, legalEntity string) (map[string]ProductInfo, error) {
	c, err := Conn(m, core.Product)
	if err != nil {
		return nil, err
	}
	uniq := map[string]bool{}
	var ask []string
	for _, id := range ids {
		if !uniq[id] {
			uniq[id] = true
			ask = append(ask, id)
		}
	}
	out := map[string]ProductInfo{}
	ps := productv3.NewProductServiceClient(c)
	for _, id := range ask {
		asked := time.Now()
		e, err := ps.CheckEligibility(ctx, &productv3.CheckEligibilityRequest{ProductId: id, LegalEntityId: legalEntity,
			Purpose: productv3.Purpose_PURPOSE_PURCHASE}, calls.Idempotent())
		if err != nil {
			return nil, err
		}
		if !e.GetEligible() {
			return nil, core.ErrIneligible.New("product " + id + " is not eligible to purchase: " + e.GetReason())
		}
		out[id] = ProductInfo{ID: id, Stocked: e.GetStocked(), EvaluatedAt: asked}
	}
	res, err := productv3.NewSourceServiceClient(c).Read(ctx, &productv3.ReadRequest{
		Aggregate: productv3.Aggregate_AGGREGATE_PRODUCT, Ids: ask}, calls.Idempotent())
	if err != nil {
		return nil, err
	}
	for _, r := range res.GetResults() {
		var p struct {
			ID       string `json:"id"`
			Code     string `json:"code"`
			Name     string `json:"name"`
			BaseUnit string `json:"base_unit"`
		}
		if r.GetState() != productv3.ReadState_READ_STATE_EXISTS || json.Unmarshal(r.GetData(), &p) != nil {
			continue
		}
		info := out[p.ID]
		info.Code, info.Name, info.BaseUnit = p.Code, p.Name, p.BaseUnit
		out[p.ID] = info
	}
	return out, nil
}

// Convert answers a quantity of the product in its base unit (rounded to the base unit's decimals by mdm/product) and
// the factor: how many base units one unit is.
func Convert(m *core.Base, ctx context.Context, product, quantity, unit, baseUnit string) (string, string, error) {
	if unit == baseUnit {
		return quantity, "1", nil
	}
	c, err := Conn(m, core.Product)
	if err != nil {
		return "", "", err
	}
	res, err := productv3.NewProductServiceClient(c).Convert(ctx, &productv3.ConvertRequest{ProductId: product,
		Quantity: quantity, FromUnit: unit, ToUnit: baseUnit}, calls.Idempotent())
	if err != nil {
		if errs.Is(err, core.Product, "NO_CONVERSION") {
			return "", "", core.ErrUnit.New("unit " + unit + " does not convert to " + baseUnit + " for product " + product)
		}
		return "", "", err
	}
	return res.GetQuantity(), res.GetFactor(), nil
}

// CurrencyScale is an active currency's minor unit.
func CurrencyScale(m *core.Base, ctx context.Context, code string) (int32, error) {
	c, err := Conn(m, core.Currency)
	if err != nil {
		return 0, err
	}
	res, err := currencyv3.NewSourceServiceClient(c).Read(ctx, &currencyv3.ReadRequest{
		Aggregate: currencyv3.Aggregate_AGGREGATE_CURRENCY, Ids: []string{code}}, calls.Idempotent())
	if err != nil {
		return 0, err
	}
	if len(res.GetResults()) != 1 || res.GetResults()[0].GetState() != currencyv3.ReadState_READ_STATE_EXISTS {
		return 0, core.ErrCurrency.New("no currency " + code)
	}
	var cur struct {
		MinorUnit int32 `json:"minor_unit"`
		Active    bool  `json:"active"`
	}
	if err := json.Unmarshal(res.GetResults()[0].GetData(), &cur); err != nil {
		return 0, err
	}
	if !cur.Active {
		return 0, core.ErrCurrency.New("currency " + code + " is not active")
	}
	return cur.MinorUnit, nil
}

// ReturnLimit is how much of a receipt movement erp/costing lets go back (LayerService.ReturnLimit, protocol 05b): a
// FIFO layer's units in stock (limited); not limited when costing values by moving average, has not valued the
// receipt, is not deployed or does not answer (it then values the excess as stock below zero, with a warning).
func ReturnLimit(m *core.Base, ctx context.Context, movement string) (quantity string, limited bool) {
	c, ok := m.RT.GRPC(core.Costing)
	if !ok {
		return "", false
	}
	res, err := costingv3.NewLayerServiceClient(c).ReturnLimit(ctx, &costingv3.ReturnLimitRequest{OriginMovementId: movement},
		calls.Idempotent())
	if err != nil {
		m.RT.Logger.Warn("erp/costing did not say how much of the receipt may go back; not limited", "event", "return_limit_unknown",
			"origin_movement_id", movement, "error", err)
		return "", false
	}
	if !res.GetKnown() || !res.GetLimited() {
		return "", false
	}
	return res.GetQuantity(), true
}
