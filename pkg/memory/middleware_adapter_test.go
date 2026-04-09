package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStore is a test double for the memory Store interface.
type mockStore struct {
	noopStore
	entityLookupFn func(ctx context.Context, urn, persona string) ([]Record, error)
}

func (m *mockStore) EntityLookup(ctx context.Context, urn, persona string) ([]Record, error) {
	if m.entityLookupFn != nil {
		return m.entityLookupFn(ctx, urn, persona)
	}
	return nil, nil
}

func TestNewMiddlewareAdapter(t *testing.T) {
	store := NewNoopStore()
	adapter := NewMiddlewareAdapter(store)
	assert.NotNil(t, adapter)
}

func TestRecallForEntities_EmptyURNs(t *testing.T) {
	adapter := NewMiddlewareAdapter(NewNoopStore())

	snippets, err := adapter.RecallForEntities(context.Background(), nil, "analyst", 5)
	assert.NoError(t, err)
	assert.Nil(t, snippets)

	snippets, err = adapter.RecallForEntities(context.Background(), []string{}, "analyst", 5)
	assert.NoError(t, err)
	assert.Nil(t, snippets)
}

func TestRecallForEntities_SingleURN(t *testing.T) {
	now := time.Now()
	store := &mockStore{
		entityLookupFn: func(_ context.Context, urn, _ string) ([]Record, error) {
			if urn == "urn:li:dataset:foo" {
				return []Record{
					{
						ID:         "mem-001",
						Content:    "Revenue column insight",
						Dimension:  DimensionKnowledge,
						Category:   CategoryBusinessCtx,
						Confidence: ConfidenceHigh,
						CreatedAt:  now,
					},
				}, nil
			}
			return nil, nil
		},
	}

	adapter := NewMiddlewareAdapter(store)
	snippets, err := adapter.RecallForEntities(context.Background(), []string{"urn:li:dataset:foo"}, "", 10)
	require.NoError(t, err)
	require.Len(t, snippets, 1)
	assert.Equal(t, "mem-001", snippets[0].ID)
	assert.Equal(t, "Revenue column insight", snippets[0].Content)
	assert.Equal(t, DimensionKnowledge, snippets[0].Dimension)
	assert.Equal(t, CategoryBusinessCtx, snippets[0].Category)
	assert.Equal(t, ConfidenceHigh, snippets[0].Confidence)
}

func TestRecallForEntities_MultipleURNs(t *testing.T) {
	now := time.Now()
	store := &mockStore{
		entityLookupFn: func(_ context.Context, urn, _ string) ([]Record, error) {
			switch urn {
			case "urn:li:dataset:foo":
				return []Record{
					{ID: "mem-001", Content: "Foo insight", CreatedAt: now},
				}, nil
			case "urn:li:dataset:bar":
				return []Record{
					{ID: "mem-002", Content: "Bar insight", CreatedAt: now},
				}, nil
			}
			return nil, nil
		},
	}

	adapter := NewMiddlewareAdapter(store)
	snippets, err := adapter.RecallForEntities(context.Background(),
		[]string{"urn:li:dataset:foo", "urn:li:dataset:bar"}, "", 10)
	require.NoError(t, err)
	assert.Len(t, snippets, 2)
}

func TestRecallForEntities_Dedup(t *testing.T) {
	now := time.Now()
	sharedRecord := Record{
		ID:        "mem-shared",
		Content:   "Shared insight",
		CreatedAt: now,
	}

	store := &mockStore{
		entityLookupFn: func(_ context.Context, _ string, _ string) ([]Record, error) {
			// Both URNs return the same record.
			return []Record{sharedRecord}, nil
		},
	}

	adapter := NewMiddlewareAdapter(store)
	snippets, err := adapter.RecallForEntities(context.Background(),
		[]string{"urn:li:dataset:foo", "urn:li:dataset:bar"}, "", 10)
	require.NoError(t, err)
	// Should be deduplicated to 1.
	assert.Len(t, snippets, 1)
	assert.Equal(t, "mem-shared", snippets[0].ID)
}

func TestRecallForEntities_LimitCapping(t *testing.T) {
	now := time.Now()
	store := &mockStore{
		entityLookupFn: func(_ context.Context, _ string, _ string) ([]Record, error) {
			records := make([]Record, 0, 10)
			for i := range 10 {
				records = append(records, Record{
					ID:        fmt.Sprintf("mem-%03d", i),
					Content:   "Some insight content",
					CreatedAt: now,
				})
			}
			return records, nil
		},
	}

	adapter := NewMiddlewareAdapter(store)
	snippets, err := adapter.RecallForEntities(context.Background(),
		[]string{"urn:li:dataset:foo"}, "", 3)
	require.NoError(t, err)
	assert.Len(t, snippets, 3)
}

func TestRecallForEntities_DefaultLimit(t *testing.T) {
	now := time.Now()
	store := &mockStore{
		entityLookupFn: func(_ context.Context, _ string, _ string) ([]Record, error) {
			records := make([]Record, 0, 10)
			for i := range 10 {
				records = append(records, Record{
					ID:        fmt.Sprintf("mem-%03d", i),
					Content:   "Some insight content",
					CreatedAt: now,
				})
			}
			return records, nil
		},
	}

	adapter := NewMiddlewareAdapter(store)
	// Passing 0 should default to defaultRecallLimit (5).
	snippets, err := adapter.RecallForEntities(context.Background(),
		[]string{"urn:li:dataset:foo"}, "", 0)
	require.NoError(t, err)
	assert.Len(t, snippets, defaultRecallLimit)
}

func TestRecallForEntities_LookupError(t *testing.T) {
	store := &mockStore{
		entityLookupFn: func(_ context.Context, _ string, _ string) ([]Record, error) {
			return nil, errors.New("database unavailable")
		},
	}

	adapter := NewMiddlewareAdapter(store)
	snippets, err := adapter.RecallForEntities(context.Background(),
		[]string{"urn:li:dataset:foo"}, "analyst", 5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "entity lookup")
	assert.Nil(t, snippets)
}
