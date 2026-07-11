package promptlayer

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// searchableStore adds prompt.Searcher to the mock prompt store.
type searchableStore struct {
	*mockPromptStore
	gotQuery prompt.SearchQuery
	result   []prompt.ScoredPrompt
	err      error
}

func (s *searchableStore) Search(_ context.Context, q prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	s.gotQuery = q
	return s.result, s.err
}

var _ prompt.Searcher = (*searchableStore)(nil)

func TestHandlePromptSearch_Unavailable(t *testing.T) {
	// Plain mock store does not implement prompt.Searcher.
	h, _ := newTestHandle()
	r, _, _ := h.handleManagePrompt(adminCtx(), managePromptInput{Command: cmdList, Query: "sales"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "unavailable")
}

func TestHandlePromptSearch_Success(t *testing.T) {
	store := &searchableStore{
		mockPromptStore: newMockPromptStore(),
		result: []prompt.ScoredPrompt{
			{Prompt: prompt.Prompt{ID: "p-1", Name: "daily-sales"}, Score: 0.9},
		},
	}
	h, _ := newTestHandle()
	h.store = store

	r, _, _ := h.handleManagePrompt(userCtx("alice@example.com", "analyst"),
		managePromptInput{Command: cmdList, Query: "sales report", Limit: 3})
	require.False(t, r.IsError)

	var out struct {
		Prompts []prompt.ScoredPrompt `json:"prompts"`
		Count   int                   `json:"count"`
		Ranking string                `json:"ranking"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &out))
	require.Len(t, out.Prompts, 1)
	assert.Equal(t, "daily-sales", out.Prompts[0].Prompt.Name)
	assert.Equal(t, 1, out.Count)
	// No embedding provider configured in the test handle, so ranking degrades
	// to lexical.
	assert.Equal(t, "lexical", out.Ranking)

	// Visibility is scoped to the caller.
	assert.Equal(t, "alice@example.com", store.gotQuery.OwnerEmail)
	assert.Equal(t, "analyst", store.gotQuery.Persona)
	assert.False(t, store.gotQuery.IsAdmin)
	assert.Equal(t, 3, store.gotQuery.Limit)
}

func TestHandlePromptSearch_AdminFlag(t *testing.T) {
	store := &searchableStore{mockPromptStore: newMockPromptStore()}
	h, _ := newTestHandle()
	h.store = store

	r, _, _ := h.handleManagePrompt(adminCtx(), managePromptInput{Command: cmdList, Query: "x"})
	require.False(t, r.IsError)
	assert.True(t, store.gotQuery.IsAdmin)
}

func TestHandlePromptSearch_StoreError(t *testing.T) {
	store := &searchableStore{mockPromptStore: newMockPromptStore(), err: assert.AnError}
	h, _ := newTestHandle()
	h.store = store

	r, _, _ := h.handleManagePrompt(adminCtx(), managePromptInput{Command: cmdList, Query: "x"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "failed to search prompts")
}
