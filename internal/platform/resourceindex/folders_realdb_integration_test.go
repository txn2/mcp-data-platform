//go:build integration

package resourceindex

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// TestFolderGaps_RealDB_ADescriptionWrittenSinceIsOwed is #2111's folder
// entry against PostgreSQL: a folder of unindexed files with a current entry
// is not owed, and becomes owed when its description is written, since the
// queue keeps no second job while one runs and the reconciler is what reads
// the change.
func TestFolderGaps_RealDB_ADescriptionWrittenSinceIsOwed(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO resources (id, scope, scope_id, path, filename, display_name, description,
		mime_type, size_bytes, s3_key, uri, uploader_sub, uploader_email, index_policy)
		VALUES ('rg1', 'global', NULL, 'pipelines/gap', 'a.csv', 'A', 'a', 'text/csv', 1, 'k', 'mcp://global/pipelines/gap/a.csv', 'u', 'u@example.com', 'none')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO resource_folders (scope, scope_id, path, summary_text, embedding, embedding_model)
		VALUES ('global', NULL, 'pipelines/gap', 'Folder pipelines/gap', array_fill(0.01, ARRAY[768])::vector, 'm')`)
	require.NoError(t, err)
	store := NewStore(db)
	key := resource.FolderKey(resource.ScopeFilter{Scope: resource.ScopeGlobal}, "pipelines/gap")

	gaps, err := store.findFolderGaps(ctx, "m")
	require.NoError(t, err)
	assert.NotContains(t, gaps, key, "a current entry is not owed")

	folders, ok := resource.NewPostgresStore(db).(resource.FolderDescriber)
	require.True(t, ok)
	require.NoError(t, folders.DescribeFolder(ctx, resource.ScopeFilter{Scope: resource.ScopeGlobal}, "pipelines/gap", "Daily archive"))
	gaps, err = store.findFolderGaps(ctx, "m")
	require.NoError(t, err)
	assert.Contains(t, gaps, key, "a description written since the entry was embedded is owed")
}

// TestFolderSearch_RealDB_TheWordsOfADescriptionFindTheFolder: a folder whose
// description holds the words searched for is returned though its vector is
// not among the nearest, as a file is by its lexical arm.
func TestFolderSearch_RealDB_TheWordsOfADescriptionFindTheFolder(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	near := "array_fill(0.01, ARRAY[768])::vector"
	far := "(array_fill(0.0, ARRAY[767]) || ARRAY[-1.0]::float4[])::vector"
	for i, row := range []struct{ path, desc, vec string }{
		{"pipelines/near-a", "", near}, {"pipelines/near-b", "", near}, {"pipelines/near-c", "", near},
		{"pipelines/far", "Daily schedule exports of the kxyzbackfill station", far},
	} {
		_, err := db.ExecContext(ctx, `INSERT INTO resource_folders (scope, scope_id, path, description, summary_text, embedding, embedding_model)
			VALUES ('global', NULL, $1, $2, 'Folder ' || $1, `+row.vec+`, 'm')`, row.path, row.desc)
		require.NoError(t, err, i)
	}
	searcher, ok := resource.NewPostgresStore(db).(interface {
		SearchFolders(context.Context, resource.SearchQuery) ([]resource.ScoredFolder, error)
	})
	require.True(t, ok)
	query := make([]float32, 768)
	for i := range query {
		query[i] = 0.01
	}
	got, err := searcher.SearchFolders(ctx, resource.SearchQuery{
		QueryText: "kxyzbackfill", Embedding: query, Limit: 3,
		Scopes: []resource.ScopeFilter{{Scope: resource.ScopeGlobal}},
	})
	require.NoError(t, err)
	paths := make([]string, 0, len(got))
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	assert.Contains(t, paths, "pipelines/far", "the description's words find the folder")
	assert.Equal(t, "pipelines/far", got[0].Path, "a lexical match ranks above vector neighbors that match nothing")
}
