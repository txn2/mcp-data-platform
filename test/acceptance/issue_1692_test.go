//go:build integration

package acceptance

// Issue #1692: most of the platform's own tools registered an mcp.Tool with no
// Annotations field, so tools/list carried no annotations object for them. A
// client that honors readOnlyHint is told to assume a tool is not read-only
// when the hint is absent, so it treated platform_info, search and fetch --
// the three calls the platform's own instructions require first, in that
// order -- as write actions and asked the user to approve each one.
//
// The criteria are read where the defect showed: tools/list, over a real MCP
// session. The annotation an agent's client acts on is the one on the wire, so
// no criterion reads a Go struct: a middleware that rebuilt a listed tool
// would drop the field with every unit test still green.
//
// Wire forms: tools/list takes no parameters, and this ticket changes no tool
// parameter, so there is one wire form and every criterion sends it. The
// admin tool listing #1691 also reads is a GET with no body.
//
// Gateway-proxied tools (a name carrying the "__" namespace separator) are
// outside the criteria: the gateway passes an upstream's annotations through
// verbatim, and an upstream that publishes none leaves nil here. What that
// upstream advertises is the upstream's to fix.

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// issue1692Expectation is what one tool must advertise: whether it can modify
// state, and -- when it can -- whether any of its actions removes or
// overwrites state that is already there.
type issue1692Expectation struct {
	readOnly bool
	// destructive is read only when readOnly is false, which is the same
	// condition the MCP specification puts on destructiveHint.
	destructive bool
}

// issue1692Expected classifies every tool this repository registers. A tool
// present in tools/list and absent from this table fails the first criterion,
// so a toolkit added later cannot ship without the decision being made.
//
// The read-only set is the first seven rows plus the three that already
// carried the hint before this ticket; the write rows carry destructive=true
// wherever one of the tool's actions removes or overwrites existing state, and
// destructive=false where every action only adds.
var issue1692Expected = map[string]issue1692Expectation{
	// Reads. Nothing here modifies state, so a conforming client can call
	// them without asking anyone.
	"platform_info":       {readOnly: true},
	"platform_find_tools": {readOnly: true},
	"list_connections":    {readOnly: true},
	"search":              {readOnly: true},
	"fetch":               {readOnly: true},
	"show_prompts":        {readOnly: true},
	"show_scripts":        {readOnly: true},
	"api_discover":        {readOnly: true},
	"graphql_discover":    {readOnly: true},
	"s3_list":             {readOnly: true},

	// Writes that can remove or overwrite. apply_knowledge carries
	// bulk_untag and delete_tag; memory_manage carries forget and
	// consolidate, and memory_capture supersedes a record it restates;
	// manage_asset carries delete, manage_table unregister, manage_resource
	// delete and replace_content, manage_prompt delete, manage_script
	// delete. run_script executes a saved script, which reaches any of
	// them. api_invoke_endpoint and graphql_query carry whatever verb or
	// mutation the caller writes.
	"apply_knowledge":     {destructive: true},
	"memory_capture":      {destructive: true},
	"memory_manage":       {destructive: true},
	"manage_asset":        {destructive: true},
	"manage_table":        {destructive: true},
	"manage_resource":     {destructive: true},
	"manage_prompt":       {destructive: true},
	"manage_script":       {destructive: true},
	"run_script":          {destructive: true},
	"api_invoke_endpoint": {destructive: true},
	"graphql_query":       {destructive: true},
	"s3_object":           {destructive: true},

	// Writes that only add. An export lands a new asset, or the next
	// version of a managed resource with the earlier versions kept;
	// save_asset lands a new asset or a new version of one; a feedback
	// thread accumulates a timeline and has no action that removes from it.
	"save_asset":      {destructive: false},
	"manage_feedback": {destructive: false},
	"trino_export":    {destructive: false},
	"api_export":      {destructive: false},
	"graphql_export":  {destructive: false},

	// The trino, datahub and s3 toolkits are built on the mcp-trino,
	// mcp-datahub and mcp-s3 libraries, which declare these annotations
	// themselves. #1692 did not change them and named them as already
	// correct; the rows are here so that claim is asserted rather than
	// assumed, and so an upstream upgrade that drops one is caught where a
	// user would feel it.
	"trino_query":               {readOnly: true},
	"trino_explain":             {readOnly: true},
	"trino_browse":              {readOnly: true},
	"trino_describe_table":      {readOnly: true},
	"trino_list_connections":    {readOnly: true},
	"trino_execute":             {destructive: true},
	"datahub_search":            {readOnly: true},
	"datahub_get_entity":        {readOnly: true},
	"datahub_get_schema":        {readOnly: true},
	"datahub_get_lineage":       {readOnly: true},
	"datahub_get_queries":       {readOnly: true},
	"datahub_browse":            {readOnly: true},
	"datahub_get_glossary_term": {readOnly: true},
	"datahub_get_data_product":  {readOnly: true},
	"datahub_list_connections":  {readOnly: true},
	"datahub_create":            {destructive: false},
	"datahub_update":            {destructive: false},
	"datahub_delete":            {destructive: true},
}

// issue1692Native returns the tools this repository is answerable for: what
// the session lists, less the gateway's proxied upstream tools.
func issue1692Native(t *testing.T, c *client) []*mcp.Tool {
	t.Helper()
	var native []*mcp.Tool
	for _, tool := range c.tools() {
		if strings.Contains(tool.Name, "__") {
			continue
		}
		native = append(native, tool)
	}
	if len(native) == 0 {
		t.Fatal("tools/list carries no platform tools")
	}
	return native
}

// issue1692Destructive reports what a listed tool says about destructiveHint.
// The field is a pointer because the specification's default when it is absent
// is true, so "unset" and "false" are different answers and the criteria
// require the field to be present on a write tool rather than inherited.
func issue1692Destructive(tool *mcp.Tool) (value, present bool) {
	if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil {
		return false, false
	}
	return *tool.Annotations.DestructiveHint, true
}

// TestIssue1692_EveryListedToolCarriesAnnotations is the defect itself: a tool
// listed with no annotations object is the one a strict client treats as a
// write. It also holds the table above to the inventory, so a tool this
// repository adds later is classified rather than quietly unannotated.
func TestIssue1692_EveryListedToolCarriesAnnotations(t *testing.T) {
	c := connect(t)

	for _, tool := range issue1692Native(t, c) {
		if tool.Annotations == nil {
			t.Errorf("%s is listed with no annotations object", tool.Name)
			continue
		}
		if _, ok := issue1692Expected[tool.Name]; !ok {
			t.Errorf("%s is listed and this ticket's table does not say whether it is read-only", tool.Name)
		}
	}
}

// TestIssue1692_ReadToolsAdvertiseReadOnly is the user-visible half. The
// platform's own instructions require platform_info, then search, then a
// scoped tool; on a client that honors the hint, each of those was a
// confirmation prompt before any data came back.
func TestIssue1692_ReadToolsAdvertiseReadOnly(t *testing.T) {
	c := connect(t)

	// The three the opening sequence requires are present on every
	// deployment, so their absence is a failure rather than a skip.
	required := map[string]bool{"platform_info": true, "search": true, "fetch": true}

	for _, tool := range issue1692Native(t, c) {
		want, ok := issue1692Expected[tool.Name]
		if !ok || !want.readOnly {
			continue
		}
		delete(required, tool.Name)
		if tool.Annotations == nil {
			t.Errorf("%s is listed with no annotations object", tool.Name)
			continue
		}
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s reads and does not advertise readOnlyHint: true", tool.Name)
		}
		if !tool.Annotations.IdempotentHint {
			t.Errorf("%s reads and does not advertise idempotentHint: true", tool.Name)
		}
	}
	for name := range required {
		t.Errorf("%s is not listed on this deployment", name)
	}
}

// TestIssue1692_WriteToolsAdvertiseTheirReach is the other half: a tool that
// can modify state says so, and says whether it can remove or overwrite, so
// the prompt a client does raise carries information.
func TestIssue1692_WriteToolsAdvertiseTheirReach(t *testing.T) {
	c := connect(t)

	for _, tool := range issue1692Native(t, c) {
		want, ok := issue1692Expected[tool.Name]
		if !ok || want.readOnly {
			continue
		}
		if tool.Annotations == nil {
			t.Errorf("%s is listed with no annotations object", tool.Name)
			continue
		}
		if tool.Annotations.ReadOnlyHint {
			t.Errorf("%s can modify state and advertises readOnlyHint: true", tool.Name)
		}
		got, present := issue1692Destructive(tool)
		if !present {
			t.Errorf("%s writes and advertises no destructiveHint", tool.Name)
			continue
		}
		if got != want.destructive {
			t.Errorf("%s advertises destructiveHint: %t, want %t", tool.Name, got, want.destructive)
		}
	}
}
