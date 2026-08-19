package s3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"
)

// TestNullTolerantOutputSchemas_AdmitTheZeroValueAndTheRealResult proves the
// override admits what the SDK substitutes for a failed call (the zero output
// struct, whose slices marshal as null) while still validating a real listing,
// and that it touches only the tools whose schema has a top-level array.
func TestNullTolerantOutputSchemas_AdmitTheZeroValueAndTheRealResult(t *testing.T) {
	overrides := nullTolerantOutputSchemas()
	require.Contains(t, overrides, s3tools.ToolListBuckets)
	require.Contains(t, overrides, s3tools.ToolListObjects)
	assert.NotContains(t, overrides, s3tools.ToolGetObject, "a schema with no top-level array is left to mcp-s3's default")

	resolve := func(schema any) *jsonschema.Resolved {
		raw, err := json.Marshal(schema)
		require.NoError(t, err)
		var s jsonschema.Schema
		require.NoError(t, json.Unmarshal(raw, &s))
		r, err := s.Resolve(nil)
		require.NoError(t, err)
		return r
	}
	validate := func(r *jsonschema.Resolved, v any) error {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		var decoded any
		require.NoError(t, json.Unmarshal(raw, &decoded))
		return r.Validate(decoded)
	}

	// The default rejects the zero value; the override admits it and the
	// real result alike.
	zeroBuckets := s3tools.ListBucketsResult{}
	assert.Error(t, validate(resolve(s3tools.DefaultOutputSchema(s3tools.ToolListBuckets)), zeroBuckets),
		"mcp-s3's default schema rejects the zero value the SDK substitutes on failure")
	buckets := resolve(overrides[s3tools.ToolListBuckets])
	assert.NoError(t, validate(buckets, zeroBuckets))
	assert.NoError(t, validate(buckets, s3tools.ListBucketsResult{Buckets: []s3tools.BucketResult{{Name: "b"}}, Count: 1}))

	zeroObjects := s3tools.ListObjectsResult{}
	assert.Error(t, validate(resolve(s3tools.DefaultOutputSchema(s3tools.ToolListObjects)), zeroObjects))
	objects := resolve(overrides[s3tools.ToolListObjects])
	assert.NoError(t, validate(objects, zeroObjects))
	assert.NoError(t, validate(objects, s3tools.ListObjectsResult{Bucket: "b", Objects: []s3tools.ObjectResult{{Key: "k"}}, Count: 1}))

	// The default itself is not mutated by deriving the override.
	def, ok := s3tools.DefaultOutputSchema(s3tools.ToolListBuckets).(map[string]any)
	require.True(t, ok)
	defProps, ok := def[schemaKeyProperties].(map[string]any)
	require.True(t, ok)
	bucketsProp, ok := defProps["buckets"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "array", bucketsProp[schemaKeyType])

	// Shapes the normalizer refuses.
	_, changed := nullTolerantSchema(nil)
	assert.False(t, changed)
	_, changed = nullTolerantSchema(map[string]any{schemaKeyType: "object", schemaKeyProperties: map[string]any{"n": map[string]any{schemaKeyType: "integer"}}})
	assert.False(t, changed, "no array property, nothing to change")
	_, changed = nullTolerantSchema(make(chan int))
	assert.False(t, changed)
	_, changed = nullTolerantSchema(map[string]any{schemaKeyProperties: map[string]any{"n": "not a schema"}})
	assert.False(t, changed, "a non-object property is skipped")
}

// TestFailedListingReachesTheClientAsTheToolsOwnError drives the real mcp-s3
// toolkit, built by this adapter, through an MCP server and an in-memory client
// against an endpoint that refuses every request: s3_list_buckets and
// s3_list_objects must answer with the tool's error result, not with the SDK's
// output-validation error in its place.
func TestFailedListingReachesTheClientAsTheToolsOwnError(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `<Error><Code>AccessDenied</Code><Message>refused</Message></Error>`, http.StatusForbidden)
	}))
	t.Cleanup(refusing.Close)

	tk, err := New("acme", Config{
		Region:          s3TestRegionEast,
		Endpoint:        refusing.URL,
		AccessKeyID:     "a",
		SecretAccessKey: "b",
		UsePathStyle:    true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	server := mcp.NewServer(&mcp.Implementation{Name: "s", Version: "v0"}, nil)
	tk.RegisterTools(server)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	for name, args := range map[string]map[string]any{
		"s3_list_buckets": {},
		"s3_list_objects": {"bucket": "b"},
	} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err, "%s: the failure is a tool result, not a protocol error", name)
		require.True(t, res.IsError, "%s: the listing failed", name)
		require.NotEmpty(t, res.Content)
		content, ok := res.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.False(t, strings.Contains(content.Text, "validating tool output"), "%s: the SDK's validation text must not replace the tool's error: %s", name, content.Text)
		assert.Contains(t, content.Text, "failed to list", "%s: the tool's own reason reaches the client: %s", name, content.Text)
	}
}
