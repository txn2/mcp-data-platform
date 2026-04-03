package configstore

import (
	"context"
	"errors"
	"sort"
	"testing"
)

func TestFileStore_Get_Success(t *testing.T) {
	store := NewFileStore(map[string]string{
		"server.name": "test-server",
	})

	entry, err := store.Get(context.Background(), "server.name")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if entry.Key != "server.name" {
		t.Errorf("Key = %q, want %q", entry.Key, "server.name")
	}
	if entry.Value != "test-server" {
		t.Errorf("Value = %q, want %q", entry.Value, "test-server")
	}
}

func TestFileStore_Get_NotFound(t *testing.T) {
	store := NewFileStore(map[string]string{})

	_, err := store.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestFileStore_Set_ReturnsReadOnly(t *testing.T) {
	store := NewFileStore(map[string]string{})

	err := store.Set(context.Background(), "key", "val", "admin")
	if !errors.Is(err, ErrReadOnly) {
		t.Errorf("Set() error = %v, want ErrReadOnly", err)
	}
}

func TestFileStore_Delete_ReturnsReadOnly(t *testing.T) {
	store := NewFileStore(map[string]string{})

	err := store.Delete(context.Background(), "key", "admin")
	if !errors.Is(err, ErrReadOnly) {
		t.Errorf("Delete() error = %v, want ErrReadOnly", err)
	}
}

func TestFileStore_List(t *testing.T) {
	store := NewFileStore(map[string]string{
		"a.key": "alpha",
		"b.key": "bravo",
	})

	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List() returned %d entries, want 2", len(entries))
	}

	// Sort for deterministic assertion (map iteration is random).
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })

	if entries[0].Key != "a.key" || entries[0].Value != "alpha" {
		t.Errorf("entries[0] = {%q, %q}, want {%q, %q}", entries[0].Key, entries[0].Value, "a.key", "alpha")
	}
	if entries[1].Key != "b.key" || entries[1].Value != "bravo" {
		t.Errorf("entries[1] = {%q, %q}, want {%q, %q}", entries[1].Key, entries[1].Value, "b.key", "bravo")
	}
}

func TestFileStore_Changelog_ReturnsEmpty(t *testing.T) {
	store := NewFileStore(map[string]string{})

	cl, err := store.Changelog(context.Background(), 10)
	if err != nil {
		t.Fatalf("Changelog() error = %v", err)
	}
	if len(cl) != 0 {
		t.Errorf("Changelog() returned %d entries, want 0", len(cl))
	}
}

func TestFileStore_Mode(t *testing.T) {
	store := NewFileStore(map[string]string{})

	if got := store.Mode(); got != "file" {
		t.Errorf("Mode() = %q, want %q", got, "file")
	}
}

func TestNewFileStore_NilMap(t *testing.T) {
	store := NewFileStore(nil)

	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List() returned %d entries, want 0", len(entries))
	}

	// Get on nil-initialized store should return ErrNotFound, not panic.
	_, err = store.Get(context.Background(), "any")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() on nil-init store error = %v, want ErrNotFound", err)
	}
}
