package memory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
)

func TestTruncateRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than limit", "hello", 10, "hello"},
		{"exactly at limit", "hello", 5, "hello"},
		{"truncated", "hello world", 5, "hello..."},
		{"empty", "", 5, ""},
		{"multibyte not split", "héllo wörld", 5, "héllo..."},
		// Rune count <= n but byte length > n: the loop never reaches n, so the
		// full string is returned unchanged (guards the trailing return).
		{"multibyte fewer runes than bytes", "héééé", 5, "héééé"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := truncateRunes(tc.in, tc.n)
			assert.Equal(t, tc.want, got)
			assert.True(t, utf8ValidPrefix(got), "result must be valid UTF-8")
		})
	}
}

// utf8ValidPrefix reports whether s is valid UTF-8 (guards the rune-boundary
// slice in truncateRunes).
func utf8ValidPrefix(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestToPairSummary_DropsHeavyFields(t *testing.T) {
	t.Parallel()

	p := memstore.SimilarPair{
		Older: memstore.Record{
			ID:        "old",
			CreatedBy: "user@example.com",
			Status:    memstore.StatusActive,
			Content:   strings.Repeat("a", previewMaxLen+100),
			Embedding: []float32{0.1, 0.2},
			Metadata:  map[string]any{"k": "v"},
		},
		Newer: memstore.Record{ID: "new", CreatedBy: "user@example.com", Status: memstore.StatusActive, Content: "short"},
		Score: 0.91,
	}
	s := toPairSummary(p)

	assert.Equal(t, "old", s.Older.ID)
	assert.Equal(t, 0.91, s.Score)
	// Preview is bounded; the full 4000-char-capable content never rides along.
	assert.Equal(t, strings.Repeat("a", previewMaxLen)+"...", s.Older.Preview)
	assert.LessOrEqual(t, len([]rune(s.Older.Preview)), previewMaxLen+3)
	assert.Equal(t, "short", s.Newer.Preview)
}

func TestEffectiveDuplicateLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int
		want int
	}{
		{"zero defaults", 0, memstore.DefaultLimit},
		{"negative defaults", -5, memstore.DefaultLimit},
		{"in range preserved", 7, 7},
		{"at page ceiling preserved", maxDuplicatePageSize, maxDuplicatePageSize},
		{"store MaxLimit clamped to page ceiling", memstore.MaxLimit, maxDuplicatePageSize},
		{"over ceiling clamped", 1000, maxDuplicatePageSize},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, effectiveDuplicateLimit(tc.in))
		})
	}
}

// smallPairs builds n pairs whose serialized summaries are small, to exercise
// budgetSummaries without tripping the byte budget.
func smallPairs(n int) []memstore.SimilarPair {
	pairs := make([]memstore.SimilarPair, n)
	for i := range pairs {
		pairs[i] = memstore.SimilarPair{
			Older: memstore.Record{ID: string(rune('a' + i)), Content: "x"},
			Newer: memstore.Record{ID: string(rune('A' + i)), Content: "y"},
			Score: 0.9,
		}
	}
	return pairs
}

// bigContentPairs builds n pairs whose per-side content is large, so a small
// budget forces truncation.
func bigContentPairs(n int) []memstore.SimilarPair {
	pairs := make([]memstore.SimilarPair, n)
	for i := range pairs {
		pairs[i] = memstore.SimilarPair{
			Older: memstore.Record{ID: "o", Content: strings.Repeat("x", 4000)},
			Newer: memstore.Record{ID: "n", Content: strings.Repeat("y", 4000)},
			Score: 0.9,
		}
	}
	return pairs
}

func TestBudgetSummaries_AllFit(t *testing.T) {
	t.Parallel()

	summaries, truncated := budgetSummaries(smallPairs(10), duplicatePairBudgetBytes)
	assert.Len(t, summaries, 10)
	assert.False(t, truncated)
}

func TestBudgetSummaries_ByteBudgetTruncates(t *testing.T) {
	t.Parallel()

	// A tiny budget truncates after the always-included first pair.
	summaries, truncated := budgetSummaries(bigContentPairs(5), 500)
	require.Len(t, summaries, 1, "first pair is always included even when it alone exceeds the budget")
	assert.True(t, truncated)
}

func TestBudgetSummaries_MeasuresRealNesting(t *testing.T) {
	t.Parallel()

	// The kept summaries must actually serialize under the budget at the nesting
	// depth jsonResult emits them (result["pairs"][]), guarding the summarySize
	// under-count the review flagged (#783).
	summaries, _ := budgetSummaries(bigContentPairs(60), duplicatePairBudgetBytes)
	b, err := json.MarshalIndent(map[string]any{"pairs": summaries}, "", "  ")
	require.NoError(t, err)
	assert.LessOrEqual(t, len(b), duplicatePairBudgetBytes+512,
		"the serialized pairs array must stay within the byte budget (plus small map framing)")
}

func TestBudgetSummaries_EmptyInput(t *testing.T) {
	t.Parallel()

	summaries, truncated := budgetSummaries(nil, duplicatePairBudgetBytes)
	assert.Empty(t, summaries)
	assert.NotNil(t, summaries, "an empty result must be a non-nil slice for a clean [] in JSON")
	assert.False(t, truncated)
}
