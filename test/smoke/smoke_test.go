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
		out := call(t, ctx, s, "manage_prompt", map[string]any{
			"command": "create",
			"name":    name,
			"content": "Smoke test prompt body.",
			"scope":   "personal",
		})
		if out["status"] != "created" {
			t.Fatalf("manage_prompt create: unexpected result: %v", out)
		}
		// Cleanup.
		callRaw(ctx, s, "manage_prompt", map[string]any{"command": "delete", "name": name, "scope": "personal"})
	})

	t.Run("save_artifact", func(t *testing.T) {
		out := call(t, ctx, s, "save_artifact", map[string]any{
			"name":         fmt.Sprintf("smoke-artifact-%d", stamp),
			"content":      "# Smoke\nLive smoke artifact.",
			"content_type": "text/markdown",
		})
		id, _ := out["asset_id"].(string)
		if id == "" {
			t.Fatalf("save_artifact: no asset_id in result: %v", out)
		}
		callRaw(ctx, s, "manage_artifact", map[string]any{"action": "delete", "asset_id": id})
	})

	t.Run("capture_insight", func(t *testing.T) {
		out := call(t, ctx, s, "capture_insight", map[string]any{
			"category":     "business_context",
			"insight_text": "Smoke test insight: this is a synthetic live-smoke record.",
			"confidence":   "low",
			"source":       "agent_discovery",
		})
		id, _ := out["insight_id"].(string)
		if id == "" {
			t.Fatalf("capture_insight: no insight_id in result: %v", out)
		}
		callRaw(ctx, s, "apply_knowledge", map[string]any{
			"action": "reject", "insight_ids": []string{id}, "review_notes": "smoke cleanup",
		})
	})

	t.Run("memory_manage remember", func(t *testing.T) {
		out := call(t, ctx, s, "memory_manage", map[string]any{
			"command":    "remember",
			"content":    "Smoke test memory: synthetic live-smoke preference record.",
			"dimension":  "preference",
			"confidence": "low",
		})
		id, _ := out["id"].(string)
		if id == "" {
			t.Fatalf("memory_manage remember: no id in result: %v", out)
		}
		callRaw(ctx, s, "memory_manage", map[string]any{"command": "forget", "id": id})
	})
}
