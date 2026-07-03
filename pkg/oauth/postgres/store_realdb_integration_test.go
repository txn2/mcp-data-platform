//go:build integration

package postgres

// Real-Postgres round-trip test for the OAuth client store. CreateClient
// marshals RedirectURIs/GrantTypes to JSONB; a nil slice becomes JSON null,
// which Postgres accepts into the NOT NULL JSONB columns (no 23502, unlike a
// nil pq.Array into a TEXT[] column). This test pins that behavior against the
// real schema so the JSONB-vs-array distinction cannot silently regress.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
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
