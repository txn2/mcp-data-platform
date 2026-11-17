//go:build integration

package foldergrant

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/grantsource"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// TestGrants_RealDB pins #2684 against a real PostgreSQL: a grant covers its
// folder and every folder beneath it by segment, is read for a person by
// address and for a persona, lists inherited beside its own, is refused twice
// for one grantee, ends when removed, follows its folder through an
// administrator's rename in the move's transaction, and is deleted with it.
func TestGrants_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := NewStore(db)
	pg := resource.NewPostgresStore(db)
	folders := pg.(resource.FolderStore)
	global := resource.ScopeFilter{Scope: resource.ScopeGlobal}
	for _, p := range []string{"reports", "reports/q3", "reports2"} {
		require.NoError(t, folders.CreateFolder(ctx, global, p, "admin@example.com"))
	}

	ok, err := s.FolderExists(ctx, "reports/q3")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = s.FolderExists(ctx, "nowhere")
	require.NoError(t, err)
	assert.False(t, ok)

	jane := &Grant{Folder: "reports", Email: "jane@example.com", CreatedBy: "admin@example.com"}
	require.NoError(t, s.Add(ctx, jane))
	require.ErrorIs(t, s.Add(ctx, &Grant{Folder: "reports", Email: "Jane@Example.com", CreatedBy: "admin@example.com"}), ErrDuplicate)
	finance := &Grant{Folder: "reports/q3", Persona: "finance", CreatedBy: "admin@example.com"}
	require.NoError(t, s.Add(ctx, finance))

	got, err := s.Writable(ctx, grantsource.Grantee{Email: "JANE@example.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{"reports"}, got, "a person's grant, by address in any case")
	got, err = s.Writable(ctx, grantsource.Grantee{Email: "bob@example.com", Personas: []string{"finance"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"reports/q3"}, got, "a persona's grant")
	got, err = s.Writable(ctx, grantsource.Grantee{Email: "bob@example.com", Personas: []string{"sales", "finance"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"reports/q3"}, got, "a persona's grant, among the personas the roles reach")
	external := Source{Store: s, External: func(p string) bool { return p == "finance" }}
	got, err = external.WritableFolders(ctx, grantsource.Grantee{Email: "bob@example.com", Personas: []string{"finance"}})
	require.NoError(t, err)
	assert.Empty(t, got, "an external persona holds no grant")

	listed, err := s.List(ctx, "reports/q3")
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, "reports", listed[0].Folder)
	assert.True(t, listed[0].Inherited, "the grant on the folder above is inherited")
	assert.False(t, listed[1].Inherited)
	listed, err = s.List(ctx, "reports2")
	require.NoError(t, err)
	assert.Empty(t, listed, "reports does not cover reports2")

	// An administrator renames reports: both grants follow, in one move.
	require.NoError(t, folders.MoveFolderTree(ctx, resource.FolderRename{Library: global, From: "reports", To: "reporting"}, nil))
	got, err = s.Writable(ctx, grantsource.Grantee{Email: "jane@example.com", Personas: []string{"finance"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"reporting", "reporting/q3"}, got)

	removed, err := s.Remove(ctx, jane.ID)
	require.NoError(t, err)
	assert.Equal(t, "reporting", removed.Folder)
	_, err = s.Remove(ctx, jane.ID)
	require.ErrorIs(t, err, ErrNotFound)

	// Deleting the folder deletes the grants on it and beneath it.
	require.NoError(t, folders.DeleteFolder(ctx, global, "reporting"))
	var left int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM portal_shares WHERE folder_path IS NOT NULL`).Scan(&left))
	assert.Zero(t, left)

	sites, err := s.Sites(ctx, "reporting")
	require.NoError(t, err)
	assert.Equal(t, []string{}, sites)
}
