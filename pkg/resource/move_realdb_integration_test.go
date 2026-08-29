//go:build integration

package resource

// Real-Postgres round-trip tests for moving a resource between libraries and
// for the address it leaves behind.
//
// The move shipped in #1502 could never run: GetByURI's alias lookup joined
// resources to resource_uri_aliases and projected an unqualified column list,
// and both tables carry a uri column, so PostgreSQL refused the statement with
// 42702. The package's sqlmock tests matched that statement as a string and
// returned canned rows, so nothing before this file put it in front of a
// database (#1506).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// filedResource returns a resource ready to insert under the given id and URI.
func filedResource(id, uri string) Resource {
	return Resource{
		ID: id, Scope: ScopeUser, ScopeID: "sub-mover",
		Path: "templates", Filename: "report.md", DisplayName: "Report",
		Description: "Move round-trip fixture.", MIMEType: "text/markdown",
		SizeBytes: 12, S3Key: "resources/" + id + "/report.md", URI: uri,
		UploaderSub: "sub-mover", Tags: []string{},
	}
}

// TestResourceMove_RealDB_RewritesTheRowAndKeepsTheOldAddressResolving is the
// whole feature end to end against a database: the file is filed somewhere
// else, its new address answers, and the address it left still resolves to it.
func TestResourceMove_RealDB_RewritesTheRowAndKeepsTheOldAddressResolving(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	const (
		from = "mcp://user/sub-mover/templates/report.md"
		to   = "mcp://persona/ops/templates/report.md"
	)
	require.NoError(t, store.Insert(ctx, filedResource("res_move_1", from)))

	require.NoError(t, store.Move(ctx, []Move{{
		ID: "res_move_1", Path: "templates",
		Scope: ScopePersona, ScopeID: "ops", URI: to, FromURI: from,
	}}), "move to the ops persona library")

	moved, err := store.Get(ctx, "res_move_1")
	require.NoError(t, err)
	require.NotNil(t, moved)
	assert.Equal(t, ScopePersona, moved.Scope)
	assert.Equal(t, "ops", moved.ScopeID)
	assert.Equal(t, to, moved.URI)

	byNew, err := store.GetByURI(ctx, to)
	require.NoError(t, err, "the address the resource now holds")
	require.NotNil(t, byNew)
	assert.Equal(t, "res_move_1", byNew.ID)

	// The address a knowledge page or a script body may already cite.
	byOld, err := store.GetByURI(ctx, from)
	require.NoError(t, err, "the vacated address still resolves")
	require.NotNil(t, byOld)
	assert.Equal(t, "res_move_1", byOld.ID)
	assert.Equal(t, to, byOld.URI, "resolving an alias answers with the resource's current address")
}

// TestResourceMove_RealDB_ALiveAddressWinsOverAVacatedOne covers the ordering
// GetByURI exists to enforce: someone moves a file out of their library and
// uploads another under the same name, and that name must reach the new file.
func TestResourceMove_RealDB_ALiveAddressWinsOverAVacatedOne(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	const (
		addr = "mcp://user/sub-mover/templates/report.md"
		to   = "mcp://global/templates/report.md"
	)
	require.NoError(t, store.Insert(ctx, filedResource("res_move_2", addr)))
	require.NoError(t, store.Move(ctx, []Move{{
		ID: "res_move_2", Path: "templates",
		Scope: ScopeGlobal, URI: to, FromURI: addr,
	}}))

	// A second file takes the address the first one vacated.
	require.NoError(t, store.Insert(ctx, filedResource("res_move_3", addr)))

	got, err := store.GetByURI(ctx, addr)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "res_move_3", got.ID, "the live occupant wins over the alias")
}

// TestResourceMove_RealDB_MovingBackLeavesNoAliasOfItsOwnAddress covers the
// DELETE half of readdressAliases: a resource must never hold an alias
// claiming the address it currently occupies.
func TestResourceMove_RealDB_MovingBackLeavesNoAliasOfItsOwnAddress(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	const (
		home = "mcp://user/sub-mover/templates/report.md"
		away = "mcp://global/templates/report.md"
	)
	require.NoError(t, store.Insert(ctx, filedResource("res_move_4", home)))
	require.NoError(t, store.Move(ctx, []Move{{
		ID: "res_move_4", Path: "templates",
		Scope: ScopeGlobal, URI: away, FromURI: home,
	}}))
	require.NoError(t, store.Move(ctx, []Move{{
		ID: "res_move_4", Path: "templates",
		Scope: ScopeUser, ScopeID: "sub-mover", URI: home, FromURI: away,
	}}), "move back where it came from")

	var claimsOwnURI int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM resource_uri_aliases WHERE uri = $1`, home).Scan(&claimsOwnURI))
	assert.Zero(t, claimsOwnURI, "no alias claims the address the resource holds")

	// The address it left on the way back is the one that now aliases.
	byAway, err := store.GetByURI(ctx, away)
	require.NoError(t, err)
	require.NotNil(t, byAway)
	assert.Equal(t, "res_move_4", byAway.ID)
}

// TestResourceMove_RealDB_MissingAddressIsNotFound is the miss path every
// caller depends on: declaring a reference to a URI that names nothing has to
// come back as not-found so the caller can say so, rather than as a driver
// error. This is the second half of what #1506 broke.
func TestResourceMove_RealDB_MissingAddressIsNotFound(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	got, err := store.GetByURI(ctx, "mcp://global/test/nothing-is-filed-here.md")
	assert.Nil(t, got)
	require.Error(t, err)
	assert.True(t, IsNotFound(err), "a URI naming nothing is not found, not a database error: %v", err)
}

// folderFile returns a resource filed at one path in the fixture library, with
// the URI the platform would have minted for it.
func folderFile(id, path, filename string) Resource {
	return Resource{
		ID: id, Scope: ScopeUser, ScopeID: "sub-folder",
		Path: path, Filename: filename, DisplayName: filename,
		Description: "Folder rename fixture.", MIMEType: "text/csv",
		SizeBytes: 3, S3Key: "resources/" + id + "/" + filename,
		URI:         BuildURI("mcp", ScopeUser, "sub-folder", path, filename),
		UploaderSub: "sub-folder", Tags: []string{},
	}
}

// TestResourceFolderRename_RealDB_RewritesTheWholeSubtreeInOneTransaction is
// what only a database can answer: the batch rewrites every row's address and
// records every alias, under the UNIQUE constraint on uri that a fake does not
// enforce.
func TestResourceFolderRename_RealDB_RewritesTheWholeSubtreeInOneTransaction(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	files := []Resource{
		folderFile("res_fold_1", "data", "top.csv"),
		folderFile("res_fold_2", "data/media-manager", "mid.csv"),
		folderFile("res_fold_3", "data/media-manager/shows", "deep.csv"),
	}
	for _, r := range files {
		require.NoError(t, store.Insert(ctx, r))
	}

	moves := make([]Move, 0, len(files))
	for _, r := range files {
		path := RepointPath(r.Path, "data", "archive")
		moves = append(moves, Move{
			ID: r.ID, Scope: r.Scope, ScopeID: r.ScopeID, Path: path,
			URI: BuildURI("mcp", r.Scope, r.ScopeID, path, r.Filename), FromURI: r.URI,
		})
	}
	require.NoError(t, store.Move(ctx, moves), "rename the whole subtree")

	for i, m := range moves {
		got, err := store.Get(ctx, m.ID)
		require.NoError(t, err)
		assert.Equal(t, m.Path, got.Path)
		assert.Equal(t, m.URI, got.URI)

		vacated, err := store.GetByURI(ctx, files[i].URI)
		require.NoError(t, err, "the address %s no longer resolves", files[i].URI)
		assert.Equal(t, m.ID, vacated.ID)
	}
}

// TestResourceFolderRename_RealDB_MovingAFolderUpItsOwnTree is the case the
// parking step exists for. Renaming a/b to a gives res_up_1 the address res_up_2
// is still holding, and the UNIQUE constraint on uri is not deferrable, so
// without vacating both first the batch fails on statement order alone.
func TestResourceFolderRename_RealDB_MovingAFolderUpItsOwnTree(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	first := folderFile("res_up_1", "a/b", "x.csv")
	second := folderFile("res_up_2", "a/b/b", "x.csv")
	require.NoError(t, store.Insert(ctx, first))
	require.NoError(t, store.Insert(ctx, second))

	require.NoError(t, store.Move(ctx, []Move{
		{
			ID: first.ID, Scope: first.Scope, ScopeID: first.ScopeID, Path: "a",
			URI: BuildURI("mcp", first.Scope, first.ScopeID, "a", "x.csv"), FromURI: first.URI,
		},
		{
			ID: second.ID, Scope: second.Scope, ScopeID: second.ScopeID, Path: "a/b",
			URI: BuildURI("mcp", second.Scope, second.ScopeID, "a/b", "x.csv"), FromURI: second.URI,
		},
	}))

	up, err := store.Get(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "mcp://user/sub-folder/a/x.csv", up.URI)
	moved, err := store.Get(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, "mcp://user/sub-folder/a/b/x.csv", moved.URI)
}

// TestResourceFolderRename_RealDB_ACollisionRollsTheWholeBatchBack is the
// all-or-nothing half: a half-renamed folder is not a state anyone should be
// able to observe, and the constraint is the last word on it.
func TestResourceFolderRename_RealDB_ACollisionRollsTheWholeBatchBack(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	moving := folderFile("res_clash_1", "data", "a.csv")
	sibling := folderFile("res_clash_2", "data", "b.csv")
	occupant := folderFile("res_clash_3", "archive", "a.csv")
	for _, r := range []Resource{moving, sibling, occupant} {
		require.NoError(t, store.Insert(ctx, r))
	}

	err := store.Move(ctx, []Move{
		{
			ID: moving.ID, Scope: moving.Scope, ScopeID: moving.ScopeID, Path: "archive",
			URI: occupant.URI, FromURI: moving.URI,
		},
		{
			ID: sibling.ID, Scope: sibling.Scope, ScopeID: sibling.ScopeID, Path: "archive",
			URI: BuildURI("mcp", sibling.Scope, sibling.ScopeID, "archive", "b.csv"), FromURI: sibling.URI,
		},
	})
	require.ErrorIs(t, err, ErrURIConflict)

	// Neither row moved, and neither is left parked on the sentinel address the
	// batch writes before it takes new ones.
	for _, r := range []Resource{moving, sibling} {
		got, err := store.Get(ctx, r.ID)
		require.NoError(t, err)
		assert.Equal(t, r.URI, got.URI, "a refused batch left %s rewritten", r.ID)
		assert.Equal(t, "data", got.Path)
	}
}

// TestResourceList_RealDB_PathFilterReturnsTheSubtree is the listing half: a
// folder's contents are the folder and everything beneath it, and a sibling
// whose name merely starts with the same letters is not in it.
func TestResourceList_RealDB_PathFilterReturnsTheSubtree(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	for _, r := range []Resource{
		folderFile("res_list_1", "data", "top.csv"),
		folderFile("res_list_2", "data/shows", "deep.csv"),
		folderFile("res_list_3", "data-archive", "sibling.csv"),
		folderFile("res_list_4", "other", "elsewhere.csv"),
	} {
		require.NoError(t, store.Insert(ctx, r))
	}

	got, total, err := store.List(ctx, Filter{
		Scopes: []ScopeFilter{{Scope: ScopeUser, ScopeID: "sub-folder"}},
		Path:   "data",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	ids := make([]string, 0, len(got))
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	assert.ElementsMatch(t, []string{"res_list_1", "res_list_2"}, ids)
}
