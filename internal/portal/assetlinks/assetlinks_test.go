package assetlinks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

func reach() *portaldomain.Reach {
	return portaldomain.NewReach("u-ana", "ana@example.com", "analyst", portaldomain.ScopeAll)
}

func TestReplaceRewritesTheLinks(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteLinksSQL)).WithArgs("a1").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_links")).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	require.NoError(t, store.Replace(context.Background(), "a1", []string{"b", "c"}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReplaceWithNoLinksOnlyClears(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteLinksSQL)).WithArgs("a1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, store.Replace(context.Background(), "a1", nil))
	require.NoError(t, mock.ExpectationsWereMet(), "a version that links nothing removes what the last one linked")
}

func TestReplaceReportsEachFailedStep(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("no conn"))
	assert.ErrorContains(t, store.Replace(context.Background(), "a1", nil), "starting transaction")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteLinksSQL)).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	assert.ErrorContains(t, store.Replace(context.Background(), "a1", nil), "clearing links")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteLinksSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO portal_asset_links")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	assert.ErrorContains(t, store.Replace(context.Background(), "a1", []string{"b"}), "recording links")

	store, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(deleteLinksSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit().WillReturnError(errors.New("commit"))
	assert.ErrorContains(t, store.Replace(context.Background(), "a1", nil), "committing links")
}

func TestLinksFrom(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(linksFromSQL)).WithArgs("a1").
		WillReturnRows(sqlmock.NewRows([]string{"to_asset_id"}).AddRow("b").AddRow("c"))
	ids, err := store.LinksFrom(context.Background(), "a1")
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "c"}, ids)

	mock.ExpectQuery(regexp.QuoteMeta(linksFromSQL)).WithArgs("a2").WillReturnRows(sqlmock.NewRows([]string{"to_asset_id"}))
	ids, err = store.LinksFrom(context.Background(), "a2")
	require.NoError(t, err)
	assert.NotNil(t, ids, "an asset linking nothing reads an empty list, not null")

	mock.ExpectQuery(regexp.QuoteMeta(linksFromSQL)).WillReturnError(errors.New("boom"))
	_, err = store.LinksFrom(context.Background(), "a3")
	assert.ErrorContains(t, err, "reading links")

	mock.ExpectQuery(regexp.QuoteMeta(linksFromSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"to_asset_id", "x"}).AddRow("b", "y"))
	_, err = store.LinksFrom(context.Background(), "a4")
	assert.ErrorContains(t, err, "scanning link")

	mock.ExpectQuery(regexp.QuoteMeta(linksFromSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"to_asset_id"}).AddRow("b").RowError(0, errors.New("row")))
	_, err = store.LinksFrom(context.Background(), "a5")
	assert.ErrorContains(t, err, "iterating links")
}

func TestBuildLinkedFromReadsThroughTheReach(t *testing.T) {
	query, args, err := BuildLinkedFrom("b", reach())
	require.NoError(t, err)
	assert.Contains(t, query, "FROM portal_asset_links l JOIN portal_assets ON portal_assets.id = l.from_asset_id")
	assert.Contains(t, query, "l.to_asset_id = $1")
	assert.Contains(t, query, "portal_assets.deleted_at IS NULL")
	assert.Contains(t, query, "portal_shares", "an asset is listed only through the reader's own reach")
	assert.Contains(t, query, "LIMIT 100")
	assert.Equal(t, "b", args[0])
}

func TestLinkedFrom(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_links l")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "content_type"}).AddRow("a1", "Reports index", "text/markdown"))
	refs, err := store.LinkedFrom(context.Background(), "b", reach())
	require.NoError(t, err)
	assert.Equal(t, []Ref{{ID: "a1", Name: "Reports index", ContentType: "text/markdown"}}, refs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLinkedFromAnUnidentifiedReachReadsNothing(t *testing.T) {
	store, mock := newMock(t)
	refs, err := store.LinkedFrom(context.Background(), "b", portaldomain.NewReach("", "", "", portaldomain.ScopeAll))
	require.NoError(t, err)
	assert.NotNil(t, refs)
	assert.Empty(t, refs)
	require.NoError(t, mock.ExpectationsWereMet(), "no query for a caller who is nobody")
}

func TestLinkedFromReportsFailures(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_links l")).WillReturnError(errors.New("boom"))
	_, err := store.LinkedFrom(context.Background(), "b", reach())
	assert.ErrorContains(t, err, "reading linked from")

	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_links l")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a1"))
	_, err = store.LinkedFrom(context.Background(), "b", reach())
	assert.ErrorContains(t, err, "scanning linked from")

	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_links l")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "content_type"}).AddRow("a1", "n", "t").RowError(0, errors.New("row")))
	_, err = store.LinkedFrom(context.Background(), "b", reach())
	assert.ErrorContains(t, err, "iterating linked from")
}

// --- Indexed ---

type fakeVersions struct {
	portaldomain.VersionStore
	err   error
	calls int
}

func (f *fakeVersions) CreateVersion(context.Context, portaldomain.AssetVersion) (int, error) {
	f.calls++
	return 7, f.err
}

type fakeBlobs struct {
	body  map[string]string
	err   error
	reads int
}

func (f *fakeBlobs) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	f.reads++
	return []byte(f.body[key]), "", f.err
}

type fakeRecorder struct {
	from string
	to   []string
	err  error
	n    int
}

func (f *fakeRecorder) Replace(_ context.Context, from string, to []string) error {
	f.n++
	f.from, f.to = from, to
	return f.err
}

func version(key, contentType string, size int64) portaldomain.AssetVersion {
	return portaldomain.AssetVersion{AssetID: "a1", S3Bucket: "b", S3Key: key, ContentType: contentType, SizeBytes: size}
}

func TestIndexedRecordsTheLinksOfHTMLAndMarkdown(t *testing.T) {
	blobs := &fakeBlobs{body: map[string]string{
		"page.html": `<p><a href="mcp:asset:b1">guide</a> <a href="mcp://global/x.csv">file</a> <img src="mcp:asset:img"></p>`,
		"doc.md":    "See [regional](mcp:asset:b2#top) and [guide](mcp:asset:b1).\n\n```\n[x](mcp:asset:example)\n```\n",
	}}
	for _, tc := range []struct {
		key, ct string
		want    []string
	}{
		{"page.html", "text/html", []string{"b1"}},
		{"doc.md", "text/markdown", []string{"b2", "b1"}},
	} {
		rec := &fakeRecorder{}
		idx := indexedOver(&fakeVersions{}, rec, blobs)
		n, err := idx.CreateVersion(context.Background(), version(tc.key, tc.ct, 100))
		require.NoError(t, err)
		assert.Equal(t, 7, n, "the wrapped store's version number")
		assert.Equal(t, "a1", rec.from)
		assert.Equal(t, tc.want, rec.to, tc.key)
	}
}

func TestIndexedRecordsNothingForTypesThatCannotLinkOrAreTooLarge(t *testing.T) {
	blobs := &fakeBlobs{body: map[string]string{"x": `<a href="mcp:asset:b1">x</a>`}}
	for _, v := range []portaldomain.AssetVersion{
		version("x", "text/csv", 10),
		version("x", "application/json", 10),
		version("x", "text/html", maxIndexedBytes+1),
	} {
		rec := &fakeRecorder{}
		_, err := indexedOver(&fakeVersions{}, rec, blobs).CreateVersion(context.Background(), v)
		require.NoError(t, err)
		assert.Equal(t, 1, rec.n, "the links are still replaced, with none, so an old version's links go")
		assert.Empty(t, rec.to)
	}
	assert.Zero(t, blobs.reads, "nothing is read for a type that cannot link or a document past the bound")
}

func TestIndexedWithNoBlobStoreRecordsNoLinks(t *testing.T) {
	rec := &fakeRecorder{}
	_, err := indexedOver(&fakeVersions{}, rec, nil).CreateVersion(context.Background(), version("x", "text/html", 1))
	require.NoError(t, err)
	assert.Empty(t, rec.to)
}

func TestIndexedKeepsTheVersionWhenLinksCannotBeRecorded(t *testing.T) {
	rec := &fakeRecorder{err: errors.New("db down")}
	blobs := &fakeBlobs{body: map[string]string{"x": `<a href="mcp:asset:b1">x</a>`}}
	n, err := indexedOver(&fakeVersions{}, rec, blobs).CreateVersion(context.Background(), version("x", "text/html", 10))
	require.NoError(t, err, "a link list that failed to record does not fail the write")
	assert.Equal(t, 7, n)

	rec = &fakeRecorder{}
	blobs.err = errors.New("s3 down")
	_, err = indexedOver(&fakeVersions{}, rec, blobs).CreateVersion(context.Background(), version("x", "text/html", 10))
	require.NoError(t, err)
	assert.Empty(t, rec.to, "unreadable content records no links")
}

func TestIndexedRecordsNothingWhenTheVersionFails(t *testing.T) {
	rec := &fakeRecorder{}
	versions := &fakeVersions{err: errors.New("conflict")}
	_, err := indexedOver(versions, rec, &fakeBlobs{}).CreateVersion(context.Background(), version("x", "text/html", 1))
	require.Error(t, err)
	assert.Zero(t, rec.n)
}

func TestLinkable(t *testing.T) {
	for ct, want := range map[string]bool{
		"text/html": true, "application/xhtml+xml": true, "text/jsx": true, "image/svg+xml": true,
		"text/markdown": true, "text/csv": false, "application/json": false, "text/plain": false,
	} {
		assert.Equal(t, want, Linkable(ct), ct)
	}
}

// --- API ---

type fakeAssets struct {
	portaldomain.AssetStore
	rows map[string]*portaldomain.Asset
}

func (f *fakeAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	a, ok := f.rows[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *a
	return &cp, nil
}

type fakeShares struct{ portaldomain.ShareStore }

func (*fakeShares) ListByAsset(context.Context, string) ([]portaldomain.Share, error) {
	return nil, nil
}

func (*fakeShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

func linksRequest(mux *http.ServeMux, email string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets/b/links", http.NoBody)
	if email != "" {
		req = req.WithContext(access.ContextWithUser(req.Context(), &access.User{UserID: "u-" + email, Email: email}))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func linksFixture(t *testing.T) (*http.ServeMux, sqlmock.Sqlmock) {
	t.Helper()
	store, mock := newMock(t)
	assets := &fakeAssets{rows: map[string]*portaldomain.Asset{"b": {ID: "b", OwnerID: "u-ana@example.com", OwnerEmail: "ana@example.com"}}}
	mux := http.NewServeMux()
	Mount(store, assets)(mux, access.New(access.Config{Assets: assets, Shares: &fakeShares{}}))
	return mux, mock
}

func TestLinksRoute(t *testing.T) {
	mux, mock := linksFixture(t)
	mock.ExpectQuery(regexp.QuoteMeta(linksFromSQL)).WithArgs("b").
		WillReturnRows(sqlmock.NewRows([]string{"to_asset_id"}).AddRow("c"))
	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_links l")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "content_type"}).AddRow("a", "Index", "text/markdown"))
	rec := linksRequest(mux, "ana@example.com")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got linksResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []string{"c"}, got.Links)
	assert.Equal(t, []Ref{{ID: "a", Name: "Index", ContentType: "text/markdown"}}, got.LinkedFrom)
}

func TestLinksRouteRefusals(t *testing.T) {
	mux, _ := linksFixture(t)
	assert.Equal(t, http.StatusUnauthorized, linksRequest(mux, "").Code)
	assert.Equal(t, http.StatusNotFound, linksRequest(mux, "stranger@example.com").Code)
}

func TestLinksRouteReadFailures(t *testing.T) {
	mux, mock := linksFixture(t)
	mock.ExpectQuery(regexp.QuoteMeta(linksFromSQL)).WillReturnError(errors.New("boom"))
	rec := linksRequest(mux, "ana@example.com")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.False(t, strings.Contains(rec.Body.String(), "boom"))

	mux, mock = linksFixture(t)
	mock.ExpectQuery(regexp.QuoteMeta(linksFromSQL)).WillReturnRows(sqlmock.NewRows([]string{"to_asset_id"}))
	mock.ExpectQuery(regexp.QuoteMeta("FROM portal_asset_links l")).WillReturnError(errors.New("boom"))
	assert.Equal(t, http.StatusInternalServerError, linksRequest(mux, "ana@example.com").Code)
}
