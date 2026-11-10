package requisitions

import (
	"context"
	"fmt"
	"strings"

	core "github.com/brickKit/erp-purchase/v3/internal/core"
	masterdata "github.com/brickKit/erp-purchase/v3/internal/masterdata"
	orgunits "github.com/brickKit/erp-purchase/v3/internal/orgunits"
	policies "github.com/brickKit/erp-purchase/v3/internal/policies"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/bizdate"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/idem"
	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/list"
	"github.com/brickKit/be-sdk-go/v3/search"
)

// Requisitions: drafts, their change, submission, approval or rejection, cancellation, and the list.

type lineIn struct {
	ProductID  string  `json:"product_id"`
	Quantity   string  `json:"quantity"`
	Unit       string  `json:"unit"`
	NeededDate *string `json:"needed_date"`
}

// requisitionIn is the contract's RequisitionIn: a new draft, or a draft's change (its whole lines).
type requisitionIn struct {
	LegalEntityID string   `json:"legal_entity_id"`
	DepartmentID  *string  `json:"department_id"`
	Version       *int64   `json:"version"`
	Lines         []lineIn `json:"lines"`
}

const maxLines = 200

func checkLines(in []lineIn) error {
	if len(in) == 0 || len(in) > maxLines {
		return core.Invalid("lines", fmt.Sprintf("a requisition has 1-%d lines", maxLines))
	}
	for i := range in {
		l := &in[i]
		field := fmt.Sprintf("lines[%d]", i)
		if !core.UUIDForm.MatchString(l.ProductID) {
			return core.Invalid(field+".product_id", "product_id is a product's ID")
		}
		if strings.TrimSpace(l.Unit) == "" {
			return core.Invalid(field+".unit", "unit is required")
		}
		if l.NeededDate != nil && !core.DateForm.MatchString(*l.NeededDate) {
			return core.Invalid(field+".needed_date", "needed_date is YYYY-MM-DD")
		}
		var err error
		if l.Quantity, err = core.Decimal(field+".quantity", l.Quantity, core.QuantityScale, true); err != nil {
			return err
		}
	}
	return nil
}

// prepared is a draft's header and lines with their evidence, taken outside the transaction (PR01).
type prepared struct {
	le       masterdata.LegalEntity
	deptID   *string
	deptPath *string
	products map[string]masterdata.ProductInfo
}

// prepare checks a draft's input and takes its evidence: the department, the legal entity, each product's
// eligibility to purchase, each unit's conversion to the base unit.
func prepare(m *core.Base, ctx context.Context, in *requisitionIn) (prepared, error) {
	var p prepared
	if !core.UUIDForm.MatchString(in.LegalEntityID) {
		return p, core.Invalid("legal_entity_id", "legal_entity_id is a legal entity's ID")
	}
	if err := checkLines(in.Lines); err != nil {
		return p, err
	}
	var err error
	if p.deptID, p.deptPath, err = orgunits.Usable(m, ctx, in.DepartmentID, in.LegalEntityID); err != nil {
		return p, err
	}
	if p.le, err = masterdata.LegalEntityOf(m, ctx, in.LegalEntityID); err != nil {
		return p, err
	}
	pids := make([]string, len(in.Lines))
	for i, l := range in.Lines {
		pids[i] = l.ProductID
	}
	if p.products, err = masterdata.ProductsOf(m, ctx, pids, in.LegalEntityID); err != nil {
		return p, err
	}
	for _, l := range in.Lines {
		if _, _, err := masterdata.Convert(m, ctx, l.ProductID, l.Quantity, l.Unit, p.products[l.ProductID].BaseUnit); err != nil {
			return p, err
		}
	}
	return p, nil
}

// writeLines replaces the requisition's lines whole.
func writeLines(ctx context.Context, tx db.Tx, id string, in []lineIn, products map[string]masterdata.ProductInfo) error {
	if _, err := tx.Exec(ctx, `DELETE FROM requisition_line WHERE requisition_id = $1`, id); err != nil {
		return err
	}
	for i, l := range in {
		p := products[l.ProductID]
		if _, err := tx.Exec(ctx, `INSERT INTO requisition_line (id, requisition_id, line_no, product_id, product_code,
			product_name, base_unit, quantity, unit, needed_date) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::numeric, $9, $10::date)`,
			ids.New().String(), id, i+1, l.ProductID, p.Code, p.Name, p.BaseUnit, l.Quantity, l.Unit, l.NeededDate); err != nil {
			return err
		}
	}
	return nil
}

func subject(c *httpx.Context) string {
	u, _ := identity.UserFrom(c.Ctx())
	return u.Subject
}

// CreateRequisition makes a draft (PR01); its requester is the one who makes it.
func CreateRequisition(m *core.Base, c *httpx.Context) error {
	req, err := idem.FromHTTP(c)
	if err != nil {
		return err
	}
	var in requisitionIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Version != nil {
		return core.Invalid("version", "a new requisition has no version")
	}
	p, err := prepare(m, c.Ctx(), &in)
	if err != nil {
		return err
	}
	ctx, actor, err := core.Check(c, core.OpReqEdit, core.DocFacts(in.LegalEntityID, p.deptPath, subject(c)))
	if err != nil {
		return err
	}
	today, err := bizdate.Today(p.le.Timezone)
	if err != nil {
		return err
	}
	number, err := core.Number(m, ctx, "PR", p.le.ID, p.le.Code, today)
	if err != nil {
		return err
	}
	out, _, err := idem.Do(ctx, m.RT.DB, req, func(ctx context.Context, tx db.Tx) (idem.Outcome, error) {
		id := ids.New().String()
		if _, err := tx.Exec(ctx, `INSERT INTO requisition (id, number, legal_entity_id, department_id, department_path,
			requester_id, search, search_version) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, number, p.le.ID, p.deptID,
			p.deptPath, actor, search.Text(number), search.Version); err != nil {
			return idem.Outcome{}, err
		}
		if err := writeLines(ctx, tx, id, in.Lines, p.products); err != nil {
			return idem.Outcome{}, err
		}
		r, err := emit(m, ctx, tx, id)
		if err != nil {
			return idem.Outcome{}, err
		}
		return core.IDResult(id), core.Change(m, ctx, tx, actor, core.OpReqEdit, core.Requisitions, id, r)
	})
	if err != nil {
		return err
	}
	r, err := read(m, ctx, core.ResultID(out))
	if err != nil {
		return err
	}
	return c.JSON(200, r)
}

func reqFacts(r Requisition) sdkauthz.Facts {
	return core.DocFacts(r.LegalEntityID, r.DepartmentPath, r.RequesterID)
}

func GetRequisition(m *core.Base, c *httpx.Context) error {
	r, err := read(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	if _, _, err := core.Check(c, core.OpReqRead, reqFacts(r)); err != nil {
		return err
	}
	return c.JSON(200, r)
}

// UpdateRequisition changes a draft (PR01): its department and its whole lines, by version; the legal entity stays.
func UpdateRequisition(m *core.Base, c *httpx.Context) error {
	var in requisitionIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	cur, err := read(m, c.Ctx(), c.Param("id"))
	if err != nil {
		return err
	}
	ctx, actor, err := core.Check(c, core.OpReqEdit, reqFacts(cur))
	if err != nil {
		return err
	}
	if in.LegalEntityID != cur.LegalEntityID {
		return core.Invalid("legal_entity_id", "a requisition's legal entity does not change")
	}
	if d := Lifecycle.Decide(cur.State, Edit); d.Effect == core.Refuse { // before the evidence; decided again below
		return core.Refused(d, cur.State)
	}
	p, err := prepare(m, ctx, &in)
	if err != nil {
		return err
	}
	if _, _, err := core.Check(c, core.OpReqEdit, core.DocFacts(cur.LegalEntityID, p.deptPath, cur.RequesterID)); err != nil {
		return err
	}
	var out Requisition
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		r, err := get(ctx, tx, cur.ID, true)
		if err != nil {
			return err
		}
		if d := Lifecycle.Decide(r.State, Edit); d.Effect == core.Refuse {
			return core.Refused(d, r.State)
		}
		if in.Version != nil && *in.Version != r.Version {
			return core.ErrVersion.New(fmt.Sprintf("the requisition is at version %d", r.Version))
		}
		if err := writeLines(ctx, tx, r.ID, in.Lines, p.products); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE requisition SET department_id = $2, department_path = $3, version = version + 1,
			updated_at = now() WHERE id = $1`, r.ID, p.deptID, p.deptPath); err != nil {
			return err
		}
		if out, err = emit(m, ctx, tx, r.ID); err != nil {
			return err
		}
		return core.Change(m, ctx, tx, actor, core.OpReqEdit, core.Requisitions, r.ID, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

type actionIn struct {
	Version *int64  `json:"version"`
	Reason  *string `json:"reason"`
}

// Action is one of the commands submit, approve, reject and cancel, as the table decides it in the requisition's
// state; an ignored command answers the requisition as it is.
func Action(m *core.Base, command string) httpx.Handler {
	op := map[string]string{Submit: core.OpReqEdit, Approve: core.OpReqApprove, Reject: core.OpReqApprove,
		Cancel: core.OpReqEdit}[command]
	return func(c *httpx.Context) error {
		var in actionIn
		if err := core.BindOptional(c, &in); err != nil {
			return err
		}
		cur, err := read(m, c.Ctx(), c.Param("id"))
		if err != nil {
			return err
		}
		ctx, actor, err := core.Check(c, op, reqFacts(cur))
		if err != nil {
			return err
		}
		set := Set{}
		if command == Reject {
			if in.Reason == nil || strings.TrimSpace(*in.Reason) == "" {
				if d := Lifecycle.Decide(cur.State, Reject); d.Effect == core.Move {
					return core.Invalid("reason", "a rejection has its reason")
				}
			}
			set.Reason = in.Reason
		}
		var out Requisition
		err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
			r, err := get(ctx, tx, cur.ID, true)
			if err != nil {
				return err
			}
			out = r
			d := Lifecycle.Decide(r.State, command)
			switch d.Effect {
			case core.Ignore:
				return nil
			case core.Refuse:
				return core.Refused(d, r.State)
			}
			if in.Version != nil && *in.Version != r.Version {
				return core.ErrVersion.New(fmt.Sprintf("the requisition is at version %d", r.Version))
			}
			switch d.Guard {
			case GuardPolicy:
				set.SubmittedBy = actor
				p, err := policies.Of(ctx, tx, r.LegalEntityID)
				if err != nil {
					return err
				}
				if !p.NeedsApproval() {
					d.To = Approved
				}
			case GuardNotSubmitter:
				if r.SubmittedBy != nil && *r.SubmittedBy == actor {
					return core.ErrApprover.New("the requisition was submitted by " + actor + ": another approves it")
				}
			case GuardNothingOrdered:
				for _, l := range r.Lines {
					if l.Ordered != "0" {
						return core.ErrState.New("order lines were made from the requisition: it is not cancelled")
					}
				}
			}
			out, err = Transition(m, ctx, tx, r, d, set, actor)
			return err
		})
		if err != nil {
			return err
		}
		return c.JSON(200, out)
	}
}

var listSpec = list.Spec{Operation: core.OpReqRead, Alias: "r", IDColumn: "id", IDKind: list.UUID,
	Columns: []list.Column{
		{Field: "created_at", Column: "created_at", Kind: list.Time, Sortable: true},
		{Field: "legal_entity_id", Column: "legal_entity_id", Kind: list.UUID, Filterable: true},
		{Field: "department_id", Column: "department_id", Kind: list.UUID, Filterable: true},
		{Field: "requester_id", Column: "requester_id", Kind: list.Text, Filterable: true},
		{Field: "state", Column: "state", Kind: list.Text, Filterable: true}},
	Search: []list.SearchColumn{{Name: "number", Column: "search", Fields: []string{"number"}}}}

// ListRequisitions is one page, newest first, by legal entity, department, state and number.
func ListRequisitions(m *core.Base, c *httpx.Context) error {
	var f []list.Filter
	for _, p := range []string{"legal_entity_id", "department_id", "state"} {
		v := c.Query(p)
		if v == "" {
			continue
		}
		if p != "state" && !core.UUIDForm.MatchString(v) {
			return core.Invalid(p, p+" is an ID")
		}
		f = append(f, list.Filter{Field: p, Op: "eq", Values: []string{v}})
	}
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, listSpec, list.Request{Filters: f, Q: c.Query("q"), Limit: limit,
		Cursor: c.Query("cursor"), Sort: []list.Order{{Field: "created_at", Desc: true}}})
	if err != nil {
		return err
	}
	out, next, err := list.Page(ctx, m.RT.DB, q, "requisition", cols, scan,
		func(r Requisition) ([]string, string) { return []string{r.CreatedAt}, r.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"requisitions": out, "next_cursor": next})
}
