package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// fakeReloadNotifier records the cross-replica reload broadcasts the
// admin handler emits after a local config change.
type fakeReloadNotifier struct {
	catalogs []string
	conns    [][2]string
	personas int
	apikeys  int
}

func (f *fakeReloadNotifier) PublishCatalogReload(id string) { f.catalogs = append(f.catalogs, id) }
func (f *fakeReloadNotifier) PublishConnectionReload(kind, name string) {
	f.conns = append(f.conns, [2]string{kind, name})
}
func (f *fakeReloadNotifier) PublishPersonaReload() { f.personas++ }
func (f *fakeReloadNotifier) PublishAPIKeyReload()  { f.apikeys++ }

// TestReloadNotifier_PublishedOnAdminChange proves the admin handler
// broadcasts a reload to peer replicas after each local config change
// (issue #501): a catalog spec edit broadcasts a catalog reload, and a
// connection add/remove broadcasts a connection reload.
func TestReloadNotifier_PublishedOnAdminChange(t *testing.T) {
	tk := apigateway.New("apigateway") // Kind() == "api"
	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	notifier := &fakeReloadNotifier{}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
		ReloadNotifier:  notifier,
	}, nil)

	h.reloadConnectionsForCatalog("things")
	h.hotAddConnection("api", "c1", map[string]any{"base_url": "https://x"})
	h.hotRemoveConnection("api", "c1")

	require.Equal(t, []string{"things"}, notifier.catalogs, "catalog reload should broadcast")
	require.Equal(t, [][2]string{{"api", "c1"}, {"api", "c1"}}, notifier.conns,
		"connection add and remove should each broadcast")
}

// TestReloadNotifier_PersonaBroadcast proves a persona create and delete
// each broadcast a persona reload to peer replicas (issue #501 authz gap).
func TestReloadNotifier_PersonaBroadcast(t *testing.T) {
	notifier := &fakeReloadNotifier{}
	pReg := &mockPersonaRegistry{allResult: testPersonas("admin", "analyst")}
	h := NewHandler(Deps{
		PersonaRegistry: pReg,
		Config:          testConfig(),
		ConfigStore:     &mockConfigStore{mode: "database"},
		ReloadNotifier:  notifier,
	}, nil)

	body := `{"name":"viewer","display_name":"Viewer","roles":["viewer"],"allow_tools":["trino_*"]}`
	createReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", strings.NewReader(body))
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	require.Equal(t, http.StatusCreated, createW.Code)

	updateBody := `{"display_name":"Senior Analyst","roles":["analyst","viewer"]}`
	updateReq := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/personas/analyst", strings.NewReader(updateBody))
	updateReq.SetPathValue("name", "analyst")
	updateW := httptest.NewRecorder()
	h.ServeHTTP(updateW, updateReq)
	require.Equal(t, http.StatusOK, updateW.Code)

	delReq := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/personas/analyst", http.NoBody)
	delReq.SetPathValue("name", "analyst")
	delW := httptest.NewRecorder()
	h.ServeHTTP(delW, delReq)
	require.Equal(t, http.StatusOK, delW.Code)

	assert.Equal(t, 3, notifier.personas, "persona create, update, and delete should each broadcast a persona reload")
}

// TestReloadNotifier_APIKeyBroadcast proves an API-key create and delete
// each broadcast an api-key reload to peer replicas (issue #501 authn gap).
func TestReloadNotifier_APIKeyBroadcast(t *testing.T) {
	notifier := &fakeReloadNotifier{}
	mgr := &mockAPIKeyManager{removeFn: func(_ string) bool { return true }}
	h := NewHandler(Deps{
		APIKeyManager:   mgr,
		PersonaRegistry: &mockPersonaRegistry{},
		Config:          testConfig(),
		ConfigStore:     &mockConfigStore{mode: "database"},
		ReloadNotifier:  notifier,
	}, nil)

	body := `{"name":"ci-key","roles":["analyst"]}`
	createReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/auth/keys", strings.NewReader(body))
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	require.Equal(t, http.StatusCreated, createW.Code)

	delReq := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/auth/keys/ci-key", http.NoBody)
	delReq.SetPathValue("name", "ci-key")
	delW := httptest.NewRecorder()
	h.ServeHTTP(delW, delReq)
	require.Equal(t, http.StatusOK, delW.Code)

	assert.Equal(t, 2, notifier.apikeys, "api-key create and delete should each broadcast an api-key reload")
}

// TestReloadNotifier_NilNotifierSafe proves a nil notifier (single-replica
// or unwired deployments) is a safe no-op.
func TestReloadNotifier_NilNotifierSafe(t *testing.T) {
	tk := apigateway.New("apigateway")
	reg := &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ToolkitRegistry: reg,
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)
	h.reloadConnectionsForCatalog("things")
	h.hotRemoveConnection("api", "c1")
	// Reaching here without a nil-pointer panic is the assertion.
	require.NotNil(t, h)
}
