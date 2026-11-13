//go:build integration

package itest

import (
	"time"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/sectest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attachments: the orders' attachments as their owner asks infra/attachment for them (the stand-in records what it is
// asked): the order's edit for an upload and a detach, its read for the list and a download, the invoice scans also
// with their restricted field; none on a cancelled order; the order's end commanded as a work item (D68).
func (fx *fixture) attachments() {
	t := fx.t
	withContract := append(append([]string{}, core.OrderFields...), "contract_file")
	for _, who := range []struct {
		id     string
		fields []string
	}{{erin, withContract}, {frank, core.OrderFields}, {gina, append(append([]string{}, withContract...), "invoice_scan")}} {
		for _, op := range []string{core.OpOrderRead, core.OpOrderEdit} {
			fx.w.Grant(who.id, sdkauthz.Segment{ResourceType: core.Orders, Operation: op, ReadFields: who.fields,
				Atoms: allScope(), Valid: sdkauthz.Window{From: "2026-01-01T00:00:00Z"}})
		}
	}
	e, f, g := client{t, fx.w.Bearer(erin)}, client{t, fx.w.Bearer(frank)}, client{t, fx.w.Bearer(gina)}
	o := fx.order(fx.le, nil, fx.svc("1", "100"))
	id := o["id"].(string)
	base := "/api/purchase/orders/" + id + "/attachments"
	upload := func(c client, category string) (int, map[string]any) {
		return c.do("POST", base, map[string]any{"category": category, "file_name": "c.pdf", "size": 1024,
			"content_type": "application/pdf"})
	}

	code, up := upload(fx.alice, core.OrderContract)
	require.Equal(t, 200, code, up)
	assert.NotEmpty(t, up["upload_url"])
	assert.NotEmpty(t, up["confirm_credential"])
	assert.Equal(t, "/api/attachment/uploads/confirm", at(up, "confirm", "path"))
	asked := fx.att.last("CreateUpload")
	assert.Equal(t, core.Orders, asked.parent)
	assert.Equal(t, id, asked.record)
	assert.Equal(t, sectest.Alice, asked.user, "the session is the user's")
	contract := up["attachment_id"].(string)

	require.Eventually(t, func() bool { // the member takes erin's grants with its next view
		code, _ := e.do("GET", base, nil)
		code2, _ := f.do("GET", base, nil)
		code3, _ := g.do("GET", base, nil)
		return code == 200 && code2 == 200 && code3 == 200
	}, 20*time.Second, 100*time.Millisecond)
	code, out := upload(fx.alice, "erp.purchase.unknown")
	assert.Equal(t, 400, code, out)
	assert.Equal(t, "REQUEST_INVALID", out["reason"])
	code, out = upload(e, core.OrderInvoice)
	assert.Equal(t, 403, code, "an invoice scan needs its restricted field: %v", out)
	code, up = upload(fx.alice, core.OrderInvoice)
	require.Equal(t, 200, code, up)
	invoice := up["attachment_id"].(string)
	code, out = upload(fx.carol, core.OrderContract)
	assert.Equal(t, 404, code, "not the reader's order: %v", out)

	list := func(c client) []any {
		code, out := c.do("GET", base, nil)
		require.Equal(t, 200, code, out)
		return out["attachments"].([]any)
	}
	assert.Len(t, list(fx.alice), 2)
	assert.Len(t, list(e), 1, "the invoice scans are not erin's to see")
	assert.Equal(t, contract, at(list(e), 0, "id"))
	assert.Len(t, list(g), 1, "gina reads the invoice_scan field but holds no read_invoice: the restricted field stays hidden")
	assert.Empty(t, list(f), "frank reads the order but not its contract_file field: no category is his to see")
	code, out = f.do("POST", base+"/"+contract+"/download", nil)
	assert.Equal(t, 404, code, "a category whose field is not readable: %v", out)
	code, out = upload(f, core.OrderContract)
	assert.Equal(t, 403, code, "an upload to a field frank cannot read: %v", out)
	// a replacement takes the attachment's own category: an invoice scan erin cannot see is not found, whatever
	// category the request names (or none)
	for _, cat := range []string{"", core.OrderContract} {
		code, out = e.do("POST", base, map[string]any{"category": cat, "file_name": "x.pdf", "size": 10,
			"content_type": "application/pdf", "replace_attachment_id": invoice})
		assert.Equal(t, 404, code, "replacing an invoice scan with category %q: %v", cat, out)
	}

	code, dl := e.do("POST", base+"/"+contract+"/download", nil)
	require.Equal(t, 200, code, dl)
	assert.NotEmpty(t, dl["url"], "a normal category is a presigned address")
	code, out = e.do("POST", base+"/"+invoice+"/download", nil)
	assert.Equal(t, 404, code, out)
	code, dl = fx.alice.do("POST", base+"/"+invoice+"/download", nil)
	require.Equal(t, 200, code, dl)
	assert.NotEmpty(t, dl["credential"], "a strict category is a credential presented at infra/attachment")
	assert.Equal(t, "/api/attachment/downloads", at(dl, "download", "path"))
	other := fx.order(fx.le, nil, fx.svc("1", "100"))
	code, out = fx.alice.do("POST", "/api/purchase/orders/"+other["id"].(string)+"/attachments/"+contract+"/download", nil)
	assert.Equal(t, 404, code, "an attachment of another order: %v", out)

	code, out = e.do("DELETE", base+"/"+invoice, nil)
	assert.Equal(t, 404, code, out)
	code, _ = fx.alice.do("DELETE", base+"/"+contract, nil)
	assert.Equal(t, 204, code)
	assert.Equal(t, contract, fx.att.last("Detach").attachment)

	// the order's end: a cancelled order's attachments are detached with the date as their retention start, a closed
	// one's get the date as their retention start
	fx.orderAction(fx.alice, id, "cancel", nil, 200)
	today := time.Now().UTC().Format(time.DateOnly)
	require.Eventually(t, func() bool {
		c := fx.att.find("Detach", id)
		return c != nil && c.attachment == "" && c.date == today
	}, 20*time.Second, 100*time.Millisecond, "the cancellation detaches the order's attachments")
	code, out = upload(fx.alice, core.OrderContract)
	assert.Equal(t, 400, code, "a cancelled order takes no attachment: %v", out)
	// a completed order still takes an invoice scan; its retention counts from the upload at the latest
	done := fx.approved(fx.le, fx.svc("1", "100"))
	_, err := fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".purchase_order SET state = 'completed' WHERE id = $1", done["id"])
	require.NoError(t, err)
	code, up = fx.alice.do("POST", "/api/purchase/orders/"+done["id"].(string)+"/attachments",
		map[string]any{"category": core.OrderInvoice, "file_name": "i.pdf", "size": 10, "content_type": "application/pdf"})
	require.Equal(t, 200, code, up)
	rs := fx.att.find("SetRetentionStart", done["id"].(string))
	require.NotNil(t, rs, "the upload to a completed order sets the retention start")
	assert.Equal(t, up["attachment_id"], rs.attachment)
	assert.Equal(t, today, rs.date)
	closed := fx.approved(fx.le, fx.svc("1", "100"))
	fx.orderAction(fx.alice, closed["id"].(string), "close", nil, 200)
	require.Eventually(t, func() bool {
		c := fx.att.find("SetRetentionStart", closed["id"].(string))
		return c != nil && c.date == today
	}, 20*time.Second, 100*time.Millisecond, "the close starts the attachments' retention")
}

// erin edits and reads every order but not their invoice scans.
const erin = "0190f3a1-0000-7000-8000-0000000000e5"

// gina edits and reads every order with its attachment fields, but holds no erp.purchase.order.read_invoice.
const gina = "0190f3a1-0000-7000-8000-0000000000a7"

// frank edits and reads every order but none of their attachment fields.
const frank = "0190f3a1-0000-7000-8000-0000000000f6"

func allScope() map[string]sdkauthz.Atom {
	return map[string]sdkauthz.Atom{core.DimLegalEntity: {Kind: "all"}, core.DimDepartment: {Kind: "all"},
		core.DimOwner: {Kind: "all"}}
}
