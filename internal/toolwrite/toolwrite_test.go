package toolwrite

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassify_ReadOnlyTools(t *testing.T) {
	for _, tool := range []string{
		"platform_info", "list_connections", "platform_find_tools",
		"show_prompts", "show_scripts", "search", "fetch",
		"trino_query", "trino_explain", "trino_browse", "trino_describe_table",
		"s3_list", "datahub_browse", "datahub_get_lineage",
		"api_discover", "graphql_discover",
	} {
		t.Run(tool, func(t *testing.T) {
			got := classify(tool, map[string]any{})
			assert.False(t, got.Writes, "must read")
			assert.True(t, got.Declared)
			assert.Equal(t, tool, got.Call)
		})
	}
}

func TestClassify_WriteTools(t *testing.T) {
	for _, tool := range []string{
		"save_asset", "apply_knowledge", "memory_capture", "run_script",
		"trino_execute", "trino_export", "api_export", "graphql_export",
		"datahub_create", "datahub_update", "datahub_delete",
	} {
		t.Run(tool, func(t *testing.T) {
			got := classify(tool, map[string]any{})
			assert.True(t, got.Writes, "must write")
			assert.True(t, got.Declared)
			assert.Equal(t, tool, got.Call)
		})
	}
}

func TestClassify_ActionTools(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		args  map[string]any
		write bool
		call  string
	}{
		{"asset list reads", "manage_asset", map[string]any{"action": "list"}, false, "manage_asset action=list"},
		{"asset get_content reads", "manage_asset", map[string]any{"action": "get_content"}, false, "manage_asset action=get_content"},
		{"asset patch writes", "manage_asset", map[string]any{"action": "patch"}, true, "manage_asset action=patch"},
		{"asset delete writes", "manage_asset", map[string]any{"action": "delete"}, true, "manage_asset action=delete"},
		{"asset share writes", "manage_asset", map[string]any{"action": "share"}, true, "manage_asset action=share"},
		{"table list reads", "manage_table", map[string]any{"action": "list"}, false, "manage_table action=list"},
		{"table register writes", "manage_table", map[string]any{"action": "register"}, true, "manage_table action=register"},
		{"table unregister writes", "manage_table", map[string]any{"action": "unregister"}, true, "manage_table action=unregister"},
		{"resource create writes", "manage_resource", map[string]any{"action": "create"}, true, "manage_resource action=create"},
		{"resource replace writes", "manage_resource", map[string]any{"action": "replace_content"}, true, "manage_resource action=replace_content"},
		{"feedback get reads", "manage_feedback", map[string]any{"action": "get"}, false, "manage_feedback action=get"},
		{"feedback reply writes", "manage_feedback", map[string]any{"action": "reply"}, true, "manage_feedback action=reply"},
		{"memory list reads", "memory_manage", map[string]any{"command": "list"}, false, "memory_manage command=list"},
		{"memory review_stale reads", "memory_manage", map[string]any{"command": "review_stale"}, false, "memory_manage command=review_stale"},
		{"memory help reads", "memory_manage", map[string]any{}, false, "memory_manage"},
		{"memory forget writes", "memory_manage", map[string]any{"command": "forget"}, true, "memory_manage command=forget"},
		{"prompt use reads", "manage_prompt", map[string]any{"command": "use"}, false, "manage_prompt command=use"},
		{"prompt create writes", "manage_prompt", map[string]any{"command": "create"}, true, "manage_prompt command=create"},
		{"script versions reads", "manage_script", map[string]any{"command": "versions"}, false, "manage_script command=versions"},
		{"script schedule_set writes", "manage_script", map[string]any{"command": "schedule_set"}, true, "manage_script command=schedule_set"},
		{"script state writes", "manage_script", map[string]any{"command": "state"}, true, "manage_script command=state"},
		{"object get reads", "s3_object", map[string]any{"action": "get"}, false, "s3_object action=get"},
		{"object presign reads", "s3_object", map[string]any{"action": "presign"}, false, "s3_object action=presign"},
		{"object put writes", "s3_object", map[string]any{"action": "put"}, true, "s3_object action=put"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.tool, tc.args)
			assert.Equal(t, tc.write, got.Writes)
			assert.True(t, got.Declared)
			assert.Equal(t, tc.call, got.Call)
		})
	}
}

// TestClassify_UnknownActionWrites is the deny-by-default rule applied one
// level down: a verb added to an action tool writes until somebody classifies
// it, so a new manage_asset action cannot start persisting inside drafts.
func TestClassify_UnknownActionWrites(t *testing.T) {
	for _, args := range []map[string]any{
		{"action": "publish_everything"},
		{"action": 7},
		{},
	} {
		got := classify("manage_asset", args)
		assert.True(t, got.Writes, "an unrecognized action writes: %v", args)
		assert.True(t, got.Declared)
	}
}

// TestClassify_UnknownToolWrites is the package's whole safety property: a tool
// nobody classified is treated as one that persists.
func TestClassify_UnknownToolWrites(t *testing.T) {
	got := classify("vendor__create_invoice", map[string]any{})
	assert.True(t, got.Writes)
	assert.False(t, got.Declared, "and says the platform did not decide it")
	assert.Equal(t, "vendor__create_invoice", got.Call)
}

func TestClassify_InvokeEndpointByMethod(t *testing.T) {
	cases := []struct {
		method string
		write  bool
	}{
		{"GET", false},
		{"get", false},
		{"HEAD", false},
		{"PROPFIND", false},
		{"POST", true},
		{"PUT", true},
		{"PATCH", true},
		{"DELETE", true},
		{"MKCOL", true},
		{"MOVE", true},
		{"COPY", true},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			got := classify(ToolInvokeEndpoint, map[string]any{"method": tc.method, "path": "/things"})
			assert.Equal(t, tc.write, got.Writes)
			assert.True(t, got.Declared)
		})
	}
}

// TestClassify_InvokeEndpointByOperationID covers the addressing api_discover
// hands an author: without a resolver nothing here can read what the operation
// sends, and the call is a write the platform says it cannot judge.
func TestClassify_InvokeEndpointByOperationID(t *testing.T) {
	args := map[string]any{"connection": "crm", "spec": "core", "operation_id": "listContacts"}

	bare := classify(ToolInvokeEndpoint, args)
	assert.True(t, bare.Writes)
	assert.False(t, bare.Declared, "an unresolved operation is not a decision about the call")

	var gotConn, gotSpec, gotID string
	resolved := Classifier{ResolveMethod: func(connection, spec, operationID string) (string, bool) {
		gotConn, gotSpec, gotID = connection, spec, operationID
		return "GET", true
	}}.Classify(ToolInvokeEndpoint, args)
	assert.False(t, resolved.Writes, "a resolved GET reads")
	assert.True(t, resolved.Declared)
	assert.Equal(t, "api_invoke_endpoint GET", resolved.Call)
	assert.Equal(t, "crm", gotConn)
	assert.Equal(t, "core", gotSpec)
	assert.Equal(t, "listContacts", gotID)

	writing := Classifier{ResolveMethod: func(string, string, string) (string, bool) {
		return "POST", true
	}}.Classify(ToolInvokeEndpoint, args)
	assert.True(t, writing.Writes, "a resolved POST writes")

	unresolvable := Classifier{ResolveMethod: func(string, string, string) (string, bool) {
		return "", false
	}}.Classify(ToolInvokeEndpoint, args)
	assert.True(t, unresolvable.Writes)
	assert.False(t, unresolvable.Declared)
}

// TestClassify_InvokeEndpointMethodWinsOverOperationID pins that an explicit
// method is never second-guessed by a lookup: the gateway resolves the id only
// when the call did not say what it sends.
func TestClassify_InvokeEndpointMethodWinsOverOperationID(t *testing.T) {
	called := false
	got := Classifier{ResolveMethod: func(string, string, string) (string, bool) {
		called = true
		return "POST", true
	}}.Classify(ToolInvokeEndpoint, map[string]any{
		"method": "GET", "path": "/things", "operation_id": "listContacts",
	})
	assert.False(t, got.Writes)
	assert.False(t, called, "the resolver is not consulted when the call carries its method")
}

func TestClassify_GraphQLByOperationKind(t *testing.T) {
	query := classify(ToolGraphQLQuery, map[string]any{
		"connection": "erp", "query": "query Contacts { contacts { id } }",
	})
	assert.False(t, query.Writes)
	assert.Equal(t, "graphql_query QUERY", query.Call)

	mutation := classify(ToolGraphQLQuery, map[string]any{
		"connection": "erp", "query": "mutation Add { addContact(name: \"x\") { id } }",
	})
	assert.True(t, mutation.Writes)
	assert.Equal(t, "graphql_query MUTATION", mutation.Call)
}

// TestClassify_GraphQLNamedOperation pins that the classifier judges the
// operation that will EXECUTE, not the first one in the document.
func TestClassify_GraphQLNamedOperation(t *testing.T) {
	doc := "query Read { a } mutation Write { b }"

	read := classify(ToolGraphQLQuery, map[string]any{"query": doc, "operation_name": "Read"})
	assert.False(t, read.Writes)

	write := classify(ToolGraphQLQuery, map[string]any{"query": doc, "operation_name": "Write"})
	assert.True(t, write.Writes)
}

// TestClassify_GraphQLUnparseableWrites pins the direction the classifier fails
// in: a document it cannot read is not a document it may call a read.
func TestClassify_GraphQLUnparseableWrites(t *testing.T) {
	for _, doc := range []string{"", "{{{", "query Read { a } mutation Write { b }"} {
		got := classify(ToolGraphQLQuery, map[string]any{"query": doc})
		assert.True(t, got.Writes, "unreadable document %q must write", doc)
		assert.True(t, got.Declared)
	}
}

func TestClassify_TrimsAndTolerates(t *testing.T) {
	assert.False(t, classify("  search  ", nil).Writes, "a padded tool name is the same tool")
	assert.False(t, classify("manage_asset", map[string]any{"action": " list "}).Writes,
		"a padded action is the same action")
	assert.True(t, classify("", nil).Writes, "an empty tool name is not a read")
}

// classify is the zero Classifier's decision, which is what a caller holding no
// live toolkits gets.
func classify(tool string, args map[string]any) Decision {
	return Classifier{}.Classify(tool, args)
}

func TestClassified(t *testing.T) {
	for _, tool := range []string{
		"search", "trino_execute", "manage_resource", ToolInvokeEndpoint, ToolGraphQLQuery,
	} {
		assert.True(t, Classified(tool), "%s has a rule", tool)
	}
	require.False(t, Classified("vendor__create_invoice"))
	require.False(t, Classified(""))
}

// TestReadOnly covers the per-tool question a registration asks before it
// advertises readOnlyHint (#1692), including the three answers that differ
// from Classify's per-call one: an action tool whose call happens to read is
// still not a read-only TOOL, a write tool is not, and a tool nobody named is
// not.
func TestReadOnly(t *testing.T) {
	for name, want := range map[string]bool{
		"platform_info":    true,
		"search":           true,
		"fetch":            true,
		"api_discover":     true,
		"graphql_discover": true,
		"s3_list":          true,
		// Action tools: some of their calls read, the tool does not.
		"manage_asset":    false,
		"manage_resource": false,
		"s3_object":       false,
		"memory_manage":   false,
		// Writes.
		"save_asset":      false,
		"apply_knowledge": false,
		"trino_export":    false,
		// Sent-dependent, and unknown.
		"api_invoke_endpoint": false,
		"graphql_query":       false,
		"no_such_tool":        false,
	} {
		if got := ReadOnly(name); got != want {
			t.Errorf("ReadOnly(%q) = %t, want %t", name, got, want)
		}
	}

	if !ReadOnly("  search  ") {
		t.Error("ReadOnly should trim its argument, as Classify does")
	}
}
