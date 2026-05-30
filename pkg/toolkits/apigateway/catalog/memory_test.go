package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMemoryStore_CatalogCRUD(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()

	err := s.CreateCatalog(ctx, Catalog{
		ID: "petstore", Name: "petstore", DisplayName: "Petstore",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got, err := s.GetCatalog(ctx, "petstore"); err != nil || got.DisplayName != "Petstore" {
		t.Fatalf("Get: %+v err=%v", got, err)
	}
	list, err := s.ListCatalogs(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %+v err=%v", list, err)
	}

	dn := "Updated"
	if err := s.UpdateCatalog(ctx, "petstore", Update{DisplayName: &dn}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.GetCatalog(ctx, "petstore")
	if got.DisplayName != "Updated" {
		t.Fatalf("display_name not updated: %+v", got)
	}

	if err := s.DeleteCatalog(ctx, "petstore"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetCatalog(ctx, "petstore"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err=%v", err)
	}
}

func TestMemoryStore_CreateInvalidID(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	err := s.CreateCatalog(context.Background(), Catalog{ID: "BAD"})
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("err=%v want ErrInvalidID", err)
	}
}

func TestMemoryStore_DuplicateID(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.CreateCatalog(ctx, Catalog{ID: "a", Name: "a", DisplayName: "A"})
	err := s.CreateCatalog(ctx, Catalog{ID: "a", Name: "b", DisplayName: "B"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v want ErrConflict", err)
	}
}

func TestMemoryStore_DuplicateNameVersion(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.CreateCatalog(ctx, Catalog{ID: "a", Name: "shared", Version: "1", DisplayName: "A"})
	err := s.CreateCatalog(ctx, Catalog{ID: "b", Name: "shared", Version: "1", DisplayName: "B"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v want ErrConflict", err)
	}
}

func TestMemoryStore_GetNotFound(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	_, err := s.GetCatalog(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryStore_UpdateNotFound(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	dn := "x"
	err := s.UpdateCatalog(context.Background(), "ghost", Update{DisplayName: &dn})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryStore_UpdateConflict(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.CreateCatalog(ctx, Catalog{ID: "a", Name: "n1", Version: "1", DisplayName: "A"})
	_ = s.CreateCatalog(ctx, Catalog{ID: "b", Name: "n2", Version: "2", DisplayName: "B"})
	target := "n1"
	v := "1"
	err := s.UpdateCatalog(ctx, "b", Update{Name: &target, Version: &v})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v want ErrConflict", err)
	}
}

func TestMemoryStore_DeleteNotFound(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	if err := s.DeleteCatalog(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryStore_SpecCRUD(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.CreateCatalog(ctx, Catalog{ID: "c", Name: "c", DisplayName: "C"})

	if err := s.UpsertSpec(ctx, "c", SpecEntry{
		SpecName: "default", Content: "x", SourceKind: SourceInline,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Re-upsert to exercise the update branch (CreatedAt preserved).
	if err := s.UpsertSpec(ctx, "c", SpecEntry{
		SpecName: "default", Content: "y", SourceKind: SourceInline,
	}); err != nil {
		t.Fatalf("Upsert v2: %v", err)
	}
	got, err := s.GetSpec(ctx, "c", "default")
	if err != nil || got.Content != "y" {
		t.Fatalf("Get: %+v err=%v", got, err)
	}
	list, _ := s.ListSpecs(ctx, "c")
	if len(list) != 1 {
		t.Fatalf("List: %+v", list)
	}
	if err := s.DeleteSpec(ctx, "c", "default"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestMemoryStore_UpsertSpec_InvalidName(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	err := s.UpsertSpec(context.Background(), "c", SpecEntry{
		SpecName: "BAD", SourceKind: SourceInline,
	})
	if !errors.Is(err, ErrInvalidSpecName) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryStore_UpsertSpec_InvalidKind(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	err := s.UpsertSpec(context.Background(), "c", SpecEntry{
		SpecName: "default", SourceKind: "bogus",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMemoryStore_UpsertSpec_CatalogMissing(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	err := s.UpsertSpec(context.Background(), "ghost", SpecEntry{
		SpecName: "default", Content: "x", SourceKind: SourceInline,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryStore_GetSpec_NotFound(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.CreateCatalog(ctx, Catalog{ID: "c", Name: "c", DisplayName: "C"})
	_, err := s.GetSpec(ctx, "c", "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	_, err = s.GetSpec(ctx, "ghost", "x")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryStore_ListSpecs_EmptyCatalog(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.CreateCatalog(ctx, Catalog{ID: "c", Name: "c", DisplayName: "C"})
	list, err := s.ListSpecs(ctx, "c")
	if err != nil || len(list) != 0 {
		t.Fatalf("list=%v err=%v", list, err)
	}
}

func TestMemoryStore_DeleteSpec_NotFound(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	if err := s.DeleteSpec(ctx, "ghost", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	_ = s.CreateCatalog(ctx, Catalog{ID: "c", Name: "c", DisplayName: "C"})
	if err := s.DeleteSpec(ctx, "c", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestMemoryStore_ReferencingConnections_AlwaysNil(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	refs, err := s.ReferencingConnections(context.Background(), "anything")
	if err != nil || refs != nil {
		t.Fatalf("refs=%v err=%v", refs, err)
	}
}

func TestMemoryStore_CascadeOnCatalogDelete(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.CreateCatalog(ctx, Catalog{ID: "c", Name: "c", DisplayName: "C"})
	_ = s.UpsertSpec(ctx, "c", SpecEntry{
		SpecName: "default", Content: "x", SourceKind: SourceInline,
	})
	_ = s.DeleteCatalog(ctx, "c")
	if _, err := s.GetSpec(ctx, "c", "default"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected specs gone after catalog delete: err=%v", err)
	}
}

// TestMemoryStore_UpsertSpec_RejectsInvalidBasePath proves the
// MemoryStore enforces NormalizeBasePath at write time, matching
// the PostgresStore behavior so both backends reject identical
// operator-input mistakes.
func TestMemoryStore_UpsertSpec_RejectsInvalidBasePath(t *testing.T) {
	s := NewMemoryStore()
	_ = s.CreateCatalog(context.Background(), Catalog{ID: "p", Name: "p", DisplayName: "P"})
	err := s.UpsertSpec(context.Background(), "p", SpecEntry{
		SpecName: "default", Content: "x", SourceKind: SourceInline,
		BasePath: "no-leading-slash",
	})
	if err == nil {
		t.Fatal("expected ErrInvalidBasePath for missing leading slash")
	}
	if !errors.Is(err, ErrInvalidBasePath) {
		t.Errorf("err=%v want errors.Is ErrInvalidBasePath", err)
	}
}

// TestMemoryStore_UpsertSpec_TitleAndDescription proves the operator
// summary overrides round-trip (normalized: trimmed) and an over-cap
// value is rejected with ErrInvalidSpecMetadata, matching the
// PostgresStore behavior.
func TestMemoryStore_UpsertSpec_TitleAndDescription(t *testing.T) {
	s := NewMemoryStore()
	_ = s.CreateCatalog(context.Background(), Catalog{ID: "p", Name: "p", DisplayName: "P"})
	if err := s.UpsertSpec(context.Background(), "p", SpecEntry{
		SpecName: "default", Content: "x", SourceKind: SourceInline,
		Title: "  Orders API  ", Description: "Manage orders",
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	got, err := s.GetSpec(context.Background(), "p", "default")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if got.Title != "Orders API" {
		t.Errorf("title=%q want trimmed %q", got.Title, "Orders API")
	}
	if got.Description != "Manage orders" {
		t.Errorf("description=%q want %q", got.Description, "Manage orders")
	}

	err = s.UpsertSpec(context.Background(), "p", SpecEntry{
		SpecName: "bad", Content: "x", SourceKind: SourceInline,
		Description: strings.Repeat("x", 2001),
	})
	if !errors.Is(err, ErrInvalidSpecMetadata) {
		t.Errorf("err=%v want errors.Is ErrInvalidSpecMetadata", err)
	}
}

// TestMemoryStore_OperationEmbeddings_RoundTrip exercises the
// embedding row contract: an upsert followed by a list returns the
// same rows; a second upsert with different rows replaces the set
// rather than appending; a missing (catalog, spec) returns nil
// without error.
func TestMemoryStore_OperationEmbeddings_RoundTrip(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	mustCreateCatalogWithSpec(t, s, "petstore", "default")

	rows := []OperationEmbedding{
		{OperationID: "list", TextHash: []byte{0x01}, Embedding: []float32{0.1, 0.2}, Model: "test", Dim: 2},
		{OperationID: "create", TextHash: []byte{0x02}, Embedding: []float32{0.3, 0.4}, Model: "test", Dim: 2},
	}
	if err := s.UpsertOperationEmbeddings(ctx, "petstore", "default", rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.ListOperationEmbeddings(ctx, "petstore", "default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}

	// Second upsert with one row should REPLACE the prior set.
	if err := s.UpsertOperationEmbeddings(ctx, "petstore", "default", rows[:1]); err != nil {
		t.Fatalf("upsert replace: %v", err)
	}
	got, _ = s.ListOperationEmbeddings(ctx, "petstore", "default")
	if len(got) != 1 || got[0].OperationID != "list" {
		t.Fatalf("after replace want [list]; got %+v", got)
	}

	// Unknown (catalog, spec) returns nil, nil — the toolkit treats
	// this as "no vectors yet, fall back to lexical".
	rows2, err := s.ListOperationEmbeddings(ctx, "petstore", "other")
	if err != nil || rows2 != nil {
		t.Fatalf("unknown spec: got rows=%v err=%v; want nil/nil", rows2, err)
	}
}

// TestMemoryStore_OperationEmbeddings_DeleteSpecCascades proves
// that removing a spec also drops its embedding rows, matching the
// Postgres FK CASCADE.
func TestMemoryStore_OperationEmbeddings_DeleteSpecCascades(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	mustCreateCatalogWithSpec(t, s, "p", "v1")
	_ = s.UpsertOperationEmbeddings(ctx, "p", "v1", []OperationEmbedding{
		{OperationID: "a", Embedding: []float32{1, 0}, Dim: 2},
	})
	if err := s.DeleteSpec(ctx, "p", "v1"); err != nil {
		t.Fatalf("DeleteSpec: %v", err)
	}
	rows, _ := s.ListOperationEmbeddings(ctx, "p", "v1")
	if rows != nil {
		t.Errorf("DeleteSpec must cascade to embeddings; got %v", rows)
	}
}

// TestMemoryStore_OperationEmbeddings_DeleteCatalogCascades proves
// the same cascade chain for catalog deletion (catalog → specs →
// embeddings), so the in-memory backend matches the FK cascade in
// migration 000044.
func TestMemoryStore_OperationEmbeddings_DeleteCatalogCascades(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	mustCreateCatalogWithSpec(t, s, "p", "v1")
	_ = s.UpsertOperationEmbeddings(ctx, "p", "v1", []OperationEmbedding{
		{OperationID: "a", Embedding: []float32{1, 0}, Dim: 2},
	})
	if err := s.DeleteCatalog(ctx, "p"); err != nil {
		t.Fatalf("DeleteCatalog: %v", err)
	}
	rows, _ := s.ListOperationEmbeddings(ctx, "p", "v1")
	if rows != nil {
		t.Errorf("DeleteCatalog must cascade to embeddings; got %v", rows)
	}
}

// TestMemoryStore_OperationEmbeddings_UpsertWithoutSpec rejects
// embedding writes for an unknown (catalog, spec) pair. The
// Postgres backend bounces on the FK; the memory backend mirrors
// that with an ErrNotFound for parity.
func TestMemoryStore_OperationEmbeddings_UpsertWithoutSpec(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	err := s.UpsertOperationEmbeddings(context.Background(), "nope", "nope",
		[]OperationEmbedding{{OperationID: "x", Embedding: []float32{1}, Dim: 1}})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v want ErrNotFound", err)
	}
}

// TestMemoryStore_OperationEmbeddings_DeleteIsIdempotent proves
// the delete admin path doesn't error on a (catalog, spec) that
// never had vectors.
func TestMemoryStore_OperationEmbeddings_DeleteIsIdempotent(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	if err := s.DeleteOperationEmbeddings(context.Background(), "nope", "nope"); err != nil {
		t.Errorf("delete on missing must be nil; got %v", err)
	}
}

// TestMemoryStore_UpsertOperationEmbeddingsBatch_AdditiveSemantics
// proves the per-batch path adds rows without removing rows
// outside the batch — the property the embed-jobs worker relies
// on for crash-resume across chunks. A second call with a
// disjoint set must leave the first set intact, in contrast to
// the atomic UpsertOperationEmbeddings which replaces the whole
// spec.
func TestMemoryStore_UpsertOperationEmbeddingsBatch_AdditiveSemantics(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	mustCreateCatalogWithSpec(t, s, "p", "v1")
	batch1 := []OperationEmbedding{
		{OperationID: "a", TextHash: []byte{0x01}, Embedding: []float32{1, 0}, Model: "m", Dim: 2},
	}
	if err := s.UpsertOperationEmbeddingsBatch(ctx, "p", "v1", batch1); err != nil {
		t.Fatalf("UpsertOperationEmbeddingsBatch #1: %v", err)
	}
	batch2 := []OperationEmbedding{
		{OperationID: "b", TextHash: []byte{0x02}, Embedding: []float32{0, 1}, Model: "m", Dim: 2},
	}
	if err := s.UpsertOperationEmbeddingsBatch(ctx, "p", "v1", batch2); err != nil {
		t.Fatalf("UpsertOperationEmbeddingsBatch #2: %v", err)
	}
	got, err := s.ListOperationEmbeddings(ctx, "p", "v1")
	if err != nil {
		t.Fatalf("ListOperationEmbeddings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("after additive batches want 2 rows, got %d", len(got))
	}
}

// TestMemoryStore_UpsertOperationEmbeddingsBatch_UpdatesExisting
// proves a re-upsert of an existing operation_id overwrites the
// stored vector rather than ignoring the call.
func TestMemoryStore_UpsertOperationEmbeddingsBatch_UpdatesExisting(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ctx := context.Background()
	mustCreateCatalogWithSpec(t, s, "p", "v1")
	first := []OperationEmbedding{
		{OperationID: "a", TextHash: []byte{0x01}, Embedding: []float32{1, 0}, Model: "m", Dim: 2},
	}
	if err := s.UpsertOperationEmbeddingsBatch(ctx, "p", "v1", first); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	updated := []OperationEmbedding{
		{OperationID: "a", TextHash: []byte{0x02}, Embedding: []float32{0, 1}, Model: "m", Dim: 2},
	}
	if err := s.UpsertOperationEmbeddingsBatch(ctx, "p", "v1", updated); err != nil {
		t.Fatalf("update batch: %v", err)
	}
	got, _ := s.ListOperationEmbeddings(ctx, "p", "v1")
	if len(got) != 1 {
		t.Fatalf("want 1 row after update, got %d", len(got))
	}
	if got[0].Embedding[1] != 1 {
		t.Errorf("update did not replace vector; got %+v", got[0])
	}
}

// TestMemoryStore_UpsertOperationEmbeddingsBatch_WithoutSpec
// rejects writes against an unknown (catalog, spec), mirroring
// the Postgres backend's FK violation surface.
func TestMemoryStore_UpsertOperationEmbeddingsBatch_WithoutSpec(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	err := s.UpsertOperationEmbeddingsBatch(context.Background(), "nope", "nope",
		[]OperationEmbedding{{OperationID: "x", Embedding: []float32{1}, Dim: 1}})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v want ErrNotFound", err)
	}
}

// mustCreateCatalogWithSpec is the shared boilerplate for the
// embedding tests: build a catalog + one inline spec so the FK
// guard on UpsertOperationEmbeddings is satisfied.
func mustCreateCatalogWithSpec(t *testing.T, s *MemoryStore, catalogID, specName string) {
	t.Helper()
	if err := s.CreateCatalog(context.Background(), Catalog{
		ID: catalogID, Name: catalogID, DisplayName: catalogID,
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := s.UpsertSpec(context.Background(), catalogID, SpecEntry{
		SpecName: specName, Content: "x", SourceKind: SourceInline,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
}

// TestMemoryStore_SetOperationCount round-trips the column
// the embedding worker stamps after a successful embed pass.
// MemoryStore mirrors the Postgres backend's behavior so the
// embedjobs worker tests can drive either store interchangeably.
func TestMemoryStore_SetOperationCount(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	mustCreateCatalogWithSpec(t, s, "p", "v1")
	if err := s.SetOperationCount(context.Background(), "p", "v1", 7); err != nil {
		t.Fatalf("SetOperationCount: %v", err)
	}
	got, err := s.GetSpec(context.Background(), "p", "v1")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if got.OperationCount != 7 {
		t.Errorf("OperationCount = %d; want 7", got.OperationCount)
	}
}

// TestMemoryStore_SetOperationCount_NotFound proves missing
// (catalog, spec) returns ErrNotFound so the worker can treat
// the case as best-effort and log only.
func TestMemoryStore_SetOperationCount_NotFound(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	if err := s.SetOperationCount(context.Background(), "ghost", "ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v want ErrNotFound", err)
	}
}

func TestMemoryStore_ListEmbeddingGaps(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	if err := s.CreateCatalog(ctx, Catalog{ID: "c", Name: "c", DisplayName: "c"}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	// spec "gap": operation_count 2 but zero vectors -> a gap.
	if err := s.UpsertSpec(ctx, "c", SpecEntry{SpecName: "gap", Content: "x", SourceKind: SourceInline}); err != nil {
		t.Fatalf("UpsertSpec gap: %v", err)
	}
	if err := s.SetOperationCount(ctx, "c", "gap", 2); err != nil {
		t.Fatalf("SetOperationCount: %v", err)
	}
	// spec "ok": operation_count 1 and one vector -> not a gap.
	if err := s.UpsertSpec(ctx, "c", SpecEntry{SpecName: "ok", Content: "x", SourceKind: SourceInline}); err != nil {
		t.Fatalf("UpsertSpec ok: %v", err)
	}
	if err := s.UpsertOperationEmbeddings(ctx, "c", "ok", []OperationEmbedding{
		{OperationID: "op1", TextHash: []byte("h"), Embedding: []float32{1}, Dim: 1},
	}); err != nil {
		t.Fatalf("UpsertOperationEmbeddings: %v", err)
	}
	if err := s.SetOperationCount(ctx, "c", "ok", 1); err != nil {
		t.Fatalf("SetOperationCount ok: %v", err)
	}

	gaps, err := s.ListEmbeddingGaps(ctx)
	if err != nil {
		t.Fatalf("ListEmbeddingGaps: %v", err)
	}
	if len(gaps) != 1 || gaps[0].SpecName != "gap" {
		t.Errorf("gaps = %+v; want only spec 'gap'", gaps)
	}
}
