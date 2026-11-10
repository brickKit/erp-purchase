// Command seed fills a running erp/purchase with the seed data for people (be-assembly-standard's seed and test data
// conventions):
//
//   - access, by the authorization admin: the role maintain (the seed user: every operation, legal entity, department
//     and owner); test.buyer.cn, a buyer of CN01: requisitions read in CN01, orders, receipts, acceptances and returns
//     kept in CN01's purchasing department (PUR) and below; test.buyer.lead, the purchasing lead: approves CN01's
//     requisitions and orders and reads them all (an approver is never the submitter: the seed user submits);
//   - policies: CN01 needs approval with 5% over-receipt; ES01 and US01 need none (US01 10% over-receipt);
//   - requisitions in each legal entity in every state: drafts, submitted, approved, rejected with a reason,
//     cancelled, approved and partly ordered, ordered whole (closed), ordered then the order cancelled (approved again);
//   - orders in every state: drafts, submitted, rejected back to draft with the reason, approved, partly received,
//     received whole (completed), completed then a return, partly received then closed, cancelled, revised (revision 2,
//     submitted again), services accepted by amount; CN01 has more than a page (210); receipts go through the running
//     erp/inventory (goods not tracked or tracked by batch: no serial numbers).
//
// Requisitions and orders are seeded once, into a legal entity that has none yet; a run that broke off is finished by
// running it again within a day (the idempotency keys answer the requests already made). Policies are set only when
// they differ, so a second run changes nothing. The legal entities, departments, suppliers, products and warehouses
// come from mdm/org, mdm/partner, mdm/product and erp/inventory (their seeds run first).
//
//	SEED_IAM_URL (http://localhost:18200)    the identity member's development exchange
//	SEED_AUTHZ_URL (http://localhost:18223)  the authorization member's effect point
//	SEED_ADMIN (test.alice)                  the authorization admin (the install claim)
//	SEED_USER (test.bob), SEED_PASSWORD      the user that writes the data; the test accounts' password
//	ERP_PURCHASE_URL (http://localhost:18085), MDM_ORG_URL (http://localhost:18083), MDM_PARTNER_URL (http://localhost:18225),
//	MDM_PRODUCT_URL (http://localhost:18082), ERP_INVENTORY_URL (http://localhost:18086)
package main

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"time"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/seed"
)

const prefix = "「本地测试」"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "seed erp/purchase: "+format+"\n", a...)
	os.Exit(1)
}

func check(err error) {
	if err != nil {
		fail("%v", err)
	}
}

type client struct{ seed.Client }

func (c client) must(method, path string, body any, header ...string) map[string]any {
	out, err := c.Must(method, path, body, header...)
	check(err)
	return out
}

// pages reads every page of a list.
func (c client) pages(path, key string) []map[string]any {
	var out []map[string]any
	cursor := ""
	for {
		page := c.must("GET", path+"&limit=200&cursor="+url.QueryEscape(cursor), nil)
		for _, x := range page[key].([]any) {
			out = append(out, x.(map[string]any))
		}
		if cursor, _ = page["next_cursor"].(string); cursor == "" {
			return out
		}
	}
}

// segment grants an operation on a purchase resource: in the legal entities (all when none) and the department
// subtrees (all when none), any owner. The policy carries the legal entity only.
func segment(resource, op string, legalEntities, departments []string) sdkauthz.Segment {
	s := seed.Segment(resource, op)
	le := sdkauthz.Atom{Kind: "all"}
	if len(legalEntities) > 0 {
		le = sdkauthz.Atom{Kind: "in", Values: legalEntities}
	}
	s.Atoms = map[string]sdkauthz.Atom{"legal_entity": le}
	if resource == "erp.purchase.policy" {
		return s
	}
	dept := sdkauthz.Atom{Kind: "all"}
	if len(departments) > 0 {
		dept = sdkauthz.Atom{Kind: "tree"}
		for _, p := range departments {
			dept.Items = append(dept.Items, sdkauthz.TreeItem{Path: p, Mode: "subtree"})
		}
	}
	s.Atoms["department"], s.Atoms["owner"] = dept, sdkauthz.Atom{Kind: "all"}
	return s
}

func segments(les, depts []string, ops ...[2]string) []sdkauthz.Segment {
	var out []sdkauthz.Segment
	for _, op := range ops {
		out = append(out, segment(op[0], op[1], les, depts))
	}
	return out
}

var (
	reqRead     = [2]string{"erp.purchase.requisition", "erp.purchase.requisition.read"}
	reqEdit     = [2]string{"erp.purchase.requisition", "erp.purchase.requisition.edit"}
	reqApprove  = [2]string{"erp.purchase.requisition", "erp.purchase.requisition.approve"}
	ordRead     = [2]string{"erp.purchase.order", "erp.purchase.order.read"}
	ordEdit     = [2]string{"erp.purchase.order", "erp.purchase.order.edit"}
	ordApprove  = [2]string{"erp.purchase.order", "erp.purchase.order.approve"}
	recRead     = [2]string{"erp.purchase.receipt", "erp.purchase.receipt.read"}
	recCreate   = [2]string{"erp.purchase.receipt", "erp.purchase.receipt.create"}
	retRead     = [2]string{"erp.purchase.return", "erp.purchase.return.read"}
	retEdit     = [2]string{"erp.purchase.return", "erp.purchase.return.edit"}
	polRead     = [2]string{"erp.purchase.policy", "erp.purchase.policy.read"}
	polManage   = [2]string{"erp.purchase.policy", "erp.purchase.policy.manage"}
	everyAction = [][2]string{reqRead, reqEdit, reqApprove, ordRead, ordEdit, ordApprove, recRead, recCreate, retRead, retEdit,
		polRead, polManage}
)

type product struct {
	id, code, kind, tracking, unit string
	purchasable                    map[string]bool // legal entity code → purchasable there
	stocked                        bool
}

type world struct {
	les       map[string]string            // code → ID
	depts     map[string]map[string]string // legal entity code → department code → ID
	paths     map[string]map[string]string // legal entity code → department code → path
	products  []product
	suppliers map[string][]supplier // active supplier roles by legal entity code
	whs       map[string]string     // warehouse code → ID
}

type supplier struct{ id, currency string }

func main() {
	base := env("ERP_PURCHASE_URL", "http://localhost:18085")
	if !seed.Ready(base) {
		fail("erp/purchase at %s is not ready", base)
	}
	pw := os.Getenv("SEED_PASSWORD")
	if pw == "" {
		fail("SEED_PASSWORD is not set (the test accounts' password in Casdoor)")
	}
	iam, user, admin := env("SEED_IAM_URL", "http://localhost:18200"), env("SEED_USER", "test.bob"), env("SEED_ADMIN", "test.alice")
	accounts, err := seed.Accounts(iam, pw, admin, user, "test.buyer.cn", "test.buyer.lead")
	check(err)
	t := accounts[user].Token
	w := read(t)
	a, err := seed.NewAuthz(env("SEED_AUTHZ_URL", "http://localhost:18223"), accounts[admin].Token, "erp/purchase")
	check(err)
	cn, pur := []string{w.les["CN01"]}, []string{w.paths["CN01"]["PUR"]}
	buyer := append(segments(cn, nil, reqRead, reqEdit), segments(cn, pur, ordRead, ordEdit, recRead, recCreate, retRead, retEdit)...)
	buyer = append(buyer, segment(polRead[0], polRead[1], cn, nil))
	lead := segments(cn, nil, reqRead, reqApprove, ordRead, ordApprove, recRead, retRead, polRead)
	_, err = a.GrantRoles([]seed.RoleSpec{
		{Key: "maintain", Name: prefix + "采购维护", To: []string{user}, Segments: segments(nil, nil, everyAction...)},
		{Key: "buyer-cn", Name: prefix + "采购员（CN01 采购部）", To: []string{"test.buyer.cn"}, Segments: buyer},
		{Key: "lead-cn", Name: prefix + "采购主管（CN01 审批）", To: []string{"test.buyer.lead"}, Segments: lead},
	}, accounts)
	check(err)

	c, approver := client{seed.NewClient(base, t)}, client{seed.NewClient(base, accounts["test.buyer.lead"].Token)}
	policies(c, w)
	owners := map[string]string{"buyer": accounts["test.buyer.cn"].Principal, "seed": accounts[user].Principal}
	for _, le := range []string{"CN01", "ES01", "US01"} {
		if len(c.must("GET", "/api/purchase/orders?limit=1&legal_entity_id="+w.les[le], nil)["orders"].([]any)) > 0 {
			fmt.Printf("seed erp/purchase: %s has orders already; requisitions and orders are not seeded again\n", le)
			continue
		}
		s := &seeder{c: c, lead: approver, w: w, le: le, owners: owners}
		reqs := s.requisitions()
		n := s.orders()
		fmt.Printf("seed erp/purchase: %s: %d requisitions, %d orders\n", le, reqs, n)
	}
}

// read gathers what the seed needs from the components it builds on.
func read(t string) world {
	w := world{les: map[string]string{}, depts: map[string]map[string]string{}, paths: map[string]map[string]string{},
		suppliers: map[string][]supplier{}, whs: map[string]string{}}
	org := client{seed.NewClient(env("MDM_ORG_URL", "http://localhost:18083"), t)}
	for _, e := range org.must("GET", "/api/org/legal-entities?limit=200", nil)["legal_entities"].([]any) {
		e := e.(map[string]any)
		w.les[e["code"].(string)] = e["id"].(string)
	}
	code := map[string]string{}
	for _, le := range []string{"CN01", "ES01", "US01"} {
		if w.les[le] == "" {
			fail("mdm/org has no legal entity %s: run its seed first", le)
		}
		code[w.les[le]] = le
		w.depts[le], w.paths[le] = map[string]string{}, map[string]string{}
		for _, u := range org.pages("/api/org/units?state=active&legal_entity_id="+w.les[le], "units") {
			w.depts[le][u["code"].(string)], w.paths[le][u["code"].(string)] = u["id"].(string), u["path"].(string)
		}
	}
	for _, d := range []string{"PUR", "PUR-RAW", "PUR-EQ", "RD", "QC"} {
		if w.depts["CN01"][d] == "" {
			fail("mdm/org has no CN01 department %s: run its seed first", d)
		}
	}
	mdmProduct := client{seed.NewClient(env("MDM_PRODUCT_URL", "http://localhost:18082"), t)}
	for _, p := range mdmProduct.pages("/api/product/products?state=active", "products") {
		pr := product{id: p["id"].(string), code: p["code"].(string), kind: p["kind"].(string), tracking: p["tracking"].(string),
			unit: p["base_unit"].(string), purchasable: map[string]bool{}, stocked: p["stocked"] == true}
		for _, av := range p["availability"].([]any) {
			av := av.(map[string]any)
			if le := code[av["legal_entity_id"].(string)]; le != "" && av["purchasable"] == true {
				pr.purchasable[le] = true
			}
		}
		if pr.kind == "service" || (pr.stocked && pr.tracking != "serial") {
			w.products = append(w.products, pr)
		}
	}
	sort.Slice(w.products, func(i, j int) bool { return w.products[i].code < w.products[j].code })
	partner := client{seed.NewClient(env("MDM_PARTNER_URL", "http://localhost:18225"), t)}
	partners := partner.pages("/api/partner/partners?state=active", "partners")
	sort.Slice(partners, func(i, j int) bool { return partners[i]["name"].(string) < partners[j]["name"].(string) })
	for _, p := range partners {
		for _, r := range partner.must("GET", "/api/partner/partners/"+p["id"].(string)+"/roles", nil)["roles"].([]any) {
			r := r.(map[string]any)
			le, _ := r["legal_entity_id"].(string)
			cur, _ := r["default_currency"].(string)
			if r["role"] == "supplier" && r["state"] == "active" && code[le] != "" && cur != "" {
				w.suppliers[code[le]] = append(w.suppliers[code[le]], supplier{p["id"].(string), cur})
			}
		}
	}
	inv := client{seed.NewClient(env("ERP_INVENTORY_URL", "http://localhost:18086"), t)}
	for _, wh := range inv.pages("/api/inventory/warehouses?state=active", "warehouses") {
		w.whs[wh["code"].(string)] = wh["id"].(string)
	}
	for _, wh := range []string{"SH-01", "BJ-01", "MAD-01", "NYC-01"} {
		if w.whs[wh] == "" {
			fail("erp/inventory has no warehouse %s: run its seed first", wh)
		}
	}
	return w
}

// policies sets each legal entity's policy when it differs: a second run changes nothing.
func policies(c client, w world) {
	for le, want := range map[string][2]string{"CN01": {"required", "5"}, "ES01": {"none", "0"}, "US01": {"none", "10"}} {
		path := "/api/purchase/policies/" + w.les[le]
		st, cur, err := c.UntilGranted(30*time.Second, "GET", path, nil)
		check(err)
		if st != 200 {
			fail("policy %s: %d %v", le, st, cur)
		}
		if cur["approval"] == want[0] && cur["over_receipt_percent"] == want[1] {
			continue
		}
		c.must("PUT", path, map[string]any{"approval": want[0], "over_receipt_percent": want[1], "version": cur["version"]})
	}
}
