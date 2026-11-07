// Package toolsurface holds what each platform tool advertises in tools/list
// that a client decides by: the verbs an action tool's schema enumerates, and
// whether a tool reads, writes, or can remove or overwrite.
//
// Two places assert these tables. The acceptance criteria of #1692 and #1827
// read them through a real client against a running platform, in the E2E
// Nightly; TestRealDB_AdvertisedSurfaceMatchesTheTables (pkg/platform) reads
// them against the tools/list of a platform with every toolkit assembled, in
// the real-database gate every pull request runs. #2515 added two verbs to
// manage_feedback and the tables stayed behind; only the nightly saw it, after
// a release tag had been refused (#2521).
package toolsurface

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Names this file repeats.
const (
	toolMemoryManage = "memory_manage"
	verbDelete       = "delete"
	verbGet          = "get"
	verbList         = "list"
)

// Expectation is what one tool must advertise: whether it can modify state,
// and when it can, whether any of its actions removes or overwrites state that
// is already there.
type Expectation struct {
	ReadOnly bool
	// Destructive is read only when ReadOnly is false, which is the condition
	// the MCP specification puts on destructiveHint.
	Destructive bool
}

// Annotations classifies every tool this repository registers. A native tool
// in tools/list and absent from this table is a failure, so a toolkit added
// later cannot ship without the decision being made (#1692).
var Annotations = map[string]Expectation{
	// Reads. Nothing here modifies state, so a conforming client can call
	// them without asking anyone.
	"platform_info":       {ReadOnly: true},
	"platform_find_tools": {ReadOnly: true},
	"list_connections":    {ReadOnly: true},
	"search":              {ReadOnly: true},
	"fetch":               {ReadOnly: true},
	"show_prompts":        {ReadOnly: true},
	"show_scripts":        {ReadOnly: true},
	"api_discover":        {ReadOnly: true},
	"graphql_discover":    {ReadOnly: true},
	"s3_list":             {ReadOnly: true},

	// Writes that can remove or overwrite. apply_knowledge carries
	// bulk_untag and delete_tag; memory_manage carries forget and
	// consolidate, and memory_capture supersedes a record it restates;
	// manage_asset carries delete, manage_table unregister, manage_resource
	// delete and replace_content, manage_prompt delete, manage_script
	// delete. run_script executes a saved script, which reaches any of
	// them. api_invoke_endpoint and graphql_query carry whatever verb or
	// mutation the caller writes. manage_schema applies a migration, which
	// can drop what it names; manage_application deletes an application and
	// applies a release's migrations.
	"apply_knowledge":     {Destructive: true},
	"memory_capture":      {Destructive: true},
	toolMemoryManage:      {Destructive: true},
	"manage_asset":        {Destructive: true},
	"manage_table":        {Destructive: true},
	"manage_resource":     {Destructive: true},
	"manage_prompt":       {Destructive: true},
	"manage_script":       {Destructive: true},
	"run_script":          {Destructive: true},
	"api_invoke_endpoint": {Destructive: true},
	"graphql_query":       {Destructive: true},
	"s3_object":           {Destructive: true},
	"manage_schema":       {Destructive: true},
	"manage_application":  {Destructive: true},

	// Writes that only add. An export lands a new asset, or the next
	// version of a managed resource with the earlier versions kept;
	// save_asset lands a new asset or a new version of one; a notification
	// is queued, and nothing already sent is changed.
	"save_asset": {Destructive: false},
	"notify":     {Destructive: false},
	// manage_feedback edits and deletes a message of a thread (#2514).
	"manage_feedback": {Destructive: true},
	// A request for approval and its decision add a record and remove none.
	"manage_approval": {Destructive: false},
	"trino_export":    {Destructive: false},
	"api_export":      {Destructive: false},
	"graphql_export":  {Destructive: false},

	// The trino and s3 toolkits are built on the mcp-trino and mcp-s3
	// libraries, which declare these annotations themselves. #1692 did not
	// change them and named them as already correct; the rows are here so
	// that claim is asserted rather than assumed, and so an upstream upgrade
	// that drops one is caught where a user would feel it.
	"trino_query":            {ReadOnly: true},
	"trino_explain":          {ReadOnly: true},
	"trino_browse":           {ReadOnly: true},
	"trino_describe_table":   {ReadOnly: true},
	"trino_list_connections": {ReadOnly: true},
	"trino_execute":          {Destructive: true},
}

// ActionEnums are the verbs each action tool's schema advertises (#1827),
// each written as the tool advertises it.
var ActionEnums = map[string][]string{
	"manage_asset": {
		verbList, verbGet, "update", verbDelete, "list_versions", "revert", "search", "provenance", "patch",
		"locate", "get_content", "outline", "stats", "diff", "share", "list_shares", "revoke_share",
		"create_collection", "list_collections", "get_collection", "update_collection",
		"delete_collection", "set_sections",
	},
	"manage_feedback": {"create", verbList, verbGet, "reply", "resolve", "request_validation", "respond_validation", "edit", verbDelete},
	toolMemoryManage:  {"update", "forget", verbList, "review_stale", "review_duplicates", "consolidate"},
	"s3_object":       {verbGet, "metadata", "put", "copy", verbDelete, "presign"},
	"notify":          {verbList, "send", "publish"},
}

// VerbArg is the argument a tool names its verb in, where it is not "action".
var VerbArg = map[string]string{toolMemoryManage: "command"}

// VerbArgOf is the argument the named tool takes its verb in.
func VerbArgOf(tool string) string {
	if arg := VerbArg[tool]; arg != "" {
		return arg
	}
	return "action"
}

// Native returns the tools this repository is answerable for: the listing less
// the gateway's proxied upstream tools, whose names carry the "__" separator
// and whose annotations are the upstream's.
func Native(tools []*mcp.Tool) []*mcp.Tool {
	native := make([]*mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		if !strings.Contains(tool.Name, "__") {
			native = append(native, tool)
		}
	}
	return native
}

// Enum reads one property's enum out of an advertised input schema.
func Enum(schema any, arg string) ([]string, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encoding the schema: %w", err)
	}
	var parsed struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("the schema is not an object: %w", err)
	}
	return parsed.Properties[arg].Enum, nil
}

// Destructive reports what a listed tool says about destructiveHint. The field
// is a pointer because the specification's default when it is absent is true,
// so "unset" and "false" are different answers.
func Destructive(tool *mcp.Tool) (value, present bool) {
	if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil {
		return false, false
	}
	return *tool.Annotations.DestructiveHint, true
}

// Mismatches compares a tools/list with both tables and returns one sentence
// per disagreement, sorted. A tool in ActionEnums that the listing does not
// carry is a mismatch too: the caller lists a platform built to register it.
func Mismatches(tools []*mcp.Tool) []string {
	var out []string
	listed := make(map[string]*mcp.Tool, len(tools))
	for _, tool := range Native(tools) {
		listed[tool.Name] = tool
		out = append(out, annotationMismatches(tool)...)
	}
	for name, want := range ActionEnums {
		tool, ok := listed[name]
		if !ok {
			out = append(out, name+" is in ActionEnums and is not listed")
			continue
		}
		out = append(out, enumMismatch(tool, want)...)
	}
	sort.Strings(out)
	return out
}

func annotationMismatches(tool *mcp.Tool) []string {
	want, ok := Annotations[tool.Name]
	if !ok {
		return []string{tool.Name + " is listed and Annotations does not say whether it is read-only"}
	}
	if tool.Annotations == nil {
		return []string{tool.Name + " is listed with no annotations object"}
	}
	if want.ReadOnly {
		var out []string
		if !tool.Annotations.ReadOnlyHint {
			out = append(out, tool.Name+" reads and does not advertise readOnlyHint: true")
		}
		if !tool.Annotations.IdempotentHint {
			out = append(out, tool.Name+" reads and does not advertise idempotentHint: true")
		}
		return out
	}
	if tool.Annotations.ReadOnlyHint {
		return []string{tool.Name + " can modify state and advertises readOnlyHint: true"}
	}
	got, present := Destructive(tool)
	if !present {
		return []string{tool.Name + " writes and advertises no destructiveHint"}
	}
	if got != want.Destructive {
		return []string{fmt.Sprintf("%s advertises destructiveHint: %t, want %t", tool.Name, got, want.Destructive)}
	}
	return nil
}

func enumMismatch(tool *mcp.Tool, want []string) []string {
	arg := VerbArgOf(tool.Name)
	got, err := Enum(tool.InputSchema, arg)
	if err != nil {
		return []string{tool.Name + ": " + err.Error()}
	}
	got = slices.Clone(got)
	want = slices.Clone(want)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return []string{fmt.Sprintf("%s %s enum = %v; want %v", tool.Name, arg, got, want)}
	}
	return nil
}
