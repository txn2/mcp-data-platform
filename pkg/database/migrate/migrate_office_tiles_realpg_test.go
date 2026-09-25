package migrate

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq" // postgres driver for the real-database gate

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// officeTilesVersion is the migration under test: the one that forgets tiles
// stored for a type nothing draws (#1882).
const officeTilesVersion = 162

// officeTileCases are the stored types seeded at the revision before the
// migration, each with a tile, and whether the tile is still there after it.
// The expectations are written out rather than read from internal/thumbtypes:
// this is what the migration did when it shipped, and a later change to the
// rule does not change what it did.
var officeTileCases = []struct {
	id, contentType string
	keeps           bool
}{
	{"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", false},
	{"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", false},
	{"pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", false},
	{"sqlite", "application/x-sqlite3", false},
	{"csv", "text/csv", true},
	{"csv-params", "text/csv; charset=utf-8", true},
	{"calendar", "text/calendar", true},
	{"xml", "application/xml", true},
	{"atom", "application/atom+xml", true},
	{"svg", "image/svg+xml", true},
	{"vendor-json", "application/vnd.acme.report+json", true},
	{"yaml-alias", "application/x-yaml", true},
	{"pdf", "APPLICATION/PDF", true},
	{"png", "image/png", true},
}

// TestMigrationsAgainstRealPostgres_OfficeTilesCleared seeds assets and
// resources carrying tiles at the prior revision and reads them after it: a
// workbook, a Word document and a presentation lose the tile drawn from their
// zip bytes, with the failure and attempts recorded against it, and every type
// the rule draws keeps its tile.
//
// The file sorts before migrate_realpg_test.go for the reason the other seeded
// migration tests give: the template database is left in whatever state the
// last test here leaves, and the full-lifecycle test hands it a clean schema.
func TestMigrationsAgainstRealPostgres_OfficeTilesCleared(t *testing.T) {
	dsn := os.Getenv("MIGRATE_TEST_DSN")
	if dsn == "" {
		t.Skip("MIGRATE_TEST_DSN not set; skipping real-Postgres migration gate (run via `make migrate-check`)")
	}
	migratorFactory = newMigrator

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "open test database")
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(t.Context()), "ping test database")
	resetSchema(t, db)

	require.NoError(t, Steps(db, officeTilesVersion-1), "migrate to the prior revision")

	for _, tc := range officeTileCases {
		seedTiledAsset(t, db, tc.id, tc.contentType)
		seedTiledResource(t, db, tc.id, tc.contentType)
	}

	require.NoError(t, Steps(db, 1), "apply the tile migration")

	for _, tc := range officeTileCases {
		asset := assetTile(t, db, tc.id)
		resource := resourceTile(t, db, tc.id)
		if tc.keeps {
			assert.Equal(t, tiledAsset(tc.id), asset, "asset %s (%s) keeps its tile", tc.id, tc.contentType)
			assert.Equal(t, tiledResource(tc.id), resource, "resource %s (%s) keeps its tile", tc.id, tc.contentType)
			continue
		}
		assert.Equal(t, tileState{}, asset, "asset %s (%s) has no tile, failure or attempts", tc.id, tc.contentType)
		assert.Equal(t, tileState{}, resource, "resource %s (%s) has no tile, failure or attempts", tc.id, tc.contentType)
	}

	// Down has nothing to restore and must still apply, so the upgrade stays
	// reversible.
	require.NoError(t, Steps(db, -1), "roll the migration back")
	require.NoError(t, Steps(db, 1), "roll forward again")
}

// tileState is everything the migration touches on one row, read back as
// strings so an asset's versions and a resource's dates compare alike.
type tileState struct {
	Light, Dark, LightStamp, DarkStamp, Failure, FailedAt string
	Attempts                                              int
}

func tiledAsset(id string) tileState {
	return tileState{
		Light: "a/" + id + "/.thumbnail.png", Dark: "a/" + id + "/.thumbnail_dark.png",
		LightStamp: "2", DarkStamp: "2", Failure: "renderer gone", FailedAt: "2", Attempts: 3,
	}
}

func tiledResource(id string) tileState {
	return tileState{
		Light: "r/" + id + "/.thumbnail.png", Dark: "r/" + id + "/.thumbnail_dark.png",
		LightStamp: "2026-09-01", DarkStamp: "2026-09-01", Failure: "renderer gone", FailedAt: "2026-09-01", Attempts: 3,
	}
}

func seedTiledAsset(t *testing.T, db *sql.DB, id, contentType string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `
		INSERT INTO portal_assets (id, owner_id, owner_email, name, content_type, s3_bucket, s3_key,
			thumbnail_s3_key, thumbnail_dark_s3_key, thumbnail_version, thumbnail_dark_version,
			thumbnail_failure, thumbnail_failed_version, thumbnail_attempts)
		VALUES ($1, 'owner', 'owner@example.com', $1, $2, 'bucket', 'a/'||$1||'/content',
			'a/'||$1||'/.thumbnail.png', 'a/'||$1||'/.thumbnail_dark.png', 2, 2, 'renderer gone', 2, 3)`,
		id, contentType)
	require.NoError(t, err, "seed asset %s", id)
}

func seedTiledResource(t *testing.T, db *sql.DB, id, contentType string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `
		INSERT INTO resources
			(id, scope, scope_id, path, filename, display_name, description,
			 mime_type, size_bytes, s3_key, uri, uploader_sub, uploader_email,
			 thumbnail_s3_key, thumbnail_dark_s3_key, thumbnail_captured_at, thumbnail_dark_captured_at,
			 thumbnail_failure, thumbnail_failed_at, thumbnail_attempts)
		VALUES ($1, 'global', NULL, 'samples', $1, $1, '', $2, 10, 'r/'||$1, 'mcp://global/samples/'||$1,
			'sub', 'owner@example.com',
			'r/'||$1||'/.thumbnail.png', 'r/'||$1||'/.thumbnail_dark.png', '2026-09-01T12:00:00Z', '2026-09-01T12:00:00Z',
			'renderer gone', '2026-09-01T12:00:00Z', 3)`,
		id, contentType)
	require.NoError(t, err, "seed resource %s", id)
}

func assetTile(t *testing.T, db *sql.DB, id string) tileState {
	t.Helper()
	var s tileState
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT thumbnail_s3_key, thumbnail_dark_s3_key,
		       CASE WHEN thumbnail_version = 0 THEN '' ELSE thumbnail_version::text END,
		       CASE WHEN thumbnail_dark_version = 0 THEN '' ELSE thumbnail_dark_version::text END,
		       thumbnail_failure,
		       CASE WHEN thumbnail_failed_version = 0 THEN '' ELSE thumbnail_failed_version::text END,
		       thumbnail_attempts
		FROM portal_assets WHERE id = $1`, id).
		Scan(&s.Light, &s.Dark, &s.LightStamp, &s.DarkStamp, &s.Failure, &s.FailedAt, &s.Attempts))
	return s
}

func resourceTile(t *testing.T, db *sql.DB, id string) tileState {
	t.Helper()
	var s tileState
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT thumbnail_s3_key, thumbnail_dark_s3_key,
		       COALESCE(to_char(thumbnail_captured_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), ''),
		       COALESCE(to_char(thumbnail_dark_captured_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), ''),
		       thumbnail_failure,
		       COALESCE(to_char(thumbnail_failed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), ''),
		       thumbnail_attempts
		FROM resources WHERE id = $1`, id).
		Scan(&s.Light, &s.Dark, &s.LightStamp, &s.DarkStamp, &s.Failure, &s.FailedAt, &s.Attempts))
	return s
}
