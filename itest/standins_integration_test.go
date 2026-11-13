//go:build integration

package itest

// The stand-ins of the integration test: the master data components, each speaking its published contract over mTLS.

import (
	"context"
	"encoding/json"
	"sync"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/ids"
	costingv3 "github.com/brickKit/erp-costing/contract/v3/gen/go/be/erp/costing/v3"
	attachmentv3 "github.com/brickKit/infra-attachment/contract/v3/gen/go/be/attachment/v3"
	inventoryv3 "github.com/brickKit/erp-inventory/contract/v3/gen/go/be/erp/inventory/v3"
	currencyv3 "github.com/brickKit/mdm-currency/contract/v3/gen/go/be/mdm/currency/v3"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	partnerv3 "github.com/brickKit/mdm-partner/contract/v3/gen/go/be/mdm/partner/v3"
	productv3 "github.com/brickKit/mdm-product/contract/v3/gen/go/be/mdm/product/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- mdm/org: legal entities and departments ---

type orgStandIn struct {
	orgv3.UnimplementedSourceServiceServer
	mu    sync.Mutex
	les   map[string]map[string]any
	units map[string]map[string]any
}

var orgStamp = &orgv3.Stamp{Instance: "org-it", Epoch: "1.1"}

func (o *orgStandIn) Read(_ context.Context, in *orgv3.ReadRequest) (*orgv3.ReadResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	src := o.les
	if in.GetAggregate() == orgv3.Aggregate_AGGREGATE_ORG_UNIT {
		src = o.units
	}
	out := &orgv3.ReadResponse{Stamp: orgStamp}
	for _, id := range in.GetIds() {
		r := &orgv3.ReadResult{Id: id, State: orgv3.ReadState_READ_STATE_PURGED}
		if v, ok := src[id]; ok {
			data, _ := json.Marshal(v)
			r = &orgv3.ReadResult{Id: id, State: orgv3.ReadState_READ_STATE_EXISTS, Version: v["version"].(int64), Data: data}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

func (o *orgStandIn) Snapshot(_ context.Context, in *orgv3.SnapshotRequest) (*orgv3.SnapshotResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := &orgv3.SnapshotResponse{Stamp: orgStamp}
	if in.GetAggregate() != orgv3.Aggregate_AGGREGATE_ORG_UNIT || in.GetCursor() != "" {
		return out, nil
	}
	for id, v := range o.units {
		data, _ := json.Marshal(v)
		out.Records = append(out.Records, &orgv3.Record{Id: id, Version: v["version"].(int64), Data: data})
	}
	return out, nil
}

// --- mdm/partner ---

type partnerStandIn struct {
	partnerv3.UnimplementedSourceServiceServer
	partnerv3.UnimplementedPartnerServiceServer
	names   map[string]string
	blocked map[string]bool
}

func (p *partnerStandIn) CheckEligibility(_ context.Context, in *partnerv3.CheckEligibilityRequest) (*partnerv3.CheckEligibilityResponse, error) {
	if _, ok := p.names[in.GetPartnerId()]; !ok {
		return &partnerv3.CheckEligibilityResponse{Reason: "not_found"}, nil
	}
	if p.blocked[in.GetPartnerId()] {
		return &partnerv3.CheckEligibilityResponse{Reason: "partner_suspended"}, nil
	}
	return &partnerv3.CheckEligibilityResponse{Eligible: true, Reason: "eligible"}, nil
}

func (p *partnerStandIn) Read(_ context.Context, in *partnerv3.ReadRequest) (*partnerv3.ReadResponse, error) {
	out := &partnerv3.ReadResponse{Stamp: &partnerv3.Stamp{}}
	for _, id := range in.GetIds() {
		r := &partnerv3.ReadResult{Id: id, State: partnerv3.ReadState_READ_STATE_PURGED}
		if n, ok := p.names[id]; ok {
			data, _ := json.Marshal(map[string]any{"id": id, "name": n, "tax_id": "91310000-" + id[:4]})
			r = &partnerv3.ReadResult{Id: id, State: partnerv3.ReadState_READ_STATE_EXISTS, Version: 1, Data: data}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

// --- mdm/product ---

// noConversion is mdm/product's own reason, as its contract answers it.
var noConversion = errs.Define(core.Product, "NO_CONVERSION", errs.FailedPrecondition, 409, errs.RetryNo,
	"The unit does not convert")

type productInfoIT struct {
	Code, Name, BaseUnit string
	Stocked              bool
	Box                  string // the factor of BX to the base unit, when it has one
}

type productStandIn struct {
	productv3.UnimplementedSourceServiceServer
	productv3.UnimplementedProductServiceServer
	known map[string]productInfoIT
}

func (p *productStandIn) CheckEligibility(_ context.Context, in *productv3.CheckEligibilityRequest) (*productv3.CheckEligibilityResponse, error) {
	v, ok := p.known[in.GetProductId()]
	if !ok {
		return &productv3.CheckEligibilityResponse{Reason: "not_found"}, nil
	}
	return &productv3.CheckEligibilityResponse{Eligible: true, Reason: "eligible", Stocked: v.Stocked}, nil
}

func (p *productStandIn) Convert(_ context.Context, in *productv3.ConvertRequest) (*productv3.ConvertResponse, error) {
	v := p.known[in.GetProductId()]
	if in.GetFromUnit() != "BX" || v.Box == "" || in.GetToUnit() != v.BaseUnit {
		return nil, noConversion.New("no conversion of " + in.GetFromUnit() + " to " + in.GetToUnit())
	}
	return &productv3.ConvertResponse{Quantity: core.Times(in.GetQuantity(), v.Box, 0), Factor: v.Box}, nil
}

func (p *productStandIn) Read(_ context.Context, in *productv3.ReadRequest) (*productv3.ReadResponse, error) {
	out := &productv3.ReadResponse{Stamp: &productv3.Stamp{}}
	for _, id := range in.GetIds() {
		r := &productv3.ReadResult{Id: id, State: productv3.ReadState_READ_STATE_PURGED}
		if v, ok := p.known[id]; ok {
			data, _ := json.Marshal(map[string]any{"id": id, "code": v.Code, "name": v.Name, "base_unit": v.BaseUnit})
			r = &productv3.ReadResult{Id: id, State: productv3.ReadState_READ_STATE_EXISTS, Version: 1, Data: data}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

// --- mdm/currency: CNY and USD ---

type currencyStandIn struct {
	currencyv3.UnimplementedSourceServiceServer
}

func (currencyStandIn) Read(_ context.Context, in *currencyv3.ReadRequest) (*currencyv3.ReadResponse, error) {
	out := &currencyv3.ReadResponse{Stamp: &currencyv3.Stamp{}}
	for _, code := range in.GetIds() {
		r := &currencyv3.ReadResult{Id: code, State: currencyv3.ReadState_READ_STATE_PURGED}
		if code == "CNY" || code == "USD" {
			data, _ := json.Marshal(map[string]any{"code": code, "minor_unit": 2, "active": true})
			r = &currencyv3.ReadResult{Id: code, State: currencyv3.ReadState_READ_STATE_EXISTS, Version: 1, Data: data}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

// --- erp/inventory: purchase receipts and returns ---

// inventoryStandIn takes purchase receipts as erp/inventory's StockService does: idempotent by the receipt's ID, one
// movement per line carrying the receipt line's ID, each published as erp.inventory_movement.recorded.v1 (its
// forwarder). The mode drives the receipt's outcomes: "" answers, "refuse" refuses (nothing taken in), "lost" takes
// it in and loses the answer, "down" answers nothing.
type inventoryStandIn struct {
	inventoryv3.UnimplementedStockServiceServer
	mu       sync.Mutex
	mode     string
	received map[string][]string // receipt ID → its movements
	returned map[string]string   // return line ID → its movement
	calls    map[string]int      // Receive and Return calls per receipt and return line
	publish  func(movement map[string]any)
}

func (s *inventoryStandIn) set(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
}

func (s *inventoryStandIn) callsOf(receipt string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[receipt]
}

var warehouseNotUsable = errs.Define(core.Inventory, "WAREHOUSE_NOT_USABLE", errs.FailedPrecondition, 409, errs.RetryNo,
	"The warehouse is not usable")

func (s *inventoryStandIn) Receive(_ context.Context, in *inventoryv3.ReceiveRequest) (*inventoryv3.ReceiveResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[in.GetReceiptId()]++
	switch s.mode {
	case "down":
		return nil, status.Error(codes.Unavailable, "the stand-in is down")
	case "refuse":
		return nil, warehouseNotUsable.New("warehouse " + in.GetWarehouseId() + " is closed")
	}
	mvs, seen := s.received[in.GetReceiptId()]
	if !seen {
		for _, l := range in.GetLines() {
			id := ids.New().String()
			mvs = append(mvs, id)
			s.publish(map[string]any{"fact_id": id, "kind": "receipt", "product_id": l.GetProductId(),
				"warehouse_id": in.GetWarehouseId(), "legal_entity_id": in.GetLegalEntityId(), "quantity": l.GetQuantity(),
				"batch": nil, "serials": []string{}, "business_date": in.GetBusinessDate(),
				"reference": map[string]any{"kind": in.GetReference().GetKind(), "id": in.GetReference().GetId(),
					"line_id": l.GetLineId()},
				"branch_id": nil, "consumption_id": nil, "inventory_seq": map[string]any{"epoch": "1.1", "seq": 1},
				"valuation_seq": map[string]any{"epoch": "1.1", "seq": 1}, "recorded_at": "2026-11-10T08:00:00.000000Z"})
		}
		s.received[in.GetReceiptId()] = mvs
	}
	if s.mode == "lost" {
		return nil, status.Error(codes.Unavailable, "the answer was lost")
	}
	return &inventoryv3.ReceiveResponse{ReceiptId: in.GetReceiptId(), MovementIds: mvs}, nil
}

var returnNotPossible = errs.Define(core.Inventory, "RETURN_NOT_POSSIBLE", errs.FailedPrecondition, 409, errs.RetryNo,
	"The return is not possible")

// Return issues a purchase return line, idempotent by its ID; the mode as for Receive ("lost" is not used).
func (s *inventoryStandIn) Return(_ context.Context, in *inventoryv3.ReturnRequest) (*inventoryv3.ReturnResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[in.GetReturnId()]++
	switch s.mode {
	case "down":
		return nil, status.Error(codes.Unavailable, "the stand-in is down")
	case "refuse":
		return nil, returnNotPossible.New("the goods of movement " + in.GetOriginMovementId() + " are not in stock")
	}
	mv, seen := s.returned[in.GetReturnId()]
	if !seen {
		mv = ids.New().String()
		s.returned[in.GetReturnId()] = mv
	}
	return &inventoryv3.ReturnResponse{ReturnId: in.GetReturnId(), MovementId: mv}, nil
}

// --- erp/costing: how much of a receipt may go back ---

// costingStandIn answers LayerService.ReturnLimit as erp/costing does: a receipt it has a FIFO layer for is limited to
// the layer's units in stock, one it has not valued is not known; "down" answers nothing.
type costingStandIn struct {
	costingv3.UnimplementedLayerServiceServer
	mu     sync.Mutex
	down   bool
	limits map[string]string // origin movement → its layer's units in stock
}

func (s *costingStandIn) limit(movement, quantity string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits[movement] = quantity
}

func (s *costingStandIn) setDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

func (s *costingStandIn) ReturnLimit(_ context.Context, in *costingv3.ReturnLimitRequest) (*costingv3.ReturnLimitResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down {
		return nil, status.Error(codes.Unavailable, "the stand-in is down")
	}
	q, ok := s.limits[in.GetOriginMovementId()]
	if !ok {
		return &costingv3.ReturnLimitResponse{}, nil
	}
	return &costingv3.ReturnLimitResponse{Known: true, Limited: true, Quantity: q}, nil
}

// --- infra/attachment: the owner's sessions and commands ---

// attachmentStandIn answers infra/attachment's AttachmentService for the purchase orders: an upload session makes the
// attachment available at once (the user's upload and confirmation are infra/attachment's, not the owner's), a
// download of a normal category is an address and of a strict one a credential; every call is recorded.
type attachmentStandIn struct {
	attachmentv3.UnimplementedAttachmentServiceServer
	mu    sync.Mutex
	calls []attachmentCall
	held  map[string][]*attachmentv3.Attachment // record → its attachments
}

type attachmentCall struct {
	method, parent, record, attachment, user, date string
}

func (s *attachmentStandIn) record(c attachmentCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
}

// last is the latest call of a method.
func (s *attachmentStandIn) last(method string) attachmentCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.calls) - 1; i >= 0; i-- {
		if s.calls[i].method == method {
			return s.calls[i]
		}
	}
	return attachmentCall{}
}

// find is the latest call of a method for a record, nil when there is none.
func (s *attachmentStandIn) find(method, record string) *attachmentCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.calls) - 1; i >= 0; i-- {
		if c := s.calls[i]; c.method == method && c.record == record {
			return &c
		}
	}
	return nil
}

func (s *attachmentStandIn) CreateUpload(_ context.Context, in *attachmentv3.CreateUploadRequest) (*attachmentv3.CreateUploadResponse, error) {
	s.record(attachmentCall{method: "CreateUpload", parent: in.GetParent().GetResourceType(),
		record: in.GetParent().GetRecordId(), user: in.GetUser()})
	id := ids.New().String()
	sens := "normal"
	if in.GetCategory() == core.OrderInvoice {
		sens = "strict"
	}
	s.mu.Lock()
	if s.held == nil {
		s.held = map[string][]*attachmentv3.Attachment{}
	}
	r := in.GetParent().GetRecordId()
	s.held[r] = append(s.held[r], &attachmentv3.Attachment{Id: id, Category: in.GetCategory(), State: "available",
		FileName: in.GetFileName(), Size: in.GetSize(), ContentType: in.GetContentType(), Version: 1, Unscanned: true,
		CreatedAt: "2026-11-13T00:00:00.000000Z", Sensitivity: sens})
	s.mu.Unlock()
	return &attachmentv3.CreateUploadResponse{AttachmentId: id, Version: 1, UploadUrl: "http://store.invalid/staging/" + id,
		ConfirmCredential: ids.New().String(), ExpiresAt: "2026-11-13T01:00:00.000000Z"}, nil
}

func (s *attachmentStandIn) List(_ context.Context, in *attachmentv3.ListRequest) (*attachmentv3.ListResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &attachmentv3.ListResponse{Attachments: s.held[in.GetParent().GetRecordId()]}, nil
}

func (s *attachmentStandIn) IssueDownload(_ context.Context, in *attachmentv3.IssueDownloadRequest) (*attachmentv3.IssueDownloadResponse, error) {
	s.record(attachmentCall{method: "IssueDownload", parent: in.GetParent().GetResourceType(),
		record: in.GetParent().GetRecordId(), attachment: in.GetAttachmentId(), user: in.GetUser()})
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.held[in.GetParent().GetRecordId()] {
		if a.GetId() == in.GetAttachmentId() {
			if a.GetSensitivity() == "strict" {
				return &attachmentv3.IssueDownloadResponse{Credential: ids.New().String(), ExpiresAt: "2026-11-13T00:05:00.000000Z"}, nil
			}
			return &attachmentv3.IssueDownloadResponse{Url: "http://store.invalid/final/" + a.GetId(), ExpiresAt: "2026-11-13T00:05:00.000000Z"}, nil
		}
	}
	return nil, errs.New(errs.ReasonNotFound, "attachment not found")
}

func (s *attachmentStandIn) Detach(_ context.Context, in *attachmentv3.DetachRequest) (*attachmentv3.DetachResponse, error) {
	s.record(attachmentCall{method: "Detach", parent: in.GetParent().GetResourceType(), record: in.GetParent().GetRecordId(),
		attachment: in.GetAttachmentId(), date: in.GetRetentionStart()})
	return &attachmentv3.DetachResponse{Detached: 1}, nil
}

func (s *attachmentStandIn) SetRetentionStart(_ context.Context, in *attachmentv3.SetRetentionStartRequest) (*attachmentv3.SetRetentionStartResponse, error) {
	s.record(attachmentCall{method: "SetRetentionStart", parent: in.GetParent().GetResourceType(),
		record: in.GetParent().GetRecordId(), attachment: in.GetAttachmentId(), date: in.GetDate()})
	return &attachmentv3.SetRetentionStartResponse{Set: 1}, nil
}
