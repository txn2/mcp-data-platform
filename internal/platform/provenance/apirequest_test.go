package provenance

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// forecastSpec is a catalog whose one operation takes its address in the path,
// which is the shape that made twelve calls read identically (#1423).
const forecastSpec = `openapi: 3.0.0
info:
  title: weather
  version: "1"
paths:
  /gridpoints/{office}/{grid}/forecast:
    get:
      operationId: getGridpointForecast
      parameters:
        - name: office
          in: path
          required: true
          schema:
            type: string
        - name: grid
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok
  /fetch:
    post:
      operationId: fetch_url
      responses:
        "200":
          description: ok`

// apiEvent builds one recorded api gateway call on connection "nws".
func apiEvent(id string, index int, params map[string]any) audit.Event {
	ev := event(id, "api_invoke_endpoint", "api", index, withParams(params))
	ev.Connection = "nws"
	return ev
}

// forecastRegistry is the live toolkit registry a deployment hands the
// capturer: a real api gateway toolkit, holding a real catalog, registered the
// way the platform registers it.
func forecastRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	tk := apigatewaykit.New("api")
	store := catalog.NewMemoryStore()
	tk.SetCatalogStore(store)
	require.NoError(t, store.CreateCatalog(context.Background(), catalog.Catalog{
		ID: "weather", Name: "weather", DisplayName: "weather",
	}))
	require.NoError(t, store.UpsertSpec(context.Background(), "weather", catalog.SpecEntry{
		SpecName: "default", Content: forecastSpec, SourceKind: catalog.SourceInline,
	}))
	require.NoError(t, tk.AddConnection("nws", map[string]any{
		"base_url":   "https://api.weather.example",
		"catalog_id": "weather",
	}))

	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(otherToolkit{}))
	require.NoError(t, reg.Register(tk))
	return reg
}

// otherToolkit is a registered toolkit that resolves nothing, which is every
// toolkit but the api gateway. The walk must pass over it rather than stop.
type otherToolkit struct{}

func (otherToolkit) Kind() string                          { return "trino" }
func (otherToolkit) Name() string                          { return "warehouse" }
func (otherToolkit) Connection() string                    { return "warehouse" }
func (otherToolkit) RegisterTools(*mcp.Server)             {}
func (otherToolkit) Tools() []string                       { return nil }
func (otherToolkit) SetSemanticProvider(semantic.Provider) {}
func (otherToolkit) SetQueryProvider(query.Provider)       {}
func (otherToolkit) Close() error                          { return nil }

// captureAPI runs one capture over the given rows and returns the calls it
// recorded, with the toolkit registry wired exactly as the platform wires it.
func captureAPI(t *testing.T, toolkits ToolkitLister, events ...audit.Event) []portal.ProvenanceCall {
	t.Helper()
	c := New(&fakeReader{events: events}, nil, WithToolkits(toolkits))
	c.now = func() time.Time { return testNow.Add(time.Hour) }
	return c.Capture(context.Background(), saveRequest()).Calls
}

// An operation addressed by id records the path its arguments resolve to, so
// two calls to the same operation read differently. This is the defect: the
// audit row holds the operation and the values, the path template holding them
// lives in the connection's catalog.
func TestCaptureAPI_ResolvesOperationIntoPath(t *testing.T) {
	calls := captureAPI(t, forecastRegistry(t),
		apiEvent("e1", 0, map[string]any{
			"connection": "nws", "operation_id": "getGridpointForecast",
			"path_params": map[string]any{"office": "LWX", "grid": "96,72"},
		}),
		apiEvent("e2", 1, map[string]any{
			"connection": "nws", "operation_id": "getGridpointForecast",
			"path_params": map[string]any{"office": "LOX", "grid": "155,45"},
		}),
	)

	require.Len(t, calls, 2)
	assert.Equal(t, "GET", calls[0].Method)
	assert.Equal(t, "/gridpoints/LWX/96%2C72/forecast", calls[0].Path)
	assert.Equal(t, "GET /gridpoints/LWX/96%2C72/forecast", calls[0].Request)
	assert.Equal(t, "getGridpointForecast", calls[0].OperationID,
		"the operation it was addressed by stays on the record")
	assert.NotEqual(t, calls[0].Request, calls[1].Request,
		"two fetches of different resources must not read identically")
}

// A call the catalog cannot resolve — the connection is gone, the operation
// was removed, the values no longer fit its template — still says which of
// several calls to that operation it was.
func TestCaptureAPI_UnresolvedOperationKeepsItsValues(t *testing.T) {
	cases := []struct {
		name     string
		toolkits ToolkitLister
		params   map[string]any
		want     string
	}{
		{
			name:     "no registry wired",
			toolkits: nil,
			params: map[string]any{
				"connection": "nws", "operation_id": "getGridpointForecast",
				"path_params": map[string]any{"office": "LWX", "grid": "96,72"},
			},
			want: "getGridpointForecast grid=96,72 office=LWX",
		},
		{
			name:     "operation not in the catalog",
			toolkits: forecastRegistry(t),
			params: map[string]any{
				"connection": "nws", "operation_id": "getRetiredThing",
				"path_params": map[string]any{"id": "7"},
			},
			want: "getRetiredThing id=7",
		},
		{
			name:     "values no longer fit the template",
			toolkits: forecastRegistry(t),
			params: map[string]any{
				"connection": "nws", "operation_id": "getGridpointForecast",
				"path_params": map[string]any{"office": "LWX"},
			},
			want: "getGridpointForecast office=LWX",
		},
		{
			name:     "connection is gone",
			toolkits: forecastRegistry(t),
			params: map[string]any{
				"connection": "retired", "operation_id": "getGridpointForecast",
				"path_params": map[string]any{"office": "LWX", "grid": "96,72"},
			},
			want: "getGridpointForecast grid=96,72 office=LWX",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := apiEvent("e1", 0, c.params)
			if conn, ok := c.params["connection"].(string); ok {
				ev.Connection = conn
			}
			calls := captureAPI(t, c.toolkits, ev)
			require.Len(t, calls, 1)
			assert.Equal(t, c.want, calls[0].Request)
			assert.Empty(t, calls[0].Path, "an unresolved operation must not invent a path")
		})
	}
}

// A call addressed by method and path renders as it always has, now carrying
// what it sent as well.
func TestCaptureAPI_RequestFromRow(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{
			name:   "method and path",
			params: map[string]any{"method": "GET", "path": "/v1/orders"},
			want:   "GET /v1/orders",
		},
		{
			name: "query string, in key order",
			params: map[string]any{
				"method": "GET", "path": "/v1/orders",
				"query_params": map[string]any{"status": "open", "limit": float64(5)},
			},
			want: "GET /v1/orders?limit=5&status=open",
		},
		{
			name: "a repeated query parameter keeps every value",
			params: map[string]any{
				"method": "GET", "path": "/v1/orders",
				"query_params": map[string]any{"id": []any{"a", "b"}},
			},
			want: "GET /v1/orders?id=a&id=b",
		},
		{
			name: "a json body follows the request line",
			params: map[string]any{
				"method": "POST", "path": "/fetch",
				"body": map[string]any{"url": "https://example.com/a.json"},
			},
			want: "POST /fetch\n{\"url\":\"https://example.com/a.json\"}",
		},
		{
			name: "a text body is kept as it was sent",
			params: map[string]any{
				"method": "POST", "path": "/notes", "body": "plain text",
			},
			want: "POST /notes\nplain text",
		},
		{
			name: "a redacted body reads as the redaction",
			params: map[string]any{
				"method": "POST", "path": "/fetch", "body": "[REDACTED]",
			},
			want: "POST /fetch\n[REDACTED]",
		},
		{
			name: "a value that is absent or will not encode records as nothing",
			params: map[string]any{
				"method": "GET", "path": "/v1/orders",
				"query_params": map[string]any{"limit": math.NaN(), "cursor": nil},
				"body":         math.Inf(1),
			},
			want: "GET /v1/orders?cursor=&limit=",
		},
		{
			name:   "a row with no arguments has no request",
			params: nil,
			want:   "",
		},
		{
			name:   "an unaddressed row has no request",
			params: map[string]any{"connection": "nws"},
			want:   "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := captureAPI(t, nil, apiEvent("e1", 0, c.params))
			require.Len(t, calls, 1)
			assert.Equal(t, c.want, calls[0].Request)
		})
	}
}

// The url of a fetch is the whole difference between two calls to the same
// operation, and it is in the body. This is the reported case (#1423).
func TestCaptureAPI_FetchURLsReadDifferently(t *testing.T) {
	calls := captureAPI(t, forecastRegistry(t),
		apiEvent("e1", 0, map[string]any{
			"connection": "nws", "operation_id": "fetch_url",
			"body": map[string]any{"url": "https://example.com/a.json"},
		}),
		apiEvent("e2", 1, map[string]any{
			"connection": "nws", "operation_id": "fetch_url",
			"body": map[string]any{"url": "https://example.com/b.json"},
		}),
	)

	require.Len(t, calls, 2)
	assert.Equal(t, "POST /fetch\n{\"url\":\"https://example.com/a.json\"}", calls[0].Request)
	assert.NotEqual(t, calls[0].Request, calls[1].Request)
}

// A request body is arbitrary size and the capture is stored on the asset, so
// what is recorded is bounded and says that it was cut.
func TestBoundRequest(t *testing.T) {
	assert.Equal(t, "GET /v1/orders", boundRequest("GET /v1/orders"))

	large := "POST /upload\n" + strings.Repeat("x", maxRequestBytes)
	bounded := boundRequest(large)
	assert.True(t, strings.HasSuffix(bounded, truncationMarker))
	assert.Len(t, bounded, maxRequestBytes+len(truncationMarker))

	// The cut lands on a rune boundary: a multi-byte character straddling the
	// bound is dropped whole rather than left as an invalid fragment.
	straddling := boundRequest("x" + strings.Repeat("é", maxRequestBytes))
	kept := strings.TrimSuffix(straddling, truncationMarker)
	assert.True(t, utf8.ValidString(kept))
	assert.Len(t, kept, maxRequestBytes-1, "the character the bound split is dropped whole")
}

// A redacted argument renders as the redaction rather than as its value.
func TestCaptureAPI_RedactedArguments(t *testing.T) {
	calls := captureAPI(t, forecastRegistry(t), apiEvent("e1", 0, map[string]any{
		"connection": "nws", "operation_id": "getGridpointForecast",
		"path_params": "[REDACTED]", "query_params": "[REDACTED]",
	}))

	require.Len(t, calls, 1)
	assert.Equal(t, "getGridpointForecast [REDACTED]?[REDACTED]", calls[0].Request)
	assert.Empty(t, calls[0].Path, "a redacted value must not resolve to a path")
}
