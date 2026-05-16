package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// newCatalogTestHandler wires an in-memory catalog store and turns
// the admin handler into mutable mode (the routes are gated by
// isMutable). Returns the store so tests can seed data directly.
func newCatalogTestHandler(t *testing.T) (*Handler, *apicatalog.MemoryStore) {
	t.Helper()
	store := apicatalog.NewMemoryStore()
	h := NewHandler(Deps{
		APICatalogStore:   store,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	return h, store
}

func doJSON(t *testing.T, h *Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rc io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rc = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, rc)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCatalog_CreateAndList(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "petstore-1", "name": "petstore", "version": "1",
		"display_name": "Petstore", "description": "demo",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", res.Code, res.Body.String())
	}
	res = doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("list: %d %s", res.Code, res.Body.String())
	}
	var list []catalogResponse
	_ = json.Unmarshal(res.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != "petstore-1" {
		t.Errorf("unexpected list: %+v", list)
	}
}

func TestCatalog_CreateRequiresDisplayNameAndName(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "x", "name": "x",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("missing display_name: %d %s", res.Code, res.Body.String())
	}
	res = doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "x", "display_name": "X",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("missing name: %d %s", res.Code, res.Body.String())
	}
}

func TestCatalog_CreateInvalidID(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "BAD ID", "name": "x", "display_name": "X",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d %s", res.Code, res.Body.String())
	}
}

func TestCatalog_CreateConflict(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	body := map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	}
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", body)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", body)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409: %d %s", res.Code, res.Body.String())
	}
}

func TestCatalog_GetAndUpdate(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/p", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("get: %d", res.Code)
	}
	newDN := "Pretty"
	res = doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p", map[string]any{
		"display_name": newDN,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("update: %d %s", res.Code, res.Body.String())
	}
	var c catalogResponse
	_ = json.Unmarshal(res.Body.Bytes(), &c)
	if c.DisplayName != "Pretty" {
		t.Errorf("not updated: %+v", c)
	}
}

func TestCatalog_GetNotFound(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/ghost", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404: %d", res.Code)
	}
}

func TestCatalog_Delete(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/p", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("delete: %d", res.Code)
	}
	res = doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/p", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete: %d", res.Code)
	}
}

func TestCatalog_DeleteNotFound(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/ghost", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404: %d", res.Code)
	}
}

func TestCatalog_Clone(t *testing.T) {
	t.Parallel()
	h, store := newCatalogTestHandler(t)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "src", Name: "petstore", Version: "1", DisplayName: "Petstore",
	})
	_ = store.UpsertSpec(context.Background(), "src", apicatalog.SpecEntry{
		SpecName: "default", Content: "x", SourceKind: apicatalog.SourceInline,
		BasePath: "/v3",
	})
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/src/clone", map[string]any{
		"id": "dst", "name": "petstore", "version": "2",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("clone: %d %s", res.Code, res.Body.String())
	}
	specs, _ := store.ListSpecs(context.Background(), "dst")
	if len(specs) != 1 {
		t.Fatalf("clone did not copy specs: %+v", specs)
	}
	if specs[0].BasePath != "/v3" {
		t.Errorf("clone did not preserve BasePath; got %q, want %q", specs[0].BasePath, "/v3")
	}
}

func TestCatalog_CloneSourceMissing(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/ghost/clone", map[string]any{
		"id": "dst", "name": "x",
	})
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404: %d", res.Code)
	}
}

func TestCatalog_UpsertSpecInline(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     "openapi: 3.0.0\ninfo: {title: x, version: '1'}\npaths: {}\n",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", res.Code, res.Body.String())
	}
}

func TestCatalog_UpsertSpecRejectsInvalidSource(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default", map[string]any{
		"source_kind": "bogus",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d %s", res.Code, res.Body.String())
	}
}

func TestCatalog_UpsertSpecRejectsInlineWithoutContent(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default", map[string]any{
		"source_kind": "inline",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d %s", res.Code, res.Body.String())
	}
}

func TestCatalog_GetSpecNotFound(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/p/specs/ghost", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404: %d", res.Code)
	}
}

func TestCatalog_DeleteSpec(t *testing.T) {
	t.Parallel()
	h, store := newCatalogTestHandler(t)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	_ = store.UpsertSpec(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "default", Content: "x", SourceKind: apicatalog.SourceInline,
	})
	res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/p/specs/default", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("delete spec: %d", res.Code)
	}
}

func TestCatalog_DeleteSpecNotFound(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/p/specs/ghost", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404: %d", res.Code)
	}
}

func TestCatalog_RefreshRequiresURLSource(t *testing.T) {
	t.Parallel()
	h, store := newCatalogTestHandler(t)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	_ = store.UpsertSpec(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "default", Content: "x", SourceKind: apicatalog.SourceInline,
	})
	res := doJSON(t, h, http.MethodPost,
		"/api/v1/admin/api-catalogs/p/specs/default/refresh", nil)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d %s", res.Code, res.Body.String())
	}
}

func TestCatalog_UploadHappyPath(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("file", "spec.yaml")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("openapi: 3.0.0\ninfo: {title: x, version: '1'}\npaths: {}\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w.Close()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/api-catalogs/p/specs/uploaded/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCatalog_UploadMissingFile(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.Close()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/api-catalogs/p/specs/x/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d", rec.Code)
	}
}

func TestCatalog_RoutesDisabledWithoutStore(t *testing.T) {
	t.Parallel()
	h := NewHandler(Deps{DatabaseAvailable: true}, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without store: %d", res.Code)
	}
}

func TestCatalogResponse_HasRefAndSpecCounts(t *testing.T) {
	t.Parallel()
	h, store := newCatalogTestHandler(t)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	_ = store.UpsertSpec(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "default", Content: "x", SourceKind: apicatalog.SourceInline,
	})
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("list: %d", res.Code)
	}
	var list []catalogResponse
	_ = json.Unmarshal(res.Body.Bytes(), &list)
	if len(list) != 1 || list[0].SpecCount != 1 {
		t.Errorf("spec_count missing: %+v", list)
	}
}

func TestValidateConnectionCatalog_NonAPIKind(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
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
	h, _ := newCatalogTestHandler(t)
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
	h, _ := newCatalogTestHandler(t)
	msg, ok := h.validateConnectionCatalog(context.Background(), "api",
		map[string]any{})
	if !ok || msg != "" {
		t.Errorf("missing catalog_id should be fine: ok=%v msg=%q", ok, msg)
	}
}

func TestValidateConnectionCatalog_HappyPath(t *testing.T) {
	t.Parallel()
	h, store := newCatalogTestHandler(t)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	msg, ok := h.validateConnectionCatalog(context.Background(), "api",
		map[string]any{"catalog_id": "p"})
	if !ok || msg != "" {
		t.Errorf("happy path: ok=%v msg=%q", ok, msg)
	}
}

// TestCatalog_UpsertSpecInlineWithBasePath proves the operator-set
// per-spec BasePath round-trips through the JSON upsert path and
// shows up on the GET response.
func TestCatalog_UpsertSpecInlineWithBasePath(t *testing.T) {
	t.Parallel()
	h, store := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     "openapi: 3.0.0\ninfo: {title: x, version: '1'}\npaths: {}\n",
		"base_path":   "/v3",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", res.Code, res.Body.String())
	}
	saved, _ := store.GetSpec(context.Background(), "p", "default")
	if saved == nil || saved.BasePath != "/v3" {
		t.Fatalf("expected stored BasePath=/v3, got %+v", saved)
	}
}

// TestCatalog_UpsertSpecRejectsInvalidBasePath proves a malformed
// base_path is rejected with HTTP 400 (not 500), so operator input
// mistakes do not pollute alerts.
func TestCatalog_UpsertSpecRejectsInvalidBasePath(t *testing.T) {
	t.Parallel()
	h, _ := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     "openapi: 3.0.0\ninfo: {title: x, version: '1'}\npaths: {}\n",
		"base_path":   "v3", // missing leading slash
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid base_path, got %d %s", res.Code, res.Body.String())
	}
}

// TestCatalog_UploadWithBasePath proves the ?base_path= query
// parameter is honored on multipart uploads (the operator can set
// the prefix during the upload step instead of having to switch to
// the paste tab afterwards).
func TestCatalog_UploadWithBasePath(t *testing.T) {
	t.Parallel()
	h, store := newCatalogTestHandler(t)
	doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("file", "spec.yaml")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("openapi: 3.0.0\ninfo: {title: x, version: '1'}\npaths: {}\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w.Close()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/api-catalogs/p/specs/uploaded/upload?base_path=%2Fv1", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	saved, _ := store.GetSpec(context.Background(), "p", "uploaded")
	if saved == nil || saved.BasePath != "/v1" {
		t.Fatalf("expected uploaded spec BasePath=/v1, got %+v", saved)
	}
}

// TestCatalog_UploadPreservesExistingBasePath proves a re-upload
// without a ?base_path= query keeps the previously-stored value
// instead of zeroing it out.
func TestCatalog_UploadPreservesExistingBasePath(t *testing.T) {
	t.Parallel()
	h, store := newCatalogTestHandler(t)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	_ = store.UpsertSpec(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "uploaded", Content: "openapi: 3.0.0\ninfo: {title: x, version: '1'}\npaths: {}\n",
		SourceKind: apicatalog.SourceUpload, BasePath: "/preset/v1",
	})
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, _ := w.CreateFormFile("file", "spec.yaml")
	_, _ = part.Write([]byte("openapi: 3.0.0\ninfo: {title: x, version: '2'}\npaths: {}\n"))
	_ = w.Close()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/api-catalogs/p/specs/uploaded/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	saved, _ := store.GetSpec(context.Background(), "p", "uploaded")
	if saved == nil || saved.BasePath != "/preset/v1" {
		t.Fatalf("expected preserved BasePath=/preset/v1, got %+v", saved)
	}
}
