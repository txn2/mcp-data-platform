//go:build integration

package resourceshare

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// The store against PostgreSQL (#2258): a share on a resource is listed,
// found for its recipient by address, user id or persona with the editor
// share winning, listed as shared with them, names who shared, is revoked,
// and goes with the resource.
func TestRealDB_ResourceSharesRoundTrip(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO resources (id, scope, scope_id, path, filename, display_name, description, mime_type, size_bytes, s3_key, uri, uploader_sub, uploader_email)
		VALUES ('r1', 'user', 'owner', '', 'extract.csv', 'Extract', '', 'text/csv', 4, 'k', 'mcp://user/owner/extract.csv', 'owner', 'owner@example.com')`)
	require.NoError(t, err)
	s := NewStore(db)

	share := func(spec portaldomain.ShareSpec) portaldomain.Share {
		sh, err := portaldomain.BuildShare(portaldomain.ShareTarget{ResourceID: "r1"}, "owner@example.com", spec)
		require.NoError(t, err)
		require.NoError(t, s.Insert(ctx, &sh))
		assert.False(t, sh.CreatedAt.IsZero())
		return sh
	}
	viewer := share(portaldomain.ShareSpec{RecipientEmail: "Reader@Example.com"})
	share(portaldomain.ShareSpec{RecipientPersona: "marketing", Permission: "editor"})

	list, err := s.List(ctx, "r1")
	require.NoError(t, err)
	assert.Len(t, list, 2)

	perm, err := s.Permission(ctx, "r1", Recipient{Email: "reader@example.com"})
	require.NoError(t, err)
	assert.Equal(t, "viewer", perm)
	perm, err = s.Permission(ctx, "r1", Recipient{Email: "reader@example.com", Persona: "marketing"})
	require.NoError(t, err)
	assert.Equal(t, "editor", perm, "the editor share wins")
	perm, err = s.Permission(ctx, "r1", Recipient{Email: "other@example.com", Persona: "sales"})
	require.NoError(t, err)
	assert.Empty(t, perm)

	refs, err := s.SharedWith(ctx, Recipient{Email: "reader@example.com", Persona: "marketing"})
	require.NoError(t, err)
	require.Len(t, refs, 1, "one row per resource")
	assert.Equal(t, "editor", refs[0].Permission)
	assert.Equal(t, "marketing", refs[0].ViaPersona)

	sharers, err := s.Sharers(ctx, Recipient{Email: "reader@example.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{"owner@example.com"}, sharers)

	require.NoError(t, s.Revoke(ctx, "r1", viewer.ID))
	require.ErrorIs(t, s.Revoke(ctx, "r1", viewer.ID), ErrNotFound)
	perm, err = s.Permission(ctx, "r1", Recipient{Email: "reader@example.com"})
	require.NoError(t, err)
	assert.Empty(t, perm)

	_, err = db.ExecContext(ctx, `DELETE FROM resources WHERE id = 'r1'`)
	require.NoError(t, err)
	var left int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM portal_shares WHERE resource_id = 'r1'`).Scan(&left))
	assert.Zero(t, left, "deleting the resource removes its shares")

	_, err = db.ExecContext(ctx, `INSERT INTO portal_shares (id, token, created_by) VALUES ('x', 'tx', 'a@example.com')`)
	assert.Error(t, err, "a share has exactly one target")
}
