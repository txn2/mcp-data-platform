package resource

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// describingFolders is a FolderStore that also keeps descriptions (#2111).
type describingFolders struct {
	fakeFolders
	desc     map[string]string
	writeErr error
}

func (d *describingFolders) FolderDescription(_ context.Context, _ ScopeFilter, path string) (string, error) {
	desc, ok := d.desc[path]
	if !ok {
		return "", ErrFolderEmpty
	}
	return desc, nil
}

func (d *describingFolders) DescribeFolder(_ context.Context, _ ScopeFilter, path, desc string) error {
	if d.writeErr != nil {
		return d.writeErr
	}
	if _, ok := d.desc[path]; !ok {
		return ErrFolderEmpty
	}
	d.desc[path] = desc
	return nil
}

func describeRoutes(t *testing.T, claims *Claims, folders FolderStore) *Handler {
	t.Helper()
	deps := Deps{Store: newFolderStore(), URIScheme: "mcp", Folders: folders}
	return NewHandler(deps, func(*http.Request) (*Claims, error) { return claims, nil }, nil)
}

func TestFolderDescribeRoute(t *testing.T) {
	owner := &Claims{Sub: "sub-1", Email: "me@example.com"}
	body := `{"scope":"user","scope_id":"sub-1","path":"data","description":"  Daily station archive  "}`
	cases := map[string]struct {
		claims  *Claims
		folders FolderStore
		body    string
		want    int
	}{
		"described":         {owner, &describingFolders{desc: map[string]string{"data": ""}}, body, http.StatusOK},
		"no such folder":    {owner, &describingFolders{desc: map[string]string{}}, body, http.StatusNotFound},
		"somebody else's":   {&Claims{Sub: "sub-2"}, &describingFolders{desc: map[string]string{"data": ""}}, body, http.StatusForbidden},
		"too long":          {owner, &describingFolders{desc: map[string]string{"data": ""}}, `{"scope":"user","scope_id":"sub-1","path":"data","description":"` + string(bytes.Repeat([]byte("x"), MaxDescriptionLen+1)) + `"}`, http.StatusBadRequest},
		"bad body":          {owner, &describingFolders{}, `{`, http.StatusBadRequest},
		"store keeps none":  {owner, &fakeFolders{}, body, http.StatusServiceUnavailable},
		"the write failing": {owner, &describingFolders{desc: map[string]string{"data": ""}, writeErr: errors.New("boom")}, body, http.StatusInternalServerError},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			describeRoutes(t, tc.claims, tc.folders).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(),
				http.MethodPut, "/api/v1/resources/folders/description", bytes.NewReader([]byte(tc.body))))
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
			if tc.want == http.StatusOK {
				folders, ok := tc.folders.(*describingFolders)
				require.True(t, ok)
				assert.Equal(t, "Daily station archive", folders.desc["data"])
				assert.Contains(t, rec.Body.String(), `"description":"Daily station archive"`)
			}
		})
	}
}

func TestFolderDescriptionRoute(t *testing.T) {
	owner := &Claims{Sub: "sub-1"}
	folders := &describingFolders{desc: map[string]string{"data": "Daily station archive"}}
	get := func(claims *Claims, query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		describeRoutes(t, claims, folders).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(),
			http.MethodGet, "/api/v1/resources/folders/description?"+query, http.NoBody))
		return rec
	}
	rec := get(owner, "scope=user&scope_id=sub-1&path=data")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Daily station archive")
	assert.Equal(t, http.StatusNotFound, get(&Claims{Sub: "sub-2"}, "scope=user&scope_id=sub-1&path=data").Code,
		"a library the caller cannot see holds no folder they can read")
	assert.Equal(t, http.StatusNotFound, get(owner, "scope=user&scope_id=sub-1&path=elsewhere").Code)
	assert.Equal(t, http.StatusBadRequest, get(owner, "scope=user&scope_id=sub-1&path=/x/").Code)
}

func TestPostgresDescribeFolderFeedsTheFoldersEntry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	enq := &fakeEnqueuer{}
	folders := indexjobs.NewProducer(FolderIndexKind)
	folders.Bind(enq)
	store := NewPostgresStore(db, indexjobs.WithAlsoProducer(folders))
	describer, ok := store.(FolderDescriber)
	require.True(t, ok)
	global := ScopeFilter{Scope: ScopeGlobal}

	mock.ExpectExec("UPDATE resource_folders SET description").
		WithArgs("global", "", "pipelines/nltv", "NLTV archive").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, describer.DescribeFolder(context.Background(), global, "pipelines/nltv", "NLTV archive"))
	assert.Equal(t, []indexjobs.Key{{SourceKind: FolderIndexKind, SourceID: "global::pipelines/nltv"}}, enq.keys)

	mock.ExpectExec("UPDATE resource_folders SET description").WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, describer.DescribeFolder(context.Background(), global, "gone", "x"), ErrFolderEmpty)
	mock.ExpectExec("UPDATE resource_folders SET description").WillReturnError(errors.New("db down"))
	assert.Error(t, describer.DescribeFolder(context.Background(), global, "x", "x"))

	mock.ExpectQuery("SELECT description FROM resource_folders").
		WillReturnRows(sqlmock.NewRows([]string{"description"}).AddRow("NLTV archive"))
	desc, err := describer.FolderDescription(context.Background(), global, "pipelines/nltv")
	require.NoError(t, err)
	assert.Equal(t, "NLTV archive", desc)
	mock.ExpectQuery("SELECT description FROM resource_folders").WillReturnRows(sqlmock.NewRows([]string{"description"}))
	_, err = describer.FolderDescription(context.Background(), global, "gone")
	assert.ErrorIs(t, err, ErrFolderEmpty)
	mock.ExpectQuery("SELECT description FROM resource_folders").WillReturnError(errors.New("db down"))
	_, err = describer.FolderDescription(context.Background(), global, "x")
	assert.Error(t, err)
}

func TestSearchFoldersFailsClosedAndRanks(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := NewPostgresStore(db)
	searcher, ok := store.(interface {
		SearchFolders(context.Context, SearchQuery) ([]ScoredFolder, error)
	})
	require.True(t, ok)

	got, err := searcher.SearchFolders(context.Background(), SearchQuery{QueryText: "nltv"})
	require.NoError(t, err)
	assert.Empty(t, got, "no visible library, nothing to rank")

	cols := []string{"scope", "scope_id", "path", "description", "summary_text", "vec_score", "lex_match"}
	mock.ExpectQuery("FROM resource_folders WHERE embedding IS NOT NULL AND .*plainto_tsquery").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("global", "", "a", "", "Folder a", 0.0, true))
	mock.ExpectQuery("UNION ALL").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("global", "", "low", "", "Folder low", 0.1, false).
			AddRow("global", "", "high", "", "Folder high", 0.9, true).
			AddRow("global", "", "low", "", "Folder low", 0.1, true))

	global := []ScopeFilter{{Scope: ScopeGlobal}}
	lex, err := searcher.SearchFolders(context.Background(), SearchQuery{QueryText: "a", Scopes: global})
	require.NoError(t, err)
	require.Len(t, lex, 1)
	ranked, err := searcher.SearchFolders(context.Background(), SearchQuery{QueryText: "x", Embedding: []float32{1}, Scopes: global})
	require.NoError(t, err)
	require.Len(t, ranked, 2, "a folder in both arms is one hit")
	assert.Equal(t, "high", ranked[0].Path)
	assert.Greater(t, ranked[1].Score, fuseHybridScore(0.1, false), "it keeps the score its lexical match gives it")

	mock.ExpectQuery("FROM resource_folders").WillReturnError(errors.New("db down"))
	_, err = searcher.SearchFolders(context.Background(), SearchQuery{QueryText: "x", Scopes: global})
	assert.Error(t, err)
}
