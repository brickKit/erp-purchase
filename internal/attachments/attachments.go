// Package attachments is the purchase orders' attachments as their owner asks infra/attachment for them (assembly
// protocol 12; docs/design.md, the attachments): the user's access judged here by the order and the category's field,
// then the upload session, the list, the download or the detach asked of infra/attachment on the user's behalf; and
// the order's end (cancelled, completed, closed) commanded to it by a work item (D68: the owner commands).
package attachments

import (
	"context"
	"encoding/json"
	"time"

	core "github.com/brickKit/erp-purchase/v3/internal/core"
	orders "github.com/brickKit/erp-purchase/v3/internal/orders"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/calls"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/work"
	attachmentv3 "github.com/brickKit/infra-attachment/contract/v3/gen/go/be/attachment/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// client is infra/attachment's system surface; absent when the deployment has none.
func client(m *core.Base) (attachmentv3.AttachmentServiceClient, error) {
	conn, ok := m.RT.GRPC(core.Attachment)
	if !ok {
		return nil, errs.New(errs.ReasonDependencyUnavailable, "the deployment has no attachment component")
	}
	return attachmentv3.NewAttachmentServiceClient(conn), nil
}

func parent(o orders.Order) *attachmentv3.Parent {
	return &attachmentv3.Parent{ResourceType: core.Orders, RecordId: o.ID}
}

// fields are the order's fields the caller may read (security 01's Readable, P8.15), the restricted ones also with
// their read key: an attachment category is the caller's to see when its field is (assembly protocol 12, security
// 09a). Nil when the caller may not read the order at all.
func fields(c *httpx.Context, o orders.Order) map[string]bool {
	s, ok := sdkauthz.From(c.Ctx())
	if !ok {
		return nil
	}
	facts := orders.OrderFacts(o)
	_, d, err := s.Check(c.Ctx(), core.OpOrderRead, facts)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, f := range d.Fields {
		if def, ok := core.OrderField(f); ok && def.Class == sdkauthz.Restricted {
			if _, _, err := s.Check(c.Ctx(), def.ReadKey, facts); err != nil {
				continue
			}
		}
		out[f] = true
	}
	return out
}

// readable says whether the caller may see a category of the order: its field is readable.
func readable(seen map[string]bool, category string) bool {
	cat, ok := core.OrderAttachments[category]
	return ok && seen[cat.Field]
}

// order reads the order of the route and decides op on it.
func order(m *core.Base, c *httpx.Context, op string) (context.Context, string, orders.Order, error) {
	o, err := orders.Read(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return nil, "", o, err
	}
	ctx, user, err := core.Check(c, op, orders.OrderFacts(o))
	return ctx, user, o, err
}

// Upload asks for an upload session of a contract or an invoice scan (the order's edit; not on a cancelled or closed
// order); the user then uploads to the address and confirms at infra/attachment with the confirm credential.
func Upload(m *core.Base, c *httpx.Context) error {
	var in struct {
		Category    string `json:"category"`
		FileName    string `json:"file_name"`
		Size        int64  `json:"size"`
		ContentType string `json:"content_type"`
		Replace     string `json:"replace_attachment_id"`
	}
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	ctx, user, o, err := order(m, c, core.OpOrderEdit)
	if err != nil {
		return err
	}
	if o.State == orders.Cancelled || o.State == orders.Closed {
		return core.Invalid("id", "a "+o.State+" order takes no attachment")
	}
	at, err := client(m)
	if err != nil {
		return err
	}
	seen := fields(c, o)
	if in.Replace != "" { // the category is the attachment's: one the caller cannot see is not found
		cat, err := category(ctx, at, o, in.Replace)
		if err != nil {
			return err
		}
		if !readable(seen, cat) {
			return core.NotFound("attachment")
		}
		if in.Category != "" && in.Category != cat {
			return core.Invalid("category", "a replacement keeps the attachment's category")
		}
		in.Category = cat
	} else if _, ok := core.OrderAttachments[in.Category]; !ok {
		return core.Invalid("category", "an order takes "+core.OrderContract+" or "+core.OrderInvoice)
	} else if !readable(seen, in.Category) {
		return errs.New(errs.ReasonPermissionDenied, "the "+in.Category+" files of this order are not yours to see")
	}
	up, err := at.CreateUpload(ctx, &attachmentv3.CreateUploadRequest{Parent: parent(o), Category: in.Category,
		FileName: in.FileName, Size: in.Size, ContentType: in.ContentType, User: user, ReplaceAttachmentId: in.Replace})
	if err != nil {
		return err
	}
	if o.State == orders.Completed { // the order has ended: the retention counts from now at the latest (D68)
		if _, err := at.SetRetentionStart(ctx, &attachmentv3.SetRetentionStartRequest{Parent: parent(o),
			AttachmentId: up.GetAttachmentId(), Date: time.Now().UTC().Format(time.DateOnly)}, calls.Idempotent()); err != nil {
			return err
		}
	}
	return c.JSON(200, map[string]any{"attachment_id": up.GetAttachmentId(), "version": up.GetVersion(),
		"upload_url": up.GetUploadUrl(), "confirm_credential": up.GetConfirmCredential(), "expires_at": up.GetExpiresAt(),
		"confirm": map[string]string{"component": core.Attachment, "path": "/api/attachment/uploads/confirm"}})
}

// List answers the order's attachments the caller may read (the order's read; the invoice scans with their field's).
func List(m *core.Base, c *httpx.Context) error {
	ctx, _, o, err := order(m, c, core.OpOrderRead)
	if err != nil {
		return err
	}
	at, err := client(m)
	if err != nil {
		return err
	}
	res, err := at.List(ctx, &attachmentv3.ListRequest{Parent: parent(o)}, calls.Idempotent())
	if err != nil {
		return err
	}
	out := []map[string]any{}
	seen := fields(c, o)
	for _, a := range res.GetAttachments() {
		if !readable(seen, a.GetCategory()) {
			continue
		}
		out = append(out, map[string]any{"id": a.GetId(), "category": a.GetCategory(), "state": a.GetState(),
			"file_name": a.GetFileName(), "size": a.GetSize(), "content_type": a.GetContentType(),
			"version": a.GetVersion(), "unscanned": a.GetUnscanned(), "created_at": a.GetCreatedAt()})
	}
	return c.JSON(200, map[string]any{"attachments": out})
}

// Download asks for the caller's download of an attachment (the order's read and the category's field): a presigned
// address, or a one-time credential the user presents at infra/attachment.
func Download(m *core.Base, c *httpx.Context) error {
	ctx, user, o, err := order(m, c, core.OpOrderRead)
	if err != nil {
		return err
	}
	at, err := client(m)
	if err != nil {
		return err
	}
	cat, err := category(ctx, at, o, c.Param("aid"))
	if err != nil {
		return err
	}
	if !readable(fields(c, o), cat) {
		return core.NotFound("attachment")
	}
	dl, err := at.IssueDownload(ctx, &attachmentv3.IssueDownloadRequest{Parent: parent(o), AttachmentId: c.Param("aid"), User: user})
	if err != nil {
		return err
	}
	out := map[string]any{"expires_at": dl.GetExpiresAt()}
	if dl.GetUrl() != "" {
		out["url"] = dl.GetUrl()
	} else {
		out["credential"] = dl.GetCredential()
		out["download"] = map[string]string{"component": core.Attachment, "path": "/api/attachment/downloads"}
	}
	return c.JSON(200, out)
}

// category is an attachment's category, from the order's list (an attachment of another order is not found).
func category(ctx context.Context, at attachmentv3.AttachmentServiceClient, o orders.Order, id string) (string, error) {
	res, err := at.List(ctx, &attachmentv3.ListRequest{Parent: parent(o)}, calls.Idempotent())
	if err != nil {
		return "", err
	}
	for _, a := range res.GetAttachments() {
		if a.GetId() == id {
			return a.GetCategory(), nil
		}
	}
	return "", core.NotFound("attachment")
}

// Remove takes an attachment off the order (the order's edit; the category's field to see it).
func Remove(m *core.Base, c *httpx.Context) error {
	ctx, _, o, err := order(m, c, core.OpOrderEdit)
	if err != nil {
		return err
	}
	at, err := client(m)
	if err != nil {
		return err
	}
	cat, err := category(ctx, at, o, c.Param("aid"))
	if err != nil {
		return err
	}
	if !readable(fields(c, o), cat) {
		return core.NotFound("attachment")
	}
	if _, err := at.Detach(ctx, &attachmentv3.DetachRequest{Parent: parent(o), AttachmentId: c.Param("aid")},
		calls.Idempotent()); err != nil {
		return err
	}
	return c.NoContent(204)
}

// Ended is the work item an order's end queues (core.JobOrderEnded): a cancelled order's attachments are detached
// with the cancellation's date as their retention start; a completed or closed order's get the date as their
// retention start. A deployment without infra/attachment has nothing to command.
func Ended(m *core.Base, ctx context.Context, r *work.Run) error {
	var in core.OrderEnded
	if err := json.Unmarshal(r.Args, &in); err != nil {
		return err
	}
	at, err := client(m)
	if err != nil {
		return nil
	}
	p := &attachmentv3.Parent{ResourceType: core.Orders, RecordId: in.OrderID}
	if in.State == orders.Cancelled {
		_, err = at.Detach(ctx, &attachmentv3.DetachRequest{Parent: p, RetentionStart: in.Date}, calls.Idempotent())
	} else {
		_, err = at.SetRetentionStart(ctx, &attachmentv3.SetRetentionStartRequest{Parent: p, Date: in.Date}, calls.Idempotent())
	}
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Aborted, codes.Internal, codes.Unknown:
		return err // retried: unreachable, or the catalog has not the orders yet (DEFINITION_PENDING)
	}
	// a refusal that a retry does not change (not the owner, a malformed command): an alert, and the item ends
	m.RT.Logger.Error("infra/attachment refused an order's end", "event", "order_attachments_refused",
		"order", in.OrderID, "state", in.State, "error", err.Error())
	return nil
}
