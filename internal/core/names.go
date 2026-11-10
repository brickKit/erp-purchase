package core

import (
	"time"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
)

// ID is the component's ID.
const ID = "erp/purchase"

// Dependencies (the IDs runtime.GRPC takes). The inventory is optional: without it goods lines cannot be received.
const (
	Partner   = "mdm/partner"
	Product   = "mdm/product"
	Org       = "mdm/org"
	Currency  = "mdm/currency"
	Inventory = "erp/inventory"
	// Finance reads the match basis (protocol 05, D50).
	Finance = "erp/finance"
)

// The stream and its event types (contract/events).
const (
	Stream             = "erp.purchase"
	RequisitionChanged = "erp.purchase_requisition.changed.v1"
	OrderChanged       = "erp.purchase_order.changed.v1"
	ReceiptChanged     = "erp.purchase_receipt.changed.v1"
	ReturnChanged      = "erp.purchase_return.changed.v1"
	ValuationBasis     = "erp.purchase_receipt.valuation_basis.v1"
	ValuationWithdrawn = "erp.purchase_receipt.valuation_basis_withdrawn.v1"
	AcceptanceRecorded = "erp.purchase_acceptance.recorded.v1"
	ReturnPosted       = "erp.purchase_return.posted.v1"
	// the source stream of the department projection
	OrgStream       = "mdm.org"
	OrgUnitChanged  = "mdm.org_unit.changed.v1"
	OrgUnitEvents   = "mdm.org_unit.>" // every department event, later actions and majors too (protocol P6.33)
	OrgUnitsProject = "org_units"
	// the inventory's movements: the received quantities come from its inbound facts
	InventoryStream  = "erp.inventory"
	MovementRecorded = "erp.inventory_movement.recorded.v1"
)

// Resources and operations (contract/openapi.yaml names each route's operation).
const (
	Requisitions    = "erp.purchase.requisition"
	OpReqRead       = "erp.purchase.requisition.read"
	OpReqEdit       = "erp.purchase.requisition.edit"
	OpReqApprove    = "erp.purchase.requisition.approve"
	Orders          = "erp.purchase.order"
	OpOrderRead     = "erp.purchase.order.read"
	OpOrderEdit     = "erp.purchase.order.edit"
	OpOrderApprove  = "erp.purchase.order.approve"
	Receipts        = "erp.purchase.receipt"
	OpReceiptRead   = "erp.purchase.receipt.read"
	OpReceiptCreate = "erp.purchase.receipt.create"
	Returns         = "erp.purchase.return"
	OpReturnRead    = "erp.purchase.return.read"
	OpReturnEdit    = "erp.purchase.return.edit"
	Policies        = "erp.purchase.policy"
	OpPolicyRead    = "erp.purchase.policy.read"
	OpPolicyManage  = "erp.purchase.policy.manage"
	System          = "erp.purchase.system"
	OpSysRead       = "erp.purchase.system.read"
	// OpResend is the redelivery of this component's stream (be-protocol P6.38: every component with streams serves
	// it; any component of the deployment may call it).
	OpResend = "erp.purchase.system.resend"
	// OpRecover is a full recovery's checks (be-protocol chapter 14: the recovery tool only).
	OpRecover = "erp.purchase.system.recover"
)

// Dimensions (security 07): the legal entity, the department (an org-tree path) and the owner.
const (
	DimLegalEntity = "legal_entity"
	DimDepartment  = "department"
	DimOwner       = "owner"
)

// Parameters (protocol 07, protocol 14).
const (
	EvidenceW     = 30 * time.Second // how long an eligibility answer is evidence
	MaxLines      = 500
	MaxReadIDs    = 500
	PriceScale    = 6 // unit prices: NUMERIC(19,6)
	QuantityScale = 6
)

// DepartmentTables are the tables that copy their department's path (internal/orgunits rewrites them when a
// department moves).
var DepartmentTables = []string{"requisition", "purchase_order"}

func fields(names ...string) map[string]sdkauthz.Field {
	out := map[string]sdkauthz.Field{}
	for _, n := range names {
		out[n] = sdkauthz.Field{Class: sdkauthz.Ordinary}
	}
	return out
}

// scoped are the three dimensions of requisitions, orders, receipts, acceptances and returns (the last three inherit
// their order's); owner is the owner's column (a requisition's is its requester).
func scoped(owner string) map[string]sdkauthz.Dimension {
	return map[string]sdkauthz.Dimension{
		DimLegalEntity: {Kind: sdkauthz.ValueSet, Column: "legal_entity_id"},
		DimDepartment:  {Kind: sdkauthz.OrgTree, Column: "department_path"},
		DimOwner:       {Kind: sdkauthz.Principal, Column: owner, PrincipalRole: "owner"},
	}
}

// RequisitionFields are what a requisition list may filter and sort on.
var RequisitionFields = []string{"id", "number", "legal_entity_id", "department_id", "requester_id", "state", "reason",
	"lines", "version", "updated_at", "created_at"}

// OrderFields are what an order list may filter and sort on.
var OrderFields = []string{"id", "number", "legal_entity_id", "supplier_id", "supplier", "currency", "business_date",
	"owner_id", "department_id", "state", "revision", "total", "submitted_by", "approved_by", "reason", "lines",
	"version", "created_at", "updated_at"}

// Definitions are the component's resources and operations.
func Definitions() *sdkauthz.Definitions {
	low := func(key, typ, purpose string, audit bool) sdkauthz.Operation {
		op := sdkauthz.Operation{Key: key, ResourceType: typ, Purpose: purpose, RiskClass: "low", Audit: audit}
		if purpose != "read" {
			op.SideEffects = []string{"business_write"}
		}
		return op
	}
	return &sdkauthz.Definitions{Component: ID,
		Resources: []sdkauthz.Resource{
			{Type: Requisitions, ViewOperation: OpReqRead, Dimensions: scoped("requester_id"), Fields: fields(RequisitionFields...)},
			{Type: Orders, ViewOperation: OpOrderRead, Dimensions: scoped("owner_id"), Fields: fields(OrderFields...)},
			{Type: Receipts, ViewOperation: OpReceiptRead, Dimensions: scoped("owner_id"), Fields: fields("id")},
			{Type: Returns, ViewOperation: OpReturnRead, Dimensions: scoped("owner_id"), Fields: fields("id")},
			{Type: Policies, ViewOperation: OpPolicyRead,
				Dimensions: map[string]sdkauthz.Dimension{DimLegalEntity: {Kind: sdkauthz.ValueSet, Column: "legal_entity_id"}},
				Fields:     fields("legal_entity_id", "approval", "over_receipt_percent", "version")},
			{Type: System, ViewOperation: OpSysRead, Dimensions: map[string]sdkauthz.Dimension{},
				Fields: map[string]sdkauthz.Field{}},
		},
		Operations: []sdkauthz.Operation{
			low(OpReqRead, Requisitions, "read", false),
			low(OpReqEdit, Requisitions, "manage", true),
			low(OpReqApprove, Requisitions, "action", true),
			low(OpOrderRead, Orders, "read", false),
			low(OpOrderEdit, Orders, "manage", true),
			low(OpOrderApprove, Orders, "action", true),
			low(OpReceiptRead, Receipts, "read", false),
			low(OpReceiptCreate, Receipts, "create", true),
			low(OpReturnRead, Returns, "read", false),
			low(OpReturnEdit, Returns, "manage", true),
			low(OpPolicyRead, Policies, "read", false),
			low(OpPolicyManage, Policies, "manage", true),
			low(OpSysRead, System, "read", false),
			{Key: OpResend, ResourceType: System, Purpose: "read", RiskClass: "low"},
			{Key: OpRecover, ResourceType: System, Purpose: "read", RiskClass: "low"},
		}}
}
