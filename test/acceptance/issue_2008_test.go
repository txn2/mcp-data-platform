//go:build integration

package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #2008: every HTTP deployment serves MCP 2026-07-28, the sessionless
// revision, and 2025-11-25 alike, whichever session store it runs. The dev
// stack runs the database store; the memory store is the default of a
// deployment with no database, so that criterion starts this tree's binary on
// a configuration with none. Each client asks for its revision, is agreed it,
// and reads it back from platform_info as protocol_version.
//
// Wire forms: platform_info takes no parameters, and list_connections is sent
// with the session handle alone, a string, its one form.

var issue2008Revisions = []string{"2026-07-28", "2025-11-25"}

// issue2008Session opens a session at version against base with apiKey and
// returns the platform_info result it reads.
func issue2008Session(t *testing.T, base, apiKey, version string) (*mcp.ClientSession, map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance-2008", Version: "1.0.0"}, nil)
	httpClient := &http.Client{Transport: authRoundTripper{key: apiKey, base: http.DefaultTransport}}
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base, HTTPClient: httpClient},
		&mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatalf("connecting on %s to %s: %v", version, base, err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if got := cs.InitializeResult().ProtocolVersion; got != version {
		t.Fatalf("asked for %s, the platform agreed %s", version, got)
	}
	return cs, issue2008Call(t, ctx, cs, "platform_info", nil)
}

// issue2008Call calls one tool and decodes its structured result.
func issue2008Call(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s failed: %s", name, firstText(res))
	}
	out := map[string]any{}
	raw, err := json.Marshal(res.StructuredContent)
	if err == nil && string(raw) != "null" {
		err = json.Unmarshal(raw, &out)
	}
	if err != nil || len(out) == 0 {
		if jerr := json.Unmarshal([]byte(firstText(res)), &out); jerr != nil {
			t.Fatalf("%s answered no object: %s", name, firstText(res))
		}
	}
	return out
}

func TestIssue2008_TheDatabaseStoreServesEveryRevision(t *testing.T) {
	for _, version := range issue2008Revisions {
		t.Run(version, func(t *testing.T) {
			_, info := issue2008Session(t, baseURL(), devAPIKey(), version)
			if info["protocol_version"] != version {
				t.Errorf("platform_info reports protocol_version %v, want %s", info["protocol_version"], version)
			}
		})
	}
}

const issue2008MemoryConfig = `
server:
  name: acceptance-2008
  transport: http
  address: "%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2008-key"
        name: "acceptance"
        roles: ["admin"]
personas:
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
`

func TestIssue2008_TheMemoryStoreServesEveryRevision(t *testing.T) {
	p := startPlatform(t, issue2008MemoryConfig)
	p.serving(t, 2*time.Minute)
	for _, version := range issue2008Revisions {
		t.Run(version, func(t *testing.T) {
			cs, info := issue2008Session(t, p.base, "acceptance-2008-key", version)
			if info["protocol_version"] != version {
				t.Errorf("platform_info reports protocol_version %v, want %s", info["protocol_version"], version)
			}
			// A second call on the same session is answered on it: the
			// session the memory store keeps is the one the first call made.
			listed := issue2008Call(t, t.Context(), cs, "list_connections", map[string]any{"session_id": info["session_id"]})
			if _, ok := listed["connections"]; !ok {
				t.Errorf("list_connections answered without connections: %v", listed)
			}
		})
	}
}
