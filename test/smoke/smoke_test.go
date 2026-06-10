//go:build integration

// Package smoke is a post-deploy live smoke test: it connects to a running
// MCP Data Platform over HTTP as a real MCP client (admin API key) and exercises
// every user-facing write tool end to end, then cleans up. It is the layer that
// catches failures the unit/integration gates cannot see — deployment drift,
// schema-not-at-version, config — by actually calling the deployed tools.
//
// It is the test that, had it existed, would have caught the prompt-create
// failure the moment it shipped: it creates a prompt against the live server
// and fails if the call errors.
//
// Skipped unless MCP_API_KEY is set. Run against any instance:
//
//	MCP_BASE_URL=http://localhost:8099 MCP_API_KEY=... \
//	  go test -tags=integration ./test/smoke/ -run Smoke -v
package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// authRoundTripper injects the admin API key as a Bearer token on every request.
type authRoundTripper struct {
	key  string
	base http.RoundTripper
}

func (a authRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+a.key)
	return a.base.RoundTrip(r)
}

// smokeSession connects an authenticated MCP client to the target server.
func smokeSession(t *testing.T) (*mcp.ClientSession, context.Context) {
	t.Helper()
	apiKey := os.Getenv("MCP_API_KEY")
	if apiKey == "" {
		t.Skip("MCP_API_KEY not set; skipping live smoke test")
	}
	baseURL := os.Getenv("MCP_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8099"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	httpClient := &http.Client{Transport: authRoundTripper{key: apiKey, base: http.DefaultTransport}}
	client := mcp.NewClient(&mcp.Implementation{Name: "post-deploy-smoke", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   baseURL,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		t.Fatalf("connect to %s: %v", baseURL, err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, ctx
}

// call invokes a tool and returns the parsed JSON result, failing on a tool error.
func call(t *testing.T, ctx context.Context, s *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport error: %v", name, err)
	}
	text := firstText(res)
	if res.IsError {
		t.Fatalf("%s: tool error: %s", name, text)
	}
	var out map[string]any
	if text != "" {
		_ = json.Unmarshal([]byte(text), &out)
	}
	return out
}

// callRaw invokes a tool without failing on error, returning the result for cleanup.
func callRaw(ctx context.Context, s *mcp.ClientSession, name string, args map[string]any) {
	_, _ = s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
}

func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// TestSmoke_WriteToolsRoundTrip exercises each user-facing write tool against the
// live server and cleans up after itself.
func TestSmoke_WriteToolsRoundTrip(t *testing.T) {
	s, ctx := smokeSession(t)
	stamp := time.Now().UnixNano()

	t.Run("manage_prompt create", func(t *testing.T) {
		name := fmt.Sprintf("smoke-prompt-%d", stamp)
		content := fmt.Sprintf("Smoke test prompt body %d.", stamp)
		out := call(t, ctx, s, "manage_prompt", map[string]any{
			"command": "create",
			"name":    name,
			"content": content,
			"scope":   "personal",
		})
		if out["status"] != "created" {
			t.Fatalf("manage_prompt create: unexpected result: %v", out)
		}
		// Read-back: the write must be retrievable with the content intact.
		got := call(t, ctx, s, "manage_prompt", map[string]any{
			"command": "get", "name": name, "scope": "personal",
		})
		if got["content"] != content {
			t.Fatalf("manage_prompt read-back: content mismatch: got %q want %q", got["content"], content)
		}
		// Cleanup.
		callRaw(ctx, s, "manage_prompt", map[string]any{"command": "delete", "name": name, "scope": "personal"})
	})

	t.Run("save_artifact", func(t *testing.T) {
		name := fmt.Sprintf("smoke-artifact-%d", stamp)
		out := call(t, ctx, s, "save_artifact", map[string]any{
			"name":         name,
			"content":      "# Smoke\nLive smoke artifact.",
			"content_type": "text/markdown",
		})
		id, _ := out["asset_id"].(string)
		if id == "" {
			t.Fatalf("save_artifact: no asset_id in result: %v", out)
		}
		// Read-back: the asset must be retrievable with its metadata intact.
		got := call(t, ctx, s, "manage_artifact", map[string]any{"action": "get", "asset_id": id})
		if got["name"] != name {
			t.Fatalf("save_artifact read-back: name mismatch: got %q want %q", got["name"], name)
		}
		if got["content_type"] != "text/markdown" {
			t.Fatalf("save_artifact read-back: content_type mismatch: got %v", got["content_type"])
		}
		if size, _ := got["size_bytes"].(float64); size <= 0 {
			t.Fatalf("save_artifact read-back: size_bytes not persisted: %v", got["size_bytes"])
		}
		callRaw(ctx, s, "manage_artifact", map[string]any{"action": "delete", "asset_id": id})
	})

	t.Run("capture_insight", func(t *testing.T) {
		marker := fmt.Sprintf("smokeinsight%d", stamp)
		out := call(t, ctx, s, "capture_insight", map[string]any{
			"category":     "business_context",
			"insight_text": fmt.Sprintf("Smoke test insight %s: synthetic live-smoke record.", marker),
			"confidence":   "low",
			"source":       "agent_discovery",
		})
		id, _ := out["insight_id"].(string)
		if id == "" {
			t.Fatalf("capture_insight: no insight_id in result: %v", out)
		}
		// Read-back: recall_insight must find the captured record by its marker.
		got := call(t, ctx, s, "recall_insight", map[string]any{"query": marker, "limit": float64(10)})
		if !recallContainsID(got, "insights", "insight", id) {
			t.Fatalf("capture_insight read-back: recall_insight did not return id %s: %v", id, got)
		}
		callRaw(ctx, s, "apply_knowledge", map[string]any{
			"action": "reject", "insight_ids": []string{id}, "review_notes": "smoke cleanup",
		})
	})

	t.Run("memory_manage remember", func(t *testing.T) {
		marker := fmt.Sprintf("smokememory%d", stamp)
		out := call(t, ctx, s, "memory_manage", map[string]any{
			"command":    "remember",
			"content":    fmt.Sprintf("Smoke test memory %s: synthetic preference record.", marker),
			"dimension":  "preference",
			"confidence": "low",
		})
		id, _ := out["id"].(string)
		if id == "" {
			t.Fatalf("memory_manage remember: no id in result: %v", out)
		}
		// Read-back: memory_recall must return the freshly remembered record.
		// memory_recall serializes each hit as a memory.ScoredRecord, whose
		// fields carry no json tags, so the nested object key is "Record"
		// (capitalized), not "record".
		got := call(t, ctx, s, "memory_recall", map[string]any{
			"query": marker, "include_stale": true, "limit": float64(10),
		})
		if !recallContainsID(got, "memories", "Record", id) {
			t.Fatalf("memory_manage read-back: memory_recall did not return id %s: %v", id, got)
		}
		callRaw(ctx, s, "memory_manage", map[string]any{"command": "forget", "id": id})
	})
}

// probe calls a read-only tool and returns a non-nil error only when the call
// fails (transport error or a tool-level IsError). Empty results are healthy:
// liveness is "the toolkit answered", not "the toolkit has data".
func probe(ctx context.Context, s *mcp.ClientSession, name string, args map[string]any) error {
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return fmt.Errorf("transport error: %w", err)
	}
	if res.IsError {
		return fmt.Errorf("tool error: %s", firstText(res))
	}
	return nil
}

// TestSmoke_ToolkitLiveness asserts that every toolkit the deployment reports as
// configured can actually answer one read-only call, and that every gateway
// connection reporting runtime health is reachable. A dark toolkit or a dead
// upstream — the class of failure that shipped silently before — fails the gate
// at deploy time instead of when a user hits it. Toolkit kinds the deployment
// does not run are skipped, so the gate is portable across deployments.
func TestSmoke_ToolkitLiveness(t *testing.T) {
	s, ctx := smokeSession(t)

	info := call(t, ctx, s, "platform_info", map[string]any{})
	kindsRaw, _ := info["toolkits"].([]any)
	if len(kindsRaw) == 0 {
		t.Fatalf("platform_info reported no toolkits: %v", info)
	}
	present := make(map[string]bool, len(kindsRaw))
	for _, k := range kindsRaw {
		if ks, ok := k.(string); ok {
			present[ks] = true
		}
	}

	// One cheap read-only probe per toolkit kind. The platform kind is already
	// exercised by the platform_info call above; the gateway kinds (mcp, api)
	// are probed per-connection via list_connections health below.
	probes := []struct {
		kind, tool string
		args       map[string]any
	}{
		{"trino", "trino_browse", map[string]any{}},
		{"datahub", "datahub_search", map[string]any{"query": "*"}},
		{"s3", "s3_list_buckets", map[string]any{}},
		{"memory", "memory_recall", map[string]any{"query": "liveness"}},
		{"knowledge", "recall_insight", map[string]any{"query": "liveness"}},
		{"portal", "manage_artifact", map[string]any{"action": "list"}},
	}
	for _, p := range probes {
		if !present[p.kind] {
			continue
		}
		if err := probe(ctx, s, p.tool, p.args); err != nil {
			t.Errorf("toolkit %q is dark: %s failed: %v", p.kind, p.tool, err)
		}
	}

	// Gateway/API connections expose runtime reachability via list_connections
	// (the health surface from #584). A connection that reports an error is a
	// dead upstream and fails the gate. A connection that is simply unreachable
	// with no error (e.g. an authorization_code connection awaiting its first
	// sign-in) is not flagged: it never made a call, so it is pending, not dark.
	conns := call(t, ctx, s, "list_connections", map[string]any{})
	entries, _ := conns["connections"].([]any)
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		health, ok := m["health"].(map[string]any)
		if !ok {
			continue // kinds without reachability tracking
		}
		reachable, _ := health["reachable"].(bool)
		lastErr, _ := health["last_error"].(string)
		if !reachable && lastErr != "" {
			t.Errorf("connection %v/%v is unreachable: %s", m["kind"], m["name"], lastErr)
		}
	}
}

// recallContainsID reports whether a recall-style response contains an item
// whose nested object carries the given id. Recall tools wrap each hit as
// {<arrayKey>: [{<objKey>: {id: ...}}]} (e.g. insights[].insight.id,
// memories[].Record.id), so the read-back matches the created id exactly rather
// than relying on result ordering.
func recallContainsID(resp map[string]any, arrayKey, objKey, id string) bool {
	items, ok := resp[arrayKey].([]any)
	if !ok {
		return false
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		obj, ok := m[objKey].(map[string]any)
		if !ok {
			continue
		}
		if obj["id"] == id {
			return true
		}
	}
	return false
}
