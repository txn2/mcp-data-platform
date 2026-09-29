package platform

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/auth"
)

// closedObject is one object an older release advertised closed, and the keys
// it declared there.
type closedObject struct {
	Tool    string   `json:"tool"`
	Pointer string   `json:"pointer"`
	Keys    []string `json:"keys"`
}

// closedObjectsOfPastReleases reads testdata/closed_output_objects.json.
func closedObjectsOfPastReleases(t *testing.T) []closedObject {
	t.Helper()
	raw, err := os.ReadFile("testdata/closed_output_objects.json")
	require.NoError(t, err)
	var record struct {
		Objects []closedObject `json:"objects"`
	}
	require.NoError(t, json.Unmarshal(raw, &record))
	require.NotEmpty(t, record.Objects)
	return record.Objects
}

// schemaAt resolves a JSON pointer in a decoded schema, reporting false when
// the path no longer exists.
func schemaAt(v any, pointer string) (map[string]any, bool) {
	for seg := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
		switch n := v.(type) {
		case map[string]any:
			child, ok := n[seg]
			if !ok {
				return nil, false
			}
			v = child
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(n) {
				return nil, false
			}
			v = n[i]
		default:
			return nil, false
		}
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// keysGained returns, per recorded object, the keys the current schema
// declares there beyond what the older release declared, and how many
// recorded objects the listing reached. An object whose path is gone gains
// nothing: a key the schema does not describe is one the tool does not send.
func keysGained(schemas map[string]any, record []closedObject) (gained map[string][]string, checked int) {
	gained = map[string][]string{}
	for _, obj := range record {
		schema, ok := schemas[obj.Tool]
		if !ok {
			continue
		}
		checked++
		node, ok := schemaAt(schema, obj.Pointer)
		if !ok {
			continue
		}
		props, _ := node["properties"].(map[string]any)
		for key := range props {
			if !slices.Contains(obj.Keys, key) {
				gained[obj.Tool+obj.Pointer] = append(gained[obj.Tool+obj.Pointer], key)
			}
		}
	}
	return gained, checked
}

// TestNoKeyIsAddedWhereAnOlderReleaseAdvertisedAClosedObject is #1971's gate.
// Releases up to v1.137.1 advertised nested objects closed, and a client that
// cached one of those tool lists validates every later result against it, so a
// key added to such an object refuses the whole call: platform_info, and with
// it the session, failed for every owner of a failing automation when
// notices gained failing_automations. What a release adds goes at the top
// level, which every release has advertised open, or in a new object.
func TestNoKeyIsAddedWhereAnOlderReleaseAdvertisedAClosedObject(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test-platform"},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Personas: PersonasConfig{Definitions: map[string]PersonaDef{"default": {
			DisplayName: "Default",
			Roles:       []string{auth.RoleAnonymous},
			Tools:       ToolRulesDef{Allow: []string{"*"}},
		}}},
	}
	p, err := New(WithConfig(cfg))
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := p.MCPServer().Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = cs.Close() }()

	schemas := rawAdvertisedSchemas(ctx, t, cs)
	require.Contains(t, schemas, "platform_info")
	gained, checked := keysGained(schemas, closedObjectsOfPastReleases(t))
	assert.Positive(t, checked)
	assert.Empty(t, gained, "keys added to objects an older release advertised closed")
}

// The check itself: a key added to a recorded object is reported, one the
// record already names is not, and an object whose path is gone gains
// nothing.
func TestKeysGainedReportsOnlyAnAddedKey(t *testing.T) {
	record := []closedObject{
		{Tool: "platform_info", Pointer: "/properties/notices", Keys: []string{"since", "feedback"}},
		{Tool: "platform_info", Pointer: "/properties/notices/properties/gone/items", Keys: []string{"name"}},
		{Tool: "absent_tool", Pointer: "/properties/x", Keys: nil},
	}
	schemas := map[string]any{"platform_info": map[string]any{"properties": map[string]any{
		"notices": map[string]any{"properties": map[string]any{
			"since": map[string]any{}, "feedback": map[string]any{}, "failing_automations": map[string]any{},
		}},
	}}}
	gained, checked := keysGained(schemas, record)
	assert.Equal(t, 2, checked, "a tool the listing does not carry is not checked")
	assert.Equal(t, map[string][]string{"platform_info/properties/notices": {"failing_automations"}}, gained)

	_, ok := schemaAt([]any{map[string]any{}}, "/7")
	assert.False(t, ok, "an index past the end is a path that is gone")
	_, ok = schemaAt("leaf", "/a")
	assert.False(t, ok)
	node, ok := schemaAt([]any{map[string]any{"type": "object"}}, "/0")
	require.True(t, ok)
	assert.Equal(t, "object", node["type"])
}
