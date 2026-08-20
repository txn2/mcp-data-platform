//go:build integration

package knowledgepage

// Real-Postgres tests for the built-in page reconcile (#1390). The keystone
// claims sqlmock cannot check: the insert's ON CONFLICT clause matches the
// partial unique slug index of migration 000070, the builtin column and its
// tombstone index (000117) exist as written, an update really clears
// embedding_model and re-versions, and the operator's hide survives a
// reconcile.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestBuiltins_RealDB_ReconcileLifecycle(t *testing.T) {
	store := &postgresStore{db: testdb.New(t)}
	ctx := context.Background()
	shipped := []BuiltinPage{{
		Slug: "platform-topic", Title: "Topic", Summary: "sum", Body: "body v1",
		// Two tags: jsonb re-serializes ["a","b"] with its own spacing, and the
		// unchanged-release claim below fails if the compare reads JSON text.
		Tags: []string{"scripts", "starlark"},
	}}

	// First boot: created.
	stats, err := store.ReconcileBuiltins(ctx, shipped)
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Created: 1}, stats)

	page, err := store.GetBySlug(ctx, "platform-topic")
	require.NoError(t, err)
	assert.True(t, page.Builtin)
	assert.Equal(t, 1, page.CurrentVersion)
	assert.Equal(t, "platform", page.CreatedBy)

	// Same release again: nothing touched.
	stats, err = store.ReconcileBuiltins(ctx, shipped)
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Skipped: 1}, stats)
	unchanged, err := store.Get(ctx, page.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, unchanged.CurrentVersion)

	// People cannot edit it.
	title := "Mine now"
	err = store.Update(ctx, page.ID, Update{Title: &title, UpdatedBy: "alice@example.com"})
	require.ErrorIs(t, err, ErrBuiltinReadOnly)

	// A new release rewrites it: new version, index marker cleared for re-embed.
	shipped[0].Body = "body v2"
	stats, err = store.ReconcileBuiltins(ctx, shipped)
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Updated: 1}, stats)
	updated, err := store.Get(ctx, page.ID)
	require.NoError(t, err)
	assert.Equal(t, "body v2", updated.Body)
	assert.Equal(t, 2, updated.CurrentVersion)
	versions, total, err := store.ListVersions(ctx, page.ID, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Equal(t, "Updated by platform release", versions[0].ChangeSummary)
	var embeddingModel string
	require.NoError(t, store.db.QueryRowContext(ctx,
		`SELECT embedding_model FROM portal_knowledge_pages WHERE id = $1`, page.ID).Scan(&embeddingModel))
	assert.Empty(t, embeddingModel, "a content change must clear the index marker so the page is re-embedded")

	// Hiding it sticks: the reconcile respects the tombstone.
	require.NoError(t, store.SoftDelete(ctx, page.ID))
	stats, err = store.ReconcileBuiltins(ctx, shipped)
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Skipped: 1}, stats)
	_, err = store.GetBySlug(ctx, "platform-topic")
	require.ErrorIs(t, err, ErrNotFound)

	// The operator supersedes the topic under the same slug: theirs wins.
	theirs := Page{ID: NewID(), Slug: "platform-topic", Title: "Ours", Body: "our body",
		CreatedBy: "alice@example.com", CreatedEmail: "alice@example.com"}
	require.NoError(t, store.Insert(ctx, theirs))
	stats, err = store.ReconcileBuiltins(ctx, shipped)
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Skipped: 1}, stats)
	kept, err := store.GetBySlug(ctx, "platform-topic")
	require.NoError(t, err)
	assert.Equal(t, theirs.ID, kept.ID)
	assert.False(t, kept.Builtin)

	// Restore skips a hidden page whose slug a live operator page took: theirs
	// keeps the topic, and resurrecting would collide with the slug index.
	n, err := store.RestoreHidden(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)

	// With the operator's page gone, restore is the way back from Hide.
	require.NoError(t, store.SoftDelete(ctx, theirs.ID))
	n, err = store.RestoreHidden(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	back, err := store.GetBySlug(ctx, "platform-topic")
	require.NoError(t, err)
	assert.Equal(t, page.ID, back.ID, "the original builtin row returns, history intact")
	assert.True(t, back.Builtin)
}

func TestBuiltins_RealDB_PruneRemovesADroppedPage(t *testing.T) {
	store := &postgresStore{db: testdb.New(t)}
	ctx := context.Background()

	stats, err := store.ReconcileBuiltins(ctx, []BuiltinPage{
		{Slug: "platform-old", Title: "Old", Body: "b"},
		{Slug: "platform-kept", Title: "Kept", Body: "b"},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, stats.Created)

	// The next release ships without platform-old.
	stats, err = store.ReconcileBuiltins(ctx, []BuiltinPage{
		{Slug: "platform-kept", Title: "Kept", Body: "b"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Pruned)
	_, err = store.GetBySlug(ctx, "platform-old")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.GetBySlug(ctx, "platform-kept")
	require.NoError(t, err)

	// A prune is a retirement, not a hide: the tombstone released its slug, so
	// a release that ships the slug again (a rollback ran in between, or the
	// page returned) resurrects the page instead of reading the tombstone as
	// an operator's suppression.
	stats, err = store.ReconcileBuiltins(ctx, []BuiltinPage{
		{Slug: "platform-old", Title: "Old", Body: "b"},
		{Slug: "platform-kept", Title: "Kept", Body: "b"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Created, "a re-shipped retired slug must come back")
	back, err := store.GetBySlug(ctx, "platform-old")
	require.NoError(t, err)
	assert.True(t, back.Builtin)
}
