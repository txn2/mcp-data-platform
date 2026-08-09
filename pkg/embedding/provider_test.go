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
func (f fakeProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = f.vec
	}
	return out, nil
}
func (fakeProvider) Dimension() int { return 3 }
func (f fakeProvider) Kind() string { return f.kind }

// cappedProvider is a provider that reports its own input budget, the optional
// capability the knowledge-page chunker sizes its chunks from.
type cappedProvider struct {
	fakeProvider
	cap int
}

func (c cappedProvider) MaxInputBytes() int { return c.cap }

// TestMaxInputBytes covers the budget lookup: a provider that reports a positive
// budget supplies it, and everything else falls back to the documented default,
// so a caller that must split its text always has a usable bound.
func TestMaxInputBytes(t *testing.T) {
	cases := []struct {
		name string
		p    Provider
		want int
	}{
		{"nil provider", nil, DefaultMaxInputBytes},
		{"provider without the capability", fakeProvider{kind: KindOllama}, DefaultMaxInputBytes},
		{"provider reporting zero", cappedProvider{cap: 0}, DefaultMaxInputBytes},
		{"provider reporting a budget", cappedProvider{cap: 12000}, 12000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaxInputBytes(tc.p); got != tc.want {
				t.Errorf("MaxInputBytes = %d; want %d", got, tc.want)
			}
		})
	}
}

// TestMaxInputBytes_OllamaReportsItsConfiguredCap pins the wiring end to end: the
// budget the chunker reads is the one an operator configured, not the constant.
func TestMaxInputBytes_OllamaReportsItsConfiguredCap(t *testing.T) {
	if got := MaxInputBytes(NewOllamaProvider(OllamaConfig{MaxInputBytes: 9000})); got != 9000 {
		t.Errorf("MaxInputBytes = %d; want 9000", got)
	}
	if got := MaxInputBytes(NewOllamaProvider(OllamaConfig{})); got != DefaultMaxInputBytes {
		t.Errorf("MaxInputBytes = %d; want the default %d", got, DefaultMaxInputBytes)
	}
}

// TestEmbedChunksForSearch covers the multi-vector query path: every chunk of an
// oversized query is embedded, and any failure discards the whole set so a probe
// never runs on a subset of the caller's text.
func TestEmbedChunksForSearch(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		p       Provider
		texts   []string
		wantLen int
	}{
		{"nil provider", nil, []string{"a"}, 0},
		{"noop provider", NewNoopProvider(3), []string{"a"}, 0},
		{"no texts", fakeProvider{vec: []float32{0.1}, kind: KindOllama}, nil, 0},
		{"embed error", fakeProvider{err: errors.New("boom"), kind: KindOllama}, []string{"a"}, 0},
		{"zero vector", fakeProvider{vec: []float32{0, 0, 0}, kind: KindOllama}, []string{"a", "b"}, 0},
		{"two chunks", fakeProvider{vec: []float32{0.1, 0, 0}, kind: KindOllama}, []string{"a", "b"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EmbedChunksForSearch(ctx, tc.p, tc.texts); len(got) != tc.wantLen {
				t.Errorf("EmbedChunksForSearch returned %d vectors; want %d", len(got), tc.wantLen)
			}
		})
	}
}

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
