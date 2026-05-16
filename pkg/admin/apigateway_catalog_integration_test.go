package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// failingRemoveToolkit is a registry.Toolkit + toolkit.ConnectionManager
// whose RemoveConnection always fails. Used to cover the error
// branch in hotAddConnection where the in-memory remove-before-readd
// step rejects the operation, so the handler must NOT proceed to
// AddConnection and leave the live state in an inconsistent half-
// applied form.
type failingRemoveToolkit struct {
	addCalls int
}

func (*failingRemoveToolkit) Kind() string                            { return "api" }
func (*failingRemoveToolkit) Name() string                            { return "api" }
func (*failingRemoveToolkit) Connection() string                      { return "" }
func (*failingRemoveToolkit) Tools() []string                         { return nil }
func (*failingRemoveToolkit) RegisterTools(_ *mcp.Server)             {}
func (*failingRemoveToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (*failingRemoveToolkit) SetQueryProvider(_ query.Provider)       {}
func (*failingRemoveToolkit) Close() error                            { return nil }
func (*failingRemoveToolkit) HasConnection(_ string) bool             { return true }
func (*failingRemoveToolkit) RemoveConnection(_ string) error {
	return errors.New("remove blocked by simulated failure")
}

func (f *failingRemoveToolkit) AddConnection(_ string, _ map[string]any) error {
	f.addCalls++
	return nil
}

var _ registry.Toolkit = (*failingRemoveToolkit)(nil)

// integrationSpec is an inline OpenAPI 3.0 document with two
// operations. The exact ops don't matter; the test asserts on
// presence of operations after a catalog is attached via UPDATE,
// not on operation content.
const integrationSpec = `
openapi: 3.0.3
info:
  title: Integration
  version: "1.0"
paths:
  /v1/things:
    get:
      operationId: listThings
      summary: List things
      responses:
        "200": {description: ok}
    post:
      operationId: createThing
      summary: Create a thing
      responses:
        "201": {description: created}
`

// TestUpdateConnectionAttachesCatalogToLiveToolkit covers the bug
// where attaching a catalog to an existing api-kind connection via
// the admin UI updated the DB but never reached the in-memory
// toolkit, so api_list_endpoints returned "no catalog_id configured"
// and list_connections showed no catalog binding until process
// restart. The unit test layer asserted only HTTP 200 from the
// admin PUT and missed this entirely.
//
// Setup wires the real apigateway.Toolkit, a real (memory-backed)
// catalog store shared between the toolkit and the admin handler,
// pre-registers an api connection without a catalog, and then PUTs
// a config with catalog_id set. The assertions verify that the live
// toolkit's connection picked up the new CatalogID AND populated
// OperationCount, which together prove the UPDATE path now reaches
// the runtime.
func TestUpdateConnectionAttachesCatalogToLiveToolkit(t *testing.T) {
	ctx := context.Background()

	// One catalog store, shared by the toolkit (reads spec content at
	// connection build time) and the admin handler (validates that
	// catalog_id exists before persisting).
	catStore := apicatalog.NewMemoryStore()
	require.NoError(t, catStore.CreateCatalog(ctx, apicatalog.Catalog{
		ID: "things", Name: "things", DisplayName: "Things",
	}))
	require.NoError(t, catStore.UpsertSpec(ctx, "things", apicatalog.SpecEntry{
		SpecName:   "default",
		Content:    integrationSpec,
		SourceKind: apicatalog.SourceInline,
	}))

	tk := apigateway.New("apigateway")
	tk.SetCatalogStore(catStore)

	// Pre-seed the toolkit with the api connection in its no-catalog
	// state. This is the live-runtime precondition for the UPDATE
	// path: a connection already exists, the user attaches a catalog
	// to it from the admin UI. Without the fix, hotAddConnection
	// silently no-ops here because HasConnection returns true.
	require.NoError(t, tk.AddConnection("acme", map[string]any{
		"base_url": "https://api.example.com",
	}))

	before := tk.ListConnections()
	require.Len(t, before, 1)
	require.Empty(t, before[0].CatalogID,
		"precondition: connection should start with no catalog bound")
	require.Zero(t, before[0].OperationCount,
		"precondition: no operations until catalog is attached")

	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	store := &mockConnectionStore{}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
		APICatalogStore: catStore,
	}, nil)

	body := `{"config":{"base_url":"https://api.example.com","catalog_id":"things"},"description":"acme api"}`
	req := httptest.NewRequestWithContext(ctx, http.MethodPut,
		"/api/v1/admin/connection-instances/api/acme", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "PUT response body: %s", w.Body.String())

	after := tk.ListConnections()
	require.Len(t, after, 1)
	assert.Equal(t, "things", after[0].CatalogID,
		"the live toolkit must reflect the catalog binding after UPDATE; "+
			"if this fails, hotAddConnection regressed to fail-silent on existing connections")
	assert.Equal(t, 2, after[0].OperationCount,
		"operations from the catalog's spec must be loaded into the live "+
			"connection; OperationCount==0 means the catalog reference "+
			"was accepted but specs were never resolved")
}

// TestUpdateConnectionChangesCatalogOnLiveToolkit is the
// catalog-swap variant: an existing connection is bound to catalog
// A and the operator attaches catalog B instead. The live toolkit
// must replace, not retain, the original binding.
func TestUpdateConnectionChangesCatalogOnLiveToolkit(t *testing.T) {
	ctx := context.Background()

	catStore := apicatalog.NewMemoryStore()
	for _, id := range []string{"cat-a", "cat-b"} {
		require.NoError(t, catStore.CreateCatalog(ctx, apicatalog.Catalog{
			ID: id, Name: id, DisplayName: id,
		}))
		require.NoError(t, catStore.UpsertSpec(ctx, id, apicatalog.SpecEntry{
			SpecName: "default", Content: integrationSpec,
			SourceKind: apicatalog.SourceInline,
		}))
	}

	tk := apigateway.New("apigateway")
	tk.SetCatalogStore(catStore)
	require.NoError(t, tk.AddConnection("acme", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "cat-a",
	}))
	require.Equal(t, "cat-a", tk.ListConnections()[0].CatalogID)

	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
		APICatalogStore: catStore,
	}, nil)

	body := `{"config":{"base_url":"https://api.example.com","catalog_id":"cat-b"},"description":""}`
	req := httptest.NewRequestWithContext(ctx, http.MethodPut,
		"/api/v1/admin/connection-instances/api/acme", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "PUT response body: %s", w.Body.String())

	after := tk.ListConnections()
	require.Len(t, after, 1)
	assert.Equal(t, "cat-b", after[0].CatalogID,
		"swap must replace the live binding, not retain the prior catalog")
}

// TestUpdateConnectionDetachesCatalogOnLiveToolkit covers the
// inverse path: a connection bound to a catalog has its catalog
// cleared from the admin UI. The live toolkit must drop the
// operations index, not retain stale ops from the prior binding.
func TestUpdateConnectionDetachesCatalogOnLiveToolkit(t *testing.T) {
	ctx := context.Background()

	catStore := apicatalog.NewMemoryStore()
	require.NoError(t, catStore.CreateCatalog(ctx, apicatalog.Catalog{
		ID: "things", Name: "things", DisplayName: "Things",
	}))
	require.NoError(t, catStore.UpsertSpec(ctx, "things", apicatalog.SpecEntry{
		SpecName: "default", Content: integrationSpec,
		SourceKind: apicatalog.SourceInline,
	}))

	tk := apigateway.New("apigateway")
	tk.SetCatalogStore(catStore)
	require.NoError(t, tk.AddConnection("acme", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "things",
	}))
	require.Equal(t, 2, tk.ListConnections()[0].OperationCount)

	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
		APICatalogStore: catStore,
	}, nil)

	body := `{"config":{"base_url":"https://api.example.com"},"description":""}`
	req := httptest.NewRequestWithContext(ctx, http.MethodPut,
		"/api/v1/admin/connection-instances/api/acme", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "PUT response body: %s", w.Body.String())

	after := tk.ListConnections()
	require.Len(t, after, 1)
	assert.Empty(t, after[0].CatalogID, "detach must clear the live binding")
	assert.Zero(t, after[0].OperationCount, "detach must drop the cached ops")
}

// TestUpdateConnectionPersistsStaticHeaders proves the admin PUT
// path actually saves config.static_headers and that the same
// remove-then-add fix that delivers catalog_id to the live toolkit
// also delivers static_headers. Two assertions, both load-bearing:
//
//  1. mockConnectionStore.Set received an instance whose
//     Config["static_headers"] contains the operator's entry,
//     proving the field survived JSON decode, the redaction-merge
//     branch, and the validator. (Counters the "not even getting
//     saved" report.)
//  2. The live apigateway.Toolkit's connection registers without
//     error after the PUT, which exercises the post-hotAddConnection
//     code path under the same fix.
//
// Without the hotAddConnection fix this test still demonstrates the
// save half. The runtime half is covered separately by the
// catalog-binding tests above, which share the same code path.
func TestUpdateConnectionPersistsStaticHeaders(t *testing.T) {
	ctx := context.Background()

	tk := apigateway.New("apigateway")
	require.NoError(t, tk.AddConnection("acme", map[string]any{
		"base_url": "https://api.example.com",
	}))

	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	store := &mockConnectionStore{}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)

	body := `{
		"config": {
			"base_url": "https://api.example.com",
			"static_headers": {"X-Test-Header": "test-value"}
		},
		"description": ""
	}`
	req := httptest.NewRequestWithContext(ctx, http.MethodPut,
		"/api/v1/admin/connection-instances/api/acme", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "PUT response body: %s", w.Body.String())

	require.Len(t, store.setCalls, 1, "ConnectionStore.Set must be invoked exactly once")
	saved := store.setCalls[0].Config
	require.Contains(t, saved, "static_headers",
		"static_headers must reach the store; absence indicates the field "+
			"was stripped between HTTP decode and persistence")

	headers, ok := saved["static_headers"].(map[string]any)
	require.True(t, ok,
		"static_headers must be a map[string]any after JSON decode; got %T", saved["static_headers"])
	assert.Equal(t, "test-value", headers["X-Test-Header"],
		"header value must round-trip through the handler exactly as submitted")

	after := tk.ListConnections()
	require.Len(t, after, 1, "the live toolkit must still have the connection")
	assert.Equal(t, "acme", after[0].Name)
}

// TestStaticHeadersRoundtripThroughEffectiveEndpoint is the
// PUT-then-GET test that proves what the operator sees after saving
// static_headers. The screenshot report on 2026-05-15 said headers
// "weren't even getting saved"; this exercises the same flow the UI
// uses (PUT to set, GET /effective to re-render) and asserts the
// saved headers survive the round-trip with their NAMES intact and
// values masked as [REDACTED] (operators recognize the round-trip,
// they don't expect to see the plaintext value).
//
// If a future regression strips static_headers between PUT and
// GET (a redaction bug, a serializer omission, a stale connection
// store on the list path), this test will fail loudly.
func TestStaticHeadersRoundtripThroughEffectiveEndpoint(t *testing.T) {
	ctx := context.Background()

	tk := apigateway.New("apigateway")
	require.NoError(t, tk.AddConnection("acme", map[string]any{
		"base_url": "https://api.example.com",
	}))

	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	store := &mockConnectionStore{}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: store,
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)

	// PUT: add a static header to the connection.
	putBody := `{
		"config": {
			"base_url": "https://api.example.com",
			"static_headers": {"X-Quota-Project": "acme-prod-billing"}
		},
		"description": ""
	}`
	putReq := httptest.NewRequestWithContext(ctx, http.MethodPut,
		"/api/v1/admin/connection-instances/api/acme", strings.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	h.ServeHTTP(putRec, putReq)
	require.Equal(t, http.StatusOK, putRec.Code, "PUT body: %s", putRec.Body.String())

	// Simulate the post-save state of the DB by reflecting the most
	// recent Set into the mock's List path. This mirrors what the
	// real PostgresConnectionStore would do.
	require.NotEmpty(t, store.setCalls)
	store.instances = []platform.ConnectionInstance{store.setCalls[len(store.setCalls)-1]}

	// GET /effective: the same query the UI invalidates and refetches
	// after a save.
	getReq := httptest.NewRequestWithContext(ctx, http.MethodGet,
		"/api/v1/admin/connection-instances/effective", http.NoBody)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code, "GET body: %s", getRec.Body.String())

	var effective []map[string]any
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &effective))

	var acme map[string]any
	for _, e := range effective {
		if e["kind"] == "api" && e["name"] == "acme" {
			acme = e
			break
		}
	}
	require.NotNil(t, acme, "acme api connection must be present in /effective response; got %#v", effective)

	cfg, ok := acme["config"].(map[string]any)
	require.True(t, ok, "config must be a map in the response; got %T", acme["config"])

	headers, ok := cfg["static_headers"].(map[string]any)
	require.True(t, ok,
		"static_headers must survive the PUT→GET round-trip. If this fails, "+
			"the UI rightly reports headers as 'not getting saved' because "+
			"the redaction/merge layer is dropping them on the way back. "+
			"Got config=%#v", cfg)

	require.Contains(t, headers, "X-Quota-Project",
		"header NAME must be preserved (operators recognize names, not values)")
	assert.Equal(t, "[REDACTED]", headers["X-Quota-Project"],
		"header VALUE must be redacted in the response (encrypted at rest, "+
			"never returned in plaintext)")
}

// TestHotAddConnection_RemoveFailureAbortsAdd covers the safety
// branch: if the live toolkit cannot remove the existing connection,
// hotAddConnection must abort and NOT call AddConnection. The
// alternative would leave the toolkit briefly tracking two state
// histories for the same name and risk an ErrConnectionExists from
// AddConnection. The handler logs and bails.
func TestHotAddConnection_RemoveFailureAbortsAdd(t *testing.T) {
	tk := &failingRemoveToolkit{}
	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)

	h.hotAddConnection("api", "stuck", map[string]any{"base_url": "https://x"})

	assert.Zero(t, tk.addCalls,
		"AddConnection must not run after a failed RemoveConnection. "+
			"The live state stays as-is, the caller surfaces the failure "+
			"via the structured log")
}
