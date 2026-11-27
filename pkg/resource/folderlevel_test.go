package resource

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// levelOf decodes a facets envelope's folders with their subfolder counts.
func levelOf(t *testing.T, body []byte) []Folder {
	t.Helper()
	var env struct {
		Folders []Folder `json:"folders"`
		Tags    []string `json:"tags"`
	}
	require.NoError(t, json.Unmarshal(body, &env), string(body))
	require.NotNil(t, env.Tags, "tags is an empty list, never null")
	return env.Folders
}

// TestHandleFacets_OneLevel is #2837's route: with parent, only the folders
// directly inside it, each knowing how many folders it holds, the parent first
// with its own total; a store with no level read of its own is answered from
// its whole tree.
func TestHandleFacets_OneLevel(t *testing.T) {
	store := newMockStore()
	seedIn(store, "a", "data", ScopeFilter{Scope: ScopeGlobal})
	seedIn(store, "b", "data/media-manager", ScopeFilter{Scope: ScopeGlobal})
	seedIn(store, "c", "data/media-manager/shows", ScopeFilter{Scope: ScopeGlobal})
	seedIn(store, "d", "other", ScopeFilter{Scope: ScopeGlobal})
	h := newTestHandler(store, nil, okExtractor)
	get := func(q string) []Folder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/facets?"+q, http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return levelOf(t, rec.Body.Bytes())
	}

	root := get("parent=")
	require.Len(t, root, 2)
	assert.Equal(t, "data", root[0].Path)
	assert.Equal(t, 3, root[0].Count)
	assert.Equal(t, 1, *root[0].Subfolders)
	assert.Equal(t, 0, *root[1].Subfolders)

	data := get("parent=data")
	require.Len(t, data, 2)
	assert.Equal(t, "data", data[0].Path, "the folder itself first")
	assert.Equal(t, 3, data[0].Count)
	assert.Equal(t, "data/media-manager", data[1].Path)
	assert.Equal(t, 2, data[1].Count)
	assert.Equal(t, 1, *data[1].Subfolders)

	assert.Empty(t, get("parent=nowhere"))
}

// TestFolderLevel_Store reads a level's rows: the parent's own row and every
// child row add to the parent's total, the latest change is the parent's, and
// a root level lists no parent.
func TestFolderLevel_Store(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test
	s, ok := NewPostgresStore(db).(folderLevelStore)
	require.True(t, ok, "the PostgreSQL store reads a level")
	ctx := context.Background()
	lib := Filter{Scopes: []ScopeFilter{{Scope: ScopePersona, ScopeID: "admin"}}}
	early, late := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	cols := []string{"child", "count", "updated_at", "subfolders"}

	mock.ExpectQuery(`substr\(path, 7\)`).WithArgs("persona", "admin", "a/day", "a/day/%", "persona", "admin", "a/day", "a/day/%").
		WillReturnRows(sqlmock.NewRows(cols).AddRow("", 1, early, 0).AddRow("m1", 2, late, 0).AddRow("m2", 4, nil, 3))
	got, err := s.FolderLevel(ctx, lib, "a/day")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, Folder{Path: "a/day", Count: 7, UpdatedAt: &late, Subfolders: new(2)}, got[0])
	assert.Equal(t, "a/day/m1", got[1].Path)
	assert.Nil(t, got[2].UpdatedAt)
	assert.Equal(t, 3, *got[2].Subfolders)

	mock.ExpectQuery(`SELECT path AS rest`).WillReturnRows(sqlmock.NewRows(cols).AddRow("webhooks", 9, late, 1))
	got, err = s.FolderLevel(ctx, lib, "")
	require.NoError(t, err)
	require.Len(t, got, 1, "the root lists no parent")
	assert.Equal(t, "webhooks", got[0].Path)

	none, err := s.FolderLevel(ctx, Filter{}, "a")
	require.NoError(t, err)
	assert.Nil(t, none, "no library, no folders")

	mock.ExpectQuery(`FROM resources`).WillReturnError(errors.New("down"))
	_, err = s.FolderLevel(ctx, lib, "a")
	require.Error(t, err)
	mock.ExpectQuery(`FROM resources`).WillReturnRows(sqlmock.NewRows([]string{"child"}).AddRow("x"))
	_, err = s.FolderLevel(ctx, lib, "a")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTreeLevel(t *testing.T) {
	tree := []Folder{{Path: "a", Count: 3}, {Path: "a/b", Count: 2}, {Path: "a/b/c", Count: 1}, {Path: "z", Count: 1}}
	got := treeLevel(tree, "a/b")
	require.Len(t, got, 2)
	assert.Equal(t, "a/b", got[0].Path)
	assert.Equal(t, 1, *got[0].Subfolders)
	assert.Equal(t, 0, *got[1].Subfolders)
	assert.Len(t, treeLevel(tree, ""), 2)
	assert.Empty(t, treeLevel(tree, "q"))
}
