package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// errorCatalogStore is the test double that returns the supplied
// errors from each method. Used to exercise the admin handler's
// error branches without a real Postgres.
type errorCatalogStore struct {
	createErr   error
	getErr      error
	listErr     error
	updateErr   error
	deleteErr   error
	upsertErr   error
	getSpecErr  error
	listSpecErr error
	delSpecErr  error
	refErr      error

	catalogs map[string]apicatalog.Catalog
	specs    map[string]map[string]apicatalog.SpecEntry
}

func newErrorStore() *errorCatalogStore {
	return &errorCatalogStore{
		catalogs: map[string]apicatalog.Catalog{},
		specs:    map[string]map[string]apicatalog.SpecEntry{},
	}
}

func (s *errorCatalogStore) CreateCatalog(_ context.Context, c apicatalog.Catalog) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.catalogs[c.ID] = c
	return nil
}

func (s *errorCatalogStore) GetCatalog(_ context.Context, id string) (*apicatalog.Catalog, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	c, ok := s.catalogs[id]
	if !ok {
		return nil, apicatalog.ErrNotFound
	}
	return &c, nil
}

func (s *errorCatalogStore) ListCatalogs(_ context.Context) ([]apicatalog.Catalog, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return nil, nil
}

func (s *errorCatalogStore) UpdateCatalog(_ context.Context, _ string, _ apicatalog.Update) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	return nil
}

func (s *errorCatalogStore) DeleteCatalog(_ context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.catalogs, id)
	return nil
}

func (s *errorCatalogStore) UpsertSpec(_ context.Context, catalogID string, spec apicatalog.SpecEntry) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	if _, ok := s.specs[catalogID]; !ok {
		s.specs[catalogID] = map[string]apicatalog.SpecEntry{}
	}
	s.specs[catalogID][spec.SpecName] = spec
	return nil
}

func (s *errorCatalogStore) GetSpec(_ context.Context, catalogID, specName string) (*apicatalog.SpecEntry, error) {
	if s.getSpecErr != nil {
		return nil, s.getSpecErr
	}
	bucket, ok := s.specs[catalogID]
	if !ok {
		return nil, apicatalog.ErrNotFound
	}
	sp, ok := bucket[specName]
	if !ok {
		return nil, apicatalog.ErrNotFound
	}
	return &sp, nil
}

func (s *errorCatalogStore) ListSpecs(_ context.Context, catalogID string) ([]apicatalog.SpecEntry, error) {
	if s.listSpecErr != nil {
		return nil, s.listSpecErr
	}
	out := []apicatalog.SpecEntry{}
	for _, v := range s.specs[catalogID] {
		out = append(out, v)
	}
	return out, nil
}

func (s *errorCatalogStore) DeleteSpec(_ context.Context, catalogID, specName string) error {
	if s.delSpecErr != nil {
		return s.delSpecErr
	}
	if bucket, ok := s.specs[catalogID]; ok {
		delete(bucket, specName)
	}
	return nil
}

func (s *errorCatalogStore) ReferencingConnections(_ context.Context, _ string) ([]apicatalog.ConnectionRef, error) {
	if s.refErr != nil {
		return nil, s.refErr
	}
	return nil, nil
}

func (*errorCatalogStore) UpsertOperationEmbeddings(_ context.Context, _, _ string, _ []apicatalog.OperationEmbedding) error {
	return nil
}

func (*errorCatalogStore) ListOperationEmbeddings(_ context.Context, _, _ string) ([]apicatalog.OperationEmbedding, error) {
	return nil, nil
}

func (*errorCatalogStore) DeleteOperationEmbeddings(_ context.Context, _, _ string) error {
	return nil
}

func handlerWithStore(store APICatalogStore) *Handler {
	return NewHandler(Deps{
		APICatalogStore:   store,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
}

func TestCatalog_ListInternalError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.listErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_GetInternalError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.getErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/p", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_CreateInternalError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.createErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "p", "name": "p", "display_name": "P",
	})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_CreateBadJSON(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	h := handlerWithStore(store)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/api-catalogs", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d", w.Code)
	}
}

func TestCatalog_UpdateNotFound(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.updateErr = apicatalog.ErrNotFound
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p", map[string]any{
		"display_name": "X",
	})
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404: %d", res.Code)
	}
}

func TestCatalog_UpdateConflict(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.updateErr = apicatalog.ErrConflict
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p", map[string]any{
		"display_name": "X",
	})
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409: %d", res.Code)
	}
}

func TestCatalog_UpdateInternalError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.updateErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p", map[string]any{
		"display_name": "X",
	})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_UpdateBadJSON(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	h := handlerWithStore(store)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/api-catalogs/p", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d", w.Code)
	}
}

func TestCatalog_DeleteRefsBlock(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.refErr = nil
	// Stub ReferencingConnections to return a non-empty list.
	stub := &refStubStore{errorCatalogStore: store, refs: []apicatalog.ConnectionRef{
		{Kind: "api", Name: "prod"},
	}}
	h := handlerWithStore(stub)
	res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/p", nil)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409: %d", res.Code)
	}
}

type refStubStore struct {
	*errorCatalogStore
	refs []apicatalog.ConnectionRef
}

func (r *refStubStore) ReferencingConnections(_ context.Context, _ string) ([]apicatalog.ConnectionRef, error) {
	return r.refs, nil
}

func TestCatalog_DeleteRefLookupError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.refErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/p", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_DeleteInternalError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.deleteErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/p", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_CloneBadJSON(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	h := handlerWithStore(store)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/api-catalogs/src/clone", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d", w.Code)
	}
}

func TestCatalog_CloneSourceFetchError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.getErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/src/clone",
		map[string]any{"id": "dst", "name": "x"})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_CloneDstInvalidID(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.catalogs["src"] = apicatalog.Catalog{ID: "src", Name: "n", DisplayName: "N"}
	store.createErr = apicatalog.ErrInvalidID
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/src/clone",
		map[string]any{"id": "BAD"})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d", res.Code)
	}
}

func TestCatalog_CloneDstConflict(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.catalogs["src"] = apicatalog.Catalog{ID: "src", Name: "n", DisplayName: "N"}
	store.createErr = apicatalog.ErrConflict
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/src/clone",
		map[string]any{"id": "dst"})
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409: %d", res.Code)
	}
}

func TestCatalog_CloneDstInternalError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.catalogs["src"] = apicatalog.Catalog{ID: "src", Name: "n", DisplayName: "N"}
	store.createErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/src/clone",
		map[string]any{"id": "dst"})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_CloneListSpecsError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.catalogs["src"] = apicatalog.Catalog{ID: "src", Name: "n", DisplayName: "N"}
	store.listSpecErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/src/clone",
		map[string]any{"id": "dst"})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_CloneCopyUpsertError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.catalogs["src"] = apicatalog.Catalog{ID: "src", Name: "n", DisplayName: "N"}
	store.specs["src"] = map[string]apicatalog.SpecEntry{
		"default": {SpecName: "default", Content: "x", SourceKind: apicatalog.SourceInline},
	}
	store.upsertErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/src/clone",
		map[string]any{"id": "dst"})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_ListSpecsRouteHappy(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.catalogs["p"] = apicatalog.Catalog{ID: "p", Name: "p", DisplayName: "P"}
	store.specs["p"] = map[string]apicatalog.SpecEntry{
		"default": {SpecName: "default", Content: "x", SourceKind: apicatalog.SourceInline},
	}
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/p/specs", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200: %d %s", res.Code, res.Body.String())
	}
	var out specListResponse
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if len(out.Specs) != 1 {
		t.Errorf("specs=%+v", out.Specs)
	}
}

func TestCatalog_ListSpecsRouteError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.listSpecErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/p/specs", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_GetSpecInternalError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.getSpecErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/p/specs/default", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_UpsertSpecBadJSON(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	h := handlerWithStore(store)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/api-catalogs/p/specs/default", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d", w.Code)
	}
}

func TestCatalog_UpsertSpecValidateFails(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default",
		map[string]any{
			"source_kind": "inline",
			"content":     "this is not openapi",
		})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d %s", res.Code, res.Body.String())
	}
}

func TestCatalog_UpsertSpecUploadKindRejected(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default",
		map[string]any{
			"source_kind": "upload",
			"content":     "anything",
		})
	if res.Code == http.StatusOK {
		t.Fatal("upload source_kind should be rejected on inline route")
	}
}

func TestCatalog_RefreshGetSpecNotFound(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPost,
		"/api/v1/admin/api-catalogs/p/specs/default/refresh", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404: %d", res.Code)
	}
}

func TestCatalog_RefreshGetSpecError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.getSpecErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodPost,
		"/api/v1/admin/api-catalogs/p/specs/default/refresh", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_DeleteSpecInternalError(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	store.delSpecErr = errors.New("boom")
	h := handlerWithStore(store)
	res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/p/specs/default", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500: %d", res.Code)
	}
}

func TestCatalog_UploadInvalidMultipart(t *testing.T) {
	t.Parallel()
	store := newErrorStore()
	h := handlerWithStore(store)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/api-catalogs/p/specs/x/upload", bytes.NewReader([]byte("not multipart")))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=zzz")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400: %d", w.Code)
	}
}

func TestSpecErrorStatus_AllBranches(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	cases := []struct {
		err  error
		want int
	}{
		{apicatalog.ErrNotFound, http.StatusNotFound},
		{apicatalog.ErrInvalidSpecName, http.StatusBadRequest},
		{apicatalog.ErrSSRFBlocked, http.StatusBadRequest},
		{apicatalog.ErrUpstream, http.StatusBadGateway},
		{apicatalog.ErrTooLarge, http.StatusRequestEntityTooLarge},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		got := h.specErrorStatus(c.err)
		if got != c.want {
			t.Errorf("err=%v got=%d want=%d", c.err, got, c.want)
		}
	}
}

func TestMaterializeSpec_URLRequiresURL(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	_, err := h.materializeSpec(context.Background(), "default", upsertCatalogSpecRequest{
		SourceKind: "url",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMaterializeSpec_InvalidKind(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	_, err := h.materializeSpec(context.Background(), "default", upsertCatalogSpecRequest{
		SourceKind: "bogus",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReloadConnectionsForCatalog_NoRegistry(_ *testing.T) {
	h := &Handler{}
	h.reloadConnectionsForCatalog("anything") // must not panic
}

func TestUserIDForAudit_NoUser(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	if got := userIDForAudit(req); got != "" {
		t.Errorf("got=%q want empty", got)
	}
}

func TestUserIDForAudit_UserIDFallback(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	ctx := context.WithValue(req.Context(), adminUserKey, &User{UserID: "u1"})
	req = req.WithContext(ctx)
	if got := userIDForAudit(req); got != "u1" {
		t.Errorf("got=%q want u1", got)
	}
}
