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
		Category: "templates", Filename: "report.md", DisplayName: "Report",
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

	require.NoError(t, store.Move(ctx, "res_move_1", Move{
		Scope: ScopePersona, ScopeID: "ops", URI: to, FromURI: from,
	}), "move to the ops persona library")

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
	require.NoError(t, store.Move(ctx, "res_move_2", Move{
		Scope: ScopeGlobal, URI: to, FromURI: addr,
	}))

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
	require.NoError(t, store.Move(ctx, "res_move_4", Move{
		Scope: ScopeGlobal, URI: away, FromURI: home,
	}))
	require.NoError(t, store.Move(ctx, "res_move_4", Move{
		Scope: ScopeUser, ScopeID: "sub-mover", URI: home, FromURI: away,
	}), "move back where it came from")

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
