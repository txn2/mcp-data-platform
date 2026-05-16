package catalog

import (
	"context"
	"errors"
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
