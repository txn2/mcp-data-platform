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
