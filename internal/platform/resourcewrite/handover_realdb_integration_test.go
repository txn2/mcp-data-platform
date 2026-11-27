//go:build integration

package resourcewrite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// handoverResource is a resource ready to insert under the given id and URI.
func handoverResource(id, uri string) resource.Resource {
	return resource.Resource{
		ID: id, Scope: resource.ScopeUser, ScopeID: "sub-mover",
		Path: "attachments", Filename: "report.md", DisplayName: "Report",
		Description: "Handover fixture.", MIMEType: "text/markdown",
		SizeBytes: 12, S3Key: "resources/" + id + "/report.md", URI: uri,
		UploaderSub: "sub-mover", Tags: []string{},
	}
}

// TestHandOverAddresses_RealDB is the step the imap attachment repair takes
// before deleting a duplicate copy (#2842): every address of the copy, its own
// and one an earlier move left aliased to it, resolves to the file that stays
// once the copy is deleted.
func TestHandOverAddresses_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := resource.NewPostgresStore(db)
	ctx := context.Background()

	const (
		keepURI  = "mcp://user/sub-mover/attachments/inbox-1538884683-11033/report.md"
		copyURI  = "mcp://user/sub-mover/attachments/inbox-1794972606-11033/report.md"
		earlyURI = "mcp://user/sub-mover/attachments/old/report.md"
	)
	keep, dup := handoverResource("res_keep", keepURI), handoverResource("res_copy", copyURI)
	keep.Path, dup.Path = "attachments/inbox-1538884683-11033", "attachments/inbox-1794972606-11033"
	require.NoError(t, store.Insert(ctx, keep))
	require.NoError(t, store.Insert(ctx, dup))
	_, err := db.ExecContext(ctx, `INSERT INTO resource_uri_aliases (uri, resource_id) VALUES ($1, 'res_copy')`, earlyURI)
	require.NoError(t, err)

	require.NoError(t, HandOverAddresses(ctx, db, "res_copy", "res_keep"))

	live, err := store.GetByURI(ctx, copyURI)
	require.NoError(t, err)
	require.NotNil(t, live)
	assert.Equal(t, "res_copy", live.ID, "until the copy is deleted its own address is live and wins")

	require.NoError(t, store.Delete(ctx, "res_copy"))
	for _, uri := range []string{copyURI, earlyURI} {
		got, err := store.GetByURI(ctx, uri)
		require.NoError(t, err, uri)
		require.NotNil(t, got, uri)
		assert.Equal(t, "res_keep", got.ID, "%s resolves to the file that stays", uri)
		assert.Equal(t, keepURI, got.URI)
	}

	assert.ErrorIs(t, HandOverAddresses(ctx, db, "res_gone", "res_keep"), errHandoverMissing)
	assert.Error(t, HandOverAddresses(ctx, db, "res_keep", "res_keep"), "a file is not its own copy")
}
