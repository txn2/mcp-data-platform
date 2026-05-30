package admin

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// fakeReloadNotifier records the cross-replica reload broadcasts the
// admin handler emits after a local config change.
type fakeReloadNotifier struct {
	catalogs []string
	conns    [][2]string
}

func (f *fakeReloadNotifier) PublishCatalogReload(id string) { f.catalogs = append(f.catalogs, id) }
func (f *fakeReloadNotifier) PublishConnectionReload(kind, name string) {
	f.conns = append(f.conns, [2]string{kind, name})
}

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
