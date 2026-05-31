package memory

import (
	"math"
	"testing"
)

// TestFuseHybridScore pins the fusion formula: score = 0.6*semantic +
// 0.4*lexical, where semantic maps cosine [-1,1] to [0,1] and lexical is
// binary. These are the exact values the ranking evaluation depends on.
func TestFuseHybridScore(t *testing.T) {
	t.Parallel()
	const eps = 1e-9
	tests := []struct {
		name      string
		cosineSim float64
		lexMatch  bool
		want      float64
	}{
		// semantic = (1+1)/2 = 1; lex 0 -> 0.6
		{"perfect cosine, no lexical", 1.0, false, 0.6},
		// semantic = 1; lex 1 -> 1.0
		{"perfect cosine, lexical match", 1.0, true, 1.0},
		// semantic = (0+1)/2 = 0.5; lex 0 -> 0.3
		{"orthogonal, no lexical", 0.0, false, 0.3},
		// semantic = 0.5; lex 1 -> 0.6*0.5 + 0.4 = 0.7
		{"orthogonal, lexical match", 0.0, true, 0.7},
		// semantic = (-1+1)/2 = 0; lex 1 -> 0.4 (a pure lexical hit)
		{"opposite cosine, lexical match", -1.0, true, 0.4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fuseHybridScore(tt.cosineSim, tt.lexMatch)
			if math.Abs(got-tt.want) > eps {
				t.Fatalf("fuseHybridScore(%v, %v) = %v, want %v", tt.cosineSim, tt.lexMatch, got, tt.want)
			}
		})
	}
}

// TestFuseHybridScore_LexicalBeatsHigherCosine is the core ranking
// evaluation: a modest-cosine record that lexically matches an exact
// identifier must outrank a higher-cosine record that does not. This is
// what makes hybrid measurably better than pure vector on identifier-
// heavy queries.
func TestFuseHybridScore_LexicalBeatsHigherCosine(t *testing.T) {
	t.Parallel()

	identifierMatch := fuseHybridScore(0.55, true) // modest cosine, exact-term hit
	pureVectorOnly := fuseHybridScore(0.82, false) // stronger cosine, no term hit

	if identifierMatch <= pureVectorOnly {
		t.Fatalf("hybrid must rank the lexical identifier match (%.4f) above the "+
			"higher-cosine non-match (%.4f)", identifierMatch, pureVectorOnly)
	}

	// Sanity: under PURE vector ranking the order flips, which is exactly
	// the weakness hybrid fixes.
	if 0.82 <= 0.55 {
		t.Fatal("test premise broken: pure-vector row should have higher cosine")
	}
}
