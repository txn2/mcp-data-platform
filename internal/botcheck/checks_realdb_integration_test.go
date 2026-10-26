//go:build integration

package botcheck

// Real-Postgres tests for the named bot checks (#2259): the store, the
// foreign key that refuses deleting a check an endpoint names, and migration
// 000221, which turns the one deployment-wide setting into the check named
// "default" and rewrites every stored binding into the new access modes.

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
)

// preAccessModesVersion is the schema before migration 000221.
const preAccessModesVersion = 220

// rollBackToBeforeAccessModes steps down to the version before 000221, by
// name, so a migration added later does not move what one step back undoes.
func rollBackToBeforeAccessModes(t *testing.T, db *sql.DB) {
	t.Helper()
	for {
		version, dirty, err := migrate.Version(db)
		require.NoError(t, err)
		require.False(t, dirty)
		if version == preAccessModesVersion {
			return
		}
		require.Greater(t, version, uint(preAccessModesVersion))
		require.NoError(t, migrate.Steps(db, -1))
	}
}

func insertScript(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO scripts (id, name, owner_email, status) VALUES ($1, $2, 'o@example.com', 'active')`, id, name)
	require.NoError(t, err)
}

// insertOldBinding writes a binding in 000203's shape.
func insertOldBinding(t *testing.T, db *sql.DB, scriptID, name, mode string, methods []string, openReads bool) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO script_endpoints (script_id, name, methods, auth_mode, open_reads, timeout_seconds,
		                              max_memory_bytes, max_body_bytes, rate_limit, caller_rate_limit)
		VALUES ($1, $2, $3, $4, $5, 10, 67108864, 1048576, 600, 120)`,
		scriptID, name, pq.Array(methods), mode, openReads)
	require.NoError(t, err)
}

type migrated struct {
	mode, check string
	callers     []string
	checked     []string
	enabled     bool
}

func readBinding(t *testing.T, db *sql.DB, name string) migrated {
	t.Helper()
	var m migrated
	require.NoError(t, db.QueryRow(`
		SELECT auth_mode, COALESCE(bot_check, ''), callers, checked_methods, enabled
		  FROM script_endpoints WHERE name = $1`, name).
		Scan(&m.mode, &m.check, pq.Array(&m.callers), pq.Array(&m.checked), &m.enabled))
	return m
}

// seedBeforeAccessModes writes four bindings in the old shape, and the
// section when it is not empty, then applies 000221.
func seedBeforeAccessModes(t *testing.T, db *sql.DB, section string) {
	t.Helper()
	rollBackToBeforeAccessModes(t, db)
	for i, s := range []struct {
		name, mode string
		methods    []string
		openReads  bool
	}{
		{"orders", "signed_in", []string{"GET"}, false},
		{"contact", "public", []string{"GET", "POST"}, false},
		{"form", "public", []string{"GET", "POST"}, true},
		{"lookup", "public", []string{"GET"}, true},
	} {
		id := []string{
			"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222",
			"33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444",
		}[i]
		insertScript(t, db, id, s.name)
		insertOldBinding(t, db, id, s.name, s.mode, s.methods, s.openReads)
	}
	// Stepping down recreated the section from "default"; the seed replaces it
	// with the one the case describes, or none.
	_, err := db.Exec(`DELETE FROM platform_settings WHERE section = 'bot_check'`)
	require.NoError(t, err)
	if section != "" {
		_, err := db.Exec(`INSERT INTO platform_settings (section, value, updated_by) VALUES ('bot_check', $1::jsonb, 'admin@example.com')`, section)
		require.NoError(t, err)
	}
	require.NoError(t, migrate.Steps(db, 1), "apply 000221")
}

// #2259 acceptance: a binding stored as signed_in reads back as authenticated
// admitting every caller, and one stored as public reads back as bot_check
// naming "default", built from the deployment's section.
func TestMigration_RewritesBindingsAndMakesTheDefaultCheck_RealDB(t *testing.T) {
	db := testdb.New(t)
	seedBeforeAccessModes(t, db, `{"provider":"turnstile","site_key":"site","secret_name":"ts","difficulty":18}`)

	assert.Equal(t, migrated{mode: "authenticated", callers: []string{"*"}, checked: []string{}, enabled: true}, readBinding(t, db, "orders"))
	assert.Equal(t, migrated{mode: "bot_check", check: "default", callers: []string{}, checked: []string{}, enabled: true},
		readBinding(t, db, "contact"), "every method checked")
	assert.Equal(t, migrated{mode: "bot_check", check: "default", callers: []string{}, checked: []string{"POST"}, enabled: true},
		readBinding(t, db, "form"), "open reads become the methods that are checked")
	assert.Equal(t, migrated{mode: "public", callers: []string{}, checked: []string{}, enabled: true},
		readBinding(t, db, "lookup"), "a read-only endpoint with open reads answered with no check")

	c, err := NewStore(db).Get(context.Background(), DefaultName)
	require.NoError(t, err)
	assert.Equal(t, ProviderTurnstile, c.Provider)
	assert.Equal(t, "site", c.SiteKey)
	assert.Equal(t, "ts", c.SecretName)
	assert.Equal(t, 18, c.Difficulty)
	assert.Equal(t, DefaultMaxDifficulty, c.MaxDifficulty, "a field the section left out takes the default")

	var sections int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM platform_settings WHERE section = 'bot_check'`).Scan(&sections))
	assert.Zero(t, sections, "the section is replaced by the checks")
}

// A deployment that never wrote the section was on the proof of work.
func TestMigration_NoSectionIsTheProofOfWork_RealDB(t *testing.T) {
	db := testdb.New(t)
	seedBeforeAccessModes(t, db, "")
	c, err := NewStore(db).Get(context.Background(), DefaultName)
	require.NoError(t, err)
	assert.Equal(t, New(DefaultName).Provider, c.Provider)
	assert.Equal(t, DefaultDifficulty, c.Difficulty)
	assert.Equal(t, "bot_check", readBinding(t, db, "contact").mode)
}

// none with the override admitted every public endpoint with no check: they
// are public. none without it refused them all: they name "default" and are
// off until an administrator turns them on.
func TestMigration_TheNoneProvider_RealDB(t *testing.T) {
	db := testdb.New(t)
	seedBeforeAccessModes(t, db, `{"provider":"none","allow_public_without_check":true}`)
	assert.Equal(t, migrated{mode: "public", callers: []string{}, checked: []string{}, enabled: true}, readBinding(t, db, "contact"))
	assert.Equal(t, "public", readBinding(t, db, "form").mode)

	db = testdb.New(t)
	seedBeforeAccessModes(t, db, `{"provider":"none"}`)
	assert.Equal(t, migrated{mode: "bot_check", check: "default", callers: []string{}, checked: []string{}, enabled: false},
		readBinding(t, db, "contact"))
	assert.False(t, readBinding(t, db, "form").enabled)
	assert.True(t, readBinding(t, db, "lookup").enabled, "the open read-only endpoint answered and still does")
	assert.True(t, readBinding(t, db, "orders").enabled)
	c, err := NewStore(db).Get(context.Background(), DefaultName)
	require.NoError(t, err)
	assert.Equal(t, ProviderPoW, c.Provider, "none is no provider; default is the proof of work")
}

// The migration steps back down and up again without losing a binding.
func TestMigration_RoundTrips_RealDB(t *testing.T) {
	db := testdb.New(t)
	seedBeforeAccessModes(t, db, `{"provider":"pow"}`)
	require.NoError(t, migrate.Steps(db, -1))
	var mode string
	var openReads bool
	require.NoError(t, db.QueryRow(`SELECT auth_mode, open_reads FROM script_endpoints WHERE name = 'form'`).Scan(&mode, &openReads))
	assert.Equal(t, "public", mode)
	assert.True(t, openReads)
	require.NoError(t, db.QueryRow(`SELECT auth_mode FROM script_endpoints WHERE name = 'orders'`).Scan(&mode))
	assert.Equal(t, "signed_in", mode)
	require.NoError(t, migrate.Steps(db, 1))
	assert.Equal(t, []string{"POST"}, readBinding(t, db, "form").checked)
}

// The store, and the foreign key that keeps a check an endpoint names.
func TestStore_PutListDeleteAndTheEndpointsUsingACheck_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	store := NewStore(db)

	forms := New("forms")
	forms.Difficulty, forms.MaxDifficulty = 20, 24
	saved, err := store.Put(ctx, forms, "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", saved.CreatedBy)
	assert.False(t, saved.CreatedAt.IsZero())

	forms.RateThreshold = 5
	saved, err = store.Put(ctx, forms, "other@example.com")
	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", saved.CreatedBy, "the creator is kept")
	assert.Equal(t, "other@example.com", saved.UpdatedBy)
	assert.Equal(t, 5, saved.RateThreshold)

	list, err := store.List(ctx)
	require.NoError(t, err)
	names := []string{}
	for _, c := range list {
		names = append(names, c.Name)
	}
	assert.Equal(t, []string{"default", "forms"}, names, "by name, the migration's default among them")

	_, err = store.Get(ctx, "gone")
	require.ErrorIs(t, err, ErrNotFound)

	insertScript(t, db, "55555555-5555-4555-8555-555555555555", "signup")
	_, err = db.Exec(`
		INSERT INTO script_endpoints (script_id, name, methods, auth_mode, bot_check, timeout_seconds,
		                              max_memory_bytes, max_body_bytes, rate_limit, caller_rate_limit)
		VALUES ('55555555-5555-4555-8555-555555555555', 'signup', '{POST}', 'bot_check', 'forms', 10, 67108864, 1048576, 600, 120)`)
	require.NoError(t, err)

	used, err := store.UsedBy(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"signup"}, used["forms"])

	err = store.Delete(ctx, "forms")
	var inUse *InUseError
	require.True(t, errors.As(err, &inUse), "%v", err)
	assert.Equal(t, []string{"signup"}, inUse.Endpoints)

	_, err = db.Exec(`UPDATE script_endpoints SET auth_mode = 'public', bot_check = NULL WHERE name = 'signup'`)
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, "forms"))
	require.ErrorIs(t, store.Delete(ctx, "forms"), ErrNotFound)

	_, err = db.Exec(`UPDATE script_endpoints SET auth_mode = 'bot_check', bot_check = 'gone' WHERE name = 'signup'`)
	require.Error(t, err, "a binding cannot name a check that does not exist")
	_, err = db.Exec(`UPDATE script_endpoints SET auth_mode = 'bot_check', bot_check = NULL WHERE name = 'signup'`)
	require.Error(t, err, "a bot_check binding names a check")
	_, err = db.Exec(`UPDATE script_endpoints SET auth_mode = 'signed_in' WHERE name = 'signup'`)
	require.Error(t, err, "signed_in is no longer a mode")
}
