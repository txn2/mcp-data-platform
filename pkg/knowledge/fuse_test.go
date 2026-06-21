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

func TestNormalizeAndFuse_RanksAcrossProviders(t *testing.T) {
	// Two providers on different raw scales. After per-provider min-max, the
	// top of each provider should outrank the bottom regardless of raw scale.
	a := []Hit{{Source: "a", Ref: "a1", Score: 10}, {Source: "a", Ref: "a2", Score: 90}}
	b := []Hit{{Source: "b", Ref: "b1", Score: 0.1}, {Source: "b", Ref: "b2", Score: 0.2}}

	got := normalizeAndFuse([][]Hit{a, b})
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	// a2 (norm 1.0) and b2 (norm 1.0) tie at the top; tie-break is by source,
	// so a2 precedes b2. a1 and b1 tie at 0.0; a1 precedes b1.
	wantOrder := []string{"a2", "b2", "a1", "b1"}
	for i, ref := range wantOrder {
		if got[i].Ref != ref {
			t.Errorf("position %d = %q, want %q (full: %+v)", i, got[i].Ref, ref, refs(got))
		}
	}
}

func TestNormalizeAndFuse_TieBreakIsDeterministic(t *testing.T) {
	// All scores equal -> all normalize to 1.0 -> order is purely the
	// deterministic source/ref tie-break.
	in := [][]Hit{
		{{Source: "z", Ref: "1", Score: 5}},
		{{Source: "a", Ref: "2", Score: 5}},
		{{Source: "a", Ref: "1", Score: 5}},
	}
	got := normalizeAndFuse(in)
	want := []string{"a/1", "a/2", "z/1"}
	for i, key := range want {
		gotKey := got[i].Source + "/" + got[i].Ref
		if gotKey != key {
			t.Errorf("position %d = %q, want %q", i, gotKey, key)
		}
	}
}

func TestNormalizeAndFuse_Empty(t *testing.T) {
	if got := normalizeAndFuse(nil); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func refs(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Ref
	}
	return out
}
