package callindex

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// The store's SQL is exercised against a real database by the catalog's
// RealDB gate; what these tests pin is the consumer contract: a unit is one
// record, a record that cannot be embedded completes cleanly rather than
// failing a job, and the sink writes exactly what the worker produced.

func TestSourceAndSinkAgreeOnTheKind(t *testing.T) {
	t.Parallel()

	store := NewStore(nil)
	if NewSource(store).Kind() != SourceKind || NewSink(store, "m").Kind() != SourceKind {
		t.Errorf("both halves must serve source_kind %q", SourceKind)
	}
}

func TestSourceOnSucceededHasNothingToRefresh(t *testing.T) {
	t.Parallel()

	// Search reads the vectors off call_records on every query, so a backfill
	// leaves no cache stale.
	NewSource(NewStore(nil)).OnSucceeded("call-1")
}

func TestSinkStampExpectedIsANoop(t *testing.T) {
	t.Parallel()

	// Gap detection is condition-based (no vector, or one from another model),
	// so there is no expected count to record per unit.
	if err := NewSink(NewStore(nil), "m").StampExpected(context.Background(), indexjobs.Key{}, 3); err != nil {
		t.Errorf("StampExpected: %v", err)
	}
}

func TestSinkWritesNothingForAnEmptyBatch(t *testing.T) {
	t.Parallel()

	// A record that yielded no item must not reach the database: the store is
	// nil here, so a write would panic rather than pass.
	sink := NewSink(NewStore(nil), "m")
	if err := sink.Upsert(context.Background(), indexjobs.Key{SourceID: "call-1"}, nil); err != nil {
		t.Errorf("Upsert: %v", err)
	}
	if err := sink.UpsertBatch(context.Background(), indexjobs.Key{SourceID: "call-1"}, nil); err != nil {
		t.Errorf("UpsertBatch: %v", err)
	}
}
