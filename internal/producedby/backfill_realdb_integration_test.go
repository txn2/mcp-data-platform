//go:build integration

package producedby

// Real-Postgres tests for migration 000135's backfill (#1569, acceptance
// criterion 10). The backfill is SQL over rows written before the relation
// existed, so what it derives -- and, more importantly, what it declines to
// derive -- can only be shown against a real database.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
)

const (
	backfillScriptID = "11111111-1111-4111-8111-111111111111"
	twinScriptA      = "22222222-2222-4222-8222-222222222222"
	twinScriptB      = "33333333-3333-4333-8333-333333333333"
)

// rollBackTheRelation removes content_producers and its backfill, leaving the
// schema as a deployment upgrading into this change has it.
func rollBackTheRelation(t *testing.T, db *sql.DB) {
	t.Helper()
	require.NoError(t, migrate.Steps(db, -1), "step migration 000135 down")
}

// insertScript writes a scripts row directly: the backfill reads the table, and
// this test is about SQL rather than about the script store.
func insertScript(t *testing.T, db *sql.DB, id, name, owner string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO scripts (id, name, owner_email, status) VALUES ($1, $2, $3, 'active')`,
		id, name, owner)
	require.NoError(t, err)
}

// insertAsset writes a portal_assets row with the columns the store writes.
func insertAsset(t *testing.T, db *sql.DB, id, ownerID, idempotencyKey string, version int) {
	t.Helper()
	var key any
	if idempotencyKey != "" {
		key = idempotencyKey
	}
	_, err := db.Exec(`
		INSERT INTO portal_assets
			(id, owner_id, owner_email, name, description, content_type, s3_bucket, s3_key,
			 size_bytes, tags, provenance, session_id, current_version, idempotency_key)
		VALUES ($1, $2, 'owner@example.com', $1, '', 'text/html', 'portal', 'k/'||$1,
			 10, '[]'::jsonb, '{}'::jsonb, 'run-1', $3, $4)`,
		id, ownerID, version, key)
	require.NoError(t, err)
}

// insertResource writes a resources row with the columns the store writes.
func insertResource(t *testing.T, db *sql.DB, id, uploaderSub, uploaderEmail string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO resources
			(id, scope, scope_id, path, filename, display_name, description,
			 mime_type, size_bytes, s3_key, uri, uploader_sub, uploader_email)
		VALUES ($1, 'global', NULL, 'samples', $1||'.csv', $1, '',
			 'text/csv', 10, 'r/'||$1, 'mcp://global/samples/'||$1||'.csv', $2, $3)`,
		id, uploaderSub, uploaderEmail)
	require.NoError(t, err)
}

// TestBackfillDerivesWhatItCan_RealDB is acceptance criterion 10 in both
// directions: a script's own output asset gains the producer it always had
// implicitly, and a person's upload gains none rather than a wrong one.
func TestBackfillDerivesWhatItCan_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	rollBackTheRelation(t, db)

	insertScript(t, db, backfillScriptID, "daily-sales", "owner@example.com")
	insertAsset(t, db, "asset-of-script", "script:daily-sales", "script:"+backfillScriptID+":report", 4)
	insertAsset(t, db, "asset-of-person", "sub-1", "", 1)
	insertResource(t, db, "res-of-script", "script:daily-sales", "owner@example.com")
	insertResource(t, db, "res-of-person", "sub-1", "a@example.com")

	require.NoError(t, migrate.Steps(db, 1), "step migration 000135 back up")
	store := NewPostgres(db)

	rows, err := store.ListByTarget(ctx, TargetAsset, "asset-of-script")
	require.NoError(t, err)
	require.Len(t, rows, 1, "an asset written by a script before this shipped names that script")
	assert.Equal(t, KindScript, rows[0].Producer.Kind)
	assert.Equal(t, backfillScriptID, rows[0].Producer.ID)
	assert.Equal(t, "daily-sales", rows[0].Producer.Label)
	assert.True(t, rows[0].Created)
	assert.Equal(t, 4, rows[0].WriteCount, "every version of a script output came from a run")
	assert.Equal(t, 4, rows[0].LastVersion)

	resRows, err := store.ListByTarget(ctx, TargetResource, "res-of-script")
	require.NoError(t, err)
	require.Len(t, resRows, 1)
	assert.Equal(t, backfillScriptID, resRows[0].Producer.ID)

	for _, id := range []struct{ kind, target string }{
		{TargetAsset, "asset-of-person"},
		{TargetResource, "res-of-person"},
	} {
		none, err := store.ListByTarget(ctx, id.kind, id.target)
		require.NoError(t, err)
		assert.Empty(t, none,
			"a history that was never recorded is not reconstructed by guessing: %s", id.target)
	}
}

// TestBackfillLeavesAnAmbiguousNameAlone_RealDB: uploader_sub records a
// script's NAME, and two owners may each keep a script of the same name. The
// link is genuinely ambiguous and no row is guessed.
func TestBackfillLeavesAnAmbiguousNameAlone_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	rollBackTheRelation(t, db)

	insertScript(t, db, twinScriptA, "shared-name", "a@example.com")
	insertScript(t, db, twinScriptB, "shared-name", "b@example.com")
	insertResource(t, db, "res-ambiguous", "script:shared-name", "a@example.com")

	require.NoError(t, migrate.Steps(db, 1))

	rows, err := NewPostgres(db).ListByTarget(ctx, TargetResource, "res-ambiguous")
	require.NoError(t, err)
	assert.Empty(t, rows, "two scripts bear the name, so neither is recorded")
}

// TestBackfillIsIdempotent_RealDB: the migration can be stepped down and up
// again -- a rollback and re-upgrade -- without duplicating or failing.
func TestBackfillIsIdempotent_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	rollBackTheRelation(t, db)

	insertScript(t, db, backfillScriptID, "daily-sales", "owner@example.com")
	insertAsset(t, db, "asset-twice", "script:daily-sales", "script:"+backfillScriptID+":report", 2)

	require.NoError(t, migrate.Steps(db, 1))
	require.NoError(t, migrate.Steps(db, -1))
	require.NoError(t, migrate.Steps(db, 1))

	rows, err := NewPostgres(db).ListByTarget(ctx, TargetAsset, "asset-twice")
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}
