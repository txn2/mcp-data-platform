package promptlayer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// stubCollectionStore implements only the read side of prompt.CollectionStore;
// the embedded interface panics on the write methods, which list never calls.
type stubCollectionStore struct {
	prompt.CollectionStore
	cols []prompt.Collection
	err  error
}

func (s *stubCollectionStore) ListCollections(_ context.Context) ([]prompt.Collection, error) {
	return s.cols, s.err
}

// listResult is the manage_prompt list response shape under test.
type listResult struct {
	Prompts     []prompt.Prompt     `json:"prompts"`
	Count       int                 `json:"count"`
	Collections []prompt.Collection `json:"collections"`
}

func TestHandlePromptList_PopulatesUsageBatch(t *testing.T) {
	h, store := newTestHandle()
	last := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	h.SetUsageReader(&stubUsageReader{usage: map[string]prompt.Usage{
		"p1": {RunCount: 37, LastRunAt: &last},
	}})
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "body", Enabled: true,
	}
	store.prompts["unused"] = &prompt.Prompt{
		ID: "p2", Name: "unused", Scope: prompt.ScopeGlobal, Content: "body", Enabled: true,
	}

	r, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{Command: cmdList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var out listResult
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &out))
	require.Len(t, out.Prompts, 2)
	counts := map[string]int64{}
	for _, p := range out.Prompts {
		counts[p.Name] = p.RunCount
	}
	assert.Equal(t, int64(37), counts["report"])
	assert.Zero(t, counts["unused"])
}

func TestHandlePromptList_UsageReadErrorLeavesZero(t *testing.T) {
	h, store := newTestHandle()
	h.SetUsageReader(&stubUsageReader{err: errors.New("db down")})
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "body", Enabled: true,
	}

	r, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{Command: cmdList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var out listResult
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &out))
	require.Len(t, out.Prompts, 1)
	assert.Zero(t, out.Prompts[0].RunCount)
}

func TestHandlePromptList_IncludesCollections(t *testing.T) {
	capable := &collectionCapableStore{
		mockPromptStore: newMockPromptStore(),
		CollectionStore: &stubCollectionStore{cols: []prompt.Collection{
			{ID: "col_1", Name: "Sales Reporting", PromptCount: 2},
		}},
	}
	capable.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "body", Enabled: true,
	}
	h, _ := newTestHandle()
	h.store = capable

	r, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{Command: cmdList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var out listResult
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &out))
	require.Len(t, out.Collections, 1)
	assert.Equal(t, "Sales Reporting", out.Collections[0].Name)
	assert.Equal(t, 2, out.Collections[0].PromptCount)
}

func TestHandlePromptList_NoCollectionsWithoutCapability(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "body", Enabled: true,
	}

	r, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{Command: cmdList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &raw))
	_, present := raw["collections"]
	assert.False(t, present, "collections key must be absent without the capability")
}

func TestHandlePromptList_CollectionReadErrorOmitsCollections(t *testing.T) {
	capable := &collectionCapableStore{
		mockPromptStore: newMockPromptStore(),
		CollectionStore: &stubCollectionStore{err: errors.New("db down")},
	}
	h, _ := newTestHandle()
	h.store = capable

	r, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{Command: cmdList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &raw))
	_, present := raw["collections"]
	assert.False(t, present, "collections key must be absent when the read fails")
}

// searchCollectionStore is a searchable store that also carries the collection
// capability, mirroring the production postgres store's shape.
type searchCollectionStore struct {
	*searchableStore
	prompt.CollectionStore
}

func TestHandlePromptSearch_PopulatesUsageAndCollections(t *testing.T) {
	store := &searchCollectionStore{
		searchableStore: &searchableStore{
			mockPromptStore: newMockPromptStore(),
			result: []prompt.ScoredPrompt{
				{Prompt: prompt.Prompt{ID: "p1", Name: "daily-sales"}, Score: 0.9},
			},
		},
		CollectionStore: &stubCollectionStore{cols: []prompt.Collection{
			{ID: "col_1", Name: "Sales Reporting"},
		}},
	}
	h, _ := newTestHandle()
	h.store = store
	h.SetUsageReader(&stubUsageReader{usage: map[string]prompt.Usage{
		"p1": {RunCount: 12},
	}})

	r, _, _ := h.handleManagePrompt(adminCtx(), managePromptInput{Command: cmdList, Query: "sales"})
	require.False(t, r.IsError)

	var out struct {
		Prompts     []prompt.ScoredPrompt `json:"prompts"`
		Collections []prompt.Collection   `json:"collections"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &out))
	require.Len(t, out.Prompts, 1)
	assert.Equal(t, int64(12), out.Prompts[0].Prompt.RunCount)
	require.Len(t, out.Collections, 1)
	assert.Equal(t, "Sales Reporting", out.Collections[0].Name)
}
