package authevents

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoreInsertRejectsInvalid(t *testing.T) {
	s := NewMemoryStore()
	err := s.Insert(context.Background(), Event{Kind: "mcp", Name: "x", Type: "bogus", Actor: "a"})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
}

func TestMemoryStoreInsertStampsIDAndTime(t *testing.T) {
	s := NewMemoryStore()
	if err := s.Insert(context.Background(), Event{
		Kind: "mcp", Name: "x", Type: TypeConnectStarted, Actor: "a",
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := s.List(context.Background(), Filter{Kind: "mcp", Name: "x", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].ID == "" {
		t.Error("Insert should stamp ID")
	}
	if got[0].OccurredAt.IsZero() {
		t.Error("Insert should stamp OccurredAt")
	}
}

func TestMemoryStoreListFilters(t *testing.T) {
	s := NewMemoryStore()
	must := func(ev Event) {
		t.Helper()
		if err := s.Insert(context.Background(), ev); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	must(Event{Kind: "mcp", Name: "alpha", Type: TypeConnectStarted, Actor: "u"})
	must(Event{Kind: "mcp", Name: "beta", Type: TypeConnectStarted, Actor: "u"})
	must(Event{Kind: "api", Name: "alpha", Type: TypeConnectStarted, Actor: "u"})

	// By (kind, name)
	got, err := s.List(context.Background(), Filter{Kind: "mcp", Name: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "alpha" || got[0].Kind != "mcp" {
		t.Errorf("filter (mcp, alpha) returned %+v", got)
	}

	// Cross-connection (no kind/name)
	got, err = s.List(context.Background(), Filter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("cross-connection list returned %d events, want 3", len(got))
	}

	// Newest first
	if got[0].Name != "alpha" || got[0].Kind != "api" {
		t.Errorf("got[0] should be the most recent insert; got %+v", got[0])
	}
}

func TestMemoryStoreListRequiresLimit(t *testing.T) {
	s := NewMemoryStore()
	if _, err := s.List(context.Background(), Filter{Kind: "mcp", Name: "x"}); err == nil {
		t.Error("List with Limit=0 should return an error")
	}
}

func TestMemoryStoreListSince(t *testing.T) {
	s := NewMemoryStore()
	old := Event{
		Kind: "mcp", Name: "x", Type: TypeConnectStarted, Actor: "u",
		OccurredAt: time.Now().Add(-2 * time.Hour),
	}
	if err := s.Insert(context.Background(), old); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := s.Insert(context.Background(), Event{
		Kind: "mcp", Name: "x", Type: TypeConnectCompleted, Actor: "u",
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := s.List(context.Background(), Filter{
		Kind: "mcp", Name: "x", Limit: 10, Since: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Since filter returned %d, want 1 (the 2h-old event should be dropped)", len(got))
	}
	if got[0].Type != TypeConnectCompleted {
		t.Errorf("got[0].Type = %v, want %v", got[0].Type, TypeConnectCompleted)
	}
}

func TestMemoryStorePrune(t *testing.T) {
	s := NewMemoryStore()
	old := Event{
		Kind: "mcp", Name: "x", Type: TypeConnectStarted, Actor: "u",
		OccurredAt: time.Now().Add(-30 * 24 * time.Hour),
	}
	fresh := Event{Kind: "mcp", Name: "x", Type: TypeConnectCompleted, Actor: "u"}
	_ = s.Insert(context.Background(), old)
	_ = s.Insert(context.Background(), fresh)

	removed, err := s.Prune(context.Background(), time.Now().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("Prune removed %d, want 1", removed)
	}
	got, _ := s.List(context.Background(), Filter{Kind: "mcp", Name: "x", Limit: 10})
	if len(got) != 1 || got[0].Type != TypeConnectCompleted {
		t.Errorf("after prune, expected 1 event (connect_completed), got %+v", got)
	}
}
