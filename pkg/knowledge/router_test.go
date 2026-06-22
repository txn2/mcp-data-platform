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

// flatHits flattens a router result's grouped display set into one slice, for
// tests that only care about which hits surfaced rather than their grouping.
func flatHits(res Result) []Hit {
	var hits []Hit
	for _, g := range res.Groups {
		hits = append(hits, g.Hits...)
	}
	return hits
}

func TestRouter_PerUserSkippedForAnonymousCaller(t *testing.T) {
	shared := &fakeProvider{name: "shared", scope: ScopeShared, hits: []Hit{{Source: "shared", Ref: "s1", Score: 1}}}
	perUser := &fakeProvider{name: "peruser", scope: ScopePerUser, hits: []Hit{{Source: "peruser", Ref: "p1", Score: 1}}}
	r := NewRouter(nil, nil, shared, perUser)

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
	if hits := flatHits(res); len(hits) != 1 || hits[0].Source != "shared" {
		t.Errorf("expected only the shared hit, got %+v", hits)
	}
}

func TestRouter_SourcesNarrowsButNeverWidens(t *testing.T) {
	datahub := &fakeProvider{name: "datahub", scope: ScopeShared, hits: []Hit{{Source: "datahub", Ref: "d1", Score: 1}}}
	memory := &fakeProvider{name: "memory", scope: ScopePerUser, hits: []Hit{{Source: "memory", Ref: "m1", Score: 1}}}
	r := NewRouter(nil, nil, datahub, memory)

	// Narrow to datahub only: memory is skipped even though the caller has identity.
	caller := Caller{Email: "a@example.com"}
	res, err := r.Search(context.Background(), Query{Intent: "q", Caller: caller, Sources: []string{"datahub"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !datahub.called {
		t.Error("datahub should be queried when named in sources")
	}
	if memory.called {
		t.Error("memory must NOT be queried when sources narrows to datahub")
	}
	if hits := flatHits(res); len(hits) != 1 || hits[0].Source != "datahub" {
		t.Errorf("expected only datahub, got %+v", hits)
	}
}

func TestRouter_SourcesCannotWidenPastScope(t *testing.T) {
	// An anonymous caller naming a per-user source must still get nothing from
	// it: sources narrows, it never opts past the scope gate.
	memory := &fakeProvider{name: "memory", scope: ScopePerUser, hits: []Hit{{Source: "memory", Ref: "m1", Score: 1}}}
	r := NewRouter(nil, nil, memory)
	res, err := r.Search(context.Background(), Query{Intent: "q", Sources: []string{"memory"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if memory.called {
		t.Error("per-user provider must not be queried for an anonymous caller even when named in sources")
	}
	if hits := flatHits(res); len(hits) != 0 {
		t.Errorf("expected no hits, got %+v", hits)
	}
}

func TestRouter_BlankSourcesQueriesEverything(t *testing.T) {
	a := &fakeProvider{name: "a", scope: ScopeShared, hits: []Hit{{Source: "a", Ref: "a1", Score: 1}}}
	b := &fakeProvider{name: "b", scope: ScopeShared, hits: []Hit{{Source: "b", Ref: "b1", Score: 1}}}
	r := NewRouter(nil, nil, a, b)
	// A sources slice of only blanks collapses to "no narrowing".
	_, err := r.Search(context.Background(), Query{Intent: "q", Sources: []string{"  ", ""}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.called || !b.called {
		t.Error("blank-only sources should query every provider")
	}
}

func TestRouter_PerUserQueriedWithIdentity(t *testing.T) {
	perUser := &fakeProvider{name: "peruser", scope: ScopePerUser, hits: []Hit{{Source: "peruser", Ref: "p1", Score: 1}}}
	r := NewRouter(nil, nil, perUser)

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
	r := NewRouter(nil, nil, p)
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
	r := NewRouter(fakeEmbedder{}, nil, p)
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

// fakeLineage expands every input urn to itself plus a fixed neighbor.
type fakeLineage struct{ neighbor string }

func (l fakeLineage) Expand(_ context.Context, urns []string) []string {
	return append(append([]string{}, urns...), l.neighbor)
}

func TestRouter_LineageExpandsEntityURNsForAllProviders(t *testing.T) {
	var got1, got2 Query
	p1 := &captureProvider{scope: ScopeShared, sink: &got1}
	p2 := &captureProvider{scope: ScopeShared, sink: &got2}
	r := NewRouter(nil, fakeLineage{neighbor: "urn:b"}, p1, p2)

	if _, err := r.Search(context.Background(), Query{EntityURNs: []string{"urn:a"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expansion runs once on the router and every provider sees the widened set,
	// so providers never each re-hit the lineage API.
	for i, got := range []Query{got1, got2} {
		if len(got.EntityURNs) != 2 {
			t.Fatalf("provider %d got %d urns, want 2 (expanded): %+v", i, len(got.EntityURNs), got.EntityURNs)
		}
	}
}

func TestRouter_NoLineageLeavesEntityURNsUnchanged(t *testing.T) {
	var got Query
	p := &captureProvider{scope: ScopeShared, sink: &got}
	r := NewRouter(nil, nil, p)
	if _, err := r.Search(context.Background(), Query{EntityURNs: []string{"urn:a"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.EntityURNs) != 1 || got.EntityURNs[0] != "urn:a" {
		t.Errorf("entity urns = %+v, want unchanged [urn:a]", got.EntityURNs)
	}
}

func TestRouter_AllProvidersErrorReturnsError(t *testing.T) {
	boom := errors.New("boom")
	p1 := &fakeProvider{name: "p1", scope: ScopeShared, err: boom}
	p2 := &fakeProvider{name: "p2", scope: ScopeShared, err: boom}
	r := NewRouter(nil, nil, p1, p2)

	_, err := r.Search(context.Background(), Query{Intent: "q"})
	if err == nil {
		t.Fatal("expected error when every queried provider fails")
	}
}

func TestRouter_PartialErrorTolerated(t *testing.T) {
	good := &fakeProvider{name: "good", scope: ScopeShared, hits: []Hit{{Source: "good", Ref: "g1", Score: 1}}}
	bad := &fakeProvider{name: "bad", scope: ScopeShared, err: errors.New("down")}
	r := NewRouter(nil, nil, good, bad)

	res, err := r.Search(context.Background(), Query{Intent: "q"})
	if err != nil {
		t.Fatalf("a single provider failure must not fail the search: %v", err)
	}
	if hits := flatHits(res); len(hits) != 1 || hits[0].Source != "good" {
		t.Errorf("expected the healthy provider's hit, got %+v", hits)
	}
}

func TestRouter_LimitCapsResults(t *testing.T) {
	hits := make([]Hit, 5)
	for i := range hits {
		hits[i] = Hit{Source: "p", Ref: string(rune('a' + i)), Score: float64(i)}
	}
	p := &fakeProvider{name: "p", scope: ScopeShared, hits: hits}
	r := NewRouter(nil, nil, p)

	res, err := r.Search(context.Background(), Query{Intent: "q", Limit: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits := flatHits(res); len(hits) != 3 {
		t.Errorf("len = %d, want 3 (limit)", len(hits))
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
	r := NewRouter(nil, nil, p)
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
