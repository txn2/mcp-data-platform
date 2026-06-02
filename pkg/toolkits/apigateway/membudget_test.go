package apigateway

import (
	"sync"
	"testing"
)

func TestMemBudgetAcquireRelease(t *testing.T) {
	b := NewMemBudget(100)
	if !b.Acquire(60) {
		t.Fatal("first 60-byte acquire should succeed")
	}
	if b.InUse() != 60 {
		t.Fatalf("InUse = %d, want 60", b.InUse())
	}
	// 60 + 50 = 110 > 100, must be refused without committing.
	if b.Acquire(50) {
		t.Fatal("acquire that would exceed the budget must be refused")
	}
	if b.InUse() != 60 {
		t.Fatalf("refused acquire must not change InUse; got %d, want 60", b.InUse())
	}
	// Exactly fitting the remaining budget succeeds.
	if !b.Acquire(40) {
		t.Fatal("acquire that exactly fills the budget should succeed")
	}
	if b.InUse() != 100 {
		t.Fatalf("InUse = %d, want 100", b.InUse())
	}
	// Now full: even 1 byte is refused.
	if b.Acquire(1) {
		t.Fatal("acquire on a full budget must be refused")
	}
	b.Release(100)
	if b.InUse() != 0 {
		t.Fatalf("InUse after full release = %d, want 0", b.InUse())
	}
	// Budget is reusable after release.
	if !b.Acquire(100) {
		t.Fatal("acquire after release should succeed")
	}
}

func TestMemBudgetNilIsUnlimited(t *testing.T) {
	var b *MemBudget
	if !b.Acquire(1 << 40) {
		t.Fatal("nil budget must admit any reservation")
	}
	b.Release(1 << 40) // must not panic
	if b.InUse() != 0 {
		t.Fatalf("nil budget InUse = %d, want 0", b.InUse())
	}
	if b.Max() != 0 {
		t.Fatalf("nil budget Max = %d, want 0", b.Max())
	}
	if b.Enabled() {
		t.Fatal("nil budget must report disabled")
	}
}

func TestMemBudgetDisabledZeroMax(t *testing.T) {
	b := NewMemBudget(0)
	if b.Enabled() {
		t.Fatal("zero-max budget must report disabled")
	}
	if !b.Acquire(1 << 40) {
		t.Fatal("disabled budget must admit any reservation")
	}
	if b.InUse() != 0 {
		t.Fatalf("disabled budget must not track usage; got %d", b.InUse())
	}
}

func TestMemBudgetNegativeMaxClampsToDisabled(t *testing.T) {
	b := NewMemBudget(-5)
	if b.Enabled() {
		t.Fatal("negative max must clamp to a disabled budget")
	}
	if b.Max() != 0 {
		t.Fatalf("Max = %d, want 0", b.Max())
	}
}

func TestMemBudgetNonPositiveAcquireIsNoop(t *testing.T) {
	b := NewMemBudget(100)
	if !b.Acquire(0) {
		t.Fatal("acquire(0) should succeed")
	}
	if !b.Acquire(-1) {
		t.Fatal("acquire(negative) should succeed as a no-op")
	}
	if b.InUse() != 0 {
		t.Fatalf("non-positive acquire must not commit; InUse = %d", b.InUse())
	}
}

func TestMemBudgetOverflowRefused(t *testing.T) {
	b := NewMemBudget(1<<62 + 100)
	// Reserve near the max, then ask for an amount whose sum would
	// overflow int64. Acquire must refuse rather than wrap negative.
	if !b.Acquire(1 << 62) {
		t.Fatal("initial large acquire should succeed")
	}
	if b.Acquire(1<<62 + 50) {
		t.Fatal("acquire that would overflow int64 must be refused")
	}
}

func TestMemBudgetReleaseClampsAtZero(t *testing.T) {
	b := NewMemBudget(100)
	if !b.Acquire(30) {
		t.Fatal("acquire should succeed")
	}
	// Over-release: must clamp at zero, not go negative (which would
	// silently widen the effective budget).
	b.Release(100)
	if b.InUse() != 0 {
		t.Fatalf("over-release must clamp to 0; got %d", b.InUse())
	}
	// Budget integrity preserved: full budget still available.
	if !b.Acquire(100) {
		t.Fatal("budget should be fully available after clamped release")
	}
}

// TestMemBudgetConcurrentNeverOvercommits drives many goroutines at a
// budget that fits only a few simultaneous reservations and asserts
// the committed total never exceeds the ceiling — the core invariant
// the OOM fix depends on. Run under -race.
func TestMemBudgetConcurrentNeverOvercommits(t *testing.T) {
	const (
		budgetMax  = 1000
		chunk      = 100 // at most 10 concurrent holders
		goroutines = 64
		iterations = 200
	)
	b := NewMemBudget(budgetMax)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				if b.Acquire(chunk) {
					if got := b.InUse(); got > budgetMax {
						t.Errorf("InUse %d exceeded max %d", got, budgetMax)
					}
					b.Release(chunk)
				}
			}
		})
	}
	wg.Wait()
	if b.InUse() != 0 {
		t.Fatalf("all reservations released; InUse = %d, want 0", b.InUse())
	}
}
