//go:build integration

package postgres

// Real-Postgres round-trip tests for the prompt store. These run against a
// pgvector container with the full embedded migration set applied, so they
// exercise the actual schema (NOT NULL constraints, defaults, column types)
// that the sqlmock unit tests in store_test.go cannot. They exist because a
// create-path defect shipped to production despite green sqlmock tests:
// Create bound pq.Array(nil) -> SQL NULL into the NOT NULL `tags` column, which
// sqlmock rubber-stamps but real Postgres rejects (error 23502). Any future
// regression of that class fails here, in the default integration gate.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// TestStore_Create_RealDB_RoundTrip is the regression for the production
// prompt-create failure. The decisive assertion is the first Create: with no
// tags supplied, p.Tags is nil, and before the fix pq.Array(nil) bound NULL
// into the NOT NULL tags column. This test fails (23502) without the
// normalizeSlices call in Create and passes with it.
func TestStore_Create_RealDB_RoundTrip(t *testing.T) {
	store := New(testdb.New(t))
	ctx := context.Background()

	t.Run("personal prompt with no tags persists and round-trips", func(t *testing.T) {
		p := &prompt.Prompt{
			Name:       "rt-personal",
			Content:    "Summarize {topic}.",
			Scope:      prompt.ScopePersonal,
			OwnerEmail: "user@example.com",
			Source:     prompt.SourceOperator,
			Enabled:    true,
			// Tags intentionally left nil — this is the production failure mode.
		}
		require.NoError(t, store.Create(ctx, p), "create personal prompt with nil tags")
		require.NotEmpty(t, p.ID, "id assigned")

		got, err := store.GetPersonal(ctx, "user@example.com", "rt-personal")
		require.NoError(t, err)
		require.NotNil(t, got, "personal prompt retrievable")
		assert.Equal(t, "rt-personal", got.Name)
		assert.Equal(t, "Summarize {topic}.", got.Content)
		assert.Equal(t, prompt.ScopePersonal, got.Scope)
		// Stored as the empty array literal, read back as a non-nil empty slice.
		assert.NotNil(t, got.Tags)
		assert.Empty(t, got.Tags)
		assert.NotNil(t, got.Personas)
		assert.NotNil(t, got.RequestedPersonas)
	})

	t.Run("global prompt with no tags persists", func(t *testing.T) {
		p := &prompt.Prompt{
			Name:    "rt-global",
			Content: "Global body.",
			Scope:   prompt.ScopeGlobal,
			Source:  prompt.SourceOperator,
			Enabled: true,
		}
		require.NoError(t, store.Create(ctx, p))
		got, err := store.Get(ctx, "rt-global")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, prompt.ScopeGlobal, got.Scope)
	})

	t.Run("persona prompt with personas but no tags persists", func(t *testing.T) {
		p := &prompt.Prompt{
			Name:     "rt-persona",
			Content:  "Persona body.",
			Scope:    prompt.ScopePersona,
			Personas: []string{"analyst"},
			Source:   prompt.SourceOperator,
			Enabled:  true,
		}
		require.NoError(t, store.Create(ctx, p))
		got, err := store.Get(ctx, "rt-persona")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, []string{"analyst"}, got.Personas)
		assert.NotNil(t, got.Tags)
	})

	t.Run("explicit tags persist", func(t *testing.T) {
		p := &prompt.Prompt{
			Name:    "rt-tagged",
			Content: "Tagged body.",
			Scope:   prompt.ScopeGlobal,
			Tags:    []string{"analytics", "revenue"},
			Source:  prompt.SourceOperator,
			Enabled: true,
		}
		require.NoError(t, store.Create(ctx, p))
		got, err := store.Get(ctx, "rt-tagged")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.ElementsMatch(t, []string{"analytics", "revenue"}, got.Tags)
	})
}

// TestStore_ListSourceFilters_RealDB exercises the Source and ExcludeSource
// filters against real Postgres (#593): ingested static prompts (source=system)
// must be hideable from human-facing list queries and selectable for the
// reconciler's prune.
func TestStore_ListSourceFilters_RealDB(t *testing.T) {
	store := New(testdb.New(t))
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, &prompt.Prompt{
		Name: "op-global", Content: "x", Scope: prompt.ScopeGlobal,
		Source: prompt.SourceOperator, Status: prompt.StatusApproved, Enabled: true,
	}))
	require.NoError(t, store.Create(ctx, &prompt.Prompt{
		Name: "sys-global", Content: "y", Scope: prompt.ScopeGlobal,
		Source: prompt.SourceSystem, Status: prompt.StatusApproved, Enabled: true,
	}))

	names := func(ps []prompt.Prompt) []string {
		out := make([]string, 0, len(ps))
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return out
	}

	excl, err := store.List(ctx, prompt.ListFilter{ExcludeSource: prompt.SourceSystem})
	require.NoError(t, err)
	assert.Contains(t, names(excl), "op-global")
	assert.NotContains(t, names(excl), "sys-global")

	only, err := store.List(ctx, prompt.ListFilter{Source: prompt.SourceSystem})
	require.NoError(t, err)
	assert.Equal(t, []string{"sys-global"}, names(only))
}

// TestStore_SearchLexical_RealDB_Differentiates is the #587 regression for
// prompt search: lexical ranking must differentiate two single-match prompts
// (exact short match outranking a long single-mention) rather than collapsing
// both to the flat weight-D 0.1. Fails without the ts_rank_cd normalization.
func TestStore_SearchLexical_RealDB_Differentiates(t *testing.T) {
	store := New(testdb.New(t))
	ctx := context.Background()

	exact := &prompt.Prompt{
		Name: "exact-prompt", Content: "revenue", Scope: prompt.ScopeGlobal,
		Status: prompt.StatusApproved, Enabled: true, Source: prompt.SourceOperator,
	}
	long := &prompt.Prompt{
		Name:    "long-prompt",
		Content: "Quarterly revenue grew across every region this year compared with the prior period.",
		Scope:   prompt.ScopeGlobal, Status: prompt.StatusApproved, Enabled: true, Source: prompt.SourceOperator,
	}
	require.NoError(t, store.Create(ctx, exact))
	require.NoError(t, store.Create(ctx, long))

	// No embedding -> lexical path.
	results, err := store.Search(ctx, prompt.SearchQuery{QueryText: "revenue", Limit: 10})
	require.NoError(t, err)

	scores := map[string]float64{}
	for _, r := range results {
		scores[r.Prompt.Name] = r.Score
	}
	require.Contains(t, scores, exact.Name)
	require.Contains(t, scores, long.Name)

	// Differentiation, not direction: prompt_fts is a weighted multi-column
	// function, so which of the two ranks higher depends on its weighting. What
	// the normalization guarantees is that two single-match records of different
	// lengths get clearly different scores instead of both collapsing to the
	// flat weight-D value. Assert a clear margin regardless of order.
	hi, lo := scores[exact.Name], scores[long.Name]
	if lo > hi {
		hi, lo = lo, hi
	}
	assert.Greater(t, hi, 1.2*lo,
		"single-match records of different lengths must get clearly different scores, not a flat value")
	for name, s := range scores {
		assert.Greater(t, s, 0.0, "score for %s must be positive", name)
		assert.Less(t, s, 1.0, "score for %s must be < 1", name)
	}
}

// TestStore_Update_RealDB_NilTags guards the same defect on the Update path:
// an edit that does not set tags must not bind NULL into the NOT NULL column.
func TestStore_Update_RealDB_NilTags(t *testing.T) {
	store := New(testdb.New(t))
	ctx := context.Background()

	p := &prompt.Prompt{
		Name:    "rt-update",
		Content: "Original body.",
		Scope:   prompt.ScopeGlobal,
		Tags:    []string{"keep"},
		Source:  prompt.SourceOperator,
		Enabled: true,
	}
	require.NoError(t, store.Create(ctx, p))

	// Simulate an edit that arrives with nil tags (e.g. a metadata-only change).
	p.Content = "Edited body."
	p.Tags = nil
	require.NoError(t, store.Update(ctx, p), "update with nil tags")

	got, err := store.GetByID(ctx, p.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Edited body.", got.Content)
	assert.NotNil(t, got.Tags)
	assert.Empty(t, got.Tags)
}
