package resource

import (
	"testing"
)

func TestBuildScopeWhere_GlobalOnly(t *testing.T) {
	filter := Filter{
		Scopes: []ScopeFilter{
			{Scope: ScopeGlobal},
		},
	}
	where, args := buildScopeWhere(filter)
	if where == "" {
		t.Fatal("where is empty")
	}
	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(args))
	}
	if args[0] != string(ScopeGlobal) {
		t.Errorf("arg[0] = %v, want %q", args[0], ScopeGlobal)
	}
}

func TestBuildScopeWhere_MultipleScopes(t *testing.T) {
	filter := Filter{
		Scopes: []ScopeFilter{
			{Scope: ScopeGlobal},
			{Scope: ScopeUser, ScopeID: "user-1"},
			{Scope: ScopePersona, ScopeID: "finance"},
		},
	}
	where, args := buildScopeWhere(filter)
	if where == "" {
		t.Fatal("where is empty")
	}
	// Global=1 arg, User=2 args, Persona=2 args = 5
	if len(args) != 5 {
		t.Fatalf("expected 5 args, got %d: %v", len(args), args)
	}
}

func TestBuildScopeWhere_WithCategoryFilter(t *testing.T) {
	filter := Filter{
		Scopes:   []ScopeFilter{{Scope: ScopeGlobal}},
		Category: "samples",
	}
	where, args := buildScopeWhere(filter)
	if where == "" {
		t.Fatal("where is empty")
	}
	// 1 scope arg + 1 category arg
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
	if args[1] != "samples" {
		t.Errorf("arg[1] = %v, want 'samples'", args[1])
	}
}

func TestBuildScopeWhere_WithTagFilter(t *testing.T) {
	filter := Filter{
		Scopes: []ScopeFilter{{Scope: ScopeGlobal}},
		Tag:    "finance",
	}
	_, args := buildScopeWhere(filter)
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
}

func TestBuildScopeWhere_WithQueryFilter(t *testing.T) {
	filter := Filter{
		Scopes: []ScopeFilter{{Scope: ScopeGlobal}},
		Query:  "test",
	}
	_, args := buildScopeWhere(filter)
	// 1 scope + 2 query (display_name ILIKE, description ILIKE)
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d", len(args))
	}
}

func TestBuildScopeWhere_AllFilters(t *testing.T) {
	filter := Filter{
		Scopes:   []ScopeFilter{{Scope: ScopeGlobal}, {Scope: ScopeUser, ScopeID: "u1"}},
		Category: "samples",
		Tag:      "finance",
		Query:    "test",
	}
	where, args := buildScopeWhere(filter)
	if where == "" {
		t.Fatal("where is empty")
	}
	// Expected: 7 args total (two scopes, category, tag, two query patterns).
	if len(args) != 7 {
		t.Fatalf("expected 7 args, got %d: %v", len(args), args)
	}
}

func TestDefaultListLimit(t *testing.T) {
	if DefaultListLimit != 100 {
		t.Errorf("DefaultListLimit = %d, want 100", DefaultListLimit)
	}
}
