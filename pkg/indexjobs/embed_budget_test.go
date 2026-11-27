package indexjobs

import (
	"context"
	"strings"
	"testing"
)

// TestEmbedInBatches_BoundsEachCallByBytes is #2839's budget: a call carries
// at most batchBytes of text, a text longer than the budget goes alone, and
// the count bound still applies.
func TestEmbedInBatches_BoundsEachCallByBytes(t *testing.T) {
	t.Parallel()
	texts := []string{
		strings.Repeat("a", 3000), strings.Repeat("b", 3000), strings.Repeat("c", 3000),
		strings.Repeat("d", 100), strings.Repeat("e", 100), strings.Repeat("f", 7000), "g",
	}
	var sizes []int
	err := embedInBatches(context.Background(), batchRun{
		embedder: newFakeEmbedder(), texts: texts, batchSize: 32, batchBytes: 6000,
		onChunk: func(start, end int, _ [][]float32) error {
			sizes = append(sizes, end-start)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sizes, []int{2, 3, 1, 1}; !equalInts(got, want) {
		t.Fatalf("call sizes = %v, want %v", got, want)
	}

	if got := chunkEnd(texts, 0, 1, 6000); got != 1 {
		t.Errorf("the count bound still applies: chunkEnd = %d, want 1", got)
	}
	if got := chunkEnd(texts, 0, 32, 0); got != len(texts) {
		t.Errorf("no budget bounds by count alone: chunkEnd = %d, want %d", got, len(texts))
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
