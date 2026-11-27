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

// levelScaleBudget is the most one level of a library of 50,000 folders and
// 100,000 files (#2837) may take to read: the request the Resources page makes
// for each folder it opens. The level holding every file of the library,
// scanned in full, took about 0.2 s locally. The whole tree of that library,
// which the page no longer reads, took 1.6 s locally and 3.3 s on CI, which is
// why the page reads levels; it is logged, not bounded.
const levelScaleBudget = time.Second

// TestFolders_RealDB_LargeLibraryCountsEveryDepth seeds a library of 50,000
// folders and 100,000 files, one day holding 5,000 message folders (the shape
// an imap source's per-message attachment folders reach), and reads its whole
// tree, as the dialogs that offer every folder do.
func TestFolders_RealDB_LargeLibraryCountsEveryDepth(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	seedLargeLibrary(ctx, t, db)

	lib := []ScopeFilter{{Scope: ScopePersona, ScopeID: "admin"}}
	start := time.Now()
	folders, err := store.Folders(ctx, Filter{Scopes: lib})
	took := time.Since(start)
	require.NoError(t, err)
	t.Logf("facets over 50,000 folders and 100,000 files: %d folders in %s", len(folders), took)

	byPath := map[string]int{}
	for _, f := range folders {
		byPath[f.Path] = f.Count
	}
	assert.GreaterOrEqual(t, len(folders), 50_000)
	assert.Equal(t, 100_000, byPath["webhooks"], "a folder counts every file beneath it")
	assert.Equal(t, 10_000, byPath["webhooks/mail/attachments/2026-01-01"], "the day holding 5,000 message folders")
}

// seedLargeLibrary files 100,000 files, two per message folder, in 50,000
// message folders: 5,000 on 2026-01-01 and the rest spread over 300 more days,
// with the folder rows a write records.
func seedLargeLibrary(ctx context.Context, t *testing.T, db execer) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		WITH msg AS (
			SELECT g,
			       'webhooks/mail/attachments/' ||
			       CASE WHEN g <= 5000 THEN '2026-01-01' ELSE to_char(date '2026-01-02' + (g % 300), 'YYYY-MM-DD') END ||
			       '/' || md5(g::text) AS path
			  FROM generate_series(1, 50000) g
		)
		INSERT INTO resources (id, scope, scope_id, path, filename, display_name, description,
		                       mime_type, size_bytes, s3_key, uri, uploader_sub)
		SELECT 'res_' || g || '_' || k, 'persona', 'admin', path, 'file' || k || '.pdf', 'file' || k || '.pdf', '',
		       'application/pdf', 1024, 'k' || g || '_' || k, 'mcp://persona/admin/' || path || '/file' || k || '.pdf', 'system'
		  FROM msg, generate_series(1, 2) k`)
	require.NoError(t, err, "seed the files")
	_, err = db.ExecContext(ctx, `
		INSERT INTO resource_folders (scope, scope_id, path)
		SELECT DISTINCT 'persona', 'admin', array_to_string(parts[1:i], '/')
		  FROM (SELECT DISTINCT string_to_array(path, '/') AS parts FROM resources) p,
		       generate_subscripts(p.parts, 1) i
		ON CONFLICT DO NOTHING`)
	require.NoError(t, err, "seed the folder rows")
	_, err = db.ExecContext(ctx, `ANALYZE resources; ANALYZE resource_folders`)
	require.NoError(t, err)
}

// TestFolderLevel_RealDB_LargeLibraryWithinBudget reads the same library one
// level at a time, as the Resources page does (#2837): each level within a
// second, with counts at every depth and how many
// folders each holds directly.
func TestFolderLevel_RealDB_LargeLibraryWithinBudget(t *testing.T) {
	db := testdb.New(t)
	store, ok := NewPostgresStore(db).(folderLevelStore)
	require.True(t, ok)
	ctx := context.Background()
	seedLargeLibrary(ctx, t, db)
	filter := Filter{Scopes: []ScopeFilter{{Scope: ScopePersona, ScopeID: "admin"}}}

	for _, tc := range []struct {
		parent   string
		children int
		count    int
	}{
		{"", 1, 0},
		{"webhooks", 1, 100_000},
		{"webhooks/mail/attachments", 301, 100_000},
		{"webhooks/mail/attachments/2026-01-01", 5000, 10_000},
	} {
		start := time.Now()
		level, err := store.FolderLevel(ctx, filter, tc.parent)
		took := time.Since(start)
		require.NoError(t, err, tc.parent)
		t.Logf("level %q: %d folders in %s", tc.parent, len(level), took)
		assert.Less(t, took, levelScaleBudget, "one level of a large library, %q", tc.parent)
		if tc.parent == "" {
			require.Len(t, level, tc.children)
			assert.Equal(t, "webhooks", level[0].Path)
			assert.Equal(t, 100_000, level[0].Count)
			continue
		}
		require.Len(t, level, tc.children+1, tc.parent)
		assert.Equal(t, tc.parent, level[0].Path, "the folder itself comes first")
		assert.Equal(t, tc.count, level[0].Count)
		assert.Equal(t, tc.children, *level[0].Subfolders)
	}

	day, err := store.FolderLevel(ctx, filter, "webhooks/mail/attachments/2026-01-01")
	require.NoError(t, err)
	msg := day[1]
	assert.Equal(t, 2, msg.Count, "a message folder holds its two files")
	assert.Equal(t, 0, *msg.Subfolders)
	none, err := store.FolderLevel(ctx, filter, "no/such")
	require.NoError(t, err)
	assert.Empty(t, none)
}
