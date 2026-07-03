//go:build integration

package platform

// Real-Postgres acceptance test for #762: rapid restatements of the same fact
// must consolidate to a single active record, not accumulate active
// near-duplicates. It wires the real capture pipeline (memory toolkit ->
// memoryRecallChecker -> postgres store with pgvector) and captures the same
// content three times. Before the fix, the third capture's recall could match
// the already-superseded first record (VectorSearch had no status scope),
// re-supersede it, and leave two active duplicates standing — exactly the pair
// observed on a live deployment. Run under `make test-realdb`.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
)

// fixedVecEmbedder is a real (non-noop) provider returning one fixed vector,
// so every capture in the test embeds identically (cosine 1.0).
type fixedVecEmbedder struct{ vec []float32 }

func (f fixedVecEmbedder) Embed(context.Context, string) ([]float32, error) { return f.vec, nil }
func (f fixedVecEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = f.vec
	}
	return out, nil
}
func (f fixedVecEmbedder) Dimension() int { return len(f.vec) }
func (fixedVecEmbedder) Kind() string     { return "test" }

func TestRealDB_MemoryCaptureDedup_ConsolidatesRestatements(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	store := memory.NewPostgresStore(db)
	vec := make([]float32, 768)
	vec[0] = 1

	tk, err := memorykit.New("memory", store, fixedVecEmbedder{vec: vec})
	require.NoError(t, err)
	tk.SetRecallChecker(&memoryRecallChecker{store: store})

	const owner = "dedup@example.com"
	capture := func(content string) *memorykit.CaptureResult {
		res, err := tk.AutoCapture(ctx, memorykit.AutoCaptureInput{
			SinkClass: memory.SinkBusinessKnowledge,
			Content:   content,
			CreatedBy: owner,
		})
		require.NoError(t, err)
		return res
	}

	r1 := capture("The orders feed refreshes nightly at 2am UTC.")
	assert.Empty(t, r1.Superseded, "first capture has nothing to supersede")

	r2 := capture("The orders feed is refreshed each night at 2am UTC.")
	assert.Equal(t, []string{r1.ID}, r2.Superseded, "a restatement supersedes the original")

	// The regression: recall for the third capture must match the ACTIVE second
	// record, not the already-superseded first one.
	r3 := capture("Orders data refreshes nightly at 2am UTC.")
	assert.Equal(t, []string{r2.ID}, r3.Superseded,
		"the third capture must supersede the active duplicate, not re-supersede the dead one")

	active, _, err := store.List(ctx, memory.Filter{CreatedBy: owner, Status: memory.StatusActive, Limit: 50})
	require.NoError(t, err)
	require.Len(t, active, 1, "rapid restatements must consolidate to a single active record (#762)")
	assert.Equal(t, r3.ID, active[0].ID)

	// Stale records must remain supersedable: a restatement is exactly how a
	// stale record gets corrected (superseded rows are the only recall
	// exclusion beyond archived).
	require.NoError(t, store.MarkStale(ctx, []string{r3.ID}, "schema drift"))
	r4 := capture("Orders data is refreshed nightly at 2am UTC.")
	assert.Equal(t, []string{r3.ID}, r4.Superseded, "a capture restating a stale record must supersede it")
}
