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

// An administrator's unrestricted listing (#1553) has no scope predicate to
// bind, so the clause is a constant and every later filter numbers its
// placeholders from $1.
func TestBuildScopeWhere_EveryLibrary(t *testing.T) {
	where, args := buildScopeWhere(Filter{AllScopes: true})
	if where != unrestrictedVisibility {
		t.Fatalf("where = %q, want %s", where, unrestrictedVisibility)
	}
	if len(args) != 0 {
		t.Fatalf("expected no args, got %d: %v", len(args), args)
	}

	where, args = buildScopeWhere(Filter{AllScopes: true, Path: "samples", Tag: "finance"})
	if !strings.HasPrefix(where, "TRUE AND ") {
		t.Fatalf("where = %q, want the filters appended to the constant", where)
	}
	// The folder, its subtree pattern, and the tag.
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}
	if !strings.Contains(where, "$1") || !strings.Contains(where, "$3") {
		t.Errorf("where = %q, want placeholders numbered from $1", where)
	}
}

// Scopes on an unrestricted filter are inert: nothing narrows a listing that
// spans every library.
func TestBuildScopeWhere_EveryLibraryIgnoresScopes(t *testing.T) {
	where, args := buildScopeWhere(Filter{
		AllScopes: true,
		Scopes:    []ScopeFilter{{Scope: ScopeUser, ScopeID: "u1"}},
	})
	if where != unrestrictedVisibility || len(args) != 0 {
		t.Fatalf("where = %q args = %v, want an unnarrowed listing", where, args)
	}
}

// The folder rollup and the tag rollup (#1555). Both are assembled from the
// caller's scope filter, so neither exists as text in the source; a real
// PostgreSQL parses and plans the rendering through SQLSamples.
func TestBuildFolders_ExpandsThePathAndBindsTheScopes(t *testing.T) {
	query, args := buildFolders(Filter{
		Scopes: []ScopeFilter{{Scope: ScopeGlobal}, {Scope: ScopeUser, ScopeID: "u1"}},
	})
	if !strings.Contains(query, "generate_subscripts") {
		t.Errorf("query does not expand the path into its ancestors: %s", query)
	}
	if !strings.Contains(query, "GROUP BY chain.folder") {
		t.Errorf("query does not group by the folder: %s", query)
	}
	// The two scopes: global binds its name, the user scope its name and id.
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}
}

func TestBuildFolders_EveryLibraryNeedsNoScopeArgs(t *testing.T) {
	query, args := buildFolders(Filter{AllScopes: true})
	if !strings.Contains(query, "WHERE TRUE") {
		t.Errorf("an unrestricted rollup should carry the constant predicate: %s", query)
	}
	if len(args) != 0 {
		t.Errorf("expected no args, got %v", args)
	}
}

func TestBuildTags_UnnestsTheColumn(t *testing.T) {
	query, args := buildTags(Filter{Scopes: []ScopeFilter{{Scope: ScopeGlobal}}})
	if !strings.Contains(query, "unnest(r.tags)") {
		t.Errorf("query does not unnest the tags column: %s", query)
	}
	if !strings.Contains(query, "SELECT DISTINCT tag") {
		t.Errorf("query does not take the distinct tags: %s", query)
	}
	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %v", args)
	}
}

func TestDefaultListLimit(t *testing.T) {
	if DefaultListLimit != 100 {
		t.Errorf("DefaultListLimit = %d, want 100", DefaultListLimit)
	}
}
