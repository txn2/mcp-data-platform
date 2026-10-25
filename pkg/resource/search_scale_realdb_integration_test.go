//go:build integration

package resource

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// scaleSearchBudget is the most the ranked search statement may take over the
// seeded library. The search provider's bound is 5 s (knowledge's
// defaultProviderSearchTimeout); a statement that recomputes the full-text
// expression for every matching row took 6.1 s on this shape, and reading the
// stored column takes about a tenth of a second.
const scaleSearchBudget = 2 * time.Second

// TestResourceSearch_RealDB_CommonWordWithinBudget seeds a 30,000-file library
// in which one word is in the name, folder and tags of half the files, each
// file carrying 4 KiB of extracted content, and ranks that word through both
// statements the search runs: lexical alone, and hybrid with a query vector
// (#2292). The word that names the most files is the query that failed.
func TestResourceSearch_RealDB_CommonWordWithinBudget(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `
		INSERT INTO resources (id, scope, scope_id, path, filename, display_name, description,
		                       mime_type, size_bytes, s3_key, uri, tags, uploader_sub, content_text)
		SELECT 'res_' || g,
		       CASE WHEN g % 3 = 0 THEN 'global' ELSE 'user' END,
		       CASE WHEN g % 3 = 0 THEN NULL ELSE 'sub-1' END,
		       CASE WHEN g % 2 = 0 THEN 'pipeline/nielsen/' || (g % 50) ELSE 'ledger/' || (g % 50) END,
		       '2022-03-' || g || '_OO_KCET.csv',
		       CASE WHEN g % 2 = 0 THEN 'nielsen ratings ' || g ELSE 'quarterly ledger ' || g END,
		       'extract ' || g,
		       'text/csv', 4096, 'k' || g, 'mcp://res/' || g,
		       CASE WHEN g % 2 = 0 THEN ARRAY['nielsen', 'nltv'] ELSE ARRAY['finance'] END,
		       'sub-1',
		       left(repeat('station household viewers market share weekday daypart ' || g || ' ', 80), 4096)
		  FROM generate_series(1, 30000) g`)
	require.NoError(t, err, "seed the library")
	_, err = db.ExecContext(ctx, `ANALYZE resources`)
	require.NoError(t, err)

	scopes := []ScopeFilter{{Scope: ScopeGlobal}, {Scope: ScopeUser, ScopeID: "sub-1"}}
	embedding := make([]float32, 768)
	embedding[0] = 1

	for _, tc := range []struct {
		name      string
		embedding []float32
	}{
		{name: "lexical", embedding: nil},
		{name: "hybrid", embedding: embedding},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			got, err := store.(Searcher).Search(ctx, SearchQuery{
				QueryText: "nielsen", Embedding: tc.embedding, Scopes: scopes, Limit: DefaultSearchLimit,
			})
			elapsed := time.Since(start)
			require.NoError(t, err)
			require.Len(t, got, DefaultSearchLimit, "the word matches 15,000 files")
			for _, hit := range got {
				assert.Contains(t, hit.Resource.Tags, "nielsen", "%s is a match", hit.Resource.ID)
			}
			assert.Less(t, elapsed, scaleSearchBudget, "ranking a word in half the library")
		})
	}
}
