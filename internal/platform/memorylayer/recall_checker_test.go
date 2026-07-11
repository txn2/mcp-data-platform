package memorylayer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
)

// recallFakeStore implements memory.Store; only VectorSearch is meaningful, the
// rest satisfy the interface. It records the query it received.
type recallFakeStore struct {
	results []memory.ScoredRecord
	err     error
	gotQ    memory.VectorQuery
	called  bool
}

func (s *recallFakeStore) VectorSearch(_ context.Context, q memory.VectorQuery) ([]memory.ScoredRecord, error) {
	s.called = true
	s.gotQ = q
	return s.results, s.err
}

func (*recallFakeStore) Insert(context.Context, memory.Record) error { return nil }
func (*recallFakeStore) Get(context.Context, string) (*memory.Record, error) {
	return &memory.Record{}, nil
}
func (*recallFakeStore) Update(context.Context, string, memory.RecordUpdate) error { return nil }
func (*recallFakeStore) Delete(context.Context, string) error                      { return nil }
func (*recallFakeStore) List(context.Context, memory.Filter) ([]memory.Record, int, error) {
	return nil, 0, nil
}

func (*recallFakeStore) HybridSearch(context.Context, memory.HybridQuery) ([]memory.ScoredRecord, error) {
	return nil, nil
}

func (*recallFakeStore) LexicalSearch(context.Context, memory.LexicalQuery) ([]memory.ScoredRecord, error) {
	return nil, nil
}

func (*recallFakeStore) EntityLookup(context.Context, string, string, string) ([]memory.Record, error) {
	return nil, nil
}
func (*recallFakeStore) MarkStale(context.Context, []string, string) error { return nil }
func (*recallFakeStore) MarkVerified(context.Context, []string) error      { return nil }
func (*recallFakeStore) Supersede(context.Context, string, string) error   { return nil }

var _ memory.Store = (*recallFakeStore)(nil)

func scoredRec(id string, score float64, urns ...string) memory.ScoredRecord {
	return memory.ScoredRecord{Record: memory.Record{ID: id, EntityURNs: urns}, Score: score}
}

func TestMemoryRecallChecker_Matches(t *testing.T) {
	emb := []float32{0.1, 0.2, 0.3}

	t.Run("no embedding skips search", func(t *testing.T) {
		store := &recallFakeStore{results: []memory.ScoredRecord{scoredRec("x", 0.99)}}
		c := &recallChecker{store: store}
		matches, err := c.Matches(context.Background(), memorykit.RecallQuery{CallerEmail: "a@x.com", MinScore: 0.9})
		require.NoError(t, err)
		assert.Empty(t, matches)
		assert.False(t, store.called, "must not search without an embedding")
	})

	t.Run("no caller skips search", func(t *testing.T) {
		store := &recallFakeStore{}
		c := &recallChecker{store: store}
		matches, err := c.Matches(context.Background(), memorykit.RecallQuery{Embedding: emb, MinScore: 0.9})
		require.NoError(t, err)
		assert.Empty(t, matches)
		assert.False(t, store.called)
	})

	t.Run("matches returned and query scoped to caller's active records", func(t *testing.T) {
		store := &recallFakeStore{results: []memory.ScoredRecord{scoredRec("m1", 0.95), scoredRec("m2", 0.92)}}
		c := &recallChecker{store: store}
		matches, err := c.Matches(context.Background(), memorykit.RecallQuery{
			Embedding: emb, CallerEmail: "a@x.com", MinScore: 0.9,
		})
		require.NoError(t, err)
		assert.Equal(t, []memorykit.RecallMatch{{ID: "m1", Score: 0.95}, {ID: "m2", Score: 0.92}}, matches)
		assert.Equal(t, "a@x.com", store.gotQ.CreatedBy)
		assert.Equal(t, 0.9, store.gotQ.MinScore)
		assert.Equal(t, recallCandidateK, store.gotQ.Limit)
		assert.Equal(t, emb, store.gotQ.Embedding)
		// Regression (#762): recall must exclude superseded records. Without
		// that a superseded predecessor can outrank its active successor,
		// absorb the supersede, and leave two active duplicates standing.
		// Stale records stay matchable (a restatement corrects them), so the
		// scope is an exclusion, not a Status=active restriction.
		assert.Equal(t, []string{memory.StatusSuperseded}, store.gotQ.ExcludeStatuses)
		assert.Empty(t, store.gotQ.Status)
	})

	t.Run("below threshold yields no match", func(t *testing.T) {
		store := &recallFakeStore{results: []memory.ScoredRecord{scoredRec("m1", 0.5)}}
		c := &recallChecker{store: store}
		matches, err := c.Matches(context.Background(), memorykit.RecallQuery{
			Embedding: emb, CallerEmail: "a@x.com", MinScore: 0.9,
		})
		require.NoError(t, err)
		assert.Empty(t, matches)
	})

	t.Run("entity-URN gate skips non-matching higher score", func(t *testing.T) {
		// Top hit is about a different table; the second shares the queried URN.
		store := &recallFakeStore{results: []memory.ScoredRecord{
			scoredRec("other-table", 0.97, "urn:li:dataset:B"),
			scoredRec("same-table", 0.93, "urn:li:dataset:A"),
		}}
		c := &recallChecker{store: store}
		matches, err := c.Matches(context.Background(), memorykit.RecallQuery{
			Embedding: emb, CallerEmail: "a@x.com", MinScore: 0.9,
			EntityURNs: []string{"urn:li:dataset:A"},
		})
		require.NoError(t, err)
		assert.Equal(t, []memorykit.RecallMatch{{ID: "same-table", Score: 0.93}}, matches,
			"must not supersede knowledge about a different entity")
	})

	t.Run("entity-URN gate with no overlap yields no match", func(t *testing.T) {
		store := &recallFakeStore{results: []memory.ScoredRecord{scoredRec("other", 0.99, "urn:li:dataset:B")}}
		c := &recallChecker{store: store}
		matches, err := c.Matches(context.Background(), memorykit.RecallQuery{
			Embedding: emb, CallerEmail: "a@x.com", MinScore: 0.9,
			EntityURNs: []string{"urn:li:dataset:A"},
		})
		require.NoError(t, err)
		assert.Empty(t, matches)
	})

	t.Run("store error propagates", func(t *testing.T) {
		store := &recallFakeStore{err: errors.New("boom")}
		c := &recallChecker{store: store}
		_, err := c.Matches(context.Background(), memorykit.RecallQuery{
			Embedding: emb, CallerEmail: "a@x.com", MinScore: 0.9,
		})
		require.Error(t, err)
	})
}
