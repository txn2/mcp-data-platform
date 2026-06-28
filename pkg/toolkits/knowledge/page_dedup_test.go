package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// fakeEmbedder is an embedding.Provider that returns a fixed non-zero vector so
// EmbedForSearch treats it as configured (Kind != noop) and the dedup gate runs.
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func (fakeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2, 0.3}
	}
	return out, nil
}

func (fakeEmbedder) Dimension() int { return 3 }
func (fakeEmbedder) Kind() string   { return "fake" }

// guardedApplyToolkit builds an apply toolkit with the dedup gate active: a real
// embedder and the given threshold/oversize config.
func guardedApplyToolkit(t *testing.T, store InsightStore, pw pageWriter, cfg knowledgepage.PageGuards) *Toolkit {
	t.Helper()
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})
	tk.SetPageWriter(pw)
	tk.SetPageGuards(cfg, fakeEmbedder{})
	return tk
}

// TestPromoteToPage_BlocksNearDuplicate proves the create-time dedup gate (#705):
// promoting a page whose slug does not exist but whose content is highly similar to
// an existing page is rejected with the candidate pages returned, and no page is
// written.
func TestPromoteToPage_BlocksNearDuplicate(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}}
	pw := newFakePageWriter()
	pw.searchResults = []knowledgepage.ScoredPage{
		{Page: knowledgepage.Page{ID: "kp_existing", Slug: "retail-seasons", Title: "Retail Seasons"}, Score: 0.93},
	}
	tk := guardedApplyToolkit(t, store, pw, knowledgepage.PageGuards{DedupThreshold: 0.82})

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
	require.NoError(t, err)
	require.False(t, res.IsError, "a duplicate block is a structured result, not an error")
	out := parseJSONResult(t, res)

	assert.Equal(t, true, out["duplicate_blocked"])
	candidates, ok := out["candidates"].([]any)
	require.True(t, ok, "candidates should be present")
	require.Len(t, candidates, 1)
	first, _ := candidates[0].(map[string]any)
	assert.Equal(t, "kp_existing", first["id"])
	assert.Equal(t, "retail-seasons", first["slug"])

	// Nothing was written and no insight was marked applied.
	assert.Empty(t, pw.inserted, "blocked create must not insert a page")
	assert.Empty(t, store.MarkAppliedCalls, "blocked create must not mark insights applied")
	assert.Equal(t, 1, pw.searchCalls, "the gate ran the dedup probe exactly once")
}

// TestPromoteToPage_ForceNewBypassesGate proves force_new overrides the gate: a
// near-duplicate is created anyway, and the dedup probe is never run.
func TestPromoteToPage_ForceNewBypassesGate(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}}
	pw := newFakePageWriter()
	pw.searchResults = []knowledgepage.ScoredPage{
		{Page: knowledgepage.Page{ID: "kp_existing", Slug: "retail-seasons", Title: "Retail Seasons"}, Score: 0.99},
	}
	tk := guardedApplyToolkit(t, store, pw, knowledgepage.PageGuards{DedupThreshold: 0.82})

	in := applyPageInput([]string{"i1"})
	in.Page.ForceNew = true
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, in)
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := parseJSONResult(t, res)

	assert.Equal(t, "created", out["action"])
	require.Len(t, pw.inserted, 1, "force_new creates the page despite the near-duplicate")
	assert.Equal(t, 0, pw.searchCalls, "force_new short-circuits before the dedup probe")
}

// TestPromoteToPage_SlugHitSkipsGate proves a slug that already exists is treated
// as an update (the find-or-create consolidation), never gated, even when the
// content would score as a near-duplicate.
func TestPromoteToPage_SlugHitSkipsGate(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}}
	pw := newFakePageWriter()
	pw.pages["seasons"] = &knowledgepage.Page{ID: "kp1", Slug: "seasons", CurrentVersion: 1}
	pw.searchResults = []knowledgepage.ScoredPage{
		{Page: knowledgepage.Page{ID: "kp_other", Slug: "other", Title: "Other"}, Score: 0.99},
	}
	tk := guardedApplyToolkit(t, store, pw, knowledgepage.PageGuards{DedupThreshold: 0.82})

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := parseJSONResult(t, res)

	assert.Equal(t, "updated", out["action"])
	assert.Equal(t, 0, pw.searchCalls, "a slug hit is an update; the gate is not consulted")
}

// TestPromoteToPage_GateNoOpWithoutEmbedder proves the gate degrades safely: with
// no real embedding provider the similarity score is not thresholdable, so the
// create proceeds and the probe is never run.
func TestPromoteToPage_GateNoOpWithoutEmbedder(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}}
	pw := newFakePageWriter()
	pw.searchResults = []knowledgepage.ScoredPage{
		{Page: knowledgepage.Page{ID: "kp_existing", Slug: "x", Title: "X"}, Score: 0.99},
	}
	// Threshold set, but no embedder wired (SetPageGuards with nil provider).
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})
	tk.SetPageWriter(pw)
	tk.SetPageGuards(knowledgepage.PageGuards{DedupThreshold: 0.82}, nil)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := parseJSONResult(t, res)
	assert.Equal(t, "created", out["action"], "without a real embedder the create proceeds unguarded")
	assert.Equal(t, 0, pw.searchCalls)
}

// TestPromoteToPage_SplitSuggestion proves the oversized-page soft signal (#705
// Part B): a page over the size threshold still writes, and the response carries a
// non-blocking split suggestion.
func TestPromoteToPage_SplitSuggestion(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}}
	pw := newFakePageWriter()
	tk := guardedApplyToolkit(t, store, pw, knowledgepage.PageGuards{DedupThreshold: 0.82, OversizeBytes: 20})

	in := applyPageInput([]string{"i1"})
	in.Page.Body = strings.Repeat("long body content ", 5) // > 20 bytes
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, in)
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := parseJSONResult(t, res)

	assert.Equal(t, "created", out["action"], "the oversized signal never blocks the write")
	require.Contains(t, out, "split_suggestion")
	assert.Contains(t, out["split_suggestion"], "splitting")
	require.Len(t, pw.inserted, 1)
}
