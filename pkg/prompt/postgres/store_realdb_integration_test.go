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
