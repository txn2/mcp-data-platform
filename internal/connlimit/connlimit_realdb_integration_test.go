//go:build integration

package connlimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestRealDB_TheLimitHoldsAcrossReplicas is #2209's point: two limiters over
// one database stand for two replicas, and between them a connection with a
// limit of one never has two calls in flight.
func TestRealDB_TheLimitHoldsAcrossReplicas(t *testing.T) {
	db := testdb.New(t)
	replicas := []*Limiter{New(db, nil), New(db, nil)}
	ctx := context.Background()

	var inFlight, most atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			slot, _, err := replicas[i%2].Acquire(ctx, "graphql", "erp", 1)
			if err != nil {
				t.Error(err)
				return
			}
			n := inFlight.Add(1)
			for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
			}
			time.Sleep(5 * time.Millisecond) // a call holding its slot
			inFlight.Add(-1)
			slot.Release()
		})
	}
	wg.Wait()
	if got := most.Load(); got != 1 {
		t.Fatalf("%d calls in flight at once across the replicas; the limit is 1", got)
	}
}

// TestRealDB_ASlotIsHeldOnlyWhileItIsHeld: a waiting call holds no database
// connection, a held slot frees on release, and a second slot is a second
// call in flight.
func TestRealDB_ASlotIsHeldOnlyWhileItIsHeld(t *testing.T) {
	db := testdb.New(t)
	a, b := New(db, nil), New(db, nil)
	ctx := context.Background()
	baseline := db.Stats().InUse // what the harness itself holds

	first, _, err := a.Acquire(ctx, "api", "billing", 2)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := b.Acquire(ctx, "api", "billing", 2)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, _, err := b.Acquire(short, "api", "billing", 2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a third call took a slot of two: %v", err)
	}
	if open := db.Stats().InUse - baseline; open != 2 {
		t.Errorf("%d database connections in use; the two held slots hold one each and the waiter none", open)
	}
	first.Release()
	third, _, err := b.Acquire(ctx, "api", "billing", 2)
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
	third.Release()
	if open := db.Stats().InUse - baseline; open != 0 {
		t.Errorf("%d database connections still in use after every slot was given back", open)
	}
}
