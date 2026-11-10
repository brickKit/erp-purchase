package main

import (
	"fmt"
	"time"
)

// seeder makes one legal entity's requisitions and orders: the seed user writes and submits, the purchasing lead
// approves (CN01); ES01 and US01 need no approval, so their submissions are approved at once.
type seeder struct {
	c, lead client
	w       world
	le      string
	owners  map[string]string
	goods   []product
	svcs    []product
	// approved requisition lines the orders are made from
	open []reqLine
}

type reqLine struct {
	id, product, unit string
	use               int // 5: ordered in part, 6: ordered whole (the requisition closes), 7: ordered, the order cancelled
}

func (s *seeder) key(kind string, n int) string {
	return fmt.Sprintf("seed:erp/purchase:%s:%s:%d", s.le, kind, n)
}

func (s *seeder) post(c client, path, key string, body any) (int, map[string]any) {
	st, out, err := c.Do("POST", path, body, "Idempotency-Key", key)
	check(err)
	return st, out
}

// ok posts and fails the seed unless the answer is one of want.
func (s *seeder) ok(c client, path, key string, body any, want ...int) map[string]any {
	st, out := s.post(c, path, key, body)
	for _, w := range want {
		if st == w {
			return out
		}
	}
	fail("POST %s (%s): %d %v", path, key, st, out)
	return nil
}

func (s *seeder) approves() bool { return s.le == "CN01" }

// asLead approves or rejects a submitted requisition or order as the purchasing lead, waiting for the lead's grant
// the first time.
func (s *seeder) asLead(path string, body any) {
	st, out, err := s.lead.UntilGranted(30*time.Second, "POST", path, body)
	check(err)
	if st != 200 {
		fail("POST %s as the purchasing lead: %d %v", path, st, out)
	}
}

func (s *seeder) products() {
	for _, p := range s.w.products {
		if !p.purchasable[s.le] {
			continue
		}
		if p.kind == "service" {
			s.svcs = append(s.svcs, p)
		} else {
			s.goods = append(s.goods, p)
		}
	}
	if len(s.goods) == 0 || len(s.svcs) == 0 {
		fail("%s has no purchasable goods or services: run mdm/product's seed first", s.le)
	}
	if len(s.w.suppliers[s.le]) == 0 {
		fail("%s has no supplier: run mdm/partner's seed (3.0.40 or later) first", s.le)
	}
}

// requisitions seeds the legal entity's requisitions in every state; the approved lines of states 5-7 go to
// the orders. It answers how many it made.
func (s *seeder) requisitions() int {
	s.products()
	depts := map[string][]string{"CN01": {"RD", "QC", "PUR-RAW", "PUR-EQ", "PUR"}}[s.le]
	total := map[string]int{"CN01": 48, "ES01": 12, "US01": 12}[s.le]
	for n := 0; n < total; n++ {
		g := s.goods[n%len(s.goods)]
		body := map[string]any{"legal_entity_id": s.w.les[s.le], "lines": []map[string]any{
			{"product_id": g.id, "quantity": "10", "unit": g.unit, "needed_date": "2026-12-15"}}}
		if n%3 == 0 {
			sv := s.svcs[n%len(s.svcs)]
			body["lines"] = append(body["lines"].([]map[string]any), map[string]any{"product_id": sv.id, "quantity": "4", "unit": sv.unit})
		}
		if len(depts) > 0 {
			body["department_id"] = s.w.depts[s.le][depts[n%len(depts)]]
		}
		r := s.ok(s.c, "/api/purchase/requisitions", s.key("requisition", n), body, 200)
		id := r["id"].(string)
		path := "/api/purchase/requisitions/" + id
		state := n % 8
		if state == 0 {
			continue // draft
		}
		if state == 4 { // cancelled as a draft
			s.ok(s.c, path+"/cancel", s.key("requisition-cancel", n), nil, 200)
			continue
		}
		r = s.ok(s.c, path+"/submit", s.key("requisition-submit", n), nil, 200)
		if state == 1 && s.approves() {
			continue // submitted, waiting for approval
		}
		if state == 3 && s.approves() {
			s.asLead(path+"/reject", map[string]any{"reason": "预算已用完，下季度再提"})
			continue
		}
		if s.approves() {
			s.asLead(path+"/approve", nil)
		}
		if state >= 5 { // ordered from below: part, whole, then the order cancelled
			line := r["lines"].([]any)[0].(map[string]any)
			s.open = append(s.open, reqLine{id: line["id"].(string), product: line["product_id"].(string), unit: line["unit"].(string),
				use: state})
		}
	}
	return total
}

// orders seeds the legal entity's orders in every state; it answers how many it made.
func (s *seeder) orders() int {
	local := map[string]string{"CN01": "CNY", "ES01": "EUR", "US01": "USD"}[s.le]
	wh := map[string]string{"CN01": "SH-01", "ES01": "MAD-01", "US01": "NYC-01"}[s.le]
	var sups []supplier
	for _, sp := range s.w.suppliers[s.le] {
		if sp.currency == local {
			sups = append(sups, sp)
		}
	}
	if len(sups) == 0 {
		fail("%s has no supplier in %s", s.le, local)
	}
	var foreign []supplier
	for _, sp := range s.w.suppliers[s.le] {
		if sp.currency != local {
			foreign = append(foreign, sp)
		}
	}
	slots := []slot{{"", s.owners["seed"]}}
	if s.le == "CN01" {
		slots = []slot{{"PUR-RAW", s.owners["buyer"]}, {"PUR-EQ", s.owners["buyer"]}, {"PUR", s.owners["buyer"]},
			{"PUR-RAW", s.owners["seed"]}, {"RD", s.owners["seed"]}} // RD's are outside the buyer's department
	}
	total := map[string]int{"CN01": 210, "ES01": 30, "US01": 24}[s.le]
	made := 0
	// first the orders from requisitions (PR06): part, whole, and one whose order is cancelled
	for i, rl := range s.open {
		qty := "4"
		if rl.use == 6 {
			qty = "10"
		}
		body := s.head(sups[i%len(sups)], slots[i%len(slots)])
		body["lines"] = []map[string]any{{"product_id": rl.product, "quantity": qty, "unit": rl.unit, "unit_price": "12.5",
			"warehouse_id": s.w.whs[wh], "requisition_line_id": rl.id}}
		o := s.ok(s.c, "/api/purchase/orders", s.key("from-requisition", i), body, 200)
		made++
		if rl.use == 7 {
			s.ok(s.c, "/api/purchase/orders/"+o["id"].(string)+"/cancel", s.key("from-requisition-cancel", i), nil, 200)
		}
	}
	for n := 0; n < total; n++ {
		sp := sups[n%len(sups)]
		if n%17 == 5 && len(foreign) > 0 { // a supplier paid in a foreign currency
			sp = foreign[n%len(foreign)]
		}
		state := n % 12
		body := s.head(sp, slots[n%len(slots)])
		g := s.goods[n%len(s.goods)]
		lines := []map[string]any{{"product_id": g.id, "quantity": fmt.Sprint(4 + n%5), "unit": g.unit,
			"unit_price": fmt.Sprintf("%d.%02d", 8+n%40, (n*13)%100), "warehouse_id": s.w.whs[wh], "expected_date": "2026-12-20"}}
		if state == 11 { // services only, accepted by amount
			sv := s.svcs[n%len(s.svcs)]
			lines = []map[string]any{{"product_id": sv.id, "quantity": "8", "unit": sv.unit, "unit_price": "150"}}
		} else if n%4 == 1 {
			sv := s.svcs[n%len(s.svcs)]
			lines = append(lines, map[string]any{"product_id": sv.id, "quantity": "2", "unit": sv.unit, "unit_price": "300"})
		}
		body["lines"] = lines
		o := s.ok(s.c, "/api/purchase/orders", s.key("order", n), body, 200)
		made++
		id := o["id"].(string)
		path := "/api/purchase/orders/" + id
		switch state {
		case 0, 1: // drafts
			continue
		case 9: // a cancelled draft
			s.ok(s.c, path+"/cancel", s.key("order-cancel", n), nil, 200)
			continue
		}
		o = s.ok(s.c, path+"/submit", s.key("order-submit", n), nil, 200)
		if s.approves() {
			if state == 2 {
				continue // submitted, waiting for approval
			}
			if state == 3 { // rejected back to draft with its reason
				s.asLead(path+"/reject", map[string]any{"reason": "单价高于上次采购，请重新询价"})
				continue
			}
			s.asLead(path+"/approve", nil)
		}
		o = s.c.must("GET", path, nil)
		goodsLine := o["lines"].([]any)[0].(map[string]any)
		switch state {
		case 5: // partly received
			s.receive(path, n, goodsLine, "1")
		case 6, 7: // received whole: completed (services accepted whole too); 7: then 1 returned
			s.receive(path, n, goodsLine, goodsLine["quantity"].(string))
			for _, l := range o["lines"].([]any)[1:] {
				l := l.(map[string]any)
				s.ok(s.c, path+"/acceptances", s.key("acceptance", n), map[string]any{"lines": []map[string]any{
					{"order_line_id": l["id"], "quantity": l["quantity"]}}}, 200)
			}
			if state == 7 {
				mv := s.c.must("GET", "/api/purchase/receipts?state=posted&order_id="+id, nil)["receipts"].([]any)[0].(map[string]any)
				movement := mv["lines"].([]any)[0].(map[string]any)["movement_ids"].([]any)[0]
				s.ok(s.c, path+"/returns", s.key("return", n), map[string]any{"reason": "包装破损", "lines": []map[string]any{
					{"origin_movement_id": movement, "base_quantity": "1"}}}, 200)
			}
		case 8: // partly received, then closed
			s.receive(path, n, goodsLine, "1")
			s.ok(s.c, path+"/close", s.key("order-close", n), nil, 200)
		case 10: // revised: a new price, submitted again (revision 2)
			kept := map[string]any{"id": goodsLine["id"], "product_id": goodsLine["product_id"], "quantity": goodsLine["quantity"],
				"unit": goodsLine["unit"], "unit_price": "9.9", "warehouse_id": goodsLine["warehouse_id"]}
			rev := map[string]any{"legal_entity_id": o["legal_entity_id"], "supplier_id": o["supplier_id"], "currency": o["currency"],
				"owner_id": o["owner_id"], "department_id": o["department_id"], "version": o["version"], "lines": []any{kept}}
			for _, l := range o["lines"].([]any)[1:] {
				l := l.(map[string]any)
				rev["lines"] = append(rev["lines"].([]any), map[string]any{"id": l["id"], "product_id": l["product_id"],
					"quantity": l["quantity"], "unit": l["unit"], "unit_price": l["unit_price"]})
			}
			s.ok(s.c, path+"/revise", s.key("order-revise", n), rev, 200)
		case 11: // services accepted by amount, part of it
			s.ok(s.c, path+"/acceptances", s.key("acceptance", n), map[string]any{"lines": []map[string]any{
				{"order_line_id": goodsLine["id"], "amount": "450"}}}, 200)
		}
	}
	return made
}

// slot is where an order is kept: its department ("" none) and owner.
type slot struct{ dept, owner string }

// head is an order's header: the supplier, its currency, the slot's department and owner.
func (s *seeder) head(sp supplier, sl slot) map[string]any {
	body := map[string]any{"legal_entity_id": s.w.les[s.le], "supplier_id": sp.id, "currency": sp.currency, "owner_id": sl.owner}
	if sl.dept != "" && s.w.depts[s.le][sl.dept] != "" {
		body["department_id"] = s.w.depts[s.le][sl.dept]
	}
	return body
}

// receive receives a goods line (a batch for a batch-tracked product).
func (s *seeder) receive(path string, n int, line map[string]any, qty string) {
	l := map[string]any{"order_line_id": line["id"], "quantity": qty}
	for _, p := range s.goods {
		if p.id == line["product_id"] && p.tracking == "batch" {
			l["batch"] = fmt.Sprintf("SEED-%s-%03d", s.le, n)
		}
	}
	s.ok(s.c, path+"/receipts", s.key("receipt", n), map[string]any{"lines": []map[string]any{l}}, 200)
}
