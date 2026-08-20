package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// fileDeclaredConfig returns a config whose file half declares the api-kind
// connection "acme", snapshotted the way the platform snapshots it: before
// anything from the connection store joins the instances map.
func fileDeclaredConfig(t *testing.T) *platform.Config {
	t.Helper()
	cfg := testConfig()
	cfg.Toolkits = map[string]any{
		"api": map[string]any{
			"enabled": true,
			"instances": map[string]any{
				"acme": map[string]any{"base_url": "https://api.example.com"},
			},
		},
	}
	cfg.SnapshotDeclaredConnections()
	return cfg
}

// TestDeleteRefusesAConnectionTheFileDeclares drives the real DELETE route
// against a real apigateway toolkit holding the connection, because the defect
// (#1400) is not visible in the response alone: the row delete, the live
// removal from every toolkit of the kind, and the peer broadcast each have to
// be shown not to have happened.
//
// The stored row is what makes this reachable at all — connbackfill seeds one
// for every file-configured connection — so the store is given the instance
// and still must not be asked to delete it.
func TestDeleteRefusesAConnectionTheFileDeclares(t *testing.T) {
	tk := apigateway.New("apigateway")
	require.NoError(t, tk.AddConnection("acme", map[string]any{
		"base_url": "https://api.example.com",
	}))

	store := &mockConnectionStore{instances: []platform.ConnectionInstance{
		{Kind: "api", Name: "acme", CreatedBy: "system"},
	}}
	notifier := &fakeReloadNotifier{}
	h := NewHandler(Deps{
		Config:          fileDeclaredConfig(t),
		ConnectionStore: store,
		ConfigStore:     &mockConfigStore{mode: "database"},
		ToolkitRegistry: &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}},
		ReloadNotifier:  notifier,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/admin/connection-instances/api/acme", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	pd := decodeProblem(w.Body.Bytes())
	assert.Contains(t, pd.Detail, "configuration file",
		"the refusal has to name the file as the connection's owner")

	assert.Empty(t, store.deleteCalls, "the stored row survives a refused delete")
	assert.True(t, tk.HasConnection("acme"),
		"the connection keeps serving on the replica that refused")
	assert.Empty(t, notifier.conns,
		"no peer is told to drop a connection this replica did not drop")
}

// TestSaveRefusesAConnectionTheFileDeclares covers the write half of the same
// ownership rule. Saving a record for a file-declared connection used to
// succeed, hot-add it onto the live toolkits, and then quietly come to nothing:
// mergeDBConnectionsIntoConfig skips a name the file already declares, so the
// next restart brought back the file's config and left the saved record
// describing a state nothing was running.
func TestSaveRefusesAConnectionTheFileDeclares(t *testing.T) {
	tk := apigateway.New("apigateway")
	require.NoError(t, tk.AddConnection("acme", map[string]any{
		"base_url": "https://api.example.com",
	}))

	store := &mockConnectionStore{}
	notifier := &fakeReloadNotifier{}
	h := NewHandler(Deps{
		Config:          fileDeclaredConfig(t),
		ConnectionStore: store,
		ConfigStore:     &mockConfigStore{mode: "database"},
		ToolkitRegistry: &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}},
		ReloadNotifier:  notifier,
	}, nil)

	body := `{"config":{"base_url":"https://overridden.example.com"},"description":"override"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/connection-instances/api/acme", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	pd := decodeProblem(w.Body.Bytes())
	assert.Contains(t, pd.Detail, "configuration file",
		"the refusal has to name the file as the connection's owner")

	assert.Empty(t, store.setCalls, "no record is written for a connection the file owns")
	assert.Empty(t, notifier.conns, "no peer is told to reconcile a connection nothing changed")

	live := tk.ListConnections()
	require.Len(t, live, 1)
	assert.Equal(t, "acme", live[0].Name,
		"the live connection keeps the config the file gave it")
}

// TestSaveStoresAConnectionTheFileDoesNotDeclare is the other half: the
// refusal must not reach the admin UI's own connections, which have no file
// entry and are the only ones a stored record ever governed.
func TestSaveStoresAConnectionTheFileDoesNotDeclare(t *testing.T) {
	tk := apigateway.New("apigateway")
	store := &mockConnectionStore{}
	h := NewHandler(Deps{
		// The file declares "acme", not "adhoc".
		Config:          fileDeclaredConfig(t),
		ConnectionStore: store,
		ConfigStore:     &mockConfigStore{mode: "database"},
		ToolkitRegistry: &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}},
		ReloadNotifier:  &fakeReloadNotifier{},
	}, nil)

	body := `{"config":{"base_url":"https://adhoc.example.com"},"description":"ad hoc"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/connection-instances/api/adhoc", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, store.setCalls, 1)
	assert.Equal(t, "adhoc", store.setCalls[0].Name)
	assert.True(t, tk.HasConnection("adhoc"), "the save still reaches the live toolkit")
}

// TestDeleteRemovesAConnectionTheFileDoesNotDeclare is the other half of the
// same rule: the refusal must be scoped to what the file owns, or it would
// break the admin UI's only way to remove a connection it created.
func TestDeleteRemovesAConnectionTheFileDoesNotDeclare(t *testing.T) {
	tk := apigateway.New("apigateway")
	require.NoError(t, tk.AddConnection("adhoc", map[string]any{
		"base_url": "https://api.example.com",
	}))

	store := &mockConnectionStore{}
	notifier := &fakeReloadNotifier{}
	h := NewHandler(Deps{
		// The file declares "acme", not "adhoc".
		Config:          fileDeclaredConfig(t),
		ConnectionStore: store,
		ConfigStore:     &mockConfigStore{mode: "database"},
		ToolkitRegistry: &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}},
		ReloadNotifier:  notifier,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/admin/connection-instances/api/adhoc", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, []string{"api/adhoc"}, store.deleteCalls)
	assert.False(t, tk.HasConnection("adhoc"), "a stored connection is still hot-removed")
	assert.Equal(t, []connBroadcast{{kind: "api", name: "adhoc", op: platform.ReloadDelete.String()}},
		notifier.conns, "peers are still told about it")
}

// TestEffectiveConnectionsReportFileDeclared covers what the portal reads to
// withhold the delete affordance. Source cannot answer it: the backfill gives a
// file-configured connection a stored row, so it reports "both" exactly as a
// database connection the file also names would.
func TestEffectiveConnectionsReportFileDeclared(t *testing.T) {
	reg := &mockToolkitRegistry{allResult: []mockToolkit{
		{kind: "api", name: "acme", connection: "acme"},
		{kind: "api", name: "adhoc", connection: "adhoc"},
	}}
	store := &mockConnectionStore{instances: []platform.ConnectionInstance{
		{Kind: "api", Name: "acme", CreatedBy: "system"},
		{Kind: "api", Name: "adhoc", CreatedBy: "sarah@example.com"},
	}}
	h := NewHandler(Deps{
		Config:          fileDeclaredConfig(t),
		ConnectionStore: store,
		ConfigStore:     &mockConfigStore{mode: "database"},
		ToolkitRegistry: reg,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/connection-instances/effective", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var got []effectiveConnection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	byName := map[string]effectiveConnection{}
	for _, c := range got {
		byName[c.Name] = c
	}
	require.Len(t, byName, 2)

	assert.Equal(t, platform.SourceBoth, byName["acme"].Source,
		"precondition: the backfilled row makes a file connection read as both")
	assert.True(t, byName["acme"].FileDeclared)

	assert.Equal(t, platform.SourceBoth, byName["adhoc"].Source,
		"precondition: a stored connection its toolkit serves reads as both too")
	assert.False(t, byName["adhoc"].FileDeclared,
		"source cannot separate these two; file_declared has to")
}

// TestDeclaredConnectionsSnapshotPredatesTheMerge pins the ordering the whole
// fix rests on. A snapshot taken after a stored connection is merged would
// claim it for the file and make it undeletable forever.
func TestDeclaredConnectionsSnapshotPredatesTheMerge(t *testing.T) {
	cfg := fileDeclaredConfig(t)

	// What mergeDBConnectionsIntoConfig does to the same map afterwards.
	kindMap, ok := cfg.Toolkits["api"].(map[string]any)
	require.True(t, ok)
	instances, ok := kindMap["instances"].(map[string]any)
	require.True(t, ok)
	instances["adhoc"] = map[string]any{"base_url": "https://adhoc.example.com"}

	assert.True(t, cfg.DeclaresConnection("api", "acme"))
	assert.False(t, cfg.DeclaresConnection("api", "adhoc"),
		"a connection merged in after the snapshot is not the file's")
	assert.False(t, cfg.DeclaresConnection("s3", "acme"),
		"the declaration is per kind, not a bare name match")
}

// TestFileDeclaresConnectionWithoutAConfig covers the wiring a test or embedded
// caller can produce: no Config at all leaves every connection deletable, which
// is the behavior that shipped before this rule existed.
func TestFileDeclaresConnectionWithoutAConfig(t *testing.T) {
	assert.False(t, NewHandler(Deps{}, nil).fileDeclaresConnection("api", "acme"))
	assert.False(t, NewHandler(Deps{Config: testConfig()}, nil).fileDeclaresConnection("api", "acme"),
		"a config that never snapshotted declares nothing")
}
