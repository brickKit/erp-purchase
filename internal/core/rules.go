package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/brickKit/be-sdk-go/v3/audit"
	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/idem"
	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/money"
	apd "github.com/cockroachdb/apd/v3"
)

// The component's own reasons (the SDK's catalog has the shared ones).
var (
	ErrVersion = errs.Define(ID, "VERSION_CONFLICT", errs.Aborted, 409, errs.RetryNo, "The record changed since that version")
	ErrState   = errs.Define(ID, "STATE_CONFLICT", errs.FailedPrecondition, 409, errs.RetryNo,
		"The record's state does not allow this")
	ErrLegalEntity = errs.Define(ID, "LEGAL_ENTITY_NOT_USABLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"The legal entity does not exist or is not active")
	ErrIneligible = errs.Define(ID, "NOT_ELIGIBLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"A supplier or product may not be used for a purchase in this legal entity now")
	ErrDepartment = errs.Define(ID, "DEPARTMENT_NOT_USABLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"The department does not exist, is retired or belongs to another legal entity")
	ErrCurrency = errs.Define(ID, "CURRENCY_NOT_USABLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"The currency does not exist or is not active")
	ErrUnit = errs.Define(ID, "NO_CONVERSION", errs.FailedPrecondition, 409, errs.RetryNo,
		"The unit does not convert to the product's base unit")
	// PR06: a requisition line an order line is made from
	ErrRequisition = errs.Define(ID, "REQUISITION_LINE_NOT_USABLE", errs.FailedPrecondition, 409, errs.RetryNo,
		"The requisition line is not approved, is of another legal entity, product or unit, or has not that much left")
	// PO07, PO08: over the ordered quantity and the policy's over-receipt percent
	ErrOverReceipt = errs.Define(ID, "OVER_RECEIPT", errs.FailedPrecondition, 409, errs.RetryNo,
		"More than the order line allows with the legal entity's over-receipt percent")
	// PO07: erp/inventory refused the receipt; the receipt is failed (its ID in the problem's receipt_id)
	ErrReceiptFailed = errs.Define(ID, "RECEIPT_FAILED", errs.FailedPrecondition, 409, errs.RetryNo,
		"erp/inventory refused the receipt")
	// PR03, PO04: an approver is not the one who submitted (by account)
	ErrApprover = errs.Define(ID, "APPROVER_IS_SUBMITTER", errs.FailedPrecondition, 409, errs.RetryNo,
		"The one who submitted it does not approve it")
)

var (
	UUIDForm     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	CurrencyForm = regexp.MustCompile(`^[A-Z]{3}$`)
	DateForm     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// DocFacts are the facts of one document's three dimensions; a document without a department has none (security
// 07: no department never matches a tree, it never widens to the whole organization).
func DocFacts(legalEntity string, departmentPath *string, owner string) sdkauthz.Facts {
	dept := sdkauthz.Null
	if departmentPath != nil {
		dept = sdkauthz.Value(*departmentPath)
	}
	return sdkauthz.Facts{DimLegalEntity: sdkauthz.Value(legalEntity), DimDepartment: dept, DimOwner: sdkauthz.Value(owner)}
}

// EntityFacts are the facts of a record with the legal entity dimension only (the purchase policy).
func EntityFacts(legalEntity string) sdkauthz.Facts {
	return sdkauthz.Facts{DimLegalEntity: sdkauthz.Value(legalEntity)}
}

// Number takes a document number of a series that allows gaps: <prefix>-<legal entity code>-<year>-<six digits>
// (prefixes PR, PO, GR, SA, RT).
func Number(m *Base, ctx context.Context, prefix, legalEntity, code, date string) (string, error) {
	year := date[:4]
	n, err := m.RT.DB.NextGappedNumber(ctx, db.Series{LegalEntity: legalEntity, Code: prefix, Period: year})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%s-%06d", prefix, code, year, n), nil
}

// BindOptional decodes a body the contract makes optional (Versioned, Reason); no body leaves v as it is.
func BindOptional(c *httpx.Context, v any) error {
	body, err := c.Body()
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return err
	}
	return c.BindJSON(v)
}

// IDResult is what an idempotent create keeps (the new record's ID); ResultID reads it back on a replay too.
func IDResult(id string) idem.Outcome {
	b, _ := json.Marshal(map[string]string{"id": id})
	return idem.Outcome{Status: 200, Result: b}
}

func ResultID(out idem.Outcome) string {
	var r struct{ ID string }
	_ = json.Unmarshal(out.Result, &r)
	return r.ID
}

// Check decides op on a record with the given facts and returns the context to work in and the acting principal.
func Check(c *httpx.Context, op string, facts sdkauthz.Facts) (context.Context, string, error) {
	s, _ := sdkauthz.From(c.Ctx())
	ctx, _, err := s.Check(c.Ctx(), op, facts)
	if err != nil {
		return ctx, "", err
	}
	u, _ := identity.UserFrom(ctx)
	return ctx, u.Subject, nil
}

func Invalid(field, why string) error {
	return errs.New(errs.ReasonRequestInvalid, why).WithViolations(errs.Violation{Field: field, Reason: "INVALID", Description: why})
}

func NotFound(what string) error { return errs.New(errs.ReasonNotFound, what+" not found") }

// Unsupported answers a route of the contract this version does not serve (CAPABILITY_UNSUPPORTED, D75).
func Unsupported(what string) httpx.Handler {
	return func(c *httpx.Context) error {
		return errs.New(errs.ReasonCapabilityUnsupported, what+" is not in this version of erp/purchase")
	}
}

// Decimal checks a non-negative decimal string of at most scale places and returns its canonical form.
func Decimal(field, v string, scale int32, positive bool) (string, error) {
	d, err := money.Parse(v)
	if err != nil || d.Sign() < 0 || positive && d.Sign() == 0 || -d.Exponent > scale {
		return "", Invalid(field, field+" is a decimal string with at most "+strconv.Itoa(int(scale))+" places")
	}
	d.Reduce(d)
	return d.Text('f'), nil
}

// Change records the business change with the record's new state (protocol 03), in the transaction that made it.
func Change(m *Base, ctx context.Context, tx db.Tx, actor, op, typ, id string, after any) error {
	_, err := m.RT.Audit.Write(ctx, tx, audit.Record{Category: audit.BusinessChange, Operation: op, Outcome: audit.Succeeded,
		Actor: actor, Target: audit.TargetOf(typ, id, after), Changes: map[string]any{"after": after}})
	return err
}

// Emit writes an event into the outbox, in the transaction that changed the record (protocol 02, S2).
func Emit(m *Base, ctx context.Context, tx db.Tx, typ, id string, version int64, legalEntity string, data any) error {
	_, err := m.RT.Events.Emit(ctx, tx, Stream, events.Event{Type: typ, RecordID: id, Version: version,
		Projection: events.Full, LegalEntity: legalEntity, Data: data})
	return err
}

// Minus is a − b, exact (quantities and amounts are decimals with bounded places).
func Minus(a, b string) string {
	x, _, _ := apd.NewFromString(a)
	y, _, _ := apd.NewFromString(b)
	var r apd.Decimal
	_, _ = apd.BaseContext.WithPrecision(80).Sub(&r, x, y)
	r.Reduce(&r)
	return r.Text('f')
}

// Per is a ÷ b rounded half away from zero to scale places (a price per base unit).
func Per(a, b string, scale int32) string {
	x, _, _ := apd.NewFromString(a)
	y, _, _ := apd.NewFromString(b)
	var r apd.Decimal
	_, _ = apd.BaseContext.WithPrecision(80).Quo(&r, x, y)
	out, _, _ := apd.NewFromString(money.Format(&r, scale))
	out.Reduce(out)
	return out.Text('f')
}

// Fact publishes an incremental fact (protocol 05, common rules) in the transaction that made it, and keeps it for
// FactService.Read: its record is the fact's ID.
func Fact(m *Base, ctx context.Context, tx db.Tx, typ, factID, legalEntity string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO purchase_fact (fact_id, type, data) VALUES ($1, $2, $3)`, factID, typ, b); err != nil {
		return err
	}
	return Emit(m, ctx, tx, typ, factID, 1, legalEntity, json.RawMessage(b))
}

// Times is a × b rounded half away from zero to scale places.
func Times(a, b string, scale int32) string {
	x, _, _ := apd.NewFromString(a)
	y, _, _ := apd.NewFromString(b)
	var r apd.Decimal
	_, _ = apd.BaseContext.WithPrecision(80).Mul(&r, x, y)
	return money.Format(&r, scale)
}
