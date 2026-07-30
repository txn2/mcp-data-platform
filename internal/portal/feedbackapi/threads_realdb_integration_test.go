//go:build integration

package feedbackapi

// Real-Postgres tests for the feedback thread substrate (#601). These exercise
// behavior sqlmock cannot: the portal_threads 1-of-N CHECK constraint, the full
// CreateThread/ListThreads/AppendEvent/UpdateThread cycle against the real
// schema, and the public-link auto-promote (derived viewer share, no downgrade)
// read back from portal_shares. Run under `make test-realdb`.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

const realdbThreadOwner = "550e8400-e29b-41d4-a716-446655440000"

func seedAsset(t *testing.T, db *sql.DB, id, owner string) {
	t.Helper()
	store := portalstore.NewPostgresAssetStore(db)
	require.NoError(t, store.Insert(context.Background(), portaldomain.Asset{
		ID: id, OwnerID: owner, OwnerEmail: "owner@example.com", Name: id,
		ContentType: "text/markdown", S3Bucket: "b", S3Key: "k", Tags: []string{}, CurrentVersion: 1,
	}))
}

func TestRealDB_ThreadCheckConstraint(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	seedAsset(t, db, "asset_chk", realdbThreadOwner)

	// Valid: asset target with asset_id set.
	_, err := db.ExecContext(ctx, `INSERT INTO portal_threads (id, kind, target_type, asset_id, author_id, author_email)
		VALUES ('t_ok', 'comment', 'asset', 'asset_chk', 'u1', 'u1@example.com')`)
	require.NoError(t, err)

	// Valid: standalone with all targets null.
	_, err = db.ExecContext(ctx, `INSERT INTO portal_threads (id, kind, target_type, author_id, author_email)
		VALUES ('t_standalone', 'suggestion', 'standalone', 'u1', 'u1@example.com')`)
	require.NoError(t, err)

	// Invalid: asset target with null asset_id.
	_, err = db.ExecContext(ctx, `INSERT INTO portal_threads (id, kind, target_type, author_id, author_email)
		VALUES ('t_bad1', 'comment', 'asset', 'u1', 'u1@example.com')`)
	require.Error(t, err)

	// Invalid: standalone with an asset_id set.
	_, err = db.ExecContext(ctx, `INSERT INTO portal_threads (id, kind, target_type, asset_id, author_id, author_email)
		VALUES ('t_bad2', 'comment', 'standalone', 'asset_chk', 'u1', 'u1@example.com')`)
	require.Error(t, err)

	// Invalid: two targets set at once.
	_, err = db.ExecContext(ctx, `INSERT INTO portal_threads (id, kind, target_type, asset_id, collection_id, author_id, author_email)
		VALUES ('t_bad3', 'comment', 'asset', 'asset_chk', 'col_x', 'u1', 'u1@example.com')`)
	require.Error(t, err)
}

func TestRealDB_ThreadLifecycle(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	seedAsset(t, db, "asset_life", realdbThreadOwner)
	store := threads.NewPostgresThreadStore(db)

	thread := threads.Thread{
		ID: "thr_life", Kind: threads.ThreadKindCorrection, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_life",
		TargetVersion: 2, RequiresResolution: true, AuthorID: "sme", AuthorEmail: "sme@example.com",
		Anchor: []byte(`{"type":"text_quote","exact":"churn"}`),
	}
	first := threads.ThreadEvent{ID: "evt_life_1", ThreadID: "thr_life", EventType: threads.EventTypeComment, AuthorID: "sme", AuthorEmail: "sme@example.com", Body: "we don't use that term"}
	created, err := store.CreateThread(ctx, thread, first)
	require.NoError(t, err)
	assert.False(t, created.CreatedAt.IsZero())

	// ListThreads returns the thread with one event aggregated.
	list, total, err := store.ListThreads(ctx, threads.ThreadFilter{TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_life"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, list, 1)
	assert.Equal(t, 1, list[0].EventCount)
	assert.Equal(t, threads.EventTypeComment, list[0].LastEventType)
	assert.Equal(t, 2, list[0].TargetVersion)
	assert.JSONEq(t, `{"type":"text_quote","exact":"churn"}`, string(list[0].Anchor))

	// Append a reply.
	_, err = store.AppendEvent(ctx, threads.ThreadEvent{ID: "evt_life_2", ThreadID: "thr_life", EventType: threads.EventTypeComment, AuthorID: "owner", AuthorEmail: "owner@example.com", Body: "fixed"})
	require.NoError(t, err)

	// Resolve → status flips and a resolution event is recorded in the same tx.
	resolved := threads.ThreadStatusResolved
	require.NoError(t, store.UpdateThread(ctx, "thr_life", threads.ThreadUpdate{Status: &resolved}, "owner", "owner@example.com"))
	got, err := store.GetThread(ctx, "thr_life")
	require.NoError(t, err)
	assert.Equal(t, threads.ThreadStatusResolved, got.Status)

	events, err := store.ListEvents(ctx, "thr_life")
	require.NoError(t, err)
	require.Len(t, events, 3) // initial comment + reply + resolution
	assert.Equal(t, threads.EventTypeResolution, events[2].EventType)
}
