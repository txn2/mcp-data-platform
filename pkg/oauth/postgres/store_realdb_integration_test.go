//go:build integration

package postgres

// Real-Postgres round-trip test for the OAuth client store. CreateClient
// marshals RedirectURIs/GrantTypes to JSONB; a nil slice becomes JSON null,
// which Postgres accepts into the NOT NULL JSONB columns (no 23502, unlike a
// nil pq.Array into a TEXT[] column). This test pins that behavior against the
// real schema so the JSONB-vs-array distinction cannot silently regress.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
	"github.com/txn2/mcp-data-platform/pkg/oauth"
)

// TestOAuthStore_Consume_RealDB proves the DELETE ... RETURNING consume
// statements against the real schema: one statement both returns the row and
// removes it, and a second consume of the same value fails.
func TestOAuthStore_Consume_RealDB(t *testing.T) {
	store := New(testdb.New(t))
	ctx := context.Background()

	code := &oauth.AuthorizationCode{
		ID:        "ac_realdb_1",
		Code:      "code-realdb-1",
		ClientID:  "client-realdb-consume",
		UserID:    "user-realdb-1",
		Scope:     "read",
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, store.SaveAuthorizationCode(ctx, code))

	got, err := store.ConsumeAuthorizationCode(ctx, "code-realdb-1")
	require.NoError(t, err)
	assert.Equal(t, "user-realdb-1", got.UserID)

	_, err = store.ConsumeAuthorizationCode(ctx, "code-realdb-1")
	require.Error(t, err, "second consume of the same code must fail")

	token := &oauth.RefreshToken{
		ID:        "rt_realdb_1",
		Token:     "token-realdb-1",
		ClientID:  "client-realdb-consume",
		UserID:    "user-realdb-1",
		Scope:     "read",
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, store.SaveRefreshToken(ctx, token))

	gotToken, err := store.ConsumeRefreshToken(ctx, "token-realdb-1")
	require.NoError(t, err)
	assert.Equal(t, "user-realdb-1", gotToken.UserID)

	_, err = store.ConsumeRefreshToken(ctx, "token-realdb-1")
	require.Error(t, err, "second consume of the same token must fail")
}

// TestOAuthStore_StateStore_RealDB proves the authorization-state round trip
// against the real schema: save, upsert re-save (the prompt=none retry path),
// get, delete, and the created_at-based cleanup sweep.
func TestOAuthStore_StateStore_RealDB(t *testing.T) {
	store := New(testdb.New(t))
	ctx := context.Background()

	state := &oauth.AuthorizationState{
		ClientID:      "client-realdb-state",
		RedirectURI:   "http://localhost:8080/callback",
		State:         "client-state",
		CodeChallenge: "challenge",
		Scope:         "openid",
		UpstreamState: "upstream-realdb-1",
		CreatedAt:     time.Now().UTC(),
	}
	require.NoError(t, store.SaveState(ctx, "upstream-realdb-1", state))

	// Re-save with PromptNoneAttempted set: must upsert, not conflict.
	state.PromptNoneAttempted = true
	require.NoError(t, store.SaveState(ctx, "upstream-realdb-1", state))

	got, err := store.GetState(ctx, "upstream-realdb-1")
	require.NoError(t, err)
	assert.True(t, got.PromptNoneAttempted)
	assert.Equal(t, "client-realdb-state", got.ClientID)

	// Cleanup with a generous max age must keep the fresh row.
	require.NoError(t, store.CleanupExpiredStates(ctx, time.Hour))
	_, err = store.GetState(ctx, "upstream-realdb-1")
	require.NoError(t, err)

	// Cleanup with a zero max age sweeps it.
	require.NoError(t, store.CleanupExpiredStates(ctx, 0))
	_, err = store.GetState(ctx, "upstream-realdb-1")
	require.ErrorIs(t, err, oauth.ErrStateNotFound)

	// Delete path.
	require.NoError(t, store.SaveState(ctx, "upstream-realdb-2", state))
	require.NoError(t, store.DeleteState(ctx, "upstream-realdb-2"))
	_, err = store.GetState(ctx, "upstream-realdb-2")
	require.ErrorIs(t, err, oauth.ErrStateNotFound)
}

func TestOAuthStore_CreateClient_RealDB_NilSlices(t *testing.T) {
	store := New(testdb.New(t))
	ctx := context.Background()

	client := &oauth.Client{
		ID:           "oc_realdb_1",
		ClientID:     "client-realdb-1",
		ClientSecret: "secret-hash",
		Name:         "RealDB Test Client",
		CreatedAt:    time.Now().UTC(),
		Active:       true,
		// RedirectURIs and GrantTypes left nil — marshaled to JSON null into the
		// NOT NULL JSONB columns; Postgres accepts this (no constraint violation).
	}
	require.NoError(t, store.CreateClient(ctx, client), "create client with nil slices")

	got, err := store.GetClient(ctx, "client-realdb-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "client-realdb-1", got.ClientID)
	assert.Equal(t, "RealDB Test Client", got.Name)
}

// TestOAuthStore_TokensHashedAtRest_RealDB proves credentials are stored only
// as SHA-256 digests against the real schema: the persisted column value is
// the digest of the raw credential (never the raw value), while raw-value
// lookups still succeed because the store hashes at the persistence boundary.
func TestOAuthStore_TokensHashedAtRest_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := New(db)
	ctx := context.Background()

	const rawToken = "raw-refresh-token-realdb"
	token := &oauth.RefreshToken{
		ID:        "rt_realdb_hash",
		Token:     rawToken,
		ClientID:  "client-realdb-hash",
		UserID:    "user-realdb-hash",
		Scope:     "read",
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, store.SaveRefreshToken(ctx, token))

	var storedToken string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT token FROM oauth_refresh_tokens WHERE id = $1`, "rt_realdb_hash").
		Scan(&storedToken))
	assert.Equal(t, oauth.HashToken(rawToken), storedToken, "column must hold the digest")
	assert.NotEqual(t, rawToken, storedToken, "column must not hold the raw credential")

	gotToken, err := store.GetRefreshToken(ctx, rawToken)
	require.NoError(t, err, "raw-value lookup must succeed")
	assert.Equal(t, "user-realdb-hash", gotToken.UserID)

	consumedToken, err := store.ConsumeRefreshToken(ctx, rawToken)
	require.NoError(t, err, "raw-value consume must succeed")
	assert.Equal(t, "user-realdb-hash", consumedToken.UserID)

	const rawCode = "raw-auth-code-realdb"
	code := &oauth.AuthorizationCode{
		ID:        "ac_realdb_hash",
		Code:      rawCode,
		ClientID:  "client-realdb-hash",
		UserID:    "user-realdb-hash",
		Scope:     "read",
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, store.SaveAuthorizationCode(ctx, code))

	var storedCode string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT code FROM oauth_authorization_codes WHERE id = $1`, "ac_realdb_hash").
		Scan(&storedCode))
	assert.Equal(t, oauth.HashToken(rawCode), storedCode, "column must hold the digest")
	assert.NotEqual(t, rawCode, storedCode, "column must not hold the raw credential")

	gotCode, err := store.GetAuthorizationCode(ctx, rawCode)
	require.NoError(t, err, "raw-value lookup must succeed")
	assert.Equal(t, "user-realdb-hash", gotCode.UserID)

	consumedCode, err := store.ConsumeAuthorizationCode(ctx, rawCode)
	require.NoError(t, err, "raw-value consume must succeed")
	assert.Equal(t, "user-realdb-hash", consumedCode.UserID)
}

// TestOAuthStore_Migration78HashesPlaintext_RealDB proves migration
// 000078_hash_oauth_tokens converts pre-existing plaintext rows in place:
// rows inserted as plaintext at version 77 remain valid live sessions after
// migrating, because the raw-value store lookup finds the hashed row.
func TestOAuthStore_Migration78HashesPlaintext_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	// testdb applies the full migration set; step back to version 77 (one
	// migration at a time, so this stays correct as later migrations land)
	// so plaintext rows can exist the way a pre-upgrade deployment left
	// them.
	const preHashVersion = 77
	for {
		version, dirty, err := migrate.Version(db)
		require.NoError(t, err, "read migration version")
		require.False(t, dirty, "migration state must not be dirty")
		if version == preHashVersion {
			break
		}
		require.Greater(t, version, uint(preHashVersion),
			"stepped below the pre-hash version without hitting it")
		require.NoError(t, migrate.Steps(db, -1), "roll back one migration")
	}

	const rawToken = "plaintext-refresh-token"
	_, err := db.ExecContext(ctx, `
		INSERT INTO oauth_refresh_tokens (id, token, client_id, user_id, scope, expires_at, created_at)
		VALUES ('rt_mig78', $1, 'client-mig78', 'user-mig78', 'read', NOW() + INTERVAL '1 day', NOW())`,
		rawToken)
	require.NoError(t, err, "insert plaintext refresh token")

	const rawCode = "plaintext-auth-code"
	_, err = db.ExecContext(ctx, `
		INSERT INTO oauth_authorization_codes (id, code, client_id, user_id, code_challenge, redirect_uri, scope, expires_at, created_at)
		VALUES ('ac_mig78', $1, 'client-mig78', 'user-mig78', '', 'http://localhost/callback', 'read', NOW() + INTERVAL '10 minutes', NOW())`,
		rawCode)
	require.NoError(t, err, "insert plaintext authorization code")

	require.NoError(t, migrate.Steps(db, 1), "apply migration 000078")

	store := New(db)

	gotToken, err := store.GetRefreshToken(ctx, rawToken)
	require.NoError(t, err, "raw-token lookup must succeed after the migration hashed the row")
	assert.Equal(t, "user-mig78", gotToken.UserID)
	assert.Equal(t, oauth.HashToken(rawToken), gotToken.Token, "migrated row must hold the digest")

	gotCode, err := store.GetAuthorizationCode(ctx, rawCode)
	require.NoError(t, err, "raw-code lookup must succeed after the migration hashed the row")
	assert.Equal(t, "user-mig78", gotCode.UserID)
	assert.Equal(t, oauth.HashToken(rawCode), gotCode.Code, "migrated row must hold the digest")

	// Re-running the up migration must be a no-op: its WHERE guard skips
	// already-hashed values, so dirty-migration recovery or an operator
	// sweep never double-hashes digests (which would invalidate every
	// outstanding credential without any error).
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve test file path")
	upPath := filepath.Join(filepath.Dir(thisFile),
		"..", "..", "database", "migrate", "migrations", "000078_hash_oauth_tokens.up.sql")
	upSQL, err := os.ReadFile(upPath) //nolint:gosec // fixed in-repo migration path
	require.NoError(t, err, "read up migration")
	_, err = db.ExecContext(ctx, string(upSQL))
	require.NoError(t, err, "re-run up migration")

	_, err = store.GetRefreshToken(ctx, rawToken)
	require.NoError(t, err, "raw-token lookup must still succeed after re-running the migration")
	_, err = store.GetAuthorizationCode(ctx, rawCode)
	require.NoError(t, err, "raw-code lookup must still succeed after re-running the migration")
}
