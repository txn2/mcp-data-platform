//go:build integration

package portalpurge

// Real-Postgres tests for the purge of what the portal soft-deleted (#1904).
// The purge's correctness is in the foreign keys: an asset is referenced by
// shares, collection items and threads that do not cascade, and a statement
// sqlmock accepts can still be refused by the schema. These run it against the
// migrated database and assert which rows and objects are gone.

import (
	"context"
	"database/sql"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

const purgeOwner = "550e8400-e29b-41d4-a716-446655440444"

// recordedDeletes is an object store that records every delete.
type recordedDeletes struct {
	mu   sync.Mutex
	keys []string
	fail map[string]bool
}

func (r *recordedDeletes) DeleteObject(_ context.Context, bucket, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail[key] {
		return assert.AnError
	}
	r.keys = append(r.keys, bucket+"/"+key)
	return nil
}

func (r *recordedDeletes) sorted() []string {
	out := append([]string(nil), r.keys...)
	sort.Strings(out)
	return out
}

// seedAsset inserts an asset with one version, a share, a thread and a place
// in a collection, deleted at deletedAt (nil for a live one).
func seedAsset(t *testing.T, db *sql.DB, id string, deletedAt *time.Time) {
	t.Helper()
	ctx := context.Background()
	store := portalstore.NewPostgresAssetStore(db, nil)
	require.NoError(t, store.Insert(ctx, portaldomain.Asset{
		ID: id, OwnerID: purgeOwner, OwnerEmail: "u@example.com", Name: id, ContentType: "text/html",
		S3Bucket: "portal-assets", S3Key: "artifacts/u/" + id + "/v2/content.html", SizeBytes: 1,
		Tags: []string{}, CurrentVersion: 2,
	}))
	exec(t, db, `UPDATE portal_assets SET thumbnail_s3_key = $1, deleted_at = $2 WHERE id = $3`,
		"artifacts/u/"+id+"/v2/.thumbnail.png", deletedAt, id)
	exec(t, db, `INSERT INTO portal_asset_versions (id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes)
		VALUES ($1, $2, 1, $3, 'portal-assets', 'text/html', 1)`, id+"-v1", id, "artifacts/u/"+id+"/v1/content.html")
	exec(t, db, `INSERT INTO portal_shares (id, asset_id, token, created_by) VALUES ($1, $2, $3, 'u')`, id+"-share", id, id+"-token")
	exec(t, db, `INSERT INTO portal_threads (id, kind, target_type, asset_id, author_id, author_email)
		VALUES ($1, 'comment', 'asset', $2, 'u', 'u@example.com')`, id+"-thread", id)
	exec(t, db, `INSERT INTO content_producers (target_kind, target_id, producer_kind, producer_id) VALUES ('asset', $1, 'person', 'u')`, id)
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), q, args...)
	require.NoError(t, err)
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), q, args...).Scan(&n))
	return n
}

// TestPurge_RealDB_RemovesOnlyWhatWasDeletedBeforeTheCutoff is #1904's
// criterion for the portal: an item deleted before the cutoff is removed with
// every row that references it and every object it names, and a live item and
// one deleted inside the grace period are untouched.
func TestPurge_RealDB_RemovesOnlyWhatWasDeletedBeforeTheCutoff(t *testing.T) {
	db := testdb.New(t)
	now := time.Now().UTC()
	old, recent := now.AddDate(0, 0, -40), now.AddDate(0, 0, -5)

	seedAsset(t, db, "asset_expired", &old)
	seedAsset(t, db, "asset_recent", &recent)
	seedAsset(t, db, "asset_live", nil)

	// A collection deleted long ago holding the live asset, with a mosaic;
	// and a live collection holding the expired asset.
	exec(t, db, `INSERT INTO portal_collections (id, owner_id, name, thumbnail_s3_key, deleted_at) VALUES
		('coll_expired', $1, 'old', 'artifacts/collections/coll_expired/thumbnail.png', $2),
		('coll_live', $1, 'live', '', NULL)`, purgeOwner, old)
	exec(t, db, `INSERT INTO portal_collection_sections (id, collection_id) VALUES ('sec_expired', 'coll_expired'), ('sec_live', 'coll_live')`)
	exec(t, db, `INSERT INTO portal_collection_items (id, section_id, asset_id) VALUES
		('item_1', 'sec_expired', 'asset_live'), ('item_2', 'sec_live', 'asset_expired')`)
	exec(t, db, `INSERT INTO portal_threads (id, kind, target_type, author_id, author_email, deleted_at) VALUES
		('thread_expired', 'comment', 'standalone', 'u', 'u@example.com', $1),
		('thread_recent', 'comment', 'standalone', 'u', 'u@example.com', $2)`, old, recent)
	exec(t, db, `INSERT INTO portal_knowledge_pages (id, title, deleted_at, builtin) VALUES
		('page_expired', 'old', $1, FALSE), ('page_builtin_hidden', 'hidden', $1, TRUE), ('page_live', 'live', NULL, FALSE)`, old)

	objects := &recordedDeletes{}
	res, err := NewPurger(db, objects, "portal-assets").Purge(context.Background(), now.AddDate(0, 0, -30))
	require.NoError(t, err)
	assert.Equal(t, PurgeResult{Assets: 1, Collections: 1, Threads: 1, KnowledgePages: 1}, res)

	assert.Equal(t, 0, count(t, db, `SELECT COUNT(*) FROM portal_assets WHERE id = 'asset_expired'`))
	assert.Equal(t, 2, count(t, db, `SELECT COUNT(*) FROM portal_assets WHERE id IN ('asset_recent', 'asset_live')`))
	for _, q := range []string{
		`SELECT COUNT(*) FROM portal_asset_versions WHERE asset_id = 'asset_expired'`,
		`SELECT COUNT(*) FROM portal_shares WHERE asset_id = 'asset_expired'`,
		`SELECT COUNT(*) FROM portal_threads WHERE asset_id = 'asset_expired'`,
		`SELECT COUNT(*) FROM portal_collection_items WHERE asset_id = 'asset_expired'`,
		`SELECT COUNT(*) FROM content_producers WHERE target_id = 'asset_expired'`,
		`SELECT COUNT(*) FROM portal_collections WHERE id = 'coll_expired'`,
		`SELECT COUNT(*) FROM portal_threads WHERE id = 'thread_expired'`,
		`SELECT COUNT(*) FROM portal_knowledge_pages WHERE id = 'page_expired'`,
	} {
		assert.Equal(t, 0, count(t, db, q), q)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM portal_shares WHERE asset_id = 'asset_live'`,
		`SELECT COUNT(*) FROM portal_collections WHERE id = 'coll_live'`,
		`SELECT COUNT(*) FROM portal_threads WHERE id = 'thread_recent'`,
		`SELECT COUNT(*) FROM portal_knowledge_pages WHERE id IN ('page_live', 'page_builtin_hidden')`,
	} {
		assert.Positive(t, count(t, db, q), q)
	}

	want := []string{
		"portal-assets/artifacts/collections/coll_expired/thumbnail.png",
		"portal-assets/artifacts/collections/coll_expired/thumbnail_dark.png",
		"portal-assets/artifacts/u/asset_expired/v1/.thumbnail.png",
		"portal-assets/artifacts/u/asset_expired/v1/.thumbnail_dark.png",
		"portal-assets/artifacts/u/asset_expired/v1/content.html",
		// Tiles drawn before they took hidden names were stored under
		// these, and are removed as well.
		"portal-assets/artifacts/u/asset_expired/v1/thumbnail.png",
		"portal-assets/artifacts/u/asset_expired/v1/thumbnail_dark.png",
		"portal-assets/artifacts/u/asset_expired/v2/.thumbnail.png",
		"portal-assets/artifacts/u/asset_expired/v2/.thumbnail_dark.png",
		"portal-assets/artifacts/u/asset_expired/v2/content.html",
		"portal-assets/artifacts/u/asset_expired/v2/thumbnail.png",
		"portal-assets/artifacts/u/asset_expired/v2/thumbnail_dark.png",
	}
	assert.Equal(t, want, objects.sorted(), "every object the purged asset and collection named, and nothing else")
}

// An asset whose object would not delete keeps its row, so the next sweep
// tries again rather than leaving an object nothing names.
func TestPurge_RealDB_AnObjectThatWillNotDeleteKeepsItsRow(t *testing.T) {
	db := testdb.New(t)
	old := time.Now().UTC().AddDate(0, 0, -40)
	seedAsset(t, db, "asset_stuck", &old)

	objects := &recordedDeletes{fail: map[string]bool{"artifacts/u/asset_stuck/v1/content.html": true}}
	purger := NewPurger(db, objects, "portal-assets")
	res, err := purger.Purge(context.Background(), time.Now().UTC().AddDate(0, 0, -30))
	require.NoError(t, err)
	assert.Zero(t, res.Assets)
	assert.Equal(t, 1, count(t, db, `SELECT COUNT(*) FROM portal_assets WHERE id = 'asset_stuck'`))

	objects.fail = nil
	res, err = purger.Purge(context.Background(), time.Now().UTC().AddDate(0, 0, -30))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Assets)
	assert.Equal(t, 0, count(t, db, `SELECT COUNT(*) FROM portal_assets WHERE id = 'asset_stuck'`))
}
