package embedding

import (
	"context"
	"errors"
	"testing"
)

// fakeProvider is a configurable embedding provider for exercising the
// EmbedForSearch decision branches without a network call.
type fakeProvider struct {
	vec  []float32
	err  error
	kind string
}

func (f fakeProvider) Embed(context.Context, string) ([]float32, error) { return f.vec, f.err }
func (fakeProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}
func (fakeProvider) Dimension() int { return 3 }
func (f fakeProvider) Kind() string { return f.kind }

// TestEmbedForSearch covers the shared hybrid-vs-lexical decision: a usable
// vector is returned only for a configured provider that yields a non-zero
// result; every other case returns nil to select lexical-only ranking.
func TestEmbedForSearch(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		p       Provider
		wantNil bool
	}{
		{"nil provider", nil, true},
		{"noop provider", NewNoopProvider(3), true},
		{"configured embed error", fakeProvider{err: errors.New("boom"), kind: KindOllama}, true},
		{"configured zero vector", fakeProvider{vec: []float32{0, 0, 0}, kind: KindOllama}, true},
		{"configured real vector", fakeProvider{vec: []float32{0.1, 0, 0}, kind: KindOllama}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EmbedForSearch(ctx, tc.p, "query")
			if tc.wantNil && got != nil {
				t.Errorf("EmbedForSearch = %v; want nil", got)
			}
			if !tc.wantNil && got == nil {
				t.Errorf("EmbedForSearch = nil; want a vector")
			}
		})
	}
}
