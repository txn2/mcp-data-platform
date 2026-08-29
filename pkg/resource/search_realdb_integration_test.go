//go:build integration

package resource

// Real-Postgres tests for the managed-resource ranked search (#1012). The
// search's whole value is in SQL that sqlmock cannot evaluate: the resource_fts
// index expression (does a column name buried in a CSV actually match?), the
// scope-visibility predicate (does a user-scoped resource stay invisible to
// another caller?), and the embedding columns the request-path Update clears.
// These run against the real schema.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestResourceSearch_RealDB_FindsColumnNameInsideFile(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	require.NoError(t, store.Insert(ctx, Resource{
		ID: "res_dict", Scope: ScopeGlobal, Path: "references",
		Filename: "sales-dictionary.csv", DisplayName: "Sales Dictionary",
		Description: "Field reference for the sales extract.",
		MIMEType:    "text/csv", SizeBytes: 40, S3Key: "k1",
		URI: "mcp://global/references/sales-dictionary.csv", UploaderSub: "sub-1",
	}))
	// The index consumer's write: the extracted content prefix.
	_, err := db.ExecContext(ctx, `UPDATE resources SET content_text = $2 WHERE id = $1`,
		"res_dict", "column,description\ngross_margin_pct,margin after COGS\n")
	require.NoError(t, err)

	scopes := []ScopeFilter{{Scope: ScopeGlobal}}

	// AC: a column name that appears ONLY inside the file is findable.
	got, err := store.(Searcher).Search(ctx, SearchQuery{QueryText: "gross_margin_pct", Scopes: scopes})
	require.NoError(t, err)
	require.Len(t, got, 1, "content-only term should match through resource_fts")
	assert.Equal(t, "res_dict", got[0].Resource.ID)
	assert.Positive(t, got[0].Score)

	// Metadata still matches too.
	got, err = store.(Searcher).Search(ctx, SearchQuery{QueryText: "sales dictionary", Scopes: scopes})
	require.NoError(t, err)
	require.Len(t, got, 1)

	// A term in neither metadata nor content matches nothing.
	got, err = store.(Searcher).Search(ctx, SearchQuery{QueryText: "kubernetes", Scopes: scopes})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestResourceSearch_RealDB_ScopeEnforcement(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	seed := []Resource{
		{
			ID: "res_g", Scope: ScopeGlobal, Path: "references", Filename: "g.md",
			DisplayName: "Onboarding guide", MIMEType: "text/markdown", S3Key: "kg",
			URI: "mcp://global/references/g.md", UploaderSub: "admin",
		},
		{
			ID: "res_p", Scope: ScopePersona, ScopeID: "analyst", Path: "references", Filename: "p.md",
			DisplayName: "Onboarding analyst playbook", MIMEType: "text/markdown", S3Key: "kp",
			URI: "mcp://persona/analyst/references/p.md", UploaderSub: "admin",
		},
		{
			ID: "res_u", Scope: ScopeUser, ScopeID: "sub-a", Path: "references", Filename: "u.md",
			DisplayName: "Onboarding personal notes", MIMEType: "text/markdown", S3Key: "ku",
			URI: "mcp://user/sub-a/references/u.md", UploaderSub: "sub-a",
		},
	}
	for _, r := range seed {
		require.NoError(t, store.Insert(ctx, r))
	}

	ids := func(scored []ScoredResource) []string {
		out := make([]string, 0, len(scored))
		for _, s := range scored {
			out = append(out, s.Resource.ID)
		}
		return out
	}
	search := func(claims Claims) []string {
		scored, err := store.(Searcher).Search(ctx, SearchQuery{
			QueryText: "onboarding", Scopes: VisibleScopes(claims),
		})
		require.NoError(t, err)
		return ids(scored)
	}

	// The owner + persona member sees all three.
	assert.ElementsMatch(t, []string{"res_g", "res_p", "res_u"},
		search(BuildClaims("sub-a", "a@example.com", "analyst", nil, false)))

	// Another user in the same persona never sees the user-scoped resource.
	assert.ElementsMatch(t, []string{"res_g", "res_p"},
		search(BuildClaims("sub-b", "b@example.com", "analyst", nil, false)))

	// A caller outside the persona sees neither the persona nor the user resource.
	assert.ElementsMatch(t, []string{"res_g"},
		search(BuildClaims("sub-b", "b@example.com", "engineer", nil, false)))

	// An identity-less caller sees only the global resource.
	assert.ElementsMatch(t, []string{"res_g"}, search(Claims{}))
}

func TestResourceSearch_RealDB_UpdateClearsEmbedding(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	require.NoError(t, store.Insert(ctx, Resource{
		ID: "res_e", Scope: ScopeGlobal, Path: "references", Filename: "e.md",
		DisplayName: "Edited", MIMEType: "text/markdown", S3Key: "ke",
		URI: "mcp://global/references/e.md", UploaderSub: "sub-1",
	}))
	_, err := db.ExecContext(ctx,
		`UPDATE resources SET embedding = $2, embedding_model = $3, embedding_text_hash = $4 WHERE id = $1`,
		"res_e", vectorLiteral(768), "test-model", []byte("hash"))
	require.NoError(t, err)

	desc := "a new description"
	require.NoError(t, store.Update(ctx, "res_e", Update{Description: &desc}))

	var embeddingNull bool
	var model string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT embedding IS NULL, embedding_model FROM resources WHERE id = $1`, "res_e").
		Scan(&embeddingNull, &model))
	assert.True(t, embeddingNull, "a metadata edit must clear the vector so the reconciler re-embeds")
	assert.Empty(t, model)
}

// vectorLiteral builds a pgvector literal of the schema's dimensionality. The
// components are non-zero on purpose: cosine distance is undefined for a zero
// vector, so a zero-filled fixture yields NaN scores and tests nothing.
func vectorLiteral(dim int) string {
	parts := make([]string, dim)
	for i := range parts {
		parts[i] = "0.1"
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// unitQueryVector is the query-side counterpart of vectorLiteral: the same
// direction, so a stored row scores a cosine similarity of 1.
func unitQueryVector(dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = 0.1
	}
	return v
}

// The hybrid arm is the default production path (any deployment with an
// embedding provider), and sqlmock cannot evaluate SQL: it neither parses the
// UNION nor checks that the scope placeholders line up behind the vector and
// text parameters. This runs the real statement against real pgvector.
func TestResourceSearch_RealDB_HybridArm(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	seed := []Resource{
		{
			ID: "res_h1", Scope: ScopeGlobal, Path: "references", Filename: "h1.md",
			DisplayName: "Margin definitions", MIMEType: "text/markdown", S3Key: "k1",
			URI: "mcp://global/references/h1.md", UploaderSub: "sub-1",
		},
		{
			ID: "res_h2", Scope: ScopeUser, ScopeID: "sub-other", Path: "references", Filename: "h2.md",
			DisplayName: "Margin definitions (private)", MIMEType: "text/markdown", S3Key: "k2",
			URI: "mcp://user/sub-other/references/h2.md", UploaderSub: "sub-other",
		},
	}
	for _, r := range seed {
		require.NoError(t, store.Insert(ctx, r))
	}
	// Both rows carry a vector, so the ANN arm can see them; only the scope
	// predicate should keep the private one out.
	for _, id := range []string{"res_h1", "res_h2"} {
		_, err := db.ExecContext(ctx,
			`UPDATE resources SET embedding = $2, embedding_model = 'test' WHERE id = $1`, id, vectorLiteral(768))
		require.NoError(t, err)
	}

	scored, err := store.(Searcher).Search(ctx, SearchQuery{
		Embedding: unitQueryVector(768), //nolint:mnd // the schema's vector dimensionality
		QueryText: "margin definitions",
		Scopes:    []ScopeFilter{{Scope: ScopeGlobal}},
	})
	require.NoError(t, err, "the hybrid UNION must execute against real Postgres")
	require.Len(t, scored, 1, "the scope predicate must apply to BOTH arms")
	assert.Equal(t, "res_h1", scored[0].Resource.ID)
	assert.InDelta(t, 1.0, scored[0].Score, 0.001, "a row matched by both arms fuses to the top score")
}

// The no-ghost-hit guarantee is a schema property: the index lives on the
// resource row, so DELETE takes it with it. A fake store cannot establish that —
// it would pass just as well if the vector lived in a table nothing cleans up.
func TestResourceSearch_RealDB_DeleteRemovesTheIndexEntry(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	require.NoError(t, store.Insert(ctx, Resource{
		ID: "res_del", Scope: ScopeGlobal, Path: "references", Filename: "d.csv",
		DisplayName: "Doomed dictionary", MIMEType: "text/csv", S3Key: "kd",
		URI: "mcp://global/references/d.csv", UploaderSub: "sub-1",
	}))
	_, err := db.ExecContext(ctx,
		`UPDATE resources SET content_text = $2, content_indexed_at = NOW(), embedding = $3, embedding_model = 'test' WHERE id = $1`,
		"res_del", "vantablack_review procedure", vectorLiteral(768))
	require.NoError(t, err)

	scopes := []ScopeFilter{{Scope: ScopeGlobal}}
	found, err := store.(Searcher).Search(ctx, SearchQuery{QueryText: "vantablack_review", Scopes: scopes})
	require.NoError(t, err)
	require.Len(t, found, 1, "precondition: the resource is searchable by its indexed content")

	require.NoError(t, store.Delete(ctx, "res_del"))

	found, err = store.(Searcher).Search(ctx, SearchQuery{QueryText: "vantablack_review", Scopes: scopes})
	require.NoError(t, err)
	assert.Empty(t, found, "a deleted resource must leave no ghost hit")

	var rows int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM resources WHERE embedding IS NOT NULL AND id = $1`, "res_del").Scan(&rows))
	assert.Zero(t, rows, "the vector lives on the row, so the DELETE removed it")
}

// The index consumer's gap query must return a row whose content pass never
// settled, even when its embedding is present and current — that is the whole
// point of the content_indexed_at signal.
func TestResourceIndex_RealDB_UnsettledContentIsAGap(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	for _, r := range []Resource{
		{
			ID: "res_settled", Scope: ScopeGlobal, Path: "r", Filename: "a.md", DisplayName: "A",
			MIMEType: "text/markdown", S3Key: "ka", URI: "mcp://global/r/a.md", UploaderSub: "s",
		},
		{
			ID: "res_owed", Scope: ScopeGlobal, Path: "r", Filename: "b.md", DisplayName: "B",
			MIMEType: "text/markdown", S3Key: "kb", URI: "mcp://global/r/b.md", UploaderSub: "s",
		},
	} {
		require.NoError(t, store.Insert(ctx, r))
	}
	// Both embedded with the current model; only one settled its content pass.
	_, err := db.ExecContext(ctx,
		`UPDATE resources SET embedding = $1, embedding_model = 'test'`, vectorLiteral(768))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`UPDATE resources SET content_indexed_at = NOW() WHERE id = 'res_settled'`)
	require.NoError(t, err)

	var gaps []string
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM resources WHERE embedding IS NULL OR embedding_model IS DISTINCT FROM $1 OR content_indexed_at IS NULL`,
		"test")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		gaps = append(gaps, id)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"res_owed"}, gaps,
		"an embedded row whose content pass never settled must still be a gap")
}
