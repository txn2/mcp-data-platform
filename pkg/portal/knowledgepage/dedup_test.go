package knowledgepage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// fakeProber is a DuplicateProber stub returning a canned scored result (or error)
// so the dedup gate's filtering logic is exercised without a database.
type fakeProber struct {
	results []ScoredPage
	// perCall, when non-empty, returns a different result set per probe so a
	// multi-chunk candidate's merge across probes can be exercised.
	perCall [][]ScoredPage
	err     error
	gotEmb  []float32
	calls   int
}

func (f *fakeProber) SemanticSearch(_ context.Context, embedding []float32, _ int) ([]ScoredPage, error) {
	f.calls++
	f.gotEmb = embedding
	if len(f.perCall) > 0 {
		return f.perCall[(f.calls-1)%len(f.perCall)], f.err
	}
	return f.results, f.err
}

func sp(id, slug, title string, score float64) ScoredPage {
	return ScoredPage{Page: Page{ID: id, Slug: slug, Title: title}, Score: score}
}

func TestNearDuplicatePages(t *testing.T) {
	emb := [][]float32{{0.1, 0.2, 0.3}}
	tests := []struct {
		name       string
		embedding  [][]float32
		threshold  float64
		results    []ScoredPage
		searchErr  error
		wantIDs    []string
		wantNoCall bool
	}{
		{
			name:      "above threshold flagged",
			embedding: emb,
			threshold: 0.85,
			results:   []ScoredPage{sp("kp_1", "return-policy", "Return Policy", 0.91), sp("kp_2", "returns", "ACME Returns Policy", 0.86)},
			wantIDs:   []string{"kp_1", "kp_2"},
		},
		{
			name:      "below threshold dropped",
			embedding: emb,
			threshold: 0.85,
			results:   []ScoredPage{sp("kp_1", "a", "A", 0.84), sp("kp_2", "b", "B", 0.5)},
			wantIDs:   nil,
		},
		{
			name:      "boundary score equal to threshold is flagged",
			embedding: emb,
			threshold: 0.85,
			results:   []ScoredPage{sp("kp_1", "a", "A", 0.85)},
			wantIDs:   []string{"kp_1"},
		},
		{
			name:      "multiple above threshold preserved in order",
			embedding: emb,
			threshold: 0.85,
			results:   []ScoredPage{sp("kp_1", "a", "A", 0.99), sp("kp_2", "b", "B", 0.9)},
			wantIDs:   []string{"kp_1", "kp_2"},
		},
		{
			name:       "no embeddings disables gate (no search)",
			embedding:  nil,
			threshold:  0.85,
			results:    []ScoredPage{sp("kp_1", "a", "A", 0.99)},
			wantIDs:    nil,
			wantNoCall: true,
		},
		{
			name:       "non-positive threshold disables gate",
			embedding:  emb,
			threshold:  0,
			results:    []ScoredPage{sp("kp_1", "a", "A", 0.99)},
			wantIDs:    nil,
			wantNoCall: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeProber{results: tt.results, err: tt.searchErr}
			got, err := NearDuplicatePages(context.Background(), f, tt.embedding, tt.threshold)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNoCall && f.calls != 0 {
				t.Fatalf("expected no search call, got %d", f.calls)
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("got %d candidates, want %d (%v)", len(got), len(tt.wantIDs), got)
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Errorf("candidate %d: got id %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestNearDuplicatePages_SearchError(t *testing.T) {
	f := &fakeProber{err: errors.New("boom")}
	_, err := NearDuplicatePages(context.Background(), f, [][]float32{{0.1}}, 0.85)
	if err == nil {
		t.Fatal("expected error from search failure")
	}
}

// TestNearDuplicatePages_ProbesEveryCandidateChunk proves the gate sees the WHOLE
// candidate, not just its head: a candidate too large to embed in one call is
// probed with every chunk, and a page that only the second chunk resembles is
// still flagged (#1242). A page matched by more than one chunk keeps its highest
// score rather than the last one probed.
func TestNearDuplicatePages_ProbesEveryCandidateChunk(t *testing.T) {
	f := &fakeProber{perCall: [][]ScoredPage{
		{sp("kp_head", "head", "Head Match", 0.90), sp("kp_both", "both", "Both", 0.86)},
		{sp("kp_tail", "tail", "Tail Match", 0.95), sp("kp_both", "both", "Both", 0.93)},
	}}

	got, err := NearDuplicatePages(context.Background(), f, [][]float32{{0.1}, {0.2}}, 0.85)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.calls != 2 {
		t.Fatalf("probed %d times, want one per candidate chunk (2)", f.calls)
	}
	want := []struct {
		id    string
		score float64
	}{{"kp_tail", 0.95}, {"kp_both", 0.93}, {"kp_head", 0.90}}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d (%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Score != w.score {
			t.Errorf("candidate %d = (%s, %v), want (%s, %v)", i, got[i].ID, got[i].Score, w.id, w.score)
		}
	}
}

// TestNearDuplicatePages_SkipsEmptyVectors covers a provider that returned
// nothing for one chunk: the remaining probes still run rather than the gate
// issuing a meaningless zero-vector query.
func TestNearDuplicatePages_SkipsEmptyVectors(t *testing.T) {
	f := &fakeProber{results: []ScoredPage{sp("kp_1", "a", "A", 0.9)}}
	got, err := NearDuplicatePages(context.Background(), f, [][]float32{nil, {0.2}}, 0.85)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.calls != 1 {
		t.Fatalf("probed %d times, want 1 (the empty vector is skipped)", f.calls)
	}
	if len(got) != 1 || got[0].ID != "kp_1" {
		t.Fatalf("got %v, want the single flagged candidate", got)
	}
}

// TestCandidateEmbeddings covers the shared query-side composition: a candidate
// past the provider budget is embedded as several chunks, one inside it as one,
// and no configured provider disables the gate entirely.
func TestCandidateEmbeddings(t *testing.T) {
	ctx := context.Background()
	big := strings.Repeat("## Section\n\nprose about the topic.\n\n", 400)

	if got := CandidateEmbeddings(ctx, chunkProbeEmbedder{}, "T", big, nil); len(got) < 2 {
		t.Errorf("oversized candidate produced %d vectors; want one per chunk", len(got))
	}
	if got := CandidateEmbeddings(ctx, chunkProbeEmbedder{}, "T", "small body", nil); len(got) != 1 {
		t.Errorf("candidate inside the budget produced %d vectors; want 1", len(got))
	}
	if got := CandidateEmbeddings(ctx, nil, "T", "small body", nil); got != nil {
		t.Errorf("no provider must disable the gate, got %v", got)
	}
}

// chunkProbeEmbedder is a configured provider whose vectors are non-zero, so
// CandidateEmbeddings' degradation rules are not what the test is measuring.
type chunkProbeEmbedder struct{}

func (chunkProbeEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{0.1, 0.2}, nil
}

func (chunkProbeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0.1, 0.2}
	}
	return out, nil
}
func (chunkProbeEmbedder) Dimension() int { return 2 }
func (chunkProbeEmbedder) Kind() string   { return embedding.KindOllama }
