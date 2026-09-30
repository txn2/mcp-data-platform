//go:build integration

package embedyield

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// heldProvider holds each call until release is closed.
type heldProvider struct{ entered, release chan struct{} }

func (h heldProvider) Embed(context.Context, string) ([]float32, error) {
	h.entered <- struct{}{}
	<-h.release
	return []float32{1}, nil
}

func (h heldProvider) EmbedBatch(ctx context.Context, _ []string) ([][]float32, error) {
	v, err := h.Embed(ctx, "")
	return [][]float32{v}, err
}
func (heldProvider) Dimension() int { return 1 }
func (heldProvider) Kind() string   { return "test" }

// observed is a replica's signal that reports each time it answers busy.
type observed struct {
	*Store
	waiting chan struct{}
}

func (o observed) Busy(ctx context.Context) (bool, error) {
	busy, err := o.Store.Busy(ctx)
	if busy {
		select {
		case o.waiting <- struct{}{}:
		default:
		}
	}
	return busy, err
}

// TestRealDB_AReplicasWorkerYieldsToAnotherReplicasSearch is #1988 across
// replicas, over the real table: two gates stand in for two replicas sharing
// one database. While replica A has an interactive embed in flight, replica
// B's background embed waits, although nothing is in flight on B; once A's
// ends, B goes ahead. B is seen waiting -- the table answered it busy -- before
// A is let go, so no sleep decides the order.
func TestRealDB_AReplicasWorkerYieldsToAnotherReplicasSearch(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	storeA, storeB := New(db, time.Minute), New(db, time.Minute)
	a, b := embedding.NewGate(time.Hour), embedding.NewGate(time.Hour)
	a.Share(storeA, 10*time.Millisecond)
	sigB := observed{Store: storeB, waiting: make(chan struct{}, 1)}
	b.Share(sigB, 10*time.Millisecond)

	held := heldProvider{entered: make(chan struct{}, 1), release: make(chan struct{})}
	searched := make(chan struct{})
	go func() {
		defer close(searched)
		_, _ = a.Interactive(held).Embed(ctx, "a search on replica A")
	}()
	<-held.entered

	var calls atomic.Int64
	indexed := make(chan error, 1)
	go func() {
		_, err := b.Background(countingProvider{calls: &calls}).EmbedBatch(ctx, []string{"row"})
		indexed <- err
	}()
	// The deadline only bounds a failure: a mark that never lands leaves B
	// with nothing to wait on, and the test must say so rather than hang.
	select {
	case <-sigB.waiting:
	case <-time.After(time.Minute):
		t.Fatal("replica B never saw replica A's search in flight; the mark did not land")
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("replica B reached the embedder %d times while replica A had a search in flight", n)
	}

	close(held.release)
	<-searched
	if err := <-indexed; err != nil {
		t.Fatalf("replica B after A's search ended: %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("replica B called the embedder %d times, want 1", n)
	}
}

// TestRealDB_ALapsedMarkIsNotWaitedOn: a replica that stopped with its mark
// set is not waited on past its hold, and the next clear removes its row.
func TestRealDB_ALapsedMarkIsNotWaitedOn(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO embed_interactive (instance, until) VALUES ('stopped', now() - interval '1 second')`); err != nil {
		t.Fatal(err)
	}
	s := New(db, time.Minute)
	if busy, err := s.Busy(ctx); err != nil || busy {
		t.Fatalf("Busy = %v, %v; a lapsed mark must not count", busy, err)
	}
	if err := s.Mark(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Mark(ctx); err != nil {
		t.Fatalf("a second mark must refresh the row: %v", err)
	}
	if busy, _ := s.Busy(ctx); !busy {
		t.Fatal("a live mark must count")
	}
	if err := s.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM embed_interactive`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d rows remain; a clear removes this replica's row and every lapsed one", rows)
	}
}

type countingProvider struct{ calls *atomic.Int64 }

func (c countingProvider) Embed(context.Context, string) ([]float32, error) {
	c.calls.Add(1)
	return []float32{1}, nil
}

func (c countingProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	c.calls.Add(1)
	return make([][]float32, len(texts)), nil
}
func (countingProvider) Dimension() int { return 1 }
func (countingProvider) Kind() string   { return "test" }
