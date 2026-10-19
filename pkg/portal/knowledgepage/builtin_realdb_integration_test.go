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

// pageRefIDs is the knowledge pages a page's stored references point at.
func pageRefIDs(t *testing.T, store *postgresStore, pageID string) []string {
	t.Helper()
	refs, err := store.ListEntityRefs(context.Background(), pageID)
	require.NoError(t, err)
	ids := []string{}
	for _, r := range refs {
		if r.TargetType == RefTargetKnowledgePage {
			ids = append(ids, r.RefPageID)
		}
	}
	return ids
}

// #2200: built-in pages cite each other by slug, and a page can cite one
// shipped after it in the same pass. On a fresh install every such citation
// was dropped. Each now lands as a reference to the cited page's id on this
// deployment, in both directions, and a deployment whose references an
// earlier release dropped is repaired by the next start with no page changed.
func TestBuiltins_RealDB_CitationsBySlugFormAGraph(t *testing.T) {
	store := &postgresStore{db: testdb.New(t)}
	ctx := context.Background()
	shipped := []BuiltinPage{
		{Slug: "platform-a", Title: "A", Summary: "a", Body: "Read mcp:knowledge_page:platform-b next.", Tags: []string{"x"}},
		{Slug: "platform-b", Title: "B", Summary: "b", Body: "Back to mcp:knowledge_page:platform-a. `mcp:knowledge_page:platform-gone` is code.", Tags: []string{"x"}},
	}
	_, err := store.ReconcileBuiltins(ctx, shipped)
	require.NoError(t, err)

	a, err := store.GetBySlug(ctx, "platform-a")
	require.NoError(t, err)
	b, err := store.GetBySlug(ctx, "platform-b")
	require.NoError(t, err)
	assert.Equal(t, []string{b.ID}, pageRefIDs(t, store, a.ID), "A cites B, which was inserted after it")
	assert.Equal(t, []string{a.ID}, pageRefIDs(t, store, b.ID))

	backlinks, err := store.ListPagesReferencing(ctx, EntityRef{TargetType: RefTargetKnowledgePage, RefPageID: b.ID})
	require.NoError(t, err)
	require.Len(t, backlinks, 1)
	assert.Equal(t, a.ID, backlinks[0].ID)

	// A deployment an earlier release reconciled with no references: the
	// next start writes no page and still derives them.
	_, err = store.db.ExecContext(ctx, `DELETE FROM knowledge_page_entity_refs`)
	require.NoError(t, err)
	stats, err := store.ReconcileBuiltins(ctx, shipped)
	require.NoError(t, err)
	assert.Equal(t, BuiltinReconcileStats{Skipped: 2}, stats)
	assert.Equal(t, []string{b.ID}, pageRefIDs(t, store, a.ID))
	assert.Equal(t, []string{a.ID}, pageRefIDs(t, store, b.ID))
}

// An operator page citing a built-in page by slug used to fail its whole
// inline-reference write on the foreign key, losing every other reference the
// body made. The slug is stored as the page's id (#2200).
func TestBuiltins_RealDB_OperatorPageCitesABuiltinBySlug(t *testing.T) {
	store := &postgresStore{db: testdb.New(t)}
	ctx := context.Background()
	_, err := store.ReconcileBuiltins(ctx, []BuiltinPage{
		{Slug: "platform-maps", Title: "Maps", Summary: "m", Body: "maps", Tags: []string{"x"}},
	})
	require.NoError(t, err)
	maps, err := store.GetBySlug(ctx, "platform-maps")
	require.NoError(t, err)

	mine := Page{ID: NewID(), Title: "Our regions", Body: "See mcp:knowledge_page:platform-maps.", CreatedBy: "alice@example.com", UpdatedBy: "alice@example.com"}
	require.NoError(t, store.Insert(ctx, mine))
	refs := ScanBodyRefs(mine.Body)
	require.NoError(t, store.ValidateRefTargets(ctx, refs), "a slug of a live page is a target that exists")
	require.NoError(t, store.ReplaceEntityRefsBySource(ctx, mine.ID, RefSourceInline, refs))
	assert.Equal(t, []string{maps.ID}, pageRefIDs(t, store, mine.ID))

	missing := ScanBodyRefs("See mcp:knowledge_page:platform-nothing.")
	require.ErrorIs(t, store.ValidateRefTargets(ctx, missing), ErrRefTargetNotFound)
	kept, err := store.FilterExistingRefTargets(ctx, missing)
	require.NoError(t, err)
	assert.Empty(t, kept)
}
