//go:build integration

package resource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// storedFolderCounts is the folder list a library reports, path to count.
func storedFolderCounts(t *testing.T, store Store, lib ScopeFilter) map[string]int {
	t.Helper()
	folders, err := store.Folders(context.Background(), Filter{Scopes: []ScopeFilter{lib}})
	require.NoError(t, err)
	out := map[string]int{}
	for _, f := range folders {
		out[f.Path] = f.Count
		assert.NotNil(t, f.UpdatedAt, "folder %s carries when it last changed", f.Path)
	}
	return out
}

// TestStoredFolders_RealDB is #1872 against a real PostgreSQL: a folder is
// created empty, outlives its last file, moves with its subtree (up its own
// tree included), and is deleted only once empty.
func TestStoredFolders_RealDB(t *testing.T) {
	pg := NewPostgresStore(testdb.New(t))
	ctx := context.Background()
	fs := pg.(FolderStore)
	deps := Deps{Store: pg, Folders: fs, People: pg.(PeopleLister)}
	owner := BuildClaims("sub-folder", "owner@example.com", "", nil, false)
	lib := ScopeFilter{Scope: ScopeUser, ScopeID: "sub-folder"}

	file := folderFile("res_sf_1", "data/weekly", "orders.csv")
	file.UploaderEmail = "owner@example.com"
	require.NoError(t, pg.Insert(ctx, file))

	// Created empty, beside a folder that is in use.
	require.NoError(t, CreateFolder(ctx, deps, &owner, NewFolder{Library: lib, Path: "data/empty"}))
	got := storedFolderCounts(t, pg, lib)
	assert.Equal(t, 0, got["data/empty"], "an empty folder is listed")
	assert.Equal(t, 1, got["data"])
	assert.ErrorIs(t, CreateFolder(ctx, deps, &owner, NewFolder{Library: lib, Path: "data/weekly"}), ErrFolderExists)

	// The last file moving out leaves its folder behind.
	require.NoError(t, pg.Move(ctx, []Move{{
		ID: file.ID, Scope: ScopeUser, ScopeID: "sub-folder", Path: "other",
		URI: BuildURI("mcp", ScopeUser, "sub-folder", "other", file.Filename), FromURI: file.URI,
	}}))
	got = storedFolderCounts(t, pg, lib)
	assert.Contains(t, got, "data/weekly", "the emptied folder stays")
	assert.Equal(t, 0, got["data/weekly"])
	assert.Equal(t, 1, got["other"], "the destination was recorded with the move")

	// A folder holding only empty folders moves.
	moved, err := MoveFolder(ctx, deps, &owner, FolderRename{Library: lib, From: "data", To: "archive"})
	require.NoError(t, err)
	assert.Empty(t, moved.Moved)
	got = storedFolderCounts(t, pg, lib)
	assert.NotContains(t, got, "data")
	assert.Contains(t, got, "archive/weekly")
	assert.Contains(t, got, "archive/empty")

	// Up its own tree: a/b/b becomes a/b while a/b is being moved.
	require.NoError(t, CreateFolder(ctx, deps, &owner, NewFolder{Library: lib, Path: "a/b/b"}))
	_, err = MoveFolder(ctx, deps, &owner, FolderRename{Library: lib, From: "a/b", To: "a"})
	require.NoError(t, err)
	got = storedFolderCounts(t, pg, lib)
	assert.Contains(t, got, "a/b", "the inner folder took its parent's place")
	assert.NotContains(t, got, "a/b/b")

	// Deleted only once empty; a path naming nothing is not found.
	assert.ErrorIs(t, DeleteFolder(ctx, deps, &owner, NewFolder{Library: lib, Path: "other"}), ErrFolderNotEmpty)
	require.NoError(t, DeleteFolder(ctx, deps, &owner, NewFolder{Library: lib, Path: "archive"}))
	got = storedFolderCounts(t, pg, lib)
	assert.NotContains(t, got, "archive")
	assert.NotContains(t, got, "archive/weekly")
	assert.ErrorIs(t, DeleteFolder(ctx, deps, &owner, NewFolder{Library: lib, Path: "nope"}), ErrFolderEmpty)

	// Somebody else may not add a folder to this library.
	other := BuildClaims("sub-other", "other@example.com", "", nil, false)
	assert.ErrorIs(t, CreateFolder(ctx, deps, &other, NewFolder{Library: lib, Path: "x"}), ErrMoveForbidden)

	// The People list names the library, its count, and the owner's address
	// where the owner uploaded into it.
	people, err := pg.(PeopleLister).People(ctx)
	require.NoError(t, err)
	require.Len(t, people, 1)
	assert.Equal(t, Person{ScopeID: "sub-folder", Email: "owner@example.com", Count: 1}, people[0])
}

// TestStoredFolders_RealDB_TheGlobalLibraryHoldsEachPathOnce: global keys by a
// NULL scope_id, which a plain unique constraint treats as distinct every time.
func TestStoredFolders_RealDB_TheGlobalLibraryHoldsEachPathOnce(t *testing.T) {
	pg := NewPostgresStore(testdb.New(t))
	ctx := context.Background()
	fs := pg.(FolderStore)
	global := ScopeFilter{Scope: ScopeGlobal}
	require.NoError(t, fs.CreateFolder(ctx, global, "brand", "admin@example.com"))
	assert.ErrorIs(t, fs.CreateFolder(ctx, global, "brand", "admin@example.com"), ErrFolderExists)
	require.NoError(t, pg.Insert(ctx, Resource{
		ID: "res_sf_g", Scope: ScopeGlobal, Path: "brand/logos", Filename: "mark.svg",
		DisplayName: "mark.svg", MIMEType: "image/svg+xml", S3Key: "k/res_sf_g",
		URI: BuildURI("mcp", ScopeGlobal, "", "brand/logos", "mark.svg"),
	}))
	rows, err := pg.(*postgresStore).countUnder(ctx, "resource_folders", global, "brand")
	require.NoError(t, err)
	assert.Equal(t, 2, rows, "brand once, brand/logos once")
}
