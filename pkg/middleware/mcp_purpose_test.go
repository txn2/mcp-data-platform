package middleware

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// purposeLookup resolves a tool's toolkit kind from a name->kind map, so a test
// can exercise the "kind:" entries in the gated set.
type purposeLookup map[string]string

func (l purposeLookup) GetToolkitForTool(toolName string) registry.ToolkitMatch {
	kind, ok := l[toolName]
	return registry.ToolkitMatch{Kind: kind, Name: "inst", Connection: "conn", Found: ok}
}

func TestPurposeResolver_Gates(t *testing.T) {
	r := NewPurposeResolver(PurposeConfig{
		Enabled: true,
		Lookup: purposeLookup{
			"vendor__list_contacts": "mcp",
			"trino_query":           "trino",
			"platform_info":         "platform",
		},
	})

	tests := []struct {
		tool string
		want bool
		why  string
	}{
		{"trino_query", true, "named in the default set"},
		{"datahub_get_lineage", true, "matched by the datahub_get_* glob"},
		{"datahub_browse", false, "browse is discovery, not in the glob"},
		{"search", true, "named in the default set"},
		{"platform_info", false, "orientation tools are deliberately excluded"},
		{"list_connections", false, "orientation tools are deliberately excluded"},
		{"memory_capture", false, "capture is not data access"},
		{"save_asset", false, "not data access"},
		{"vendor__list_contacts", true, "gateway-proxied, matched by kind:mcp"},
		{"", false, "an empty tool name gates nothing"},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, r.Gates(tt.tool), "%s: %s", tt.tool, tt.why)
	}
}

func TestPurposeResolver_GatesDisabledAndNil(t *testing.T) {
	assert.False(t, (*PurposeResolver)(nil).Gates("trino_query"), "a nil resolver is a no-op")
	off := NewPurposeResolver(PurposeConfig{Enabled: false})
	assert.False(t, off.Gates("trino_query"), "a disabled resolver gates nothing")
}

func TestPurposeResolver_GatesCustomToolSet(t *testing.T) {
	r := NewPurposeResolver(PurposeConfig{
		Enabled: true,
		// Whitespace and an empty entry are tolerated; an override REPLACES the
		// default set, so the gateway kind no longer gates.
		Tools:  []string{" trino_execute ", "", "kind:", "s3_*"},
		Lookup: purposeLookup{"vendor__list_contacts": "mcp"},
	})
	assert.True(t, r.Gates("trino_execute"))
	assert.True(t, r.Gates("s3_object"))
	assert.False(t, r.Gates("trino_query"), "not in the override set")
	assert.False(t, r.Gates("vendor__list_contacts"), "an override drops the default kind:mcp entry")
}

func TestPurposeResolver_GatesInvalidPatternMatchesNothing(t *testing.T) {
	r := NewPurposeResolver(PurposeConfig{Enabled: true, Tools: []string{"[bad"}})
	assert.False(t, r.Gates("[bad"), "a malformed glob narrows the gated set rather than widening it")
}

func TestPurposeResolver_GatesKindNeedsLookup(t *testing.T) {
	r := NewPurposeResolver(PurposeConfig{Enabled: true, Tools: []string{"kind:mcp"}})
	assert.False(t, r.Gates("vendor__list_contacts"), "without a lookup a kind entry resolves nothing")
}

// purposeRequest builds a tools/call request carrying the given arguments.
func purposeRequest(t *testing.T, args map[string]any) *mcp.CallToolRequest {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "trino_query", Arguments: raw}}
}

// remainingArgs decodes the request arguments after the resolver has run.
func remainingArgs(t *testing.T, req *mcp.CallToolRequest) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(req.Params.Arguments, &out))
	return out
}

func TestPurposeResolver_ResolveStripsAndRecords(t *testing.T) {
	r := NewPurposeResolver(PurposeConfig{Enabled: true, Require: true})
	req := purposeRequest(t, map[string]any{"sql": "SELECT 1", "purpose": "  Sizing Q3 revenue.  "})
	pc := &PlatformContext{SessionHandleThreaded: true}

	require.Nil(t, r.resolve(req, pc, "trino_query"))
	assert.Equal(t, "Sizing Q3 revenue.", pc.Purpose, "the purpose is trimmed onto the context")
	assert.NotContains(t, remainingArgs(t, req), "purpose", "the handler must never see the platform argument")
	assert.Equal(t, "SELECT 1", remainingArgs(t, req)["sql"], "the tool's own arguments survive")
}

func TestPurposeResolver_ResolveRefusesThreadedCallWithout(t *testing.T) {
	r := NewPurposeResolver(PurposeConfig{Enabled: true, Require: true})

	for _, args := range []map[string]any{
		{"sql": "SELECT 1"},                   // absent
		{"sql": "SELECT 1", "purpose": ""},    // empty
		{"sql": "SELECT 1", "purpose": " \t"}, // whitespace only
		{"sql": "SELECT 1", "purpose": 42},    // not a string
	} {
		pc := &PlatformContext{SessionHandleThreaded: true}
		res := r.resolve(purposeRequest(t, args), pc, "trino_query")
		require.NotNil(t, res, "args %v must be refused", args)
		call, ok := res.(*mcp.CallToolResult)
		require.True(t, ok)
		assert.True(t, call.IsError)
		assert.Empty(t, pc.Purpose)
		assert.Contains(t, textOf(t, call), "PURPOSE_REQUIRED")
	}
}

func TestPurposeResolver_ResolveExemptions(t *testing.T) {
	r := NewPurposeResolver(PurposeConfig{Enabled: true, Require: true})

	t.Run("a caller that threaded no handle is never refused", func(t *testing.T) {
		// This is the whole exemption set: an MCP App's adopted call, a script
		// run, the REST and admin shims, and an isolated dpp_/dpx_ run all reach
		// the resolver with SessionHandleThreaded false.
		pc := &PlatformContext{SessionHandleThreaded: false}
		assert.Nil(t, r.resolve(purposeRequest(t, map[string]any{"sql": "SELECT 1"}), pc, "trino_query"))
	})

	t.Run("an ungated tool is never refused", func(t *testing.T) {
		pc := &PlatformContext{SessionHandleThreaded: true}
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
			Name:      "platform_info",
			Arguments: json.RawMessage(`{}`),
		}}
		assert.Nil(t, r.resolve(req, pc, "platform_info"))
	})

	t.Run("require off records without refusing", func(t *testing.T) {
		lenient := NewPurposeResolver(PurposeConfig{Enabled: true, Require: false})
		pc := &PlatformContext{SessionHandleThreaded: true}
		assert.Nil(t, lenient.resolve(purposeRequest(t, map[string]any{"sql": "SELECT 1"}), pc, "trino_query"))

		withPurpose := &PlatformContext{SessionHandleThreaded: true}
		req := purposeRequest(t, map[string]any{"sql": "SELECT 1", "purpose": "Auditing the load job."})
		assert.Nil(t, lenient.resolve(req, withPurpose, "trino_query"))
		assert.Equal(t, "Auditing the load job.", withPurpose.Purpose)
		assert.NotContains(t, remainingArgs(t, req), "purpose", "it is stripped whether or not it is required")
	})

	t.Run("a nil resolver is a no-op", func(t *testing.T) {
		pc := &PlatformContext{SessionHandleThreaded: true}
		assert.Nil(t, (*PurposeResolver)(nil).resolve(purposeRequest(t, nil), pc, "trino_query"))
	})
}

func TestBoundPurpose(t *testing.T) {
	assert.Equal(t, "", boundPurpose("   \n\t "))
	assert.Equal(t, "one sentence", boundPurpose("  one sentence  "))

	// Truncation counts runes, so a multi-byte purpose is never cut into
	// invalid UTF-8 on its way to the audit row.
	long := strings.Repeat("é", maxPurposeChars+50)
	got := boundPurpose(long)
	assert.Equal(t, maxPurposeChars, len([]rune(got)))
	assert.True(t, len(got) > maxPurposeChars, "the bound is runes, not bytes")
}

func TestDefaultPurposeTools_IsACopy(t *testing.T) {
	got := DefaultPurposeTools()
	require.NotEmpty(t, got)
	got[0] = "mutated"
	assert.NotEqual(t, "mutated", DefaultPurposeTools()[0], "callers cannot mutate the package default")
}

// textOf returns the first text content block of a tool result.
func textOf(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, r.Content)
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}
