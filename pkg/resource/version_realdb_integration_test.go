//go:build integration

package resource

// Real-Postgres round-trip tests for the content-revision trail (#1014). These
// exercise the statements sqlmock structurally cannot check: the version number
// assigned inside the insert transaction, the head-and-trail move committing
// together, the row lock that serializes concurrent revisions, and above all the
// prune, whose DELETE ... USING joins `resources` — a join that puts six column
// names in scope from both tables, so an unqualified RETURNING list is rejected
// as ambiguous by the planner and by nothing else.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// seedRevisableResource inserts a resource to hang revisions off, returning the
// store and the resource's id.
func seedRevisableResource(t *testing.T, id string) (Store, string) {
	t.Helper()
	store := NewPostgresStore(testdb.New(t))
	require.NoError(t, store.Insert(context.Background(), Resource{
		ID: id, Scope: ScopeGlobal, Category: "runbooks",
		Filename: "etl.md", DisplayName: "ETL Runbook", Description: "d",
		MIMEType: "text/markdown", SizeBytes: 10,
		S3Key: "resources/global/global/" + id + "/etl.md",
		URI:   "mcp://global/runbooks/" + id + ".md", UploaderSub: "sub-1",
	}))
	return store, id
}

func TestResourceVersions_RealDB_RevisionMovesHeadAndTrailTogether(t *testing.T) {
	store, id := seedRevisableResource(t, "res_rev_1")
	versions, ok := store.(VersionStore)
	require.True(t, ok, "the postgres store must implement VersionStore")
	ctx := context.Background()

	first, err := versions.AddRevision(ctx, Revision{
		ResourceID: id, MIMEType: "text/markdown", SizeBytes: 10,
		S3Key: "resources/global/global/" + id + "/etl.md", UploaderSub: "sub-1",
		UploaderEmail: "one@example.com",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, first.Version, "the first revision is version 1")

	second, err := versions.AddRevision(ctx, Revision{
		ResourceID: id, MIMEType: "text/plain", SizeBytes: 42,
		S3Key: "resources/global/global/" + id + "/v/rev2/etl.md", UploaderSub: "sub-2",
		UploaderEmail: "two@example.com",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, second.Version, "the store assigns the next number")

	// The head moved with the trail, in the same transaction.
	head, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "resources/global/global/"+id+"/v/rev2/etl.md", head.S3Key)
	assert.Equal(t, "text/plain", head.MIMEType)
	assert.Equal(t, int64(42), head.SizeBytes)

	trail, err := versions.ListVersions(ctx, id)
	require.NoError(t, err)
	require.Len(t, trail, 2)
	assert.Equal(t, 2, trail[0].Version, "newest first")
	assert.Equal(t, "two@example.com", trail[0].UploaderEmail)
}

func TestResourceVersions_RealDB_RestoredFromRoundTrips(t *testing.T) {
	store, id := seedRevisableResource(t, "res_rev_restore")
	versions := store.(VersionStore)
	ctx := context.Background()

	_, err := versions.AddRevision(ctx, Revision{ResourceID: id, MIMEType: "text/markdown", S3Key: "k1"})
	require.NoError(t, err)
	from := 1
	restored, err := versions.AddRevision(ctx, Revision{
		ResourceID: id, MIMEType: "text/markdown", S3Key: "k2", RestoredFrom: &from,
	})
	require.NoError(t, err)

	got, err := versions.GetVersion(ctx, id, restored.Version)
	require.NoError(t, err)
	require.NotNil(t, got.RestoredFrom)
	assert.Equal(t, 1, *got.RestoredFrom)

	fresh, err := versions.GetVersion(ctx, id, 1)
	require.NoError(t, err)
	assert.Nil(t, fresh.RestoredFrom, "a fresh upload records no source version")
}

func TestResourceVersions_RealDB_MissingVersionIsNotFound(t *testing.T) {
	store, id := seedRevisableResource(t, "res_rev_missing")
	versions := store.(VersionStore)

	_, err := versions.GetVersion(context.Background(), id, 99)
	require.Error(t, err)
	assert.True(t, IsNotFound(err), "a missing version must be recognizable as not-found, not a read failure")
}

func TestResourceVersions_RealDB_AddRevisionRefusesAMissingResource(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	versions := store.(VersionStore)

	_, err := versions.AddRevision(context.Background(), Revision{ResourceID: "no-such-resource", S3Key: "k"})
	require.Error(t, err, "a revision of a resource that does not exist must not be recorded")
}

func TestResourceVersions_RealDB_PruneKeepsTheCapAndNeverTheHead(t *testing.T) {
	store, id := seedRevisableResource(t, "res_rev_prune")
	versions := store.(VersionStore)
	ctx := context.Background()

	// Version 1 shares the head's key (the migration-backfill shape); five more
	// revisions follow, so the head is v6 at its own key.
	_, err := versions.AddRevision(ctx, Revision{
		ResourceID: id, MIMEType: "text/markdown", SizeBytes: 10,
		S3Key: "resources/global/global/" + id + "/etl.md",
	})
	require.NoError(t, err)
	for i := 2; i <= 6; i++ {
		_, err := versions.AddRevision(ctx, Revision{
			ResourceID: id, MIMEType: "text/markdown", SizeBytes: int64(i),
			S3Key: fmt.Sprintf("resources/global/global/%s/v/rev%d/etl.md", id, i),
		})
		require.NoError(t, err)
	}

	pruned, err := versions.PruneVersions(ctx, id, 3)
	require.NoError(t, err, "the prune statement must survive its join with resources")
	require.Len(t, pruned, 3, "six versions capped at three drops the oldest three")
	for _, v := range pruned {
		assert.LessOrEqual(t, v.Version, 3)
		assert.NotEmpty(t, v.S3Key, "the caller deletes these blobs, so the key must come back")
	}

	remaining, err := versions.ListVersions(ctx, id)
	require.NoError(t, err)
	require.Len(t, remaining, 3)
	assert.Equal(t, 6, remaining[0].Version)

	// The live content is still readable.
	head, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "resources/global/global/"+id+"/v/rev6/etl.md", head.S3Key)
}

func TestResourceVersions_RealDB_PruneNeverDropsTheHeadsBlob(t *testing.T) {
	store, id := seedRevisableResource(t, "res_rev_prune_head")
	versions := store.(VersionStore)
	ctx := context.Background()

	// A resource whose only version is the backfilled v1 pointing at the head's
	// own key: pruning must return nothing, whatever cap it is given.
	_, err := versions.AddRevision(ctx, Revision{
		ResourceID: id, MIMEType: "text/markdown", SizeBytes: 10,
		S3Key: "resources/global/global/" + id + "/etl.md",
	})
	require.NoError(t, err)

	pruned, err := versions.PruneVersions(ctx, id, MinMaxVersions)
	require.NoError(t, err)
	assert.Empty(t, pruned, "the blob the head points at must never be pruned")
}

func TestResourceVersions_RealDB_DeletingAResourceTakesItsTrail(t *testing.T) {
	store, id := seedRevisableResource(t, "res_rev_cascade")
	versions := store.(VersionStore)
	ctx := context.Background()

	_, err := versions.AddRevision(ctx, Revision{ResourceID: id, MIMEType: "text/markdown", S3Key: "k1"})
	require.NoError(t, err)

	require.NoError(t, store.Delete(ctx, id))

	trail, err := versions.ListVersions(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, trail, "version rows cascade with the resource")
}

func TestResourceVersions_RealDB_TouchReadStampsAndSorts(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	tracker := store.(ReadTracker)
	ctx := context.Background()

	for _, id := range []string{"res_read_a", "res_read_b"} {
		require.NoError(t, store.Insert(ctx, Resource{
			ID: id, Scope: ScopeGlobal, Category: "runbooks", Filename: id + ".md",
			DisplayName: id, Description: "d", MIMEType: "text/markdown", SizeBytes: 1,
			S3Key: "k/" + id, URI: "mcp://global/runbooks/" + id + ".md", UploaderSub: "sub",
		}))
	}

	// Only one of them is ever read.
	at := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, tracker.TouchRead(ctx, "res_read_b", at))

	stamped, err := store.Get(ctx, "res_read_b")
	require.NoError(t, err)
	require.NotNil(t, stamped.LastReadAt)
	assert.WithinDuration(t, at, *stamped.LastReadAt, time.Second)

	never, err := store.Get(ctx, "res_read_a")
	require.NoError(t, err)
	assert.Nil(t, never.LastReadAt, "a resource nobody has read carries no last-read time")

	// The read one leads a last-read ordering; the unread one sorts last.
	listed, _, err := store.List(ctx, Filter{
		Scopes: []ScopeFilter{{Scope: ScopeGlobal}},
		Sort:   SortLastRead,
		Limit:  10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, listed)
	assert.Equal(t, "res_read_b", listed[0].ID)
	assert.Equal(t, "res_read_a", listed[len(listed)-1].ID, "never-read material sorts last")
}
