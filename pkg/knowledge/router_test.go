package knowledge

import (
	"context"
	"errors"
	"testing"
)

// fakeProvider is a Provider stub recording the caller it was queried with.
type fakeProvider struct {
	name      string
	scope     Scope
	hits      []Hit
	err       error
	called    bool
	gotCaller Caller
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Scope() Scope { return f.scope }
func (f *fakeProvider) Search(_ context.Context, q Query) ([]Hit, error) {
	f.called = true
	f.gotCaller = q.Caller
	return f.hits, f.err
}

// fakeEmbedder is an embedding.Provider that returns a fixed non-zero vector,
// so the router takes the hybrid path. Kind reports a non-noop value so
// EmbedForSearch treats it as configured.
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

func TestRouter_PerUserSkippedForAnonymousCaller(t *testing.T) {
	shared := &fakeProvider{name: "shared", scope: ScopeShared, hits: []Hit{{Source: "shared", Ref: "s1", Score: 1}}}
	perUser := &fakeProvider{name: "peruser", scope: ScopePerUser, hits: []Hit{{Source: "peruser", Ref: "p1", Score: 1}}}
	r := NewRouter(nil, shared, perUser)

	res, err := r.Search(context.Background(), Query{Intent: "anything"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shared.called {
		t.Error("shared provider should be queried for an anonymous caller")
	}
	if perUser.called {
		t.Error("per-user provider must NOT be queried for an anonymous caller")
	}
	if len(res.Hits) != 1 || res.Hits[0].Source != "shared" {
		t.Errorf("expected only the shared hit, got %+v", res.Hits)
	}
}

func TestRouter_PerUserQueriedWithIdentity(t *testing.T) {
	perUser := &fakeProvider{name: "peruser", scope: ScopePerUser, hits: []Hit{{Source: "peruser", Ref: "p1", Score: 1}}}
	r := NewRouter(nil, perUser)

	caller := Caller{UserID: "uuid-1", Email: "a@example.com"}
	_, err := r.Search(context.Background(), Query{Intent: "anything", Caller: caller})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !perUser.called {
		t.Fatal("per-user provider should be queried when the caller has identity")
	}
	if perUser.gotCaller != caller {
		t.Errorf("provider got caller %+v, want %+v", perUser.gotCaller, caller)
	}
}

func TestRouter_LexicalWhenNoEmbedder(t *testing.T) {
	p := &fakeProvider{name: "p", scope: ScopeShared}
	r := NewRouter(nil, p)
	res, err := r.Search(context.Background(), Query{Intent: "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Ranking != rankingLexical {
		t.Errorf("ranking = %q, want %q", res.Ranking, rankingLexical)
	}
}

func TestRouter_HybridWithEmbedder(t *testing.T) {
	var got Query
	p := &captureProvider{scope: ScopeShared, sink: &got}
	r := NewRouter(fakeEmbedder{}, p)
	res, err := r.Search(context.Background(), Query{Intent: "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Ranking != rankingHybrid {
		t.Errorf("ranking = %q, want %q", res.Ranking, rankingHybrid)
	}
	if len(got.Embedding) == 0 {
		t.Error("provider should receive the precomputed embedding")
	}
}

// captureProvider records the Query it received.
type captureProvider struct {
	scope Scope
	sink  *Query
}

func (captureProvider) Name() string   { return "capture" }
func (c captureProvider) Scope() Scope { return c.scope }
func (c captureProvider) Search(_ context.Context, q Query) ([]Hit, error) {
	*c.sink = q
	return nil, nil
}

func TestRouter_AllProvidersErrorReturnsError(t *testing.T) {
	boom := errors.New("boom")
	p1 := &fakeProvider{name: "p1", scope: ScopeShared, err: boom}
	p2 := &fakeProvider{name: "p2", scope: ScopeShared, err: boom}
	r := NewRouter(nil, p1, p2)

	_, err := r.Search(context.Background(), Query{Intent: "q"})
	if err == nil {
		t.Fatal("expected error when every queried provider fails")
	}
}

func TestRouter_PartialErrorTolerated(t *testing.T) {
	good := &fakeProvider{name: "good", scope: ScopeShared, hits: []Hit{{Source: "good", Ref: "g1", Score: 1}}}
	bad := &fakeProvider{name: "bad", scope: ScopeShared, err: errors.New("down")}
	r := NewRouter(nil, good, bad)

	res, err := r.Search(context.Background(), Query{Intent: "q"})
	if err != nil {
		t.Fatalf("a single provider failure must not fail the search: %v", err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Source != "good" {
		t.Errorf("expected the healthy provider's hit, got %+v", res.Hits)
	}
}

func TestRouter_LimitCapsResults(t *testing.T) {
	hits := make([]Hit, 5)
	for i := range hits {
		hits[i] = Hit{Source: "p", Ref: string(rune('a' + i)), Score: float64(i)}
	}
	p := &fakeProvider{name: "p", scope: ScopeShared, hits: hits}
	r := NewRouter(nil, p)

	res, err := r.Search(context.Background(), Query{Intent: "q", Limit: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Hits) != 3 {
		t.Errorf("len = %d, want 3 (limit)", len(res.Hits))
	}
}

func TestClampLimit(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{0, defaultLimit},
		{-5, defaultLimit},
		{3, 3},
		{maxLimit, maxLimit},
		{maxLimit + 1, maxLimit},
	}
	for _, tt := range tests {
		if got := clampLimit(tt.in); got != tt.want {
			t.Errorf("clampLimit(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestRouter_Providers(t *testing.T) {
	p := &fakeProvider{name: "p", scope: ScopeShared}
	r := NewRouter(nil, p)
	if got := r.Providers(); len(got) != 1 || got[0] != p {
		t.Errorf("Providers() = %+v, want [p]", got)
	}
}

func TestScope_String(t *testing.T) {
	if ScopeShared.String() != "shared" || ScopePerUser.String() != "per_user" {
		t.Errorf("unexpected Scope strings: %q %q", ScopeShared, ScopePerUser)
	}
	if Scope(99).String() != "unknown" {
		t.Errorf("unexpected unknown scope: %q", Scope(99))
	}
}

func TestCaller_Anonymous(t *testing.T) {
	if !(Caller{}).Anonymous() {
		t.Error("empty caller should be anonymous")
	}
	if (Caller{Email: "a@example.com"}).Anonymous() {
		t.Error("caller with email is not anonymous")
	}
	if (Caller{UserID: "u"}).Anonymous() {
		t.Error("caller with user id is not anonymous")
	}
}
