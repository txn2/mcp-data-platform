//go:build integration

package knowledgepage

// Real-Postgres tests for knowledge pages (#633): they exercise the real schema,
// the portal_knowledge_page_fts function, and the inline-body lexical search that
// the sqlmock unit tests cannot. The keystone claim is that page CONTENT (body),
// not just the title, is searchable.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestPages_RealDB_CRUDAndBodySearch(t *testing.T) {
	store := &postgresStore{db: testdb.New(t)}
	ctx := context.Background()

	page := Page{
		ID:           NewID(),
		Slug:         "fiscal-calendar",
		Title:        "Fiscal Calendar",
		Summary:      "How the company defines fiscal quarters.",
		Body:         "Our fiscal year starts in February. Q1 covers February through April.",
		Tags:         []string{"finance", "calendar"},
		CreatedBy:    "alice@example.com",
		CreatedEmail: "alice@example.com",
	}
	require.NoError(t, store.Insert(ctx, page))

	// Round-trip.
	got, err := store.Get(ctx, page.ID)
	require.NoError(t, err)
	assert.Equal(t, "Fiscal Calendar", got.Title)
	assert.Equal(t, 1, got.CurrentVersion)
	assert.ElementsMatch(t, []string{"finance", "calendar"}, got.Tags)

	bySlug, err := store.GetBySlug(ctx, "fiscal-calendar")
	require.NoError(t, err)
	assert.Equal(t, page.ID, bySlug.ID)

	// Lexical search matches a word that appears ONLY in the body, proving the
	// body is indexed (the asset indexer would miss this).
	results, err := store.Search(ctx, SearchQuery{QueryText: "February", Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, results, "body-only term must match (body is indexed)")
	assert.Equal(t, page.ID, results[0].Page.ID)

	// Update creates a new version and changes searchable content.
	newTitle := "Fiscal Calendar (FY)"
	newBody := "The fiscal year now starts in March."
	newTags := []string{"finance"}
	require.NoError(t, store.Update(ctx, page.ID, Update{
		Title: &newTitle, Body: &newBody, Tags: &newTags, UpdatedBy: "bob@example.com", ChangeSummary: "shift start",
	}))
	updated, err := store.Get(ctx, page.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, updated.CurrentVersion)
	assert.Equal(t, "bob@example.com", updated.UpdatedBy)

	versions, total, err := store.ListVersions(ctx, page.ID, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, versions, 2)

	// Soft-delete removes it from search.
	require.NoError(t, store.SoftDelete(ctx, page.ID))
	afterDelete, err := store.Search(ctx, SearchQuery{QueryText: "fiscal", Limit: 10})
	require.NoError(t, err)
	for _, r := range afterDelete {
		assert.NotEqual(t, page.ID, r.Page.ID, "deleted page must not appear in search")
	}
	assert.ErrorIs(t, store.SoftDelete(ctx, page.ID), ErrNotFound)
}

// TestGraph_RealDB_ListEntityRefsForPages exercises the corpus-wide reference
// read against the real schema. sqlmock does not parse SQL, so only a real
// database proves that the ANY($1) page binding and the join that excludes a
// soft-deleted page's references actually behave as the graph endpoint assumes.
func TestGraph_RealDB_ListEntityRefsForPages(t *testing.T) {
	store := &postgresStore{db: testdb.New(t)}
	ctx := context.Background()

	live := Page{ID: NewID(), Title: "Live Page", Body: "b", CreatedBy: "a@example.com"}
	gone := Page{ID: NewID(), Title: "Removed Page", Body: "b", CreatedBy: "a@example.com"}
	other := Page{ID: NewID(), Title: "Unrequested Page", Body: "b", CreatedBy: "a@example.com"}
	for _, p := range []Page{live, gone, other} {
		require.NoError(t, store.Insert(ctx, p))
	}
	require.NoError(t, store.AddEntityRefs(ctx, live.ID, []EntityRef{
		DataHubRef("urn:li:dataset:(trino,sales.orders,PROD)", RefSourcePromoted),
		{TargetType: RefTargetKnowledgePage, RefPageID: other.ID, Source: RefSourceManual},
	}))
	require.NoError(t, store.AddEntityRefs(ctx, gone.ID, []EntityRef{
		DataHubRef("urn:li:dataset:(trino,hr.salaries,PROD)", RefSourcePromoted),
	}))
	require.NoError(t, store.AddEntityRefs(ctx, other.ID, []EntityRef{
		DataHubRef("urn:li:dataset:(trino,marketing.leads,PROD)", RefSourcePromoted),
	}))
	require.NoError(t, store.SoftDelete(ctx, gone.ID))

	refs, err := store.ListEntityRefsForPages(ctx, []string{live.ID, gone.ID})
	require.NoError(t, err)

	urns := make([]string, 0, len(refs))
	for _, r := range refs {
		assert.Equal(t, live.ID, r.PageID, "a removed page contributes no references")
		urns = append(urns, r.URN())
	}
	assert.ElementsMatch(t, []string{
		"urn:li:dataset:(trino,sales.orders,PROD)",
		"mcp:knowledge_page:" + other.ID,
	}, urns, "the requested live page's references are returned in full")

	empty, err := store.ListEntityRefsForPages(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
