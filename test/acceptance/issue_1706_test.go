//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1706: tools/list advertises an annotations object on every tool whose
// author stated one (readOnlyHint, destructiveHint, idempotentHint,
// openWorldHint), and GET /api/v1/admin/tools/schemas and GET
// /api/v1/admin/tools/{name} carried none. An operator checking that a
// deployment's per-toolkit `annotations:` override took effect had to open an
// MCP client to read the result.
//
// What these hold: both admin routes carry, for every tool, exactly the hints
// the same deployment's tools/list advertises, and a tool tools/list carries
// no annotations on carries none on the admin routes either. tools/list is the
// reference rather than a hand-written table, so an override is compared as
// the deployment applied it.
//
// Wire forms: both routes are GETs whose only input is the {name} path
// segment, a string, sent URL-escaped. No tools/call parameter is touched.

// issue1706Hints is the comparable form of one tool's annotations: the four
// hints, with the two whose absence means true in the MCP spec kept as
// pointers so "stated false" and "not stated" stay distinct.
type issue1706Hints struct {
	present     bool
	readOnly    bool
	idempotent  bool
	destructive *bool
	openWorld   *bool
}

func issue1706FromMCP(a *mcp.ToolAnnotations) issue1706Hints {
	if a == nil {
		return issue1706Hints{}
	}
	return issue1706Hints{present: true, readOnly: a.ReadOnlyHint, idempotent: a.IdempotentHint, destructive: a.DestructiveHint, openWorld: a.OpenWorldHint}
}

func issue1706FromJSON(v any) issue1706Hints {
	m, ok := v.(map[string]any)
	if !ok {
		return issue1706Hints{}
	}
	optional := func(key string) *bool {
		b, ok := m[key].(bool)
		if !ok {
			return nil
		}
		return &b
	}
	ro, _ := m["readOnlyHint"].(bool)
	idem, _ := m["idempotentHint"].(bool)
	return issue1706Hints{present: true, readOnly: ro, idempotent: idem, destructive: optional("destructiveHint"), openWorld: optional("openWorldHint")}
}

func issue1706Equal(a, b issue1706Hints) bool {
	eq := func(x, y *bool) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	return a.present == b.present && a.readOnly == b.readOnly && a.idempotent == b.idempotent &&
		eq(a.destructive, b.destructive) && eq(a.openWorld, b.openWorld)
}

// issue1706Advertised is what tools/list carries, keyed by tool name.
func issue1706Advertised(t *testing.T, c *client) map[string]issue1706Hints {
	t.Helper()
	out := map[string]issue1706Hints{}
	annotated := 0
	for _, tool := range c.tools() {
		out[tool.Name] = issue1706FromMCP(tool.Annotations)
		if tool.Annotations != nil {
			annotated++
		}
	}
	if annotated == 0 {
		t.Fatalf("tools/list carries annotations on no tool, so there is nothing for the admin routes to agree with")
	}
	return out
}

// TestIssue1706_TheSchemaListingCarriesTheAdvertisedAnnotations is the ticket's
// first criterion for the listing route.
func TestIssue1706_TheSchemaListingCarriesTheAdvertisedAnnotations(t *testing.T) {
	c := connect(t)
	advertised := issue1706Advertised(t, c)

	status, out := c.rest(http.MethodGet, "/api/v1/admin/tools/schemas", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/tools/schemas: status %d, body %v", status, out)
	}
	schemas, _ := out["schemas"].(map[string]any)
	if len(schemas) == 0 {
		t.Fatalf("the schema listing is empty: %v", out)
	}
	for name, want := range advertised {
		entry, ok := schemas[name].(map[string]any)
		if !ok {
			t.Errorf("%s: in tools/list, absent from the schema listing", name)
			continue
		}
		if got := issue1706FromJSON(entry["annotations"]); !issue1706Equal(got, want) {
			t.Errorf("%s: schema listing annotations %+v; tools/list advertises %+v", name, entry["annotations"], want)
		}
	}
}

// TestIssue1706_TheToolDetailCarriesTheAdvertisedAnnotations is the same
// criterion for the detail route, which the portal's Tools page renders its
// badges from. A read tool, a write tool that does not destroy, and a write
// tool that may are all asked for, so each badge has a tool behind it.
func TestIssue1706_TheToolDetailCarriesTheAdvertisedAnnotations(t *testing.T) {
	c := connect(t)
	advertised := issue1706Advertised(t, c)

	for _, name := range []string{"search", "save_asset", "manage_asset"} {
		t.Run(name, func(t *testing.T) {
			want, ok := advertised[name]
			if !ok {
				t.Fatalf("%s is not in this session's tools/list", name)
			}
			if !want.present {
				t.Fatalf("%s carries no annotations in tools/list; the criterion needs a tool that does", name)
			}
			status, out := c.rest(http.MethodGet, "/api/v1/admin/tools/"+url.PathEscape(name), nil)
			if status != http.StatusOK {
				t.Fatalf("GET /api/v1/admin/tools/%s: status %d, body %v", name, status, out)
			}
			if got := issue1706FromJSON(out["annotations"]); !issue1706Equal(got, want) {
				t.Errorf("detail annotations %v; tools/list advertises %+v", out["annotations"], want)
			}
		})
	}
}
