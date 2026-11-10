package purchase

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	core "github.com/brickKit/erp-purchase/v3/internal/core"
	requisitions "github.com/brickKit/erp-purchase/v3/internal/requisitions"

	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/be-sdk-go/v3/mergesafety"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheManifestDeclaresWhatTheSDKReads: component.yaml's configSchema is exactly the keys the SDK reads.
func TestTheManifestDeclaresWhatTheSDKReads(t *testing.T) {
	raw, err := os.ReadFile("../component.yaml")
	require.NoError(t, err)
	if p := launcher.ManifestProblems(raw, Definition(), nil); len(p) > 0 {
		t.Errorf("component.yaml's configSchema differs from what the SDK reads:\n%v\nthe block:\n%s", p,
			launcher.ConfigSchemaYAML(Definition(), nil))
	}
}

// TestTheManifestListsThePublishedEvents: component.yaml's events.publishes is exactly the event contracts.
func TestTheManifestListsThePublishedEvents(t *testing.T) {
	raw, err := os.ReadFile("../component.yaml")
	require.NoError(t, err)
	assert.Empty(t, launcher.EventProblems(raw, os.DirFS("../contract/events")))
}

// TestTheRoutesAreTheOpenAPIDocument: every route is in contract/openapi.yaml with its operation, and nothing else is.
func TestTheRoutesAreTheOpenAPIDocument(t *testing.T) {
	raw, err := os.ReadFile("../contract/openapi.yaml")
	require.NoError(t, err)
	var r httpx.Routes
	require.NoError(t, (&Module{}).Routes(&r))
	assert.Empty(t, httpx.OpenAPIProblems(raw, r.List()))
}

// TestThePayloadsFitTheirSchemas: what the full-state events carry is what contract/events promises.
func TestThePayloadsFitTheirSchemas(t *testing.T) {
	le, pr, dept := "01a15ab0-0000-7000-8000-0000000000a1", "01a15ab0-0000-7000-8000-0000000000c1", "01a15ab0-0000-7000-8000-0000000000e2"
	needed, reason := "2026-12-01", "not this quarter"
	for typ, v := range map[string]any{
		core.RequisitionChanged: requisitions.Requisition{ID: "01a15ab0-0000-7000-8000-0000000000aa", Number: "PR-SH-2026-000001",
			LegalEntityID: le, DepartmentID: &dept, RequesterID: "01a15ab0-0000-7000-8000-0000000000e1", State: "rejected",
			Reason: &reason, Lines: []requisitions.Line{{ID: "01a15ab0-0000-7000-8000-0000000000d1", ProductID: pr,
				Product: requisitions.Product{Code: "P-1", Name: "螺丝", Unit: "EA"}, Quantity: "2", Unit: "BX",
				NeededDate: &needed, Ordered: "0"}},
			Version: 3, UpdatedAt: "2026-10-20T08:00:00.000000Z"},
	} {
		validate(t, typ, v)
	}
}

func validate(t *testing.T, typ string, v any) {
	t.Helper()
	s, err := jsonschema.NewCompiler().Compile("../contract/events/" + typ + ".schema.json")
	require.NoError(t, err, typ)
	b, err := json.Marshal(v)
	require.NoError(t, err)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)
	assert.NoError(t, s.Validate(doc), typ)
}

// TestTheModuleIsMergeSafe: the module is compiled into the Go core shell: the entry package and every business
// module under internal/.
func TestTheModuleIsMergeSafe(t *testing.T) {
	findings, err := mergesafety.CheckDir(".")
	require.NoError(t, err)
	inner, err := mergesafety.CheckTree("../internal")
	require.NoError(t, err)
	findings = append(findings, inner...)
	for _, f := range findings {
		t.Errorf("%v", f)
	}
}
