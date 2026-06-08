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
