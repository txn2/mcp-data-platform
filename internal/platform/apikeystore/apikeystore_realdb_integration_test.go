//go:build integration

package apikeystore

// Real-Postgres tests for the two statements every replica's key decisions
// rest on (#1715). Create relies on ON CONFLICT DO NOTHING reporting zero rows
// for a taken name, and HoldsKey on a name and hash matching one row; sqlmock
// returns whatever row count and result a test supplies, so only a real
// PostgreSQL says a second create of a name is refused without touching the
// first key, and a key replaced under its name is no longer held.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestRealDB_CreateRefusesATakenNameAndKeepsTheFirstKey(t *testing.T) {
	store := NewPostgres(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, Definition{Name: "ci", KeyHash: "$2a$10$first", Roles: []string{"analyst"}, CreatedBy: "a@example.com"}))
	err := store.Create(ctx, Definition{Name: "ci", KeyHash: "$2a$10$second", Roles: []string{"admin"}, CreatedBy: "b@example.com"})
	require.ErrorIs(t, err, ErrExists)

	keys, err := store.HashedKeys(ctx)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, "$2a$10$first", keys[0].KeyHash, "the refused create replaced the stored key")
	assert.Equal(t, []string{"analyst"}, keys[0].Roles)
}

func TestRealDB_HoldsKeyMatchesNameAndHash(t *testing.T) {
	store := NewPostgres(testdb.New(t))
	ctx := context.Background()
	require.NoError(t, store.Create(ctx, Definition{Name: "ci", KeyHash: "$2a$10$first", Roles: []string{"analyst"}}))

	held, err := store.HoldsKey(ctx, "ci", "$2a$10$first")
	require.NoError(t, err)
	assert.True(t, held)

	held, err = store.HoldsKey(ctx, "ci", "$2a$10$other")
	require.NoError(t, err)
	assert.False(t, held, "a different hash under the same name is not the stored key")

	require.NoError(t, store.Delete(ctx, "ci"))
	held, err = store.HoldsKey(ctx, "ci", "$2a$10$first")
	require.NoError(t, err)
	assert.False(t, held, "a deleted key is still held")

	require.NoError(t, store.Create(ctx, Definition{Name: "ci", KeyHash: "$2a$10$second", Roles: []string{"admin"}}))
	held, err = store.HoldsKey(ctx, "ci", "$2a$10$first")
	require.NoError(t, err)
	assert.False(t, held, "the key replaced under its name is still held")
	assert.ErrorIs(t, store.Delete(ctx, "missing"), ErrNotFound)
}
