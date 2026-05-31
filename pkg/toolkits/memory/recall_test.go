package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// ---------------------------------------------------------------------------
// Mock semantic provider (for graph tests)
// ---------------------------------------------------------------------------

type mockSemanticProvider struct {
	lineageResult *semantic.LineageInfo
	lineageErr    error
}

func (*mockSemanticProvider) Name() string { return "mock" }

func (*mockSemanticProvider) GetTableContext(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
	return nil, nil //nolint:nilnil // mock returns nil for both
}

func (*mockSemanticProvider) GetColumnContext(_ context.Context, _ semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // mock returns nil for both
}

func (*mockSemanticProvider) GetColumnsContext(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // mock returns nil for both
}

func (m *mockSemanticProvider) GetLineage(_ context.Context, _ semantic.TableIdentifier, _ semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	if m.lineageErr != nil {
		return nil, m.lineageErr
	}
	return m.lineageResult, nil
}

func (*mockSemanticProvider) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return nil, nil //nolint:nilnil // mock returns nil for both
}

func (*mockSemanticProvider) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return nil, nil //nolint:nilnil // mock returns nil for both
}

func (*mockSemanticProvider) GetCuratedQueryCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (*mockSemanticProvider) Close() error { return nil }

var _ semantic.Provider = (*mockSemanticProvider)(nil)

// ---------------------------------------------------------------------------
// handleRecall tests
// ---------------------------------------------------------------------------

func TestHandleRecall_AutoStrategy(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "r1", Content: "entity matched record"},
		},
	}
	embedder := &mockEmbedder{embedResult: []float32{0.1, 0.2, 0.3}}
	tk := newTestToolkit(store, embedder)
	tk.store = store // ensure the store is set
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		EntityURNs: []string{"urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)"},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, "auto", data["strategy"])
}

func TestHandleRecall_EntityStrategy(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "r1", Content: "matched by entity URN"},
		},
	}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy:   "entity",
		EntityURNs: []string{"urn:li:dataset:test"},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, "entity", data["strategy"])
	assert.Equal(t, float64(1), data["count"])
}

func TestHandleRecall_EntityStrategy_NoURNs(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy: "entity",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "entity_urns required")
}

func TestHandleRecall_SemanticStrategy(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		hybridResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "v1", Content: "semantic match"}, Score: 0.95},
		},
	}
	embedder := &mockEmbedder{embedResult: []float32{0.1, 0.2, 0.3}}
	tk := newTestToolkit(store, embedder)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy: "semantic",
		Query:    "find related data quality issues",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, "semantic", data["strategy"])
	assert.Equal(t, "hybrid", data["ranking"])
	assert.Equal(t, float64(1), data["count"])
	assert.NotContains(t, data, "degraded")
}

func TestHandleRecall_SemanticStrategy_ZeroVectorDegradesToLexical(t *testing.T) {
	t.Parallel()

	// Noop embedder returns zero vectors: recall must degrade to
	// lexical-only and flag the response, not error.
	store := &mockStore{
		lexicalResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "l1", Content: "keyword match"}, Score: 0.4},
		},
	}
	embedder := embedding.NewNoopProvider(3)
	tk := newTestToolkit(store, embedder)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy: "semantic",
		Query:    "search something",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	data := extractJSON(t, result)
	assert.Equal(t, "lexical", data["ranking"])
	assert.Equal(t, true, data["degraded"])
	assert.Equal(t, degradedNote, data["note"])
	assert.Equal(t, float64(1), data["count"])
}

func TestHandleRecall_LexicalStrategy(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		lexicalResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "l1", Content: "error_code 42"}, Score: 0.6},
		},
	}
	// Embedder present but must not be used by the forced lexical strategy.
	tk := newTestToolkit(store, &mockEmbedder{embedResult: []float32{0.1}})
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy: "lexical",
		Query:    "error_code 42",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	data := extractJSON(t, result)
	assert.Equal(t, "lexical", data["strategy"])
	assert.Equal(t, "lexical", data["ranking"])
	assert.NotContains(t, data, "degraded", "explicit lexical is not a degradation")
	assert.Empty(t, store.hybridQueries, "lexical strategy must not embed")
}

func TestHandleRecall_SemanticStrategy_EmbeddingErrorDegradesToLexical(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		lexicalResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "l1"}, Score: 0.4},
		},
	}
	embedder := &mockEmbedder{embedErr: errors.New("embedding service down")}
	tk := newTestToolkit(store, embedder)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy: "semantic",
		Query:    "search something",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	data := extractJSON(t, result)
	assert.Equal(t, "lexical", data["ranking"])
	assert.Equal(t, true, data["degraded"])
}

func TestHandleRecall_SemanticStrategy_NoQuery(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy: "semantic",
		Query:    "",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "query required")
}

func TestHandleRecall_GraphStrategy_WithSemanticProvider(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "g1", Content: "graph matched record"},
		},
	}
	sp := &mockSemanticProvider{
		lineageResult: &semantic.LineageInfo{
			Entities: []semantic.LineageEntity{
				{URN: "urn:li:dataset:related"},
			},
		},
	}
	tk := newTestToolkit(store, nil)
	tk.SetSemanticProvider(sp)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy:   "graph",
		EntityURNs: []string{"urn:li:dataset:(urn:li:dataPlatform:trino,cat.sch.tbl,PROD)"},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, "graph", data["strategy"])
}

func TestHandleRecall_GraphStrategy_WithoutSemanticProvider_FallsBackToEntity(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "e1", Content: "entity fallback record"},
		},
	}
	tk := newTestToolkit(store, nil)
	// semanticProvider is nil by default.
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy:   "graph",
		EntityURNs: []string{"urn:li:dataset:test"},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, "graph", data["strategy"])
	assert.Equal(t, float64(1), data["count"])
}

func TestHandleRecall_UnknownStrategy(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy: "magic",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "unknown strategy")
}

func TestHandleRecall_DefaultStrategyIsAuto(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy: "", // empty should default to auto
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, "auto", data["strategy"])
}

// ---------------------------------------------------------------------------
// recallByEntity tests
// ---------------------------------------------------------------------------

func TestRecallByEntity_URNsProvided(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "r1"},
			{ID: "r2"},
		},
	}
	tk := newTestToolkit(store, nil)

	results, err := tk.recallByEntity(context.Background(), []string{"urn:1", "urn:2"}, "analyst")
	require.NoError(t, err)
	// Both URNs return the same records (mock doesn't filter), but dedup by ID
	// means we get unique records. The mock returns the same 2 records for both URNs,
	// but the seen map deduplicates.
	assert.NotEmpty(t, results)
	for _, r := range results {
		assert.Equal(t, 1.0, r.Score, "entity matches should have score 1.0")
	}
}

func TestRecallByEntity_NoURNs(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)

	results, err := tk.recallByEntity(context.Background(), nil, "analyst")
	assert.Nil(t, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_urns required")
}

func TestRecallByEntity_StoreError(t *testing.T) {
	t.Parallel()

	store := &mockStore{entityErr: errors.New("db error")}
	tk := newTestToolkit(store, nil)

	results, err := tk.recallByEntity(context.Background(), []string{"urn:1"}, "analyst")
	assert.Nil(t, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity lookup")
}

// ---------------------------------------------------------------------------
// recallBySemantic tests
// ---------------------------------------------------------------------------

func TestRecallBySemantic_Successful(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		hybridResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "v1"}, Score: 0.9},
		},
	}
	embedder := &mockEmbedder{embedResult: []float32{0.1, 0.2}}
	tk := newTestToolkit(store, embedder)

	outcome, err := tk.recallBySemantic(context.Background(), "test query", "analyst", false)
	require.NoError(t, err)
	require.Len(t, outcome.records, 1)
	assert.Equal(t, "v1", outcome.records[0].Record.ID)
	assert.Equal(t, rankingHybrid, outcome.ranking)
	assert.False(t, outcome.degraded)
	// Hybrid arm carries both the embedding and the query text.
	require.Len(t, store.hybridQueries, 1)
	assert.Equal(t, "test query", store.hybridQueries[0].QueryText)
	assert.Equal(t, []float32{0.1, 0.2}, store.hybridQueries[0].Embedding)
}

func TestRecallBySemantic_EmbeddingError_FallsBackToLexical(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		lexicalResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "l1"}, Score: 0.4},
		},
	}
	embedder := &mockEmbedder{embedErr: errors.New("service down")}
	tk := newTestToolkit(store, embedder)

	outcome, err := tk.recallBySemantic(context.Background(), "orders_fact", "analyst", false)
	require.NoError(t, err, "a down embedder must degrade, not error")
	require.Len(t, outcome.records, 1)
	assert.Equal(t, "l1", outcome.records[0].Record.ID)
	assert.Equal(t, rankingLexical, outcome.ranking)
	assert.True(t, outcome.degraded)
	assert.Equal(t, degradedNote, outcome.note)
	require.Len(t, store.lexicalQueries, 1)
	assert.Equal(t, "orders_fact", store.lexicalQueries[0].QueryText)
}

func TestRecallBySemantic_ZeroVector_FallsBackToLexical(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		lexicalResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "l1"}, Score: 0.3},
		},
	}
	embedder := &mockEmbedder{embedResult: []float32{0, 0, 0}}
	tk := newTestToolkit(store, embedder)

	outcome, err := tk.recallBySemantic(context.Background(), "test", "analyst", false)
	require.NoError(t, err)
	require.Len(t, outcome.records, 1)
	assert.Equal(t, rankingLexical, outcome.ranking)
	assert.True(t, outcome.degraded)
	// The zero-vector path must not reach the hybrid arm.
	assert.Empty(t, store.hybridQueries)
}

func TestRecallBySemantic_EmptyQuery(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)

	outcome, err := tk.recallBySemantic(context.Background(), "", "analyst", false)
	assert.Nil(t, outcome.records)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query required")
}

func TestRecallBySemantic_IncludeStale(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		hybridResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "v1", Status: memstore.StatusStale}, Score: 0.8},
		},
	}
	embedder := &mockEmbedder{embedResult: []float32{0.1, 0.2}}
	tk := newTestToolkit(store, embedder)

	outcome, err := tk.recallBySemantic(context.Background(), "test", "analyst", true)
	require.NoError(t, err)
	assert.Len(t, outcome.records, 1)
	// include_stale=true must not constrain the hybrid query's status.
	require.Len(t, store.hybridQueries, 1)
	assert.Empty(t, store.hybridQueries[0].Status)
}

func TestRecallByLexical_Forced(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		lexicalResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "l1"}, Score: 0.5},
		},
	}
	// No embedder needed for forced lexical.
	tk := newTestToolkit(store, &mockEmbedder{embedResult: []float32{0.1}})

	outcome, err := tk.recallByLexical(context.Background(), "error_code_42", "analyst", false)
	require.NoError(t, err)
	require.Len(t, outcome.records, 1)
	assert.Equal(t, rankingLexical, outcome.ranking)
	assert.False(t, outcome.degraded, "explicit lexical is not a degradation")
	assert.Empty(t, store.hybridQueries, "lexical strategy must not embed")
}

// ---------------------------------------------------------------------------
// recallByGraph tests
// ---------------------------------------------------------------------------

func TestRecallByGraph_WithSemanticProvider(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "g1"},
		},
	}
	sp := &mockSemanticProvider{
		lineageResult: &semantic.LineageInfo{
			Entities: []semantic.LineageEntity{
				{URN: "urn:li:dataset:related1"},
			},
		},
	}
	tk := newTestToolkit(store, nil)
	tk.SetSemanticProvider(sp)

	results, err := tk.recallByGraph(context.Background(),
		[]string{"urn:li:dataset:(urn:li:dataPlatform:trino,cat.sch.tbl,PROD)"},
		"analyst",
	)
	require.NoError(t, err)
	assert.NotNil(t, results)
}

func TestRecallByGraph_WithoutSemanticProvider(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "e1"},
		},
	}
	tk := newTestToolkit(store, nil)
	// No semantic provider set.

	results, err := tk.recallByGraph(context.Background(), []string{"urn:1"}, "analyst")
	require.NoError(t, err)
	assert.Len(t, results, 1, "should fall back to entity lookup")
}

func TestRecallByGraph_NoURNs(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)

	results, err := tk.recallByGraph(context.Background(), nil, "analyst")
	assert.Nil(t, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entity_urns required")
}

func TestRecallByGraph_LineageError_StillReturnsDirectEntities(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "e1"},
		},
	}
	sp := &mockSemanticProvider{
		lineageErr: errors.New("lineage service down"),
	}
	tk := newTestToolkit(store, nil)
	tk.SetSemanticProvider(sp)

	// The URN must be a valid dataset URN for ParseURNToTable to work.
	results, err := tk.recallByGraph(context.Background(),
		[]string{"urn:li:dataset:(urn:li:dataPlatform:trino,cat.sch.tbl,PROD)"},
		"analyst",
	)
	require.NoError(t, err)
	// Even though lineage failed, the direct URN is still looked up.
	assert.NotNil(t, results)
}

// ---------------------------------------------------------------------------
// recallAuto tests
// ---------------------------------------------------------------------------

func TestRecallAuto_ParallelExecution(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "e1", Content: "entity record"},
		},
		hybridResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "v1", Content: "semantic record"}, Score: 0.9},
		},
	}
	embedder := &mockEmbedder{embedResult: []float32{0.1, 0.2, 0.3}}
	tk := newTestToolkit(store, embedder)
	ctx := ctxWithPC("user@example.com", "analyst")

	outcome := tk.recallAuto(ctx, recallInput{
		Query:      "test query",
		EntityURNs: []string{"urn:1"},
	}, "analyst")

	// Should have results from both strategies merged, ranked by hybrid.
	assert.NotEmpty(t, outcome.records)
	assert.Equal(t, rankingHybrid, outcome.ranking)
	assert.False(t, outcome.degraded)
}

func TestRecallAuto_OnlyQuery(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		hybridResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "v1"}, Score: 0.85},
		},
	}
	embedder := &mockEmbedder{embedResult: []float32{0.1, 0.2}}
	tk := newTestToolkit(store, embedder)
	ctx := ctxWithPC("user@example.com", "analyst")

	outcome := tk.recallAuto(ctx, recallInput{Query: "something"}, "analyst")
	assert.Len(t, outcome.records, 1)
	assert.Equal(t, rankingHybrid, outcome.ranking)
}

func TestRecallAuto_OnlyQuery_DegradesWhenEmbedderDown(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		lexicalResults: []memstore.ScoredRecord{
			{Record: memstore.Record{ID: "l1"}, Score: 0.4},
		},
	}
	embedder := &mockEmbedder{embedErr: errors.New("down")}
	tk := newTestToolkit(store, embedder)
	ctx := ctxWithPC("user@example.com", "analyst")

	outcome := tk.recallAuto(ctx, recallInput{Query: "orders"}, "analyst")
	assert.Len(t, outcome.records, 1)
	assert.Equal(t, rankingLexical, outcome.ranking)
	assert.True(t, outcome.degraded, "auto must propagate the semantic arm's degradation")
}

func TestRecallAuto_OnlyURNs(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "e1"},
		},
	}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	outcome := tk.recallAuto(ctx, recallInput{
		EntityURNs: []string{"urn:1"},
	}, "analyst")
	assert.Len(t, outcome.records, 1)
	assert.Equal(t, strategyGraph, outcome.ranking)
}

func TestRecallAuto_NoInputs(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	outcome := tk.recallAuto(ctx, recallInput{}, "analyst")
	assert.Empty(t, outcome.records)
}

// ---------------------------------------------------------------------------
// dedup tests
// ---------------------------------------------------------------------------

func TestDedup_RemovesDuplicates(t *testing.T) {
	t.Parallel()

	records := []memstore.ScoredRecord{
		{Record: memstore.Record{ID: "a"}, Score: 0.5},
		{Record: memstore.Record{ID: "b"}, Score: 0.8},
		{Record: memstore.Record{ID: "a"}, Score: 0.9}, // duplicate with higher score
	}

	result := dedup(records)
	require.Len(t, result, 2)

	// Find "a" and verify it kept the higher score.
	for _, r := range result {
		if r.Record.ID == "a" {
			assert.Equal(t, 0.9, r.Score, "should keep highest score")
		}
	}
}

func TestDedup_NoDuplicates(t *testing.T) {
	t.Parallel()

	records := []memstore.ScoredRecord{
		{Record: memstore.Record{ID: "a"}, Score: 0.5},
		{Record: memstore.Record{ID: "b"}, Score: 0.8},
	}

	result := dedup(records)
	assert.Len(t, result, 2)
}

func TestDedup_EmptyInput(t *testing.T) {
	t.Parallel()

	result := dedup(nil)
	assert.Empty(t, result)
}

func TestDedup_KeepsLowerScoreWhenFirst(t *testing.T) {
	t.Parallel()

	records := []memstore.ScoredRecord{
		{Record: memstore.Record{ID: "x"}, Score: 0.9},
		{Record: memstore.Record{ID: "x"}, Score: 0.3}, // duplicate with lower score
	}

	result := dedup(records)
	require.Len(t, result, 1)
	assert.Equal(t, 0.9, result[0].Score, "should keep the higher score")
}

// ---------------------------------------------------------------------------
// clampLimit tests
// ---------------------------------------------------------------------------

func TestClampLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input int
		want  int
	}{
		{"zero returns default", 0, defaultRecallLimit},
		{"negative returns default", -5, defaultRecallLimit},
		{"over max returns max", 100, maxRecallLimit},
		{"valid passthrough", 25, 25},
		{"exactly default", defaultRecallLimit, defaultRecallLimit},
		{"exactly max", maxRecallLimit, maxRecallLimit},
		{"one", 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, clampLimit(tt.input))
		})
	}
}

// ---------------------------------------------------------------------------
// Dimension filter tests
// ---------------------------------------------------------------------------

func TestHandleRecall_DimensionFilter(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		entityRecords: []memstore.Record{
			{ID: "r1", Dimension: "knowledge"},
			{ID: "r2", Dimension: "event"},
			{ID: "r3", Dimension: "knowledge"},
		},
	}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy:   "entity",
		EntityURNs: []string{"urn:1"},
		Dimension:  "knowledge",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	// Only records with dimension "knowledge" should pass the filter.
	assert.Equal(t, float64(2), data["count"])
}

func TestHandleRecall_LimitTrimming(t *testing.T) {
	t.Parallel()

	records := make([]memstore.Record, 20)
	for i := range records {
		records[i] = memstore.Record{ID: fmt.Sprintf("r%d", i), Dimension: "knowledge"}
	}
	store := &mockStore{entityRecords: records}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleRecall(ctx, nil, recallInput{
		Strategy:   "entity",
		EntityURNs: []string{"urn:1"},
		Limit:      5,
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, float64(5), data["count"])
}
