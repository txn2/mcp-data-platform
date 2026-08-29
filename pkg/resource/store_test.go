package resource

import (
	"strings"
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

// TestBuildScopeWhere_WithPathFilter pins what "open a folder" means in SQL: the
// folder itself and everything beneath it, which is two bindings -- the exact
// path and the subtree pattern -- rather than the single equality the flat
// category was filtered by.
func TestBuildScopeWhere_WithPathFilter(t *testing.T) {
	filter := Filter{
		Scopes: []ScopeFilter{{Scope: ScopeGlobal}},
		Path:   "samples",
	}
	where, args := buildScopeWhere(filter)
	if !strings.Contains(where, "path = $2 OR path LIKE $3") {
		t.Fatalf("where does not read the folder and its subtree: %s", where)
	}
	// 1 scope arg + the folder + its subtree pattern
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}
	if args[1] != "samples" || args[2] != "samples/%" {
		t.Errorf("args = %v, want the folder and its subtree pattern", args[1:])
	}
}

// TestBuildScopeWhere_PathFilterEscapesWildcards guards the one way a prefix
// filter can widen: a folder name is validated at every write door, but the
// pattern is built from whatever the query string carried, and an unescaped %
// would list the whole library as if one folder had been opened.
func TestBuildScopeWhere_PathFilterEscapesWildcards(t *testing.T) {
	_, args := buildScopeWhere(Filter{
		Scopes: []ScopeFilter{{Scope: ScopeGlobal}},
		Path:   "sam%le_s",
	})
	if args[2] != `sam\%le\_s/%` {
		t.Errorf("pattern = %v, want the wildcards escaped", args[2])
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
		Scopes: []ScopeFilter{{Scope: ScopeGlobal}, {Scope: ScopeUser, ScopeID: "u1"}},
		Path:   "samples",
		Tag:    "finance",
		Query:  "test",
	}
	where, args := buildScopeWhere(filter)
	if where == "" {
		t.Fatal("where is empty")
	}
	// Expected: 8 args total (two scopes, the folder and its subtree pattern,
	// tag, two query patterns).
	if len(args) != 8 {
		t.Fatalf("expected 8 args, got %d: %v", len(args), args)
	}
}

func TestDefaultListLimit(t *testing.T) {
	if DefaultListLimit != 100 {
		t.Errorf("DefaultListLimit = %d, want 100", DefaultListLimit)
	}
}
