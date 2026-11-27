//go:build integration

package resource

import (
	"context"
	"testing"

	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestResourceSearch_RealDB_LexicalMatchesAnyWord is #2838's criterion for
// resources and their folders: ranked by words alone, a query no resource (or
// folder) holds every word of finds the ones holding some of them, the one
// holding more first.
func TestResourceSearch_RealDB_LexicalMatchesAnyWord(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	scopes := []ScopeFilter{{Scope: ScopeGlobal}}

	for id, words := range map[string][2]string{
		"res_outbound": {"outbound-orders.md", "Outbound orders handed to a carrier."},
		"res_count":    {"tally.md", "A count per warehouse per day."},
		"res_payroll":  {"payroll.md", "People on the payroll."},
	} {
		require.NoError(t, store.Insert(ctx, Resource{
			ID: id, Scope: ScopeGlobal, Path: "references", Filename: words[0], DisplayName: words[0],
			Description: words[1], MIMEType: "text/markdown", S3Key: id,
			URI: "mcp://global/references/" + words[0], UploaderSub: "sub-1",
		}))
	}
	got, err := store.(Searcher).Search(ctx, SearchQuery{QueryText: "outbound orders count", Scopes: scopes})
	require.NoError(t, err)
	ids := make([]string, 0, len(got))
	for _, s := range got {
		ids = append(ids, s.Resource.ID)
	}
	require.Equal(t, []string{"res_outbound", "res_count"}, ids)

	// A folder is searched only once it has an entry (an embedding).
	vec := make([]float32, 768)
	vec[0] = 1
	for path, desc := range map[string]string{
		"outbound": "Outbound orders handed to a carrier.",
		"tally":    "A count per warehouse per day.",
		"payroll":  "People on the payroll.",
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO resource_folders (scope, scope_id, path, description, embedding) VALUES ('global', NULL, $1, $2, $3)`,
			path, desc, pgvector.NewVector(vec))
		require.NoError(t, err)
	}
	folders, err := store.(interface {
		SearchFolders(context.Context, SearchQuery) ([]ScoredFolder, error)
	}).SearchFolders(ctx, SearchQuery{QueryText: "outbound orders count", Scopes: scopes})
	require.NoError(t, err)
	paths := make([]string, 0, len(folders))
	for _, f := range folders {
		paths = append(paths, f.Path)
	}
	require.Equal(t, []string{"outbound", "tally"}, paths)
}
