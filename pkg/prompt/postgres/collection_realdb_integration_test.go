//go:build integration

package postgres

// Real-Postgres round-trip tests for prompt collections (#1010): the actual
// migration schema (case-insensitive unique name, collection_id FK with
// ON DELETE SET NULL) that sqlmock cannot exercise.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

func TestCollections_RealDB_RoundTrip(t *testing.T) {
	store := New(testdb.New(t))
	ctx := context.Background()

	col := &prompt.Collection{Name: "Sales Reporting", Description: "Sales SOPs", CreatedBy: "jane@example.com"}
	require.NoError(t, store.CreateCollection(ctx, col))
	require.NotEmpty(t, col.ID)

	t.Run("name uniqueness is case-insensitive", func(t *testing.T) {
		dup := &prompt.Collection{Name: "sales reporting"}
		assert.ErrorIs(t, store.CreateCollection(ctx, dup), prompt.ErrCollectionExists)
	})

	p := &prompt.Prompt{
		Name: "col-rt", Content: "Body.", Scope: prompt.ScopePersonal,
		OwnerEmail: "jane@example.com", Source: prompt.SourceOperator, Enabled: true,
	}
	require.NoError(t, store.Create(ctx, p))

	t.Run("assignment round-trips through the prompt read path", func(t *testing.T) {
		require.NoError(t, store.SetPromptCollection(ctx, p.ID, col.ID))
		got, err := store.GetByID(ctx, p.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, col.ID, got.CollectionID)

		cols, err := store.ListCollections(ctx)
		require.NoError(t, err)
		require.Len(t, cols, 1)
		assert.Equal(t, 1, cols[0].PromptCount, "member count aggregates")
	})

	t.Run("assigning a dangling collection is the sentinel", func(t *testing.T) {
		err := store.SetPromptCollection(ctx, p.ID, "00000000-0000-0000-0000-000000000000")
		assert.ErrorIs(t, err, prompt.ErrCollectionNotFound)
	})

	t.Run("rename persists and collides case-insensitively", func(t *testing.T) {
		other := &prompt.Collection{Name: "Marketing"}
		require.NoError(t, store.CreateCollection(ctx, other))
		assert.ErrorIs(t, store.UpdateCollection(ctx, other.ID, "SALES REPORTING", ""), prompt.ErrCollectionExists)
		require.NoError(t, store.UpdateCollection(ctx, other.ID, "Marketing Ops", "renamed"))
		got, err := store.GetCollection(ctx, other.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Marketing Ops", got.Name)
	})

	t.Run("delete releases members to the uncollected group", func(t *testing.T) {
		require.NoError(t, store.DeleteCollection(ctx, col.ID))
		got, err := store.GetByID(ctx, p.ID)
		require.NoError(t, err)
		require.NotNil(t, got, "the prompt itself survives collection deletion")
		assert.Empty(t, got.CollectionID, "ON DELETE SET NULL clears the assignment")
	})

	t.Run("clearing an assignment binds NULL", func(t *testing.T) {
		again := &prompt.Collection{Name: "Regroup"}
		require.NoError(t, store.CreateCollection(ctx, again))
		require.NoError(t, store.SetPromptCollection(ctx, p.ID, again.ID))
		require.NoError(t, store.SetPromptCollection(ctx, p.ID, ""))
		got, err := store.GetByID(ctx, p.ID)
		require.NoError(t, err)
		assert.Empty(t, got.CollectionID)
	})
}
