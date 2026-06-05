package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// stubEmbedder is a configured (non-noop) embedding provider returning a
// fixed vector, so the hybrid path can be exercised without a network.
type stubEmbedder struct {
	vec []float32
	err error
}

func (s stubEmbedder) Embed(_ context.Context, _ string) ([]float32, error) { return s.vec, s.err }
func (stubEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}
func (stubEmbedder) Dimension() int { return 768 }
func (stubEmbedder) Kind() string   { return embedding.KindOllama }

// recallToolkit builds a knowledge toolkit whose store is the real
// memory-backed adapter over the given mock store, so handler tests
// exercise the real Search path (and the owner-scope predicate it builds).
func recallToolkit(store *mockMemoryStore, emb embedding.Provider) *Toolkit {
	return &Toolkit{store: NewMemoryInsightAdapter(store), embedder: emb}
}

// parseRecallOutput extracts the recall_insight JSON payload from a result.
func parseRecallOutput(t *testing.T, res *mcp.CallToolResult) recallInsightOutput {
	t.Helper()
	require.False(t, res.IsError, "expected a success result, got error: %s", textContent(t, res))
	var out recallInsightOutput
	require.NoError(t, json.Unmarshal([]byte(textContent(t, res)), &out))
	return out
}

func textContent(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "content is not text")
	return tc.Text
}

func TestHandleRecallInsight_HybridWhenEmbedderConfigured(t *testing.T) {
	store := &mockMemoryStore{
		searchResult: []memory.ScoredRecord{
			{Record: memory.Record{ID: "i1", CreatedBy: "sarah@example.com", Content: "churn rules"}, Score: 0.91},
		},
	}
	tk := recallToolkit(store, stubEmbedder{vec: []float32{0.1, 0.2, 0.3}})

	res, _, err := tk.handleRecallInsight(
		ctxWithUser("sarah@example.com", "sess", "analyst"), nil,
		recallInsightInput{Query: "churn", Limit: 5},
	)
	require.NoError(t, err)
	out := parseRecallOutput(t, res)

	assert.Equal(t, rankingHybrid, out.Ranking)
	require.Len(t, out.Insights, 1)
	assert.Equal(t, "i1", out.Insights[0].Insight.ID)
	assert.InDelta(t, 0.91, out.Insights[0].Score, 1e-9)
	assert.Equal(t, 1, out.Count)

	// Proves the caller's email reached the store's owner predicate through
	// the real adapter Search, on the hybrid arm.
	require.NotNil(t, store.lastHybridQ, "configured embedder must select the hybrid arm")
	assert.Nil(t, store.lastLexicalQ)
	assert.Equal(t, "sarah@example.com", store.lastHybridQ.CreatedBy)
	assert.Equal(t, memory.DimensionKnowledge, store.lastHybridQ.Dimension)
}

func TestHandleRecallInsight_LexicalFallbackPaths(t *testing.T) {
	cases := []struct {
		name string
		emb  embedding.Provider
	}{
		{"nil embedder", nil},
		{"noop embedder", embedding.NewNoopProvider(768)},
		{"zero vector", stubEmbedder{vec: []float32{0, 0, 0}}},
		{"embed error", stubEmbedder{err: errors.New("boom")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockMemoryStore{}
			tk := recallToolkit(store, tc.emb)

			res, _, err := tk.handleRecallInsight(
				ctxWithUser("u@example.com", "s", "analyst"), nil,
				recallInsightInput{Query: "churn"},
			)
			require.NoError(t, err)
			out := parseRecallOutput(t, res)

			assert.Equal(t, rankingLexical, out.Ranking)
			require.NotNil(t, store.lastLexicalQ, "must take the lexical arm")
			assert.Nil(t, store.lastHybridQ)
			assert.Equal(t, "u@example.com", store.lastLexicalQ.CreatedBy)
		})
	}
}

func TestHandleRecallInsight_EmptyQuery(t *testing.T) {
	store := &mockMemoryStore{}
	tk := recallToolkit(store, nil)

	for _, q := range []string{"", "   "} {
		res, _, err := tk.handleRecallInsight(
			ctxWithUser("u@example.com", "s", "analyst"), nil,
			recallInsightInput{Query: q},
		)
		require.NoError(t, err)
		assert.True(t, res.IsError)
		assert.Nil(t, store.lastLexicalQ, "no search runs without a query")
		assert.Nil(t, store.lastHybridQ)
	}
}

func TestHandleRecallInsight_FailsClosedWithoutEmail(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
	}{
		{"no platform context", context.Background()},
		{"empty email", middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{PersonaName: "analyst"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockMemoryStore{}
			tk := recallToolkit(store, nil)

			res, _, err := tk.handleRecallInsight(tc.ctx, nil, recallInsightInput{Query: "churn"})
			require.NoError(t, err)
			assert.True(t, res.IsError, "missing identity must fail closed")
			assert.Nil(t, store.lastLexicalQ, "no unscoped search may run")
			assert.Nil(t, store.lastHybridQ)
		})
	}
}

func TestHandleRecallInsight_StoreWithoutSearchCapability(t *testing.T) {
	// The noop store does not implement insightSearcher, so recall is
	// unavailable (memory layer disabled).
	tk := &Toolkit{store: NewNoopStore()}

	res, _, err := tk.handleRecallInsight(
		ctxWithUser("u@example.com", "s", "analyst"), nil,
		recallInsightInput{Query: "churn"},
	)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, textContent(t, res), "memory layer is not enabled")
}

func TestHandleRecallInsight_SearchError(t *testing.T) {
	store := &mockMemoryStore{searchErr: errors.New("db down")}
	tk := recallToolkit(store, nil)

	res, _, err := tk.handleRecallInsight(
		ctxWithUser("u@example.com", "s", "analyst"), nil,
		recallInsightInput{Query: "churn"},
	)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, textContent(t, res), "failed to search insights")
}

func TestTools_IncludesRecallOnlyWithSearchCapability(t *testing.T) {
	withSearch := &Toolkit{store: NewMemoryInsightAdapter(&mockMemoryStore{})}
	assert.Contains(t, withSearch.Tools(), recallToolName)

	noSearch := &Toolkit{store: NewNoopStore()}
	assert.NotContains(t, noSearch.Tools(), recallToolName)
}

func TestHandleRecallInsight_ClampsLimitToMax(t *testing.T) {
	store := &mockMemoryStore{}
	tk := recallToolkit(store, nil)

	_, _, err := tk.handleRecallInsight(
		ctxWithUser("u@example.com", "s", "analyst"), nil,
		recallInsightInput{Query: "churn", Limit: 1000},
	)
	require.NoError(t, err)
	require.NotNil(t, store.lastLexicalQ)
	assert.Equal(t, MaxLimit, store.lastLexicalQ.Limit, "an over-large limit is capped at MaxLimit before the store sees it")
}
