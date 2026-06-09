//go:build integration

package platform

// Real-Postgres proof for #593: the assembled ingestion path writes the
// platform's static prompts into the store and they become searchable, exactly
// like database-authored prompts. This exercises ingestStaticPrompts against a
// real pgvector database (not a mock), then ranks the result through the real
// store searcher, closing the loop that unit tests with a mock store cannot.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	pgstore "github.com/txn2/mcp-data-platform/pkg/prompt/postgres"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

func TestIngestStaticPrompts_RealDB_Searchable(t *testing.T) {
	store := pgstore.New(testdb.New(t))
	ctx := context.Background()

	p := &Platform{
		promptStore:     store,
		toolkitRegistry: registry.NewRegistry(),
		promptInfos: []registry.PromptInfo{
			{Name: "explore-available-data", Description: "Discover what datasets are available in the catalog", Content: "Explore data about {topic}."},
			{Name: "create-interactive-dashboard", Description: "Build a dashboard and save it", Content: "Create a dashboard about {topic}."},
		},
	}

	p.ingestStaticPrompts(ctx)

	// Stored as read-only, approved, global system rows.
	got, err := store.Get(ctx, "explore-available-data")
	require.NoError(t, err)
	require.NotNil(t, got, "static prompt ingested into the store")
	assert.Equal(t, prompt.SourceSystem, got.Source)
	assert.Equal(t, prompt.StatusApproved, got.Status)
	assert.Equal(t, prompt.ScopeGlobal, got.Scope)

	// Lexically searchable (no embedding provider needed) through the real store
	// searcher, the same path manage_prompt search uses.
	results, err := store.Search(ctx, prompt.SearchQuery{QueryText: "discover datasets catalog", Limit: 5})
	require.NoError(t, err)
	found := false
	for _, r := range results {
		if r.Prompt.Name == "explore-available-data" {
			found = true
		}
	}
	assert.True(t, found, "ingested static prompt is returned by the real store search")

	// Re-running ingest is idempotent (no duplicate system rows).
	p.ingestStaticPrompts(ctx)
	system, err := store.List(ctx, prompt.ListFilter{Source: prompt.SourceSystem})
	require.NoError(t, err)
	assert.Len(t, system, 2, "re-ingest does not duplicate system rows")
}
