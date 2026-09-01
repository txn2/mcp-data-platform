package knowledge

import (
	"context"
	"fmt"
	"testing"
)

// corpusProvider models a real provider: it holds a corpus and returns at most
// q.Limit of it, ranked. The router's capped signal is a fact about that
// interaction, so a provider that ignores the limit cannot prove it (#1585).
type corpusProvider struct {
	name     string
	corpus   int
	gotLimit int
	withheld int
}

func (p *corpusProvider) Name() string { return p.name }
func (*corpusProvider) Scope() Scope   { return ScopeShared }

func (p *corpusProvider) Search(_ context.Context, q Query) ([]Hit, error) {
	p.gotLimit = q.Limit
	q.Caller.withhold(p.withheld)
	n := p.corpus
	if q.Limit > 0 && n > q.Limit {
		n = q.Limit
	}
	hits := make([]Hit, n)
	for i := range hits {
		hits[i] = Hit{Source: p.name, Ref: fmt.Sprintf("r%03d", i), Score: float64(p.corpus - i)}
	}
	return hits, nil
}

// coverageOf finds one source's coverage entry.
func coverageOf(t *testing.T, res Result, source string) SourceCoverage {
	t.Helper()
	for _, c := range res.Coverage {
		if c.Source == source {
			return c
		}
	}
	t.Fatalf("no coverage for %q in %+v", source, res.Coverage)
	return SourceCoverage{}
}

func TestCandidateDepth(t *testing.T) {
	tests := []struct {
		name   string
		budget int
		want   int
	}{
		{"below the floor keeps the floor", 10, candidateLimitPerSource},
		{"at the floor keeps the floor", candidateLimitPerSource, candidateLimitPerSource},
		{"above the floor is the budget", 50, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := candidateDepth(tt.budget); got != tt.want {
				t.Errorf("candidateDepth(%d) = %d, want %d", tt.budget, got, tt.want)
			}
		})
	}
}

func TestTrimToDepth(t *testing.T) {
	hits := func(n int) []Hit {
		out := make([]Hit, n)
		for i := range out {
			out[i] = Hit{Ref: fmt.Sprintf("r%d", i)}
		}
		return out
	}
	tests := []struct {
		name       string
		in         []Hit
		depth      int
		wantLen    int
		wantCapped bool
	}{
		{"under the depth is untouched", hits(3), 25, 3, false},
		{"exactly the depth is untouched", hits(25), 25, 25, false},
		{"one past the depth is the probe", hits(26), 25, 25, true},
		{"a provider ignoring the limit is trimmed", hits(400), 25, 25, true},
		{"a zero depth trims nothing", hits(3), 0, 3, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, capped := trimToDepth(tt.in, tt.depth)
			if len(got) != tt.wantLen || capped != tt.wantCapped {
				t.Errorf("trimToDepth(%d hits, %d) = %d hits, capped %v; want %d, %v",
					len(tt.in), tt.depth, len(got), capped, tt.wantLen, tt.wantCapped)
			}
		})
	}
}

// TestSearch_CappedSourceReportsMatchedAsAFloor is the defect: a source with
// more matches than the router ranked reported the bound as if it were the
// count, so "matched 25, shown 25" read as "you have seen everything".
func TestSearch_CappedSourceReportsMatchedAsAFloor(t *testing.T) {
	deep := &corpusProvider{name: "deep", corpus: 500}
	shallow := &corpusProvider{name: "shallow", corpus: 4}
	r := NewRouter(nil, nil, deep, shallow)

	res, err := r.Search(context.Background(), Query{Intent: "x", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	got := coverageOf(t, res, "deep")
	if got.Matched != candidateLimitPerSource || !got.MatchedCapped {
		t.Errorf("deep coverage = %+v, want matched %d flagged capped", got, candidateLimitPerSource)
	}
	got = coverageOf(t, res, "shallow")
	if got.Matched != 4 || got.MatchedCapped {
		t.Errorf("shallow coverage = %+v, want an exact matched 4 with no flag", got)
	}
}

// TestSearch_ExactlyTheDepthIsNotFlagged is the boundary the flag exists to
// distinguish: a source holding exactly as many matches as the router ranks has
// an exact count, and flagging it would be the same lie in the other direction.
func TestSearch_ExactlyTheDepthIsNotFlagged(t *testing.T) {
	exact := &corpusProvider{name: "exact", corpus: candidateLimitPerSource}
	r := NewRouter(nil, nil, exact)

	res, err := r.Search(context.Background(), Query{Intent: "x", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	got := coverageOf(t, res, "exact")
	if got.Matched != candidateLimitPerSource || got.MatchedCapped {
		t.Errorf("exact coverage = %+v, want matched %d with no capped flag", got, candidateLimitPerSource)
	}
}

// TestSearch_ProbeIsNeverDisplayed holds that the extra candidate the router
// asks for is a signal and not a result: it must not widen the display set past
// the budget the caller asked for.
func TestSearch_ProbeIsNeverDisplayed(t *testing.T) {
	deep := &corpusProvider{name: "deep", corpus: 500}
	r := NewRouter(nil, nil, deep)

	res, err := r.Search(context.Background(), Query{Intent: "x", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	shown := 0
	for _, g := range res.Groups {
		shown += len(g.Hits)
	}
	if shown != 10 {
		t.Errorf("shown = %d, want the display budget 10", shown)
	}
	if got := coverageOf(t, res, "deep").Shown; got != 10 {
		t.Errorf("coverage shown = %d, want 10", got)
	}
}

// TestSearch_LimitAboveTheFloorRaisesTheFetchDepth is the second half of the
// defect: `limit` documented a maximum of 50, and a search narrowed to one
// source could never display more than 25 of it whatever it asked for.
func TestSearch_LimitAboveTheFloorRaisesTheFetchDepth(t *testing.T) {
	deep := &corpusProvider{name: "deep", corpus: 500}
	r := NewRouter(nil, nil, deep)

	res, err := r.Search(context.Background(), Query{Intent: "x", Sources: []string{"deep"}, Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if deep.gotLimit != 51 {
		t.Errorf("provider was asked for %d candidates, want the depth 50 plus the probe", deep.gotLimit)
	}
	shown := 0
	for _, g := range res.Groups {
		shown += len(g.Hits)
	}
	if shown != 50 {
		t.Errorf("shown = %d, want the 50 the caller asked for", shown)
	}
	got := coverageOf(t, res, "deep")
	if got.Matched != 50 || !got.MatchedCapped {
		t.Errorf("coverage = %+v, want matched 50 flagged capped", got)
	}
}

// TestSearch_WithheldShorteningIsNotCapped holds the rule the capped test is
// deliberately narrow about: a provider that filters its whole corpus reports
// every record its persona boundary hid, and those are not evidence that the
// depth bound truncated anything. Counting them would flag a source whose
// visible matches fit inside the depth with room to spare.
func TestSearch_WithheldShorteningIsNotCapped(t *testing.T) {
	guarded := &corpusProvider{name: "guarded", corpus: 3, withheld: 40}
	r := NewRouter(nil, nil, guarded)

	res, err := r.Search(context.Background(), Query{Intent: "x", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	got := coverageOf(t, res, "guarded")
	if got.MatchedCapped {
		t.Errorf("coverage = %+v, want no capped flag: the depth truncated nothing", got)
	}
	if got.Matched != 3 || got.Withheld != 40 {
		t.Errorf("coverage = %+v, want matched 3 withheld 40", got)
	}
}

// denyOneScope withholds exactly one URN and grants the rest, the shape a
// persona with one connection it may not reach produces.
type denyOneScope struct{ denied string }

func (d denyOneScope) ConnectionsForURN(urn string) []string {
	if urn == d.denied {
		return []string{"restricted"}
	}
	return nil
}

func (denyOneScope) AllowConnection(string, string) bool { return false }

// TestCatalogWithheldCandidateDoesNotConsumeASlot holds the invariant the capped
// signal rests on: every provider applies the connection boundary BEFORE
// truncating to the limit the router asked for, so a full list means the bound
// truncated something. The catalog merged its arms, cut them to the limit, and
// only then applied the boundary, so one withheld entity made a truncated arm
// come back one short -- which the router reads as "this source had nothing
// more to give", the very lie #1585 removes, and only ever for a caller whose
// persona restricts something.
func TestCatalogWithheldCandidateDoesNotConsumeASlot(t *testing.T) {
	const limit = 5
	hits := make([]CatalogIndexHit, 0, limit+3)
	for i := range limit + 3 {
		hits = append(hits, CatalogIndexHit{
			URN:  fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,d%d,PROD)", i),
			Name: fmt.Sprintf("d%d", i),
		})
	}
	p := NewCatalogProvider(&fakeTableSearcher{})
	p.SetIndexSearcher(&fakeCatalogIndex{hits: hits})

	gate := &connGate{scope: denyOneScope{denied: hits[0].URN}}
	q := Query{Intent: "d", Limit: limit}
	q.Caller.conn = gate

	got, err := p.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != limit {
		t.Errorf("got %d candidates for a limit of %d: the withheld one consumed a slot", len(got), limit)
	}
	if gate.withheld != 1 {
		t.Errorf("withheld = %d, want 1", gate.withheld)
	}
	for i := range got {
		if got[i].Ref == hits[0].URN {
			t.Errorf("the withheld entity is in the result: %+v", got[i])
		}
	}
}
