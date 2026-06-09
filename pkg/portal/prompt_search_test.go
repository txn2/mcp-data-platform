package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// searchableMockPromptStore adds the prompt.Searcher capability to the portal
// mock store so the ranked-search route can be exercised.
type searchableMockPromptStore struct {
	*mockPromptStore
	gotQuery prompt.SearchQuery
	result   []prompt.ScoredPrompt
	err      error
}

func (s *searchableMockPromptStore) Search(_ context.Context, q prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	s.gotQuery = q
	return s.result, s.err
}

var _ prompt.Searcher = (*searchableMockPromptStore)(nil)

func newSearchablePortalHandler(result []prompt.ScoredPrompt) (*Handler, *searchableMockPromptStore) {
	store := &searchableMockPromptStore{mockPromptStore: newMockPromptStore(), result: result}
	h := NewHandler(Deps{
		PromptStore: store,
		AdminRoles:  []string{"admin"},
		AssetStore:  &noopAssetStore{},
	}, nil)
	return h, store
}

func TestSearchMyPrompts_Unauthenticated(t *testing.T) {
	h, _ := newSearchablePortalHandler(nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/search?q=sales", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestSearchMyPrompts_MissingQuery(t *testing.T) {
	h, _ := newSearchablePortalHandler(nil)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/search", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearchMyPrompts_Unavailable(t *testing.T) {
	// Plain mock store does not implement prompt.Searcher.
	h, _, _ := newTestPortalPromptHandler()
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/search?q=sales", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestSearchMyPrompts_Success(t *testing.T) {
	result := []prompt.ScoredPrompt{
		{Prompt: prompt.Prompt{ID: "p-1", Name: "daily-sales"}, Score: 0.91},
	}
	h, store := newSearchablePortalHandler(result)

	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/prompts/search?q=sales+report&limit=5", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data  []prompt.ScoredPrompt `json:"data"`
		Total int                   `json:"total"`
		Limit int                   `json:"limit"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	assert.Equal(t, "daily-sales", resp.Data[0].Prompt.Name)
	assert.Equal(t, 1, resp.Total)
	assert.Equal(t, 5, resp.Limit)

	// Visibility is scoped to the caller: non-admin, own email, no persona.
	assert.Equal(t, "alice@example.com", store.gotQuery.OwnerEmail)
	assert.False(t, store.gotQuery.IsAdmin)
	assert.Equal(t, "sales report", store.gotQuery.QueryText)
	assert.Equal(t, 5, store.gotQuery.Limit)
}

func TestSearchMyPrompts_ExcludesSystemPrompts(t *testing.T) {
	// Ingested static prompts (source=system) are searchable for agents via
	// manage_prompt, but the portal omits them from a user's own prompt search.
	result := []prompt.ScoredPrompt{
		{Prompt: prompt.Prompt{ID: "p-1", Name: "daily-sales", Source: prompt.SourceOperator}, Score: 0.91},
		{Prompt: prompt.Prompt{ID: "sys-1", Name: "explore-data", Source: prompt.SourceSystem}, Score: 0.88},
	}
	h, _ := newSearchablePortalHandler(result)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/prompts/search?q=data", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data  []prompt.ScoredPrompt `json:"data"`
		Total int                   `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Total)
	require.Len(t, resp.Data, 1)
	assert.Equal(t, "daily-sales", resp.Data[0].Prompt.Name)
}

func TestSearchMyPrompts_AdminFlag(t *testing.T) {
	h, store := newSearchablePortalHandler(nil)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/prompts/search?q=x", http.NoBody), "boss@example.com", "admin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, store.gotQuery.IsAdmin)
}

func TestSearchMyPrompts_StoreError(t *testing.T) {
	h, store := newSearchablePortalHandler(nil)
	store.err = assert.AnError
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/prompts/search?q=x", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
