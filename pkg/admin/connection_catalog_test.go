package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// newCatalogValidationHandler wires an in-memory catalog store onto the admin
// handler. validateConnectionCatalog stayed in this package when the catalog
// routes moved to internal/admin/catalogapi, because its only caller is the
// connection write path.
func newCatalogValidationHandler(t *testing.T) (*Handler, *apicatalog.MemoryStore) {
	t.Helper()
	store := apicatalog.NewMemoryStore()
	h := NewHandler(Deps{
		APICatalogStore:   store,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	return h, store
}

func TestValidateConnectionCatalog_NonAPIKind(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogValidationHandler(t)
	msg, ok := h.validateConnectionCatalog(context.Background(), "trino",
		map[string]any{"catalog_id": "missing"})
	if !ok || msg != "" {
		t.Errorf("non-api kind should bypass: ok=%v msg=%q", ok, msg)
	}
}

func TestValidateConnectionCatalog_NoStore(t *testing.T) {
	t.Parallel()
	h := NewHandler(Deps{}, nil)
	msg, ok := h.validateConnectionCatalog(context.Background(), "api",
		map[string]any{"catalog_id": "anything"})
	if !ok || msg != "" {
		t.Errorf("missing store should bypass: ok=%v msg=%q", ok, msg)
	}
}

func TestValidateConnectionCatalog_MissingCatalog(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogValidationHandler(t)
	msg, ok := h.validateConnectionCatalog(context.Background(), "api",
		map[string]any{"catalog_id": "ghost"})
	if ok {
		t.Fatal("expected validation failure")
	}
	if !strings.Contains(msg, "ghost") {
		t.Errorf("error should mention the missing id: %s", msg)
	}
}

func TestValidateConnectionCatalog_NoCatalogID(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogValidationHandler(t)
	msg, ok := h.validateConnectionCatalog(context.Background(), "api",
		map[string]any{})
	if !ok || msg != "" {
		t.Errorf("missing catalog_id should be fine: ok=%v msg=%q", ok, msg)
	}
}

func TestValidateConnectionCatalog_HappyPath(t *testing.T) {
	t.Parallel()
	h, store := newCatalogValidationHandler(t)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	msg, ok := h.validateConnectionCatalog(context.Background(), "api",
		map[string]any{"catalog_id": "p"})
	if !ok || msg != "" {
		t.Errorf("happy path: ok=%v msg=%q", ok, msg)
	}
}

// failingCatalogStore satisfies APICatalogStore and fails every catalog
// lookup. Only GetCatalog is exercised here — it is the single call
// validateConnectionCatalog makes — so the remaining methods are inert.
type failingCatalogStore struct{ getErr error }

func (s *failingCatalogStore) GetCatalog(context.Context, string) (*apicatalog.Catalog, error) {
	return nil, s.getErr
}
func (*failingCatalogStore) CreateCatalog(context.Context, apicatalog.Catalog) error { return nil }
func (*failingCatalogStore) ListCatalogs(context.Context) ([]apicatalog.Catalog, error) {
	return nil, nil
}

func (*failingCatalogStore) UpdateCatalog(context.Context, string, apicatalog.Update) error {
	return nil
}
func (*failingCatalogStore) DeleteCatalog(context.Context, string) error { return nil }
func (*failingCatalogStore) UpsertSpec(context.Context, string, apicatalog.SpecEntry) error {
	return nil
}

func (*failingCatalogStore) GetSpec(context.Context, string, string) (*apicatalog.SpecEntry, error) {
	// Model the real store's contract: a miss is ErrNotFound, never a nil
	// entry with a nil error.
	return nil, apicatalog.ErrNotFound
}

func (*failingCatalogStore) ListSpecs(context.Context, string) ([]apicatalog.SpecEntry, error) {
	return nil, nil
}
func (*failingCatalogStore) DeleteSpec(context.Context, string, string) error { return nil }
func (*failingCatalogStore) ReferencingConnections(context.Context, string) ([]apicatalog.ConnectionRef, error) {
	return nil, nil
}

func (*failingCatalogStore) UpsertOperationEmbeddings(context.Context, string, string, []apicatalog.OperationEmbedding) error {
	return nil
}

func (*failingCatalogStore) ListOperationEmbeddings(context.Context, string, string) ([]apicatalog.OperationEmbedding, error) {
	return nil, nil
}

func (*failingCatalogStore) DeleteOperationEmbeddings(context.Context, string, string) error {
	return nil
}
func (*failingCatalogStore) SetOperationCount(context.Context, string, string, int) error { return nil }

// TestSetConnectionInstance_CatalogLookupError drives
// validateConnectionCatalog into its non-ErrNotFound error branch: the
// catalog store returns a transient error (not ErrNotFound) while validating
// an api-kind connection's catalog_id. The handler must reject the write with
// 400 and the "failed to validate" detail rather than silently proceeding.
func TestSetConnectionInstance_CatalogLookupError(t *testing.T) {
	t.Parallel()
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ConfigStore:     &mockConfigStore{mode: "database"},
		APICatalogStore: &failingCatalogStore{getErr: errors.New("db timeout")},
	}, nil)
	body := `{"config":{"base_url":"https://x","catalog_id":"c1"},"description":""}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/connection-instances/api/c", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on catalog lookup error: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "failed to validate catalog_id") {
		t.Errorf("expected validate-failure detail, got: %s", w.Body.String())
	}
}
