package migrate

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq" // postgres driver for the real-database gate

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// transparentTilesVersion is the migration under test: the one that leaves only
// SVG and image tiles owed a redraw when the renderer generation rises to 3
// (#1991).
const transparentTilesVersion = 172

// transparentTileCases are the stored types seeded at generation 2, each with
// a tile, and whether the migration leaves the row owed a redraw. Written out
// rather than read from internal/thumbtypes: this is what the migration did
// when it shipped.
var transparentTileCases = []struct {
	id, contentType string
	redrawn         bool
}{
	{"svg", "image/svg+xml", true},
	{"svg-params", "IMAGE/SVG+XML; charset=utf-8", true},
	{"png", "image/png", true},
	{"jpeg", "image/jpeg", true},
	{"webp", "image/webp", true},
	{"gif", "image/gif", true},
	{"icon", "image/x-icon", true},
	{"markdown", "text/markdown", false},
	{"csv", "text/csv", false},
	{"pdf", "application/pdf", false},
	{"html", "text/html", false},
	{"xml", "application/atom+xml", false},
}

// TestMigrationsAgainstRealPostgres_TransparentTilesRedrawn seeds assets and
// resources with tiles drawn at generation 2 and reads them after the
// migration: an SVG or an image is left at 2, which generation 3 owes a redraw,
// with its attempts reset and its tile still recorded; every other type is
// stamped 3 and keeps its tile untouched. A row drawn by an older generation is
// left as it was, already owed.
//
// The file sorts before migrate_realpg_test.go for the reason the other seeded
// migration tests give.
func TestMigrationsAgainstRealPostgres_TransparentTilesRedrawn(t *testing.T) {
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

	require.NoError(t, Steps(db, transparentTilesVersion-1), "migrate to the prior revision")
	for _, tc := range transparentTileCases {
		seedTiledAsset(t, db, tc.id, tc.contentType)
		seedTiledResource(t, db, tc.id, tc.contentType)
	}
	setRenderer(t, db, 2)
	// A row an older generation drew is already owed and stays so.
	seedTiledAsset(t, db, "old", "text/markdown")
	seedTiledResource(t, db, "old", "text/markdown")

	require.NoError(t, Steps(db, 1), "apply the tile migration")

	for _, tc := range transparentTileCases {
		wantRenderer, wantAttempts := 3, 3
		if tc.redrawn {
			wantRenderer, wantAttempts = 2, 0
		}
		for _, table := range []string{"portal_assets", "resources"} {
			renderer, attempts, key := rendererOf(t, db, table, tc.id)
			assert.Equal(t, wantRenderer, renderer, "%s %s (%s) renderer", table, tc.id, tc.contentType)
			assert.Equal(t, wantAttempts, attempts, "%s %s (%s) attempts", table, tc.id, tc.contentType)
			assert.NotEmpty(t, key, "%s %s keeps serving its tile until it is redrawn", table, tc.id)
		}
	}
	for _, table := range []string{"portal_assets", "resources"} {
		renderer, _, _ := rendererOf(t, db, table, "old")
		assert.Zero(t, renderer, "%s: a row an older generation drew is left owed", table)
	}

	require.NoError(t, Steps(db, -1), "roll the migration back")
	renderer, _, _ := rendererOf(t, db, "portal_assets", "markdown")
	assert.Equal(t, 2, renderer, "rolling back returns the stamp to the generation that drew it")
	require.NoError(t, Steps(db, 1), "roll forward again")
}

func setRenderer(t *testing.T, db *sql.DB, generation int) {
	t.Helper()
	for _, table := range []string{"portal_assets", "resources"} {
		_, err := db.ExecContext(t.Context(), `UPDATE `+table+` SET thumbnail_renderer = $1`, generation) // #nosec G202 -- fixed table names
		require.NoError(t, err)
	}
}

func rendererOf(t *testing.T, db *sql.DB, table, id string) (renderer, attempts int, key string) {
	t.Helper()
	require.NoError(t, db.QueryRowContext(t.Context(),
		`SELECT thumbnail_renderer, thumbnail_attempts, thumbnail_s3_key FROM `+table+` WHERE id = $1`, id). // #nosec G202 -- fixed table names
		Scan(&renderer, &attempts, &key))
	return renderer, attempts, key
}
