package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// mockKnowledgePageStore is a configurable KnowledgePageStore + searcher for
// handler tests.
type mockKnowledgePageStore struct {
	page        *knowledgepage.Page
	pages       []knowledgepage.Page
	total       int
	versions    []knowledgepage.Version
	scored      []knowledgepage.ScoredPage
	getErr      error
	insertErr   error
	updateErr   error
	deleteErr   error
	listErr     error
	versionsErr error

	inserted *knowledgepage.Page
	updated  *knowledgepage.Update
	deleted  string
	// lastFilter records the most recent List filter, so a caller that derives it
	// from query parameters can be asserted on.
	lastFilter *knowledgepage.Filter

	refs            []knowledgepage.EntityRef
	refsErr         error
	validateRefsErr error

	referencingPages []knowledgepage.PageRef
	referencingErr   error
}

func (m *mockKnowledgePageStore) ListPagesReferencing(_ context.Context, _ knowledgepage.EntityRef) ([]knowledgepage.PageRef, error) {
	return m.referencingPages, m.referencingErr
}

func (m *mockKnowledgePageStore) Insert(_ context.Context, p knowledgepage.Page) error {
	m.inserted = &p
	return m.insertErr
}

func (m *mockKnowledgePageStore) Get(_ context.Context, _ string) (*knowledgepage.Page, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.page == nil {
		return nil, knowledgepage.ErrNotFound
	}
	return m.page, nil
}

func (m *mockKnowledgePageStore) GetBySlug(_ context.Context, slug string) (*knowledgepage.Page, error) {
	if m.page != nil && m.page.Slug == slug {
		return m.page, nil
	}
	return nil, knowledgepage.ErrNotFound
}

func (m *mockKnowledgePageStore) List(_ context.Context, f knowledgepage.Filter) ([]knowledgepage.Page, int, error) {
	m.lastFilter = &f
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	return m.pages, m.total, nil
}

func (m *mockKnowledgePageStore) Update(_ context.Context, _ string, u knowledgepage.Update) error {
	m.updated = &u
	return m.updateErr
}

func (m *mockKnowledgePageStore) SoftDelete(_ context.Context, id string) error {
	m.deleted = id
	return m.deleteErr
}

func (m *mockKnowledgePageStore) ListVersions(_ context.Context, _ string, _, _ int) ([]knowledgepage.Version, int, error) {
	if m.versionsErr != nil {
		return nil, 0, m.versionsErr
	}
	return m.versions, len(m.versions), nil
}

func (*mockKnowledgePageStore) GetVersion(_ context.Context, _ string, _ int) (*knowledgepage.Version, error) {
	return nil, knowledgepage.ErrNotFound
}

func (m *mockKnowledgePageStore) Search(_ context.Context, _ knowledgepage.SearchQuery) ([]knowledgepage.ScoredPage, error) {
	return m.scored, nil
}

// SemanticSearch backs the create-time dedup probe (#705); it returns the same
// canned scored pages, so a high score blocks a create in the handler tests.
func (m *mockKnowledgePageStore) SemanticSearch(_ context.Context, _ []float32, _ int) ([]knowledgepage.ScoredPage, error) {
	return m.scored, nil
}

func (m *mockKnowledgePageStore) ListEntityRefs(_ context.Context, _ string) ([]knowledgepage.EntityRef, error) {
	return m.refs, m.refsErr
}

func (m *mockKnowledgePageStore) ValidateRefTargets(_ context.Context, _ []knowledgepage.EntityRef) error {
	return m.validateRefsErr
}

func (m *mockKnowledgePageStore) FilterExistingRefTargets(_ context.Context, refs []knowledgepage.EntityRef) ([]knowledgepage.EntityRef, error) {
	return refs, m.validateRefsErr
}

func (m *mockKnowledgePageStore) AddEntityRefs(_ context.Context, _ string, refs []knowledgepage.EntityRef) error {
	if m.refsErr != nil {
		return m.refsErr
	}
	m.refs = append(m.refs, refs...)
	return nil
}

func (m *mockKnowledgePageStore) ReplaceEntityRefs(_ context.Context, _ string, refs []knowledgepage.EntityRef) error {
	if m.refsErr != nil {
		return m.refsErr
	}
	m.refs = append([]knowledgepage.EntityRef{}, refs...)
	return nil
}

func (m *mockKnowledgePageStore) ReplaceEntityRefsBySource(_ context.Context, _, source string, refs []knowledgepage.EntityRef) error {
	if m.refsErr != nil {
		return m.refsErr
	}
	kept := m.refs[:0:0]
	for _, r := range m.refs {
		if r.Source != source {
			kept = append(kept, r)
		}
	}
	for _, r := range refs {
		r.Source = source
		kept = append(kept, r)
	}
	m.refs = kept
	return nil
}

var (
	kpAdmin  = &User{UserID: "admin-1", Email: "admin@example.com", Roles: []string{"admin"}}
	kpViewer = &User{UserID: "viewer-1", Email: "viewer@example.com", Roles: []string{"analyst"}}
)

func newKnowledgePageHandler(store knowledgepage.Store, user *User) *Handler {
	deps := Deps{
		KnowledgePageStore: store,
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	return NewHandler(deps, testAuthMiddleware(user))
}

func doKP(h *Handler, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody)
	} else {
		r = httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestKnowledgePage_CreateRequiresApplyKnowledgeAccess(t *testing.T) {
	store := &mockKnowledgePageStore{}
	h := newKnowledgePageHandler(store, kpViewer)
	rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", `{"title":"X","body":"y"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin create = %d, want 403", rec.Code)
	}
	if store.inserted != nil {
		t.Error("store must not be written by a non-admin")
	}
}

func TestKnowledgePage_CreateSucceedsForAdmin(t *testing.T) {
	store := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Title: "Fiscal Calendar"}}
	h := newKnowledgePageHandler(store, kpAdmin)
	rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", `{"title":"Fiscal Calendar","body":"# FY","tags":["finance"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if store.inserted == nil || store.inserted.Title != "Fiscal Calendar" {
		t.Errorf("unexpected inserted page: %+v", store.inserted)
	}
	if store.inserted.CreatedBy != kpAdmin.Email {
		t.Errorf("CreatedBy = %q, want %q", store.inserted.CreatedBy, kpAdmin.Email)
	}
}

func TestKnowledgePage_CreateValidation(t *testing.T) {
	h := newKnowledgePageHandler(&mockKnowledgePageStore{}, kpAdmin)
	if rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", `{"title":"  "}`); rec.Code != http.StatusBadRequest {
		t.Errorf("blank title = %d, want 400", rec.Code)
	}
	if rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", "{bad json"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", rec.Code)
	}
}

func TestKnowledgePage_ListAndGetOpenToAllUsers(t *testing.T) {
	store := &mockKnowledgePageStore{
		pages: []knowledgepage.Page{{ID: "kp1", Title: "A"}},
		total: 1,
		page:  &knowledgepage.Page{ID: "kp1", Title: "A"},
	}
	h := newKnowledgePageHandler(store, kpViewer)

	rec := doKP(h, "GET", "/api/v1/portal/knowledge-pages", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200", rec.Code)
	}
	var list knowledgePageListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || len(list.Pages) != 1 {
		t.Errorf("unexpected list: %+v", list)
	}

	if rec := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1", ""); rec.Code != http.StatusOK {
		t.Errorf("get = %d, want 200", rec.Code)
	}
}

func TestKnowledgePage_GetNotFound(t *testing.T) {
	h := newKnowledgePageHandler(&mockKnowledgePageStore{page: nil}, kpViewer)
	if rec := doKP(h, "GET", "/api/v1/portal/knowledge-pages/missing", ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing get = %d, want 404", rec.Code)
	}
}

func TestKnowledgePage_GetDeletedIsNotFound(t *testing.T) {
	now := time.Now()
	store := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Title: "A", DeletedAt: &now}}
	h := newKnowledgePageHandler(store, kpViewer)
	if rec := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1", ""); rec.Code != http.StatusNotFound {
		t.Errorf("deleted get = %d, want 404", rec.Code)
	}
}

func TestKnowledgePage_UpdateGatedAndRouted(t *testing.T) {
	store := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Title: "A"}}
	if rec := doKP(newKnowledgePageHandler(store, kpViewer), "PUT", "/api/v1/portal/knowledge-pages/kp1", `{"title":"B"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin update = %d, want 403", rec.Code)
	}
	store2 := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Title: "B"}}
	if rec := doKP(newKnowledgePageHandler(store2, kpAdmin), "PUT", "/api/v1/portal/knowledge-pages/kp1", `{"title":"B","body":"z"}`); rec.Code != http.StatusOK {
		t.Fatalf("admin update = %d, want 200 (body: %s)", rec.Code, "")
	}
	if store2.updated == nil || store2.updated.UpdatedBy != kpAdmin.Email {
		t.Errorf("update not applied with editor identity: %+v", store2.updated)
	}
}

func TestKnowledgePage_UpdateNotFound(t *testing.T) {
	store := &mockKnowledgePageStore{updateErr: knowledgepage.ErrNotFound}
	h := newKnowledgePageHandler(store, kpAdmin)
	if rec := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/missing", `{"title":"B"}`); rec.Code != http.StatusNotFound {
		t.Errorf("update missing = %d, want 404", rec.Code)
	}
}

// A create aimed at a built-in page's slug must not be told to "update it
// instead" — that update 403s — so the 409 names the way forward (#1390).
func TestKnowledgePage_CreateOnBuiltinSlugNamesTheWayForward(t *testing.T) {
	store := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Slug: "platform-topic", Title: "Topic", Builtin: true}}
	h := newKnowledgePageHandler(store, kpAdmin)
	rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", `{"slug":"platform-topic","title":"Mine","body":"b"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("builtin slug create = %d, want 409", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "built-in") || !strings.Contains(body, "hide") {
		t.Errorf("409 body must name hide-and-create, got: %s", body)
	}
	if strings.Contains(body, "update it instead") {
		t.Errorf("409 body must not point at an update that would be refused: %s", body)
	}
}

// A built-in page's content is the release's, not the deployment's (#1390):
// the store's refusal must surface as a 403 carrying the reason, not a 500.
func TestKnowledgePage_UpdateBuiltinIsForbidden(t *testing.T) {
	store := &mockKnowledgePageStore{updateErr: knowledgepage.ErrBuiltinReadOnly}
	h := newKnowledgePageHandler(store, kpAdmin)
	rec := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/kp1", `{"title":"B"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("builtin update = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "built-in") {
		t.Errorf("403 body must say the page is built-in, got: %s", rec.Body.String())
	}
}

func TestKnowledgePage_DeleteGated(t *testing.T) {
	store := &mockKnowledgePageStore{}
	if rec := doKP(newKnowledgePageHandler(store, kpViewer), "DELETE", "/api/v1/portal/knowledge-pages/kp1", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin delete = %d, want 403", rec.Code)
	}
	if store.deleted != "" {
		t.Error("store must not be deleted by a non-admin")
	}
	store2 := &mockKnowledgePageStore{}
	if rec := doKP(newKnowledgePageHandler(store2, kpAdmin), "DELETE", "/api/v1/portal/knowledge-pages/kp1", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete = %d, want 204", rec.Code)
	}
	if store2.deleted != "kp1" {
		t.Errorf("deleted id = %q, want kp1", store2.deleted)
	}
}

func TestKnowledgePage_DeleteNotFound(t *testing.T) {
	store := &mockKnowledgePageStore{deleteErr: knowledgepage.ErrNotFound}
	h := newKnowledgePageHandler(store, kpAdmin)
	if rec := doKP(h, "DELETE", "/api/v1/portal/knowledge-pages/missing", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete missing = %d, want 404", rec.Code)
	}
}

func TestKnowledgePage_Versions(t *testing.T) {
	store := &mockKnowledgePageStore{versions: []knowledgepage.Version{{ID: "v1", Version: 1}}}
	h := newKnowledgePageHandler(store, kpViewer)
	if rec := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/versions", ""); rec.Code != http.StatusOK {
		t.Errorf("versions = %d, want 200", rec.Code)
	}
}

func TestKnowledgePage_Search(t *testing.T) {
	store := &mockKnowledgePageStore{scored: []knowledgepage.ScoredPage{{Page: knowledgepage.Page{ID: "kp1"}, Score: 0.9}}}
	h := newKnowledgePageHandler(store, kpViewer)
	if rec := doKP(h, "GET", "/api/v1/portal/knowledge-pages/search?q=fiscal", ""); rec.Code != http.StatusOK {
		t.Errorf("search = %d, want 200", rec.Code)
	}
	if rec := doKP(h, "GET", "/api/v1/portal/knowledge-pages/search", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("search without q = %d, want 400", rec.Code)
	}
}

func TestKnowledgePage_Unauthenticated(t *testing.T) {
	store := &mockKnowledgePageStore{pages: []knowledgepage.Page{}}
	h := newKnowledgePageHandler(store, nil)
	if rec := doKP(h, "GET", "/api/v1/portal/knowledge-pages", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth list = %d, want 401", rec.Code)
	}
}

// kpEmbedder is a stub embedding.Provider for exercising the embed path.
type kpEmbedder struct{ err error }

func (e kpEmbedder) Embed(context.Context, string) ([]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	return []float32{0.1, 0.2}, nil
}

// EmbedBatch returns one non-zero vector per input, as a real provider does. A
// zero-length or zero-valued vector is the noop provider's signature, which
// callers treat as "embedding unavailable", so a stub must not emit one.
func (e kpEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0.1, 0.2}
	}
	return out, nil
}
func (kpEmbedder) Dimension() int { return 2 }
func (kpEmbedder) Kind() string   { return "stub" }

func TestKnowledgePage_StoreErrorsReturn500(t *testing.T) {
	boom := errors.New("db down")
	cases := []struct {
		name, method, path, body string
		store                    *mockKnowledgePageStore
		user                     *User
	}{
		{"create", "POST", "/api/v1/portal/knowledge-pages", `{"title":"X"}`, &mockKnowledgePageStore{insertErr: boom}, kpAdmin},
		{"list", "GET", "/api/v1/portal/knowledge-pages", "", &mockKnowledgePageStore{listErr: boom}, kpViewer},
		{"get", "GET", "/api/v1/portal/knowledge-pages/kp1", "", &mockKnowledgePageStore{getErr: boom}, kpViewer},
		{"update", "PUT", "/api/v1/portal/knowledge-pages/kp1", `{"title":"X"}`, &mockKnowledgePageStore{updateErr: boom}, kpAdmin},
		{"delete", "DELETE", "/api/v1/portal/knowledge-pages/kp1", "", &mockKnowledgePageStore{deleteErr: boom}, kpAdmin},
		{"versions", "GET", "/api/v1/portal/knowledge-pages/kp1/versions", "", &mockKnowledgePageStore{versionsErr: boom}, kpViewer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newKnowledgePageHandler(tc.store, tc.user)
			rec := doKP(h, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("%s = %d, want 500", tc.name, rec.Code)
			}
		})
	}
}

func TestKnowledgePage_CreateUnauthenticated(t *testing.T) {
	h := newKnowledgePageHandler(&mockKnowledgePageStore{}, nil)
	if rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", `{"title":"X"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth create = %d, want 401", rec.Code)
	}
}

func TestKnowledgePage_SearchWithEmbedder(t *testing.T) {
	store := &mockKnowledgePageStore{scored: []knowledgepage.ScoredPage{{Page: knowledgepage.Page{ID: "kp1"}, Score: 0.9}}}
	deps := Deps{
		KnowledgePageStore: store,
		AdminRoles:         []string{"admin"},
		EmbeddingProvider:  kpEmbedder{},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpViewer))
	if rec := doKP(h, "GET", "/api/v1/portal/knowledge-pages/search?q=fiscal&limit=5", ""); rec.Code != http.StatusOK {
		t.Errorf("search w/ embedder = %d, want 200", rec.Code)
	}
}

// newGuardedKnowledgePageHandler builds a handler with the create-time dedup gate
// active: a real embedder and the given threshold.
func newGuardedKnowledgePageHandler(store knowledgepage.Store, user *User, threshold float64) *Handler {
	deps := Deps{
		KnowledgePageStore:          store,
		KnowledgePageDedupThreshold: threshold,
		EmbeddingProvider:           kpEmbedder{},
		AdminRoles:                  []string{"admin"},
		RateLimit:                   RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	return NewHandler(deps, testAuthMiddleware(user))
}

// TestKnowledgePage_CreateBlocksNearDuplicate proves the REST create path shares
// the dedup gate (#705): a create whose content is highly similar to an existing
// page is rejected with 409 and the candidate pages, and nothing is written.
func TestKnowledgePage_CreateBlocksNearDuplicate(t *testing.T) {
	store := &mockKnowledgePageStore{
		scored: []knowledgepage.ScoredPage{
			{Page: knowledgepage.Page{ID: "kp_existing", Slug: "return-policy", Title: "Return Policy"}, Score: 0.93},
		},
	}
	h := newGuardedKnowledgePageHandler(store, kpAdmin, 0.82)
	rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", `{"title":"ACME Returns Policy","body":"How returns work."}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("near-duplicate create = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp knowledgePageDuplicateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode 409 body: %v", err)
	}
	if !resp.DuplicateBlocked || len(resp.Candidates) != 1 || resp.Candidates[0].ID != "kp_existing" {
		t.Errorf("unexpected duplicate response: %+v", resp)
	}
	if store.inserted != nil {
		t.Error("blocked create must not write a page")
	}
}

// TestKnowledgePage_CreateForceNewBypassesGate proves force_new overrides the gate
// on the REST path: a near-duplicate is created (201) despite the high score.
func TestKnowledgePage_CreateForceNewBypassesGate(t *testing.T) {
	store := &mockKnowledgePageStore{
		page:   &knowledgepage.Page{ID: "kp_new", Title: "ACME Returns Policy"},
		scored: []knowledgepage.ScoredPage{{Page: knowledgepage.Page{ID: "kp_existing", Title: "Return Policy"}, Score: 0.99}},
	}
	h := newGuardedKnowledgePageHandler(store, kpAdmin, 0.82)
	rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", `{"title":"ACME Returns Policy","body":"x","force_new":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("force_new create = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if store.inserted == nil {
		t.Error("force_new must create the page despite the near-duplicate")
	}
}

// TestKnowledgePage_CreateGateInactiveWithoutEmbedder proves the gate degrades
// safely: with the threshold set but no real embedder, the create proceeds (the
// similarity score is not thresholdable).
func TestKnowledgePage_CreateGateInactiveWithoutEmbedder(t *testing.T) {
	store := &mockKnowledgePageStore{
		page:   &knowledgepage.Page{ID: "kp_new", Title: "X"},
		scored: []knowledgepage.ScoredPage{{Page: knowledgepage.Page{ID: "kp_existing", Title: "X"}, Score: 0.99}},
	}
	// Threshold set, but no EmbeddingProvider in deps.
	deps := Deps{
		KnowledgePageStore:          store,
		KnowledgePageDedupThreshold: 0.82,
		AdminRoles:                  []string{"admin"},
		RateLimit:                   RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpAdmin))
	rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", `{"title":"X","body":"y"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create without embedder = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestKnowledgePage_ListPagination(t *testing.T) {
	store := &mockKnowledgePageStore{pages: []knowledgepage.Page{}, total: 0}
	h := newKnowledgePageHandler(store, kpViewer)
	if rec := doKP(h, "GET", "/api/v1/portal/knowledge-pages?limit=5&offset=10&tag=finance&q=x", ""); rec.Code != http.StatusOK {
		t.Errorf("paginated list = %d, want 200", rec.Code)
	}
}

func TestValidateKnowledgePageAndNormalizeTags(t *testing.T) {
	if msg := validateKnowledgePage(knowledgePageRequest{Title: "ok"}); msg != "" {
		t.Errorf("valid page rejected: %q", msg)
	}
	if msg := validateKnowledgePage(knowledgePageRequest{Title: strings.Repeat("x", maxKnowledgePageTitleLen+1)}); msg == "" {
		t.Error("over-long title should be rejected")
	}
	if msg := validateKnowledgePage(knowledgePageRequest{Title: "ok", Tags: []string{strings.Repeat("t", maxKnowledgePageTagLen+1)}}); msg == "" {
		t.Error("over-long tag should be rejected")
	}
	got := normalizeTags([]string{" a ", "a", "", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("normalizeTags = %v, want [a b]", got)
	}
}

// The restore route is the way back from hiding a built-in page (#1390): gated
// like every page write, reporting the count, and absent when the composition
// root wired no seam.
func TestKnowledgePage_RestoreBuiltin(t *testing.T) {
	newHandler := func(fn func(context.Context) (int, error), user *User) *Handler {
		return NewHandler(Deps{
			KnowledgePageStore:  &mockKnowledgePageStore{},
			RestoreBuiltinPages: fn,
			AdminRoles:          []string{"admin"},
			RateLimit:           RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		}, testAuthMiddleware(user))
	}
	restore := func(context.Context) (int, error) { return 2, nil }

	if rec := doKP(newHandler(restore, kpViewer), "POST", "/api/v1/portal/knowledge-pages/restore-builtin", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin restore = %d, want 403", rec.Code)
	}
	rec := doKP(newHandler(restore, kpAdmin), "POST", "/api/v1/portal/knowledge-pages/restore-builtin", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin restore = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"restored":2`) {
		t.Errorf("restore body must report the count, got: %s", rec.Body.String())
	}
	if rec := doKP(newHandler(func(context.Context) (int, error) { return 0, errors.New("boom") }, kpAdmin),
		"POST", "/api/v1/portal/knowledge-pages/restore-builtin", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("failed restore = %d, want 500", rec.Code)
	}
	// No seam wired (portalnoop / injected stores): the route does not exist,
	// so the path falls through to the GET-only {id} pattern and POST is 405.
	if rec := doKP(newKnowledgePageHandler(&mockKnowledgePageStore{}, kpAdmin),
		"POST", "/api/v1/portal/knowledge-pages/restore-builtin", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unwired restore = %d, want 405", rec.Code)
	}
}
