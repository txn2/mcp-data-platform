package knowledge

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// blockingProvider blocks in Search until its context is canceled, simulating a
// provider slow enough to exceed the per-provider fan-out deadline. Real
// providers thread ctx into their DB/network calls, so this models the same
// cancellation behavior.
type blockingProvider struct {
	name  string
	scope Scope
}

func (b *blockingProvider) Name() string { return b.name }
func (b *blockingProvider) Scope() Scope { return b.scope }
func (*blockingProvider) Search(ctx context.Context, _ Query) ([]Hit, error) {
	<-ctx.Done()
	return nil, fmt.Errorf("blocked until canceled: %w", ctx.Err())
}

// TestRouter_SlowProviderDoesNotStallFanOut is the fix for the unbounded-search
// hang: one slow provider must drop out at the deadline while the fast providers
// still return, instead of the whole search waiting for the slowest arm.
func TestRouter_SlowProviderDoesNotStallFanOut(t *testing.T) {
	fast := &fakeProvider{name: "fast", scope: ScopeShared, hits: []Hit{{Source: "fast", Ref: "f1", Score: 1}}}
	slow := &blockingProvider{name: "slow", scope: ScopeShared}
	r := NewRouter(nil, nil, fast, slow)
	r.SetProviderTimeout(50 * time.Millisecond)

	start := time.Now()
	res, err := r.Search(context.Background(), Query{Intent: "anything"})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("partial success expected (fast provider returned), got error: %v", err)
	}
	hits := flatHits(res)
	if len(hits) != 1 || hits[0].Ref != "f1" {
		t.Fatalf("expected the fast provider's hit despite the slow arm timing out, got %+v", hits)
	}
	// Bounded by the per-provider deadline, not blocked on the slow arm. Generous
	// ceiling to stay non-flaky under load.
	if elapsed > 2*time.Second {
		t.Fatalf("fan-out stalled on the slow provider: took %v", elapsed)
	}
}

// TestRouter_AllProvidersTimeOut_SurfacesError proves a total stall is reported
// as a failure rather than a silent empty success.
func TestRouter_AllProvidersTimeOut_SurfacesError(t *testing.T) {
	r := NewRouter(nil, nil,
		&blockingProvider{name: "slow1", scope: ScopeShared},
		&blockingProvider{name: "slow2", scope: ScopeShared},
	)
	r.SetProviderTimeout(30 * time.Millisecond)

	if _, err := r.Search(context.Background(), Query{Intent: "x"}); err == nil {
		t.Fatal("all providers timing out must surface an error, not an empty success")
	}
}

// TestRouter_ProviderTimeoutDisabled proves a non-positive timeout restores the
// unbounded behavior (each provider runs under the request context only).
func TestRouter_ProviderTimeoutDisabled(t *testing.T) {
	fast := &fakeProvider{name: "fast", scope: ScopeShared, hits: []Hit{{Source: "fast", Ref: "f1", Score: 1}}}
	r := NewRouter(nil, nil, fast)
	r.SetProviderTimeout(-1)

	res, err := r.Search(context.Background(), Query{Intent: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(flatHits(res)) != 1 {
		t.Fatalf("expected the fast provider's hit with the bound disabled, got %d", len(flatHits(res)))
	}
}

// TestNewRouter_DefaultProviderTimeout guards the default so a deployment that
// never configures the knob is still bounded, and that SetProviderTimeout(0)
// (an unset config value) leaves that default in place.
func TestNewRouter_DefaultProviderTimeout(t *testing.T) {
	r := NewRouter(nil, nil)
	if r.providerTimeout != defaultProviderSearchTimeout {
		t.Fatalf("default providerTimeout = %v, want %v", r.providerTimeout, defaultProviderSearchTimeout)
	}
	r.SetProviderTimeout(0) // unset config: must not clobber the default
	if r.providerTimeout != defaultProviderSearchTimeout {
		t.Fatalf("SetProviderTimeout(0) changed default to %v", r.providerTimeout)
	}
	r.SetProviderTimeout(2 * time.Second)
	if r.providerTimeout != 2*time.Second {
		t.Fatalf("SetProviderTimeout(2s) = %v", r.providerTimeout)
	}
}

// slowEmbedder returns a non-zero vector after delay, but honors context
// cancellation (returning an error, which EmbedForSearch maps to lexical
// fallback). It models a cold or CPU-throttled embed backend.
type slowEmbedder struct{ delay time.Duration }

func (s slowEmbedder) Embed(ctx context.Context, _ string) ([]float32, error) {
	select {
	case <-time.After(s.delay):
		return []float32{0.1, 0.2, 0.3}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("embed canceled: %w", ctx.Err())
	}
}

func (s slowEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		v, err := s.Embed(ctx, texts[i])
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}
func (slowEmbedder) Dimension() int { return 3 }
func (slowEmbedder) Kind() string   { return "slow" }

// TestRouter_EmbedTimeoutSeparateFromFanOut is the #795 fix: the search-path
// embedding and the per-provider fan-out arms use independent deadlines. An
// embedder slower than the (tight) fan-out bound but faster than the (generous)
// embed bound still produces hybrid ranking, while a genuinely slow provider
// still drops out at the fan-out bound. Under the old shared knob the embedder
// would have been cut at the fan-out bound and silently degraded to lexical.
func TestRouter_EmbedTimeoutSeparateFromFanOut(t *testing.T) {
	fast := &fakeProvider{name: "fast", scope: ScopeShared, hits: []Hit{{Source: "fast", Ref: "f1", Score: 1}}}
	slow := &blockingProvider{name: "slow", scope: ScopeShared}
	// Embedder takes 150ms: longer than the 40ms fan-out bound, shorter than the
	// 2s embed bound.
	r := NewRouter(slowEmbedder{delay: 150 * time.Millisecond}, nil, fast, slow)
	r.SetProviderTimeout(40 * time.Millisecond)
	r.SetEmbedTimeout(2 * time.Second)

	res, err := r.Search(context.Background(), Query{Intent: "anything"})
	if err != nil {
		t.Fatalf("partial success expected (fast provider returned), got error: %v", err)
	}
	if res.Ranking != rankingHybrid {
		t.Fatalf("embedder was slower than the fan-out bound but faster than the embed bound: "+
			"expected %q ranking (its own timeout applied), got %q", rankingHybrid, res.Ranking)
	}
	hits := flatHits(res)
	if len(hits) != 1 || hits[0].Ref != "f1" {
		t.Fatalf("expected only the fast provider's hit (slow arm dropped at the fan-out bound), got %+v", hits)
	}
}

// TestRouter_EmbedTimeoutCutShortDegradesToLexical proves the embed bound still
// bites: an embedder slower than its own timeout degrades to lexical rather than
// stalling the search.
func TestRouter_EmbedTimeoutCutShortDegradesToLexical(t *testing.T) {
	fast := &fakeProvider{name: "fast", scope: ScopeShared, hits: []Hit{{Source: "fast", Ref: "f1", Score: 1}}}
	r := NewRouter(slowEmbedder{delay: time.Second}, nil, fast)
	r.SetEmbedTimeout(30 * time.Millisecond)

	res, err := r.Search(context.Background(), Query{Intent: "anything"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Ranking != rankingLexical {
		t.Fatalf("embedder slower than its own bound must degrade to %q, got %q", rankingLexical, res.Ranking)
	}
}

// TestNewRouter_DefaultEmbedTimeout mirrors the provider-timeout guard: the
// default is set, SetEmbedTimeout(0) (an unset config value) preserves it, and a
// positive value overrides.
func TestNewRouter_DefaultEmbedTimeout(t *testing.T) {
	r := NewRouter(nil, nil)
	if r.embedTimeout != defaultSearchEmbedTimeout {
		t.Fatalf("default embedTimeout = %v, want %v", r.embedTimeout, defaultSearchEmbedTimeout)
	}
	r.SetEmbedTimeout(0) // unset config: must not clobber the default
	if r.embedTimeout != defaultSearchEmbedTimeout {
		t.Fatalf("SetEmbedTimeout(0) changed default to %v", r.embedTimeout)
	}
	r.SetEmbedTimeout(8 * time.Second)
	if r.embedTimeout != 8*time.Second {
		t.Fatalf("SetEmbedTimeout(8s) = %v", r.embedTimeout)
	}
}

// TestRouter_EmbedTimeoutDisabled proves a non-positive embed timeout runs the
// embed under the request context only (no derived deadline).
func TestRouter_EmbedTimeoutDisabled(t *testing.T) {
	fast := &fakeProvider{name: "fast", scope: ScopeShared, hits: []Hit{{Source: "fast", Ref: "f1", Score: 1}}}
	r := NewRouter(slowEmbedder{delay: 20 * time.Millisecond}, nil, fast)
	r.SetEmbedTimeout(-1)

	res, err := r.Search(context.Background(), Query{Intent: "anything"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Ranking != rankingHybrid {
		t.Fatalf("with the embed bound disabled the embedder should complete: want %q, got %q", rankingHybrid, res.Ranking)
	}
}
