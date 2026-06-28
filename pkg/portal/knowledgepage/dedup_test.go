package knowledgepage

import (
	"context"
	"errors"
	"testing"
)

// fakeProber is a DuplicateProber stub returning a canned scored result (or error)
// so the dedup gate's filtering logic is exercised without a database.
type fakeProber struct {
	results []ScoredPage
	err     error
	gotEmb  []float32
	calls   int
}

func (f *fakeProber) SemanticSearch(_ context.Context, embedding []float32, _ int) ([]ScoredPage, error) {
	f.calls++
	f.gotEmb = embedding
	return f.results, f.err
}

func sp(id, slug, title string, score float64) ScoredPage {
	return ScoredPage{Page: Page{ID: id, Slug: slug, Title: title}, Score: score}
}

func TestNearDuplicatePages(t *testing.T) {
	emb := []float32{0.1, 0.2, 0.3}
	tests := []struct {
		name       string
		embedding  []float32
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
			name:       "nil embedding disables gate (no search)",
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
	_, err := NearDuplicatePages(context.Background(), f, []float32{0.1}, 0.85)
	if err == nil {
		t.Fatal("expected error from search failure")
	}
}
