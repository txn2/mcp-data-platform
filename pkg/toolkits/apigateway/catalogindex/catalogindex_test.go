package catalogindex

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

func TestEncodeDecodeSourceID_RoundTrip(t *testing.T) {
	t.Parallel()
	cases := [][2]string{
		{"cat1", "spec1"},
		{"my-catalog", "openapi.yaml"},
		{"c", ""},
	}
	for _, c := range cases {
		id := EncodeSourceID(c[0], c[1])
		gotCat, gotSpec, ok := DecodeSourceID(id)
		if !ok {
			t.Errorf("DecodeSourceID(%q) not ok", id)
			continue
		}
		if gotCat != c[0] || gotSpec != c[1] {
			t.Errorf("round trip: (%q,%q) -> %q -> (%q,%q)", c[0], c[1], id, gotCat, gotSpec)
		}
	}
}

func TestSink_Coverage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := catalog.NewMemoryStore()
	if err := store.CreateCatalog(ctx, catalog.Catalog{ID: "c", Name: "c", DisplayName: "c"}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(ctx, "c", catalog.SpecEntry{
		SpecName: "v1", Content: "x", SourceKind: catalog.SourceInline,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	if err := store.SetOperationCount(ctx, "c", "v1", 3); err != nil {
		t.Fatalf("SetOperationCount: %v", err)
	}
	if err := store.UpsertOperationEmbeddings(ctx, "c", "v1", []catalog.OperationEmbedding{
		{OperationID: "op1", TextHash: []byte("h"), Embedding: []float32{1}, Dim: 1},
	}); err != nil {
		t.Fatalf("UpsertOperationEmbeddings: %v", err)
	}

	cov, err := NewSink(store).Coverage(ctx)
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if cov.Indexed != 1 || cov.Expected != 3 || !cov.ExpectedKnown {
		t.Errorf("coverage = %+v; want {Indexed 1, Expected 3, ExpectedKnown true}", cov)
	}
}

func TestDecodeSourceID_Malformed(t *testing.T) {
	t.Parallel()
	if _, _, ok := DecodeSourceID("no-delimiter-here"); ok {
		t.Error("a source_id without the delimiter must not decode")
	}
}

func TestEncode_DistinguishesPrefixCollidingCatalogs(t *testing.T) {
	t.Parallel()
	// "foo" must not prefix-collide with "foobar": the delimiter
	// guards the boundary so sourceIDPrefix("foo") excludes "foobar".
	pfx := sourceIDPrefix("foo")
	fooID := EncodeSourceID("foo", "s")
	foobarID := EncodeSourceID("foobar", "s")
	if len(fooID) < len(pfx) || fooID[:len(pfx)] != pfx {
		t.Errorf("foo id %q should start with prefix %q", fooID, pfx)
	}
	if len(foobarID) >= len(pfx) && foobarID[:len(pfx)] == pfx {
		t.Errorf("foobar id %q must NOT start with foo prefix %q", foobarID, pfx)
	}
}

func TestKindTriggerMapping(t *testing.T) {
	t.Parallel()
	pairs := []struct {
		kind    Kind
		trigger indexjobs.Trigger
	}{
		{KindSpecWrite, indexjobs.TriggerWrite},
		{KindReconciler, indexjobs.TriggerReconciler},
		{KindManualRetry, indexjobs.TriggerManualRetry},
	}
	for _, p := range pairs {
		if got := p.kind.toTrigger(); got != p.trigger {
			t.Errorf("%s.toTrigger() = %s; want %s", p.kind, got, p.trigger)
		}
		if got := kindFromTrigger(p.trigger); got != p.kind {
			t.Errorf("kindFromTrigger(%s) = %s; want %s", p.trigger, got, p.kind)
		}
	}
	// Unknown values default to the write vocabulary in both directions.
	if Kind("bogus").toTrigger() != indexjobs.TriggerWrite {
		t.Error("unknown kind should default to write trigger")
	}
	if kindFromTrigger(indexjobs.Trigger("bogus")) != KindSpecWrite {
		t.Error("unknown trigger should default to spec_write kind")
	}
}

// TestSink_RoundTrip exercises the Sink against the in-memory catalog
// store: list-existing, upsert, stamp-expected, and gap detection.
func TestSink_RoundTrip(t *testing.T) {
	t.Parallel()
	store := catalog.NewMemoryStore()
	ctx := context.Background()
	if err := store.CreateCatalog(ctx, catalog.Catalog{ID: "c", Name: "c", DisplayName: "c"}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(ctx, "c", catalog.SpecEntry{SpecName: "s", Content: "x", SourceKind: catalog.SourceInline}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}

	sink := NewSink(store)
	if sink.Kind() != SourceKind {
		t.Errorf("Kind() = %q; want %q", sink.Kind(), SourceKind)
	}
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: EncodeSourceID("c", "s")}

	// Initially empty.
	existing, err := sink.ListExisting(ctx, key)
	if err != nil {
		t.Fatalf("ListExisting: %v", err)
	}
	if len(existing) != 0 {
		t.Errorf("existing = %d; want 0", len(existing))
	}

	// Upsert two vectors.
	rows := []indexjobs.Vector{
		{ItemID: "op1", TextHash: []byte("h1"), Embedding: []float32{1, 2}, Model: "m", Dim: 2},
		{ItemID: "op2", TextHash: []byte("h2"), Embedding: []float32{3, 4}, Model: "m", Dim: 2},
	}
	if err := sink.Upsert(ctx, key, rows); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := sink.ListExisting(ctx, key)
	if err != nil {
		t.Fatalf("ListExisting after upsert: %v", err)
	}
	if len(got) != 2 || got["op1"].Model != "m" || got["op1"].Dim != 2 {
		t.Errorf("round-trip vectors wrong: %+v", got)
	}

	// UpsertBatch writes a chunk in place without deleting absent rows.
	if err := sink.UpsertBatch(ctx, key, []indexjobs.Vector{
		{ItemID: "op3", TextHash: []byte("h3"), Embedding: []float32{5, 6}, Model: "m", Dim: 2},
	}); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	got, err = sink.ListExisting(ctx, key)
	if err != nil {
		t.Fatalf("ListExisting after batch: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("after UpsertBatch want 3 vectors (op1, op2, op3); got %d", len(got))
	}
	// Restore the two-vector set for the gap assertions below.
	if err := sink.Upsert(ctx, key, rows); err != nil {
		t.Fatalf("Upsert restore: %v", err)
	}

	// Before StampExpected, operation_count(0) != 2 vectors -> a gap.
	gaps, err := sink.FindGaps(ctx)
	if err != nil {
		t.Fatalf("FindGaps: %v", err)
	}
	if len(gaps) != 1 || gaps[0] != EncodeSourceID("c", "s") {
		t.Errorf("gaps = %v; want one encoded c/s", gaps)
	}

	// Stamp the expected count to match -> no more gap.
	if err := sink.StampExpected(ctx, key, 2); err != nil {
		t.Fatalf("StampExpected: %v", err)
	}
	gaps, err = sink.FindGaps(ctx)
	if err != nil {
		t.Fatalf("FindGaps after stamp: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps after stamp = %v; want none", gaps)
	}
}

func TestSink_MalformedSourceIDErrors(t *testing.T) {
	t.Parallel()
	sink := NewSink(catalog.NewMemoryStore())
	ctx := context.Background()
	bad := indexjobs.Key{SourceKind: SourceKind, SourceID: "no-delim"}
	if _, err := sink.ListExisting(ctx, bad); err == nil {
		t.Error("ListExisting should reject a malformed source_id")
	}
	if err := sink.Upsert(ctx, bad, nil); err == nil {
		t.Error("Upsert should reject a malformed source_id")
	}
	if err := sink.UpsertBatch(ctx, bad, nil); err == nil {
		t.Error("UpsertBatch should reject a malformed source_id")
	}
	if err := sink.StampExpected(ctx, bad, 1); err == nil {
		t.Error("StampExpected should reject a malformed source_id")
	}
}
