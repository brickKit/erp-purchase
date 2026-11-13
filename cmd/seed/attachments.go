package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"

	"github.com/brickKit/be-sdk-go/v3/seed"
)

// contractPDF is the seed's contract file: a one-page PDF (the attachment component detects the type by content).
var contractPDF = []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
	"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")

// attachments gives the first approved and completed orders of CN01 a contract file each, through infra/attachment
// when the deployment has it (INFRA_ATTACHMENT_URL ready): the session at erp/purchase, the upload to the presigned
// address, the confirmation at infra/attachment. An order that has attachments already is left alone, so a second run
// changes nothing.
func attachments(c client, w world, token string) {
	base := env("INFRA_ATTACHMENT_URL", "http://localhost:18204")
	if !seed.Ready(base) {
		fmt.Println("seed erp/purchase: no infra/attachment; the orders get no attachments")
		return
	}
	at := client{seed.NewClient(base, token)}
	n := 0
	for _, state := range []string{"approved", "completed"} {
		orders := c.must("GET", "/api/purchase/orders?limit=3&state="+state+"&legal_entity_id="+w.les["CN01"], nil)["orders"].([]any)
		for _, o := range orders {
			id := o.(map[string]any)["id"].(string)
			path := "/api/purchase/orders/" + id + "/attachments"
			if len(c.must("GET", path, nil)["attachments"].([]any)) > 0 {
				continue
			}
			up := c.must("POST", path, map[string]any{"category": "erp.purchase.order_contract",
				"file_name": "采购合同-" + id[:8] + ".pdf", "size": len(contractPDF), "content_type": "application/pdf"})
			req, err := http.NewRequest("PUT", up["upload_url"].(string), bytes.NewReader(contractPDF))
			check(err)
			req.Header.Set("Content-Type", "application/pdf")
			req.ContentLength = int64(len(contractPDF))
			res, err := http.DefaultClient.Do(req)
			check(err)
			res.Body.Close()
			if res.StatusCode/100 != 2 {
				fail("the upload of order %s's contract: %s", id, strconv.Itoa(res.StatusCode))
			}
			at.must("POST", "/api/attachment/uploads/confirm", map[string]any{"credential": up["confirm_credential"]})
			n++
		}
	}
	fmt.Printf("seed erp/purchase: %d contracts attached\n", n)
}
