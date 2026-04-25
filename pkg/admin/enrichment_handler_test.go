package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
)

// stubEnrichmentStore is a programmable in-memory Store suitable for
// exercising the admin handler without a database.
type stubEnrichmentStore struct {
	items     map[string]enrichment.Rule
	listErr   error
	getErr    error
	createErr error
	updateErr error
	deleteErr error
}

func newStubStore() *stubEnrichmentStore {
	return &stubEnrichmentStore{items: map[string]enrichment.Rule{}}
}

func (s *stubEnrichmentStore) List(_ context.Context, connection, tool string, enabledOnly bool) ([]enrichment.Rule, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]enrichment.Rule, 0, len(s.items))
	for _, r := range s.items {
		if connection != "" && r.ConnectionName != connection {
			continue
		}
		if tool != "" && r.ToolName != tool {
			continue
		}
		if enabledOnly && !r.Enabled {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *stubEnrichmentStore) Get(_ context.Context, id string) (*enrichment.Rule, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	r, ok := s.items[id]
	if !ok {
		return nil, enrichment.ErrRuleNotFound
	}
	return &r, nil
}

func (s *stubEnrichmentStore) Create(_ context.Context, r enrichment.Rule) (enrichment.Rule, error) {
	if s.createErr != nil {
		return enrichment.Rule{}, s.createErr
	}
	if r.ID == "" {
		r.ID = "stub-" + r.ConnectionName + "-" + r.ToolName
	}
	r.CreatedAt = time.Now().UTC()
	r.UpdatedAt = r.CreatedAt
	s.items[r.ID] = r
	return r, nil
}

func (s *stubEnrichmentStore) Update(_ context.Context, r enrichment.Rule) (enrichment.Rule, error) {
	if s.updateErr != nil {
		return enrichment.Rule{}, s.updateErr
	}
	if _, ok := s.items[r.ID]; !ok {
		return enrichment.Rule{}, enrichment.ErrRuleNotFound
	}
	r.UpdatedAt = time.Now().UTC()
	s.items[r.ID] = r
	return r, nil
}

func (s *stubEnrichmentStore) Delete(_ context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.items[id]; !ok {
		return enrichment.ErrRuleNotFound
	}
	delete(s.items, id)
	return nil
}

var _ EnrichmentStore = (*stubEnrichmentStore)(nil)

func enrichmentHandler(store EnrichmentStore, mutable bool) *Handler {
	mode := "database"
	if !mutable {
		mode = "file"
	}
	return NewHandler(Deps{
		Config:          testConfig(),
		EnrichmentStore: store,
		ConfigStore:     &mockConfigStore{mode: mode},
	}, nil)
}

func enrichmentRuleBodyJSON(t *testing.T, tool string) []byte {
	t.Helper()
	b, err := json.Marshal(enrichmentRuleBody{
		ToolName:     tool,
		EnrichAction: enrichment.Action{Source: enrichment.SourceTrino, Operation: "query"},
		Enabled:      true,
	})
	require.NoError(t, err)
	return b
}

func TestListEnrichmentRules_Empty(t *testing.T) {
	h := enrichmentHandler(newStubStore(), true)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/crm/enrichment-rules", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var rules []enrichment.Rule
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rules))
	assert.Empty(t, rules)
}

func TestListEnrichmentRules_FiltersByConnection(t *testing.T) {
	store := newStubStore()
	store.items["1"] = enrichment.Rule{ID: "1", ConnectionName: "crm", ToolName: "a", Enabled: true}
	store.items["2"] = enrichment.Rule{ID: "2", ConnectionName: "marketing", ToolName: "b", Enabled: true}
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/crm/enrichment-rules", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var rules []enrichment.Rule
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rules))
	assert.Len(t, rules, 1)
	assert.Equal(t, "crm", rules[0].ConnectionName)
}

func TestListEnrichmentRules_ListError(t *testing.T) {
	store := newStubStore()
	store.listErr = errors.New("db down")
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/crm/enrichment-rules", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateEnrichmentRule_Success(t *testing.T) {
	store := newStubStore()
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/crm/enrichment-rules",
		bytes.NewReader(enrichmentRuleBodyJSON(t, "crm__get_contact")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var created enrichment.Rule
	require.NoError(t, json.NewDecoder(w.Body).Decode(&created))
	assert.Equal(t, "crm", created.ConnectionName)
	assert.Equal(t, "crm__get_contact", created.ToolName)
	assert.True(t, created.Enabled)
	assert.NotEmpty(t, created.ID)
}

func TestCreateEnrichmentRule_ValidationError(t *testing.T) {
	store := newStubStore()
	h := enrichmentHandler(store, true)

	// Body missing enrich_action.operation
	bad, _ := json.Marshal(enrichmentRuleBody{
		ToolName:     "crm__x",
		EnrichAction: enrichment.Action{Source: enrichment.SourceTrino},
	})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/crm/enrichment-rules",
		bytes.NewReader(bad))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateEnrichmentRule_BadJSON(t *testing.T) {
	h := enrichmentHandler(newStubStore(), true)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/crm/enrichment-rules",
		bytes.NewReader([]byte("{bad")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateEnrichmentRule_StoreError(t *testing.T) {
	store := newStubStore()
	store.createErr = errors.New("db down")
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/admin/gateway/connections/crm/enrichment-rules",
		bytes.NewReader(enrichmentRuleBodyJSON(t, "crm__x")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetEnrichmentRule_Success(t *testing.T) {
	store := newStubStore()
	store.items["abc"] = enrichment.Rule{ID: "abc", ConnectionName: "crm", ToolName: "x"}
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestGetEnrichmentRule_NotFound(t *testing.T) {
	h := enrichmentHandler(newStubStore(), true)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/crm/enrichment-rules/missing", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetEnrichmentRule_ConnectionMismatchIs404(t *testing.T) {
	store := newStubStore()
	store.items["abc"] = enrichment.Rule{ID: "abc", ConnectionName: "marketing", ToolName: "x"}
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetEnrichmentRule_StoreError(t *testing.T) {
	store := newStubStore()
	store.getErr = errors.New("db down")
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/crm/enrichment-rules/x", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateEnrichmentRule_Success(t *testing.T) {
	store := newStubStore()
	store.items["abc"] = enrichment.Rule{
		ID: "abc", ConnectionName: "crm", ToolName: "x",
		EnrichAction: enrichment.Action{Source: enrichment.SourceTrino, Operation: "query"},
		Enabled:      true,
	}
	h := enrichmentHandler(store, true)

	body, _ := json.Marshal(enrichmentRuleBody{
		ToolName:     "x",
		Description:  "updated",
		EnrichAction: enrichment.Action{Source: enrichment.SourceTrino, Operation: "query"},
		Enabled:      false,
	})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc",
		bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "updated", store.items["abc"].Description)
	assert.False(t, store.items["abc"].Enabled)
}

func TestUpdateEnrichmentRule_NotFound(t *testing.T) {
	h := enrichmentHandler(newStubStore(), true)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/v1/admin/gateway/connections/crm/enrichment-rules/missing",
		bytes.NewReader(enrichmentRuleBodyJSON(t, "x")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateEnrichmentRule_ConnectionMismatchIs404(t *testing.T) {
	store := newStubStore()
	store.items["abc"] = enrichment.Rule{ID: "abc", ConnectionName: "marketing", ToolName: "x"}
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc",
		bytes.NewReader(enrichmentRuleBodyJSON(t, "x")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateEnrichmentRule_BadJSON(t *testing.T) {
	store := newStubStore()
	store.items["abc"] = enrichment.Rule{ID: "abc", ConnectionName: "crm"}
	h := enrichmentHandler(store, true)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc",
		bytes.NewReader([]byte("{bad")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateEnrichmentRule_ValidationError(t *testing.T) {
	store := newStubStore()
	store.items["abc"] = enrichment.Rule{
		ID: "abc", ConnectionName: "crm", ToolName: "x",
		EnrichAction: enrichment.Action{Source: enrichment.SourceTrino, Operation: "query"},
	}
	h := enrichmentHandler(store, true)

	bad, _ := json.Marshal(enrichmentRuleBody{
		ToolName: "",
	})
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc",
		bytes.NewReader(bad))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateEnrichmentRule_GetStoreError(t *testing.T) {
	store := newStubStore()
	store.getErr = errors.New("db down")
	h := enrichmentHandler(store, true)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc",
		bytes.NewReader(enrichmentRuleBodyJSON(t, "x")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateEnrichmentRule_UpdateStoreError(t *testing.T) {
	store := newStubStore()
	store.items["abc"] = enrichment.Rule{
		ID: "abc", ConnectionName: "crm", ToolName: "x",
		EnrichAction: enrichment.Action{Source: enrichment.SourceTrino, Operation: "query"},
	}
	store.updateErr = errors.New("db down")
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc",
		bytes.NewReader(enrichmentRuleBodyJSON(t, "x")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDeleteEnrichmentRule_Success(t *testing.T) {
	store := newStubStore()
	store.items["abc"] = enrichment.Rule{ID: "abc", ConnectionName: "crm"}
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
	_, stillThere := store.items["abc"]
	assert.False(t, stillThere)
}

func TestDeleteEnrichmentRule_NotFound(t *testing.T) {
	h := enrichmentHandler(newStubStore(), true)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/v1/admin/gateway/connections/crm/enrichment-rules/missing", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteEnrichmentRule_ConnectionMismatchIs404(t *testing.T) {
	store := newStubStore()
	store.items["abc"] = enrichment.Rule{ID: "abc", ConnectionName: "marketing"}
	h := enrichmentHandler(store, true)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteEnrichmentRule_GetStoreError(t *testing.T) {
	store := newStubStore()
	store.getErr = errors.New("db down")
	h := enrichmentHandler(store, true)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDeleteEnrichmentRule_DeleteStoreError(t *testing.T) {
	store := newStubStore()
	store.items["abc"] = enrichment.Rule{ID: "abc", ConnectionName: "crm"}
	store.deleteErr = errors.New("db down")
	h := enrichmentHandler(store, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/v1/admin/gateway/connections/crm/enrichment-rules/abc", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAuthorEmailOrID(t *testing.T) {
	// nil user → empty string.
	assert.Equal(t, "", authorEmailOrID(context.Background()))

	ctxEmail := context.WithValue(context.Background(), adminUserKey, &User{Email: "e@x.com", UserID: "uid"})
	assert.Equal(t, "e@x.com", authorEmailOrID(ctxEmail))

	ctxID := context.WithValue(context.Background(), adminUserKey, &User{UserID: "uid-only"})
	assert.Equal(t, "uid-only", authorEmailOrID(ctxID))
}

func TestRegisterEnrichmentRoutes_SkipsWhenNoStore(t *testing.T) {
	// No store → routes not registered → 404
	h := enrichmentHandler(nil, true)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/crm/enrichment-rules", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRegisterEnrichmentRoutes_SkipsWhenImmutable(t *testing.T) {
	h := enrichmentHandler(newStubStore(), false)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/admin/gateway/connections/crm/enrichment-rules", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
