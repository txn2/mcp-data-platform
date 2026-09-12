package apigateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// prefixSpec describes an API served under /api/v1 on a host whose root also
// serves routes the spec does not describe, which is the platform-admin
// connection's shape (#1707). /admin/tools declares no operationId, so the id
// api_discover lists for it is the synthesized "GET /admin/tools".
const prefixSpec = `openapi: 3.0.0
info:
  title: admin
  version: "1"
servers:
  - url: /api/v1
paths:
  /admin/tools:
    get:
      summary: List tools
      responses:
        "200":
          description: ok
  /admin/things/{id}:
    get:
      operationId: getThing
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok`

// setupPrefixToolkit registers connection "admin" at baseURL, catalog-backed
// by prefixSpec, with the given required_path_prefix ("" for none).
func setupPrefixToolkit(t *testing.T, baseURL, prefix string) *Toolkit {
	t.Helper()
	tk := New("api")
	setupCatalogWithSpec(t, tk, "admin", "default", prefixSpec)
	cfg := map[string]any{"base_url": baseURL, "catalog_id": "admin"}
	if prefix != "" {
		cfg[cfgKeyRequiredPathPrefix] = prefix
	}
	if err := tk.AddConnection("admin", cfg); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	return tk
}

func TestParseConfig_RequiredPathPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, value, want, wantErr string
	}{
		{name: "unset", value: "", want: ""},
		{name: "a path", value: "/api/v1", want: "/api/v1"},
		{name: "trailing slash trimmed", value: "/api/v1/", want: "/api/v1"},
		{name: "no leading slash", value: "api/v1", wantErr: `must be a path such as "/api/v1"`},
		{name: "the root is no prefix", value: "/", want: ""},
		{name: "a query", value: "/api?v=1", wantErr: `must be a path such as "/api/v1"`},
		{name: "an empty segment", value: "/api//v1", wantErr: "is not a valid path: path must not contain empty segments"},
		{name: "a dot segment", value: "/api/../v1", wantErr: "is not a valid path: path must not contain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseConfig(map[string]any{"base_url": "https://x", cfgKeyRequiredPathPrefix: tc.value})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v; want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			if cfg.RequiredPathPrefix != tc.want {
				t.Errorf("RequiredPathPrefix = %q; want %q", cfg.RequiredPathPrefix, tc.want)
			}
		})
	}
}

func TestResolve_RequiredPathPrefix(t *testing.T) {
	c := setupPrefixToolkit(t, "https://x", "/api/v1").connections["admin"]

	refused := []struct {
		name, path string
		want       []string
		notWant    string
	}{
		{
			name: "a catalog operation missing the prefix names the path and the id",
			path: "/admin/tools",
			want: []string{`connection "admin" serves its routes under "/api/v1"`, `Send path "/api/v1/admin/tools"`, `operation_id "GET /admin/tools"`},
		},
		{
			name: "a templated operation names its declared id",
			path: "/admin/things/42",
			want: []string{`"/api/v1/admin/things/42"`, `operation_id "getThing"`},
		},
		{
			name:    "a route the catalog does not declare names only the path",
			path:    "/portal/knowledge-pages/retail-seasons",
			want:    []string{`Send path "/api/v1/portal/knowledge-pages/retail-seasons".`},
			notWant: "operation_id",
		},
		{
			name: "a query string is carried into the suggestion and ignored for the match",
			path: "/admin/tools?kind=trino",
			want: []string{`"/api/v1/admin/tools?kind=trino"`, `operation_id "GET /admin/tools"`},
		},
		{
			name: "a prefix sharing only characters is outside it",
			path: "/api/v10/admin/tools",
			want: []string{`"/api/v1/api/v10/admin/tools"`},
		},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := operationAddressing{Method: "get", Path: tc.path}.resolve(c)
			if err == nil {
				t.Fatalf("path %q was admitted", tc.path)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
			if tc.notWant != "" && strings.Contains(err.Error(), tc.notWant) {
				t.Errorf("error %q contains %q", err.Error(), tc.notWant)
			}
		})
	}

	for _, path := range []string{"/api/v1", "/api/v1/admin/tools", "/api/v1/portal/knowledge-pages/x?y=1"} {
		t.Run("admits "+path, func(t *testing.T) {
			if _, got, err := (operationAddressing{Method: "GET", Path: path}).resolve(c); err != nil || got != path {
				t.Fatalf("resolve(%q) = %q, %v; want the path unchanged", path, got, err)
			}
		})
	}

	// A path validatePath refuses is passed through to it, so the caller is
	// told what is wrong with the path rather than handed a prefixed form of
	// a path that is invalid in another way.
	for _, path := range []string{"admin/tools", "//evil.example.com/admin"} {
		t.Run("leaves an invalid path to path validation: "+path, func(t *testing.T) {
			if _, _, err := (operationAddressing{Method: "GET", Path: path}).resolve(c); err != nil {
				t.Fatalf("resolve(%q) = %v; want the path left to validatePath", path, err)
			}
		})
	}

	t.Run("an operation_id resolves inside the prefix", func(t *testing.T) {
		m, p, err := operationAddressing{OperationID: "GET /admin/tools"}.resolve(c)
		if err != nil || m != "GET" || p != "/api/v1/admin/tools" {
			t.Fatalf("resolve = %q %q, %v; want GET /api/v1/admin/tools", m, p, err)
		}
	})

	t.Run("a base URL ending in the prefix supplies it", func(t *testing.T) {
		carried := setupPrefixToolkit(t, "https://x/api/v1/", "/api/v1").connections["admin"]
		if _, _, err := (operationAddressing{Method: "GET", Path: "/admin/tools"}).resolve(carried); err != nil {
			t.Fatalf("resolve on a base URL carrying the prefix: %v", err)
		}
	})

	t.Run("a connection with no prefix admits any path", func(t *testing.T) {
		open := setupPrefixToolkit(t, "https://x", "").connections["admin"]
		if _, _, err := (operationAddressing{Method: "GET", Path: "/admin/tools"}).resolve(open); err != nil {
			t.Fatalf("resolve without a prefix: %v", err)
		}
	})
}

// TestHandlers_RequiredPathPrefixNeverReachesTheUpstream holds both tools that
// take a raw path to the rule through their real handlers, and proves the
// refused request is never sent: the upstream counts every request it sees.
func TestHandlers_RequiredPathPrefixNeverReachesTheUpstream(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}))
	t.Cleanup(upstream.Close)

	tk := setupPrefixToolkit(t, upstream.URL, "/api/v1")
	tk.SetExportDeps(defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{}))

	invokeRes, _, err := tk.handleInvoke(context.Background(), &mcp.CallToolRequest{}, InvokeInput{
		Connection: "admin", Method: "GET", Path: "/admin/tools",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	exportRes, _, err := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "admin", Method: "GET", Path: "/admin/tools", Name: "refused",
	})
	if err != nil {
		t.Fatalf("handleExport: %v", err)
	}

	// Through the handler, an invalid path is refused by path validation with
	// its own reason, and no prefix suggestion is made for it.
	badRes, _, err := tk.handleInvoke(context.Background(), &mcp.CallToolRequest{}, InvokeInput{
		Connection: "admin", Method: "GET", Path: "admin/tools",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if badRes == nil || !badRes.IsError || !strings.Contains(textContent(badRes), `must start with`) || strings.Contains(textContent(badRes), "Send path") {
		t.Errorf("a path without a leading slash: want the path-validation refusal only; got %s", textContent(badRes))
	}

	for name, res := range map[string]*mcp.CallToolResult{"api_invoke_endpoint": invokeRes, "api_export": exportRes} {
		if res == nil || !res.IsError {
			t.Fatalf("%s: want a refusal; got %+v", name, res)
		}
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(textContent(res)), &body); err != nil {
			t.Fatalf("%s: the refusal is not the error envelope: %v\n%s", name, err, textContent(res))
		}
		if !strings.Contains(body.Error, `Send path "/api/v1/admin/tools"`) {
			t.Errorf("%s: the refusal does not name the prefixed path: %s", name, body.Error)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the upstream received %d request(s) for a refused path", n)
	}
}

func TestBaseURLCarriesPrefix(t *testing.T) {
	for _, tc := range []struct {
		base string
		want bool
	}{
		{"https://x", false},
		{"https://x/", false},
		{"https://x/api/v1", true},
		{"https://x/api/v1/", true},
		{"https://x/tenant/api/v1", true},
		{"https://x/legacy", false},
		{"https://x/xapi/v1", false},
		{"https://x/api/v10", false},
		{"://bad", false},
	} {
		if got := baseURLCarriesPrefix(tc.base, "/api/v1"); got != tc.want {
			t.Errorf("baseURLCarriesPrefix(%q) = %v; want %v", tc.base, got, tc.want)
		}
	}
}
