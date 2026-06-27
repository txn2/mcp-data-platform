package knowledge

import (
	"math"
	"testing"
)

// approxEqual reports whether two scores are equal within float tolerance.
func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestNormalizeProvider(t *testing.T) {
	tests := []struct {
		name string
		in   []Hit
		want []float64 // expected normalized scores, in input order
	}{
		{
			name: "empty yields nil",
			in:   nil,
			want: nil,
		},
		{
			name: "single hit normalizes to 1.0",
			in:   []Hit{{Ref: "a", Score: 0.42}},
			want: []float64{1.0},
		},
		{
			name: "all-equal scores normalize to 1.0",
			in:   []Hit{{Ref: "a", Score: 0.5}, {Ref: "b", Score: 0.5}},
			want: []float64{1.0, 1.0},
		},
		{
			name: "spread maps min to 0 and max to 1",
			in:   []Hit{{Ref: "a", Score: 0.2}, {Ref: "b", Score: 0.7}, {Ref: "c", Score: 1.2}},
			want: []float64{0.0, 0.5, 1.0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeProvider(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if !approxEqual(got[i].Score, tt.want[i]) {
					t.Errorf("hit %d score = %v, want %v", i, got[i].Score, tt.want[i])
				}
			}
		})
	}
}

func TestNormalizeProvider_DoesNotMutateInput(t *testing.T) {
	in := []Hit{{Ref: "a", Score: 0.2}, {Ref: "b", Score: 0.8}}
	_ = normalizeProvider(in)
	if in[0].Score != 0.2 || in[1].Score != 0.8 {
		t.Fatalf("input mutated: %+v", in)
	}
}

// shownBySource collapses a group set into a source->shown-count map.
func shownBySource(groups []SourceGroup) map[string]int {
	m := make(map[string]int, len(groups))
	for _, g := range groups {
		m[g.Source] = len(g.Hits)
	}
	return m
}

// coverageBySource collapses a coverage summary into a source->coverage map.
func coverageBySource(cov []SourceCoverage) map[string]SourceCoverage {
	m := make(map[string]SourceCoverage, len(cov))
	for _, c := range cov {
		m[c.Source] = c
	}
	return m
}

func TestAllocate_Empty(t *testing.T) {
	groups, cov := allocate(nil, 10)
	if groups != nil || cov != nil {
		t.Fatalf("empty input should yield nil, nil; got %+v %+v", groups, cov)
	}
	// An all-empty-provider input collapses the same way.
	groups, cov = allocate([][]Hit{{}, nil}, 10)
	if groups != nil || cov != nil {
		t.Fatalf("all-empty providers should yield nil, nil; got %+v %+v", groups, cov)
	}
}

func TestAllocate_FloorGivesEverySourceVisibility(t *testing.T) {
	// One source has many strong candidates; another has a single weak one. A
	// flat relevance sort would bury the weak source entirely; the floor must
	// keep it visible.
	big := make([]Hit, 8)
	for i := range big {
		big[i] = Hit{Source: "catalog", Ref: string(rune('a' + i)), Score: float64(100 + i)}
	}
	small := []Hit{{Source: "memory", Ref: "m1", Score: 0.01}}

	groups, cov := allocate([][]Hit{big, small}, 6)
	shown := shownBySource(groups)
	if shown["memory"] < floorPerSource {
		t.Errorf("memory should keep at least its floor of %d, shown %d", floorPerSource, shown["memory"])
	}
	if shown["catalog"] == 0 {
		t.Error("datahub should be shown")
	}
	// memory has only one candidate, so the budget the ceiling could not place
	// on it redistributes to datahub: datahub takes the rest of the budget.
	if shown["catalog"] != 6-shown["memory"] {
		t.Errorf("datahub should absorb the leftover budget, shown %d (memory %d, budget 6)", shown["catalog"], shown["memory"])
	}
	// Coverage reports the full matched count even though only some are shown.
	c := coverageBySource(cov)
	if c["catalog"].Matched != 8 {
		t.Errorf("datahub matched = %d, want 8", c["catalog"].Matched)
	}
	if c["catalog"].Shown != shown["catalog"] {
		t.Errorf("coverage shown %d != group shown %d", c["catalog"].Shown, shown["catalog"])
	}
}

func TestAllocate_CeilingBoundsWhenOthersCanAbsorb(t *testing.T) {
	// Two sources, both with more candidates than the ceiling. Neither may run
	// away with the response: each is held at the ceiling during the balanced
	// fill, and since both can absorb budget no ceiling relaxation is needed.
	a := make([]Hit, 8)
	b := make([]Hit, 8)
	for i := range a {
		a[i] = Hit{Source: "a", Ref: string(rune('a' + i)), Score: float64(i)}
		b[i] = Hit{Source: "b", Ref: string(rune('a' + i)), Score: float64(i)}
	}
	groups, _ := allocate([][]Hit{a, b}, 6)
	shown := shownBySource(groups)
	ceiling := allocCeiling(6)
	if shown["a"] != ceiling || shown["b"] != ceiling {
		t.Errorf("each source should be held at the ceiling %d, got a=%d b=%d", ceiling, shown["a"], shown["b"])
	}
}

func TestAllocate_RedistributesWhenOneSource(t *testing.T) {
	// Only one source has hits: the leftover budget the ceiling could not place
	// must redistribute back to it rather than going to waste.
	only := make([]Hit, 10)
	for i := range only {
		only[i] = Hit{Source: "memory", Ref: string(rune('a' + i)), Score: float64(i)}
	}
	groups, _ := allocate([][]Hit{only}, 6)
	if got := shownBySource(groups)["memory"]; got != 6 {
		t.Errorf("single source should fill the whole budget via redistribution, shown %d want 6", got)
	}
}

func TestAllocate_BudgetBounds(t *testing.T) {
	// The total shown across all sources never exceeds the budget.
	a := []Hit{{Source: "a", Ref: "1", Score: 3}, {Source: "a", Ref: "2", Score: 2}}
	b := []Hit{{Source: "b", Ref: "1", Score: 5}, {Source: "b", Ref: "2", Score: 4}}
	c := []Hit{{Source: "c", Ref: "1", Score: 1}}
	groups, _ := allocate([][]Hit{a, b, c}, 3)
	total := 0
	for _, g := range groups {
		total += len(g.Hits)
	}
	if total != 3 {
		t.Errorf("total shown = %d, want 3 (budget)", total)
	}
}

func TestAllocate_ZeroBudgetStillReportsCoverage(t *testing.T) {
	// A zero budget shows nothing but must still report what matched, so the
	// agent learns the answer space exists.
	groups, cov := allocate([][]Hit{{{Source: "memory", Ref: "m1", Score: 1}}}, 0)
	if len(groups) != 0 {
		t.Errorf("zero budget should show nothing, got %+v", groups)
	}
	if len(cov) != 1 || cov[0].Source != "memory" || cov[0].Matched != 1 || cov[0].Shown != 0 {
		t.Errorf("coverage should report memory matched=1 shown=0, got %+v", cov)
	}
}

func TestAllocate_GroupsOrderedByShownThenName(t *testing.T) {
	a := []Hit{{Source: "a", Ref: "1", Score: 1}}
	b := []Hit{{Source: "b", Ref: "1", Score: 1}, {Source: "b", Ref: "2", Score: 1}, {Source: "b", Ref: "3", Score: 1}}
	groups, _ := allocate([][]Hit{a, b}, 10)
	if len(groups) != 2 || groups[0].Source != "b" {
		t.Errorf("group with more shown should lead, got %+v", groups)
	}
}
