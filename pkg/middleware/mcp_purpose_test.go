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
		{"save_asset", true, "an asset write states why it was written (#1695)"},
		{"manage_asset", true, "the tool that edits and removes an asset, named whole (#1695)"},
		{"manage_table", false, "the other manage_* tools stay outside the set"},
		{"apply_knowledge", false, "what it applies is itself the explanation"},
		{"vendor__list_contacts", true, "gateway-proxied, matched by kind:mcp"},
		{"", false, "an empty tool name gates nothing"},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, r.Gates(tt.tool), "%s: %s", tt.tool, tt.why)
	}
}

func TestPurposeResolver_GatesByName(t *testing.T) {
	// The note has to describe the gated set, and a "kind:" entry covers tools
	// whose names the platform did not choose and cannot list (#1640). This is
	// the split it reads the set along; Gates stays the single predicate.
	r := NewPurposeResolver(PurposeConfig{
		Enabled: true,
		Lookup:  purposeLookup{"vendor__list_contacts": "mcp", "trino_query": "trino"},
	})

	assert.True(t, r.GatesByName("trino_query"), "named in the default set")
	assert.True(t, r.GatesByName("datahub_get_lineage"), "matched by the datahub_get_* glob")
	assert.False(t, r.GatesByName("vendor__list_contacts"),
		"gated by kind:mcp, which names no tool")
	assert.True(t, r.Gates("vendor__list_contacts"), "and is gated all the same")
	assert.False(t, r.GatesByName("platform_info"))
	assert.False(t, r.GatesByName(""))
	assert.False(t, (*PurposeResolver)(nil).GatesByName("trino_query"), "a nil resolver is a no-op")
	assert.False(t, NewPurposeResolver(PurposeConfig{Enabled: false}).GatesByName("trino_query"),
		"a disabled resolver gates nothing")
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

// purposeRequestFor builds a tools/call request for a named tool.
func purposeRequestFor(t *testing.T, tool string, args map[string]any) *mcp.CallToolRequest {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: tool, Arguments: raw}}
}

func TestPurposeResolver_ResolveOffGate(t *testing.T) {
	lookup := purposeLookup{"vendor__list_contacts": "mcp", "manage_table": "platform"}

	t.Run("an ungated platform tool has its purpose taken and recorded", func(t *testing.T) {
		// Issue #1640: the platform's own instructions ask the model to state a
		// purpose, and a tool schema closed to unknown properties (#1057) then
		// refuses the call for stating one. The argument comes off the request
		// and the sentence reaches the audit row.
		r := NewPurposeResolver(PurposeConfig{Enabled: true, Require: true, Lookup: lookup})
		req := purposeRequestFor(t, "manage_table", map[string]any{
			"action":  "list",
			"purpose": "  Checking which tables the weekly refresh registered.  ",
		})
		pc := &PlatformContext{SessionHandleThreaded: true}

		require.Nil(t, r.resolve(req, pc, "manage_table"))
		assert.Equal(t, "Checking which tables the weekly refresh registered.", pc.Purpose)
		assert.NotContains(t, remainingArgs(t, req), "purpose",
			"the argument must not survive into the tool's input-schema validation")
		assert.Equal(t, "list", remainingArgs(t, req)["action"], "the tool's own arguments survive")
	})

	t.Run("an ungated tool that states none is not refused and records nothing", func(t *testing.T) {
		r := NewPurposeResolver(PurposeConfig{Enabled: true, Require: true, Lookup: lookup})
		req := purposeRequestFor(t, "manage_table", map[string]any{"action": "list"})
		pc := &PlatformContext{SessionHandleThreaded: true}

		require.Nil(t, r.resolve(req, pc, "manage_table"))
		assert.Empty(t, pc.Purpose)
		assert.Equal(t, map[string]any{"action": "list"}, remainingArgs(t, req),
			"a call with no purpose is left byte-for-byte alone")
	})

	t.Run("an ungated proxied tool keeps the upstream's own argument", func(t *testing.T) {
		// A deployment drops kind:mcp from purpose.tools precisely because its
		// upstream server declares a purpose parameter of its own. The platform
		// does not own that name here, so it records the value without consuming
		// it (docs/server/configuration.md, purpose.tools).
		r := NewPurposeResolver(PurposeConfig{
			Enabled: true, Require: true,
			Tools:  []string{"trino_query"},
			Lookup: lookup,
		})
		req := purposeRequestFor(t, "vendor__list_contacts", map[string]any{
			"purpose": "renewal outreach",
		})
		pc := &PlatformContext{SessionHandleThreaded: true}

		require.Nil(t, r.resolve(req, pc, "vendor__list_contacts"))
		assert.Equal(t, "renewal outreach", pc.Purpose, "the stated sentence is still recorded")
		assert.Equal(t, "renewal outreach", remainingArgs(t, req)["purpose"],
			"the upstream server must still receive its own parameter")
	})

	t.Run("a gated proxied tool has its purpose taken", func(t *testing.T) {
		// The default set gates kind:mcp, which is the platform declaring that it
		// owns the name on those tools.
		r := NewPurposeResolver(PurposeConfig{Enabled: true, Require: true, Lookup: lookup})
		req := purposeRequestFor(t, "vendor__list_contacts", map[string]any{
			"purpose": "Building the renewal list for the QBR.",
		})
		pc := &PlatformContext{SessionHandleThreaded: true}

		require.Nil(t, r.resolve(req, pc, "vendor__list_contacts"))
		assert.Equal(t, "Building the renewal list for the QBR.", pc.Purpose)
		assert.NotContains(t, remainingArgs(t, req), "purpose",
			"a gated proxied tool's upstream server never sees the platform argument")
	})

	t.Run("a disabled resolver leaves the argument alone", func(t *testing.T) {
		off := NewPurposeResolver(PurposeConfig{Enabled: false, Lookup: lookup})
		req := purposeRequestFor(t, "manage_table", map[string]any{"purpose": "mine"})
		pc := &PlatformContext{SessionHandleThreaded: true}

		require.Nil(t, off.resolve(req, pc, "manage_table"))
		assert.Empty(t, pc.Purpose)
		assert.Equal(t, "mine", remainingArgs(t, req)["purpose"],
			"with the feature off the platform claims no argument name")
	})

	t.Run("with no lookup an ungated tool is still stripped", func(t *testing.T) {
		r := NewPurposeResolver(PurposeConfig{Enabled: true, Require: true})
		req := purposeRequestFor(t, "manage_table", map[string]any{"purpose": "Auditing registrations."})
		pc := &PlatformContext{SessionHandleThreaded: true}

		require.Nil(t, r.resolve(req, pc, "manage_table"))
		assert.Equal(t, "Auditing registrations.", pc.Purpose)
		assert.NotContains(t, remainingArgs(t, req), "purpose")
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
