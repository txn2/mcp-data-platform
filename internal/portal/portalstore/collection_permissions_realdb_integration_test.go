//go:build integration

package portalstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestUserCollectionPermissions_MatchesTheOneRowRead_RealDB holds #2740: the
// page read answers every collection exactly as the one-collection read does,
// over shares to the caller's id, address and persona, an editor beside a
// viewer, an expired share, a revoked share and a share to someone else.
func TestUserCollectionPermissions_MatchesTheOneRowRead_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := portaldomain.WithCallerPersona(context.Background(), "analysts")
	colls := &postgresCollectionStore{db: db}
	shares := &postgresShareStore{db: db}
	const owner = "550e8400-e29b-41d4-a716-446655440999"
	const me, myEmail = "550e8400-e29b-41d4-a716-446655440111", "me@example.com"

	ids := make([]string, 8)
	for i := range ids {
		ids[i] = fmt.Sprintf("aaaaaaaa-0000-0000-0000-%012d", i)
		require.NoError(t, colls.Insert(ctx, portaldomain.Collection{ID: ids[i], OwnerID: owner, OwnerEmail: "owner@example.com", Name: fmt.Sprintf("c%d", i)}))
	}
	past := time.Now().Add(-time.Hour)
	n := 0
	share := func(collection string, s portaldomain.Share, perm portaldomain.SharePermission, revoked bool) {
		t.Helper()
		n++
		s.ID = fmt.Sprintf("bbbbbbbb-0000-0000-0000-%012d", n)
		s.CollectionID = collection
		s.Token = fmt.Sprintf("tok-%d", n)
		s.CreatedBy = "owner@example.com"
		require.NoError(t, shares.Insert(ctx, &s))
		if perm != "" {
			require.NoError(t, shares.SetPermission(ctx, s.ID, perm))
		}
		if revoked {
			_, err := db.ExecContext(ctx, `UPDATE portal_shares SET revoked = TRUE WHERE id = $1`, s.ID)
			require.NoError(t, err)
		}
	}
	share(ids[0], portaldomain.Share{SharedWithUserID: me}, portaldomain.PermissionViewer, false)
	share(ids[1], portaldomain.Share{SharedWithEmail: "ME@example.com"}, portaldomain.PermissionEditor, false)
	share(ids[2], portaldomain.Share{SharedWithPersona: "analysts"}, portaldomain.PermissionViewer, false)
	share(ids[3], portaldomain.Share{SharedWithUserID: me}, portaldomain.PermissionViewer, false)
	share(ids[3], portaldomain.Share{SharedWithEmail: myEmail}, portaldomain.PermissionEditor, false)
	share(ids[4], portaldomain.Share{SharedWithUserID: me, ExpiresAt: &past}, portaldomain.PermissionEditor, false)
	share(ids[5], portaldomain.Share{SharedWithUserID: me}, portaldomain.PermissionEditor, true)
	share(ids[6], portaldomain.Share{SharedWithEmail: "someone@example.com"}, portaldomain.PermissionEditor, false)

	page, err := shares.GetUserCollectionPermissions(ctx, ids, me, myEmail)
	require.NoError(t, err)
	for _, id := range ids {
		one, _ := shares.GetUserCollectionPermission(ctx, id, me, myEmail)
		require.Equal(t, one, page[id], "collection %s", id)
	}
	require.Equal(t, map[string]portaldomain.SharePermission{
		ids[0]: portaldomain.PermissionViewer, ids[1]: portaldomain.PermissionEditor,
		ids[2]: portaldomain.PermissionViewer, ids[3]: portaldomain.PermissionEditor,
	}, page)

	// A caller with no id is matched by none of the shares made to an id.
	noID, err := shares.GetUserCollectionPermissions(ctx, ids, "", "")
	require.NoError(t, err)
	require.Equal(t, map[string]portaldomain.SharePermission{ids[2]: portaldomain.PermissionViewer}, noID)

	empty, err := shares.GetUserCollectionPermissions(ctx, nil, me, myEmail)
	require.NoError(t, err)
	require.Empty(t, empty)
}
