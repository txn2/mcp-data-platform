package libraryapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/library"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalpurge"
)

var errBoom = errors.New("boom")

type fakeAssets struct {
	portaldomain.AssetStore
	byID map[string]*portaldomain.Asset
}

func (f fakeAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if a, ok := f.byID[id]; ok {
		return a, nil
	}
	return nil, errors.New("not found")
}

type fakeCollections struct {
	portaldomain.CollectionStore
	byID map[string]*portaldomain.Collection
}

func (f fakeCollections) Get(_ context.Context, id string) (*portaldomain.Collection, error) {
	if c, ok := f.byID[id]; ok {
		return c, nil
	}
	return nil, errors.New("not found")
}

// fakeShares shares asset "shared" with u2 as a viewer and nothing else.
type fakeShares struct{ portaldomain.ShareStore }

func (fakeShares) ListByAsset(_ context.Context, assetID string) ([]portaldomain.Share, error) {
	if assetID == "shared" {
		return []portaldomain.Share{{AssetID: "shared", SharedWithUserID: "u2", Permission: portaldomain.PermissionViewer}}, nil
	}
	return nil, nil
}

func (fakeShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

func (fakeShares) GetUserCollectionPermission(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

type fakeLibrary struct {
	err        error
	opened     []string
	starred    []string
	unstarred  []string
	restored   []string
	dismissed  []string
	scope      library.Scope
	since      time.Time
	retention  time.Duration
	userKey    string
	items      []library.Item
	restoreErr error
}

func (f *fakeLibrary) RecordOpen(_ context.Context, user string, kind library.Kind, id string) error {
	f.userKey = user
	f.opened = append(f.opened, string(kind)+":"+id)
	return f.err
}

func (f *fakeLibrary) Recents(_ context.Context, user string, _ *portaldomain.Reach, _ int) ([]library.Item, error) {
	f.userKey = user
	return f.items, f.err
}

func (f *fakeLibrary) Star(_ context.Context, _ string, kind library.Kind, id string) error {
	f.starred = append(f.starred, string(kind)+":"+id)
	return f.err
}

func (f *fakeLibrary) Unstar(_ context.Context, _ string, kind library.Kind, id string) error {
	f.unstarred = append(f.unstarred, string(kind)+":"+id)
	return f.err
}

func (f *fakeLibrary) Favorites(_ context.Context, _ string, _ *portaldomain.Reach, _ int) ([]library.Item, error) {
	return f.items, f.err
}

func (f *fakeLibrary) Trash(_ context.Context, scope library.Scope, retention time.Duration, _, _ int) (library.Page[library.TrashItem], error) {
	f.scope, f.retention = scope, retention
	return library.Page[library.TrashItem]{Items: []library.TrashItem{{Kind: library.KindAsset, ID: "gone"}}, Total: 1}, f.err
}

func (f *fakeLibrary) Restore(_ context.Context, kind library.Kind, id string) error {
	f.restored = append(f.restored, string(kind)+":"+id)
	return f.restoreErr
}

func (f *fakeLibrary) Unused(_ context.Context, scope library.Scope, since time.Time, _, _ int) (library.Page[library.UnusedItem], error) {
	f.scope, f.since = scope, since
	return library.Page[library.UnusedItem]{Items: []library.UnusedItem{}}, f.err
}

func (f *fakeLibrary) Duplicates(_ context.Context, _ string, scope library.Scope) ([]library.DuplicateGroup, error) {
	f.scope = scope
	return []library.DuplicateGroup{}, f.err
}

func (f *fakeLibrary) Dismiss(_ context.Context, _, digest string) error {
	f.dismissed = append(f.dismissed, "+"+digest)
	return f.err
}

func (f *fakeLibrary) Undismiss(_ context.Context, _, digest string) error {
	f.dismissed = append(f.dismissed, "-"+digest)
	return f.err
}

type fakePurger struct {
	err    error
	purged []string
}

func (f *fakePurger) PurgeAsset(_ context.Context, id string) error {
	f.purged = append(f.purged, "asset:"+id)
	return f.err
}

func (f *fakePurger) PurgeCollection(_ context.Context, id string) error {
	f.purged = append(f.purged, "collection:"+id)
	return f.err
}

var (
	owner = &access.User{UserID: "u1", Email: "owner@example.com"}
	other = &access.User{UserID: "u2", Email: "other@example.com"}
	admin = &access.User{UserID: "root", Email: "root@example.com", Roles: []string{"admin"}}
)

func newServer(t *testing.T, lib *fakeLibrary, purger Purger) *http.ServeMux {
	t.Helper()
	deleted := time.Now()
	assets := fakeAssets{byID: map[string]*portaldomain.Asset{
		"mine":   {ID: "mine", OwnerID: "u1", OwnerEmail: "owner@example.com"},
		"shared": {ID: "shared", OwnerID: "u1", OwnerEmail: "owner@example.com"},
		"gone":   {ID: "gone", OwnerID: "u1", OwnerEmail: "owner@example.com", DeletedAt: &deleted},
	}}
	colls := fakeCollections{byID: map[string]*portaldomain.Collection{
		"c1":     {ID: "c1", OwnerID: "u1"},
		"c-gone": {ID: "c-gone", OwnerID: "u1", DeletedAt: &deleted},
	}}
	checker := access.New(access.Config{
		Assets: assets, Collections: colls, Shares: fakeShares{},
		AdminRule: func(roles []string) bool { return len(roles) > 0 && roles[0] == "admin" },
	})
	mux := http.NewServeMux()
	Mount(Config{
		Library: lib, Assets: assets, Collections: colls, Purger: purger, Retention: 30 * 24 * time.Hour,
		Now: func() time.Time { return time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC) },
	})(mux, checker)
	return mux
}

func call(mux *http.ServeMux, user *access.User, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if user != nil {
		req = req.WithContext(access.ContextWithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRecentsAndFavoritesLists(t *testing.T) {
	lib := &fakeLibrary{items: []library.Item{{Kind: library.KindAsset, ID: "mine", Name: "Report"}}}
	mux := newServer(t, lib, nil)

	rec := call(mux, owner, http.MethodGet, "/api/v1/portal/recents?limit=5", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var got ItemsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 1)
	assert.Equal(t, "u1", lib.userKey)

	assert.Equal(t, http.StatusOK, call(mux, owner, http.MethodGet, "/api/v1/portal/favorites", "").Code)
	assert.Equal(t, http.StatusUnauthorized, call(mux, nil, http.MethodGet, "/api/v1/portal/recents", "").Code)

	lib.err = errBoom
	assert.Equal(t, http.StatusInternalServerError, call(mux, owner, http.MethodGet, "/api/v1/portal/favorites", "").Code)
}

// An open is recorded only for an item the caller can open, and an item they
// cannot open answers as a missing one does.
func TestRecordOpen(t *testing.T) {
	lib := &fakeLibrary{}
	mux := newServer(t, lib, nil)

	assert.Equal(t, http.StatusNoContent, call(mux, other, http.MethodPost, "/api/v1/portal/recents", `{"kind":"asset","id":"shared"}`).Code)
	assert.Equal(t, http.StatusNoContent, call(mux, owner, http.MethodPost, "/api/v1/portal/recents", `{"kind":"collection","id":"c1"}`).Code)
	assert.Equal(t, []string{"asset:shared", "collection:c1"}, lib.opened)

	assert.Equal(t, http.StatusNotFound, call(mux, other, http.MethodPost, "/api/v1/portal/recents", `{"kind":"asset","id":"mine"}`).Code)
	assert.Equal(t, http.StatusNotFound, call(mux, other, http.MethodPost, "/api/v1/portal/recents", `{"kind":"asset","id":"nope"}`).Code)
	assert.Equal(t, http.StatusNotFound, call(mux, owner, http.MethodPost, "/api/v1/portal/recents", `{"kind":"asset","id":"gone"}`).Code)
	assert.Equal(t, http.StatusNotFound, call(mux, other, http.MethodPost, "/api/v1/portal/recents", `{"kind":"collection","id":"c1"}`).Code)
	assert.Equal(t, http.StatusBadRequest, call(mux, owner, http.MethodPost, "/api/v1/portal/recents", `{"kind":"prompt","id":"p"}`).Code)
	assert.Equal(t, http.StatusBadRequest, call(mux, owner, http.MethodPost, "/api/v1/portal/recents", `not json`).Code)
	assert.Equal(t, http.StatusUnauthorized, call(mux, nil, http.MethodPost, "/api/v1/portal/recents", `{"kind":"asset","id":"mine"}`).Code)
	assert.Len(t, lib.opened, 2)

	lib.err = errBoom
	assert.Equal(t, http.StatusInternalServerError, call(mux, owner, http.MethodPost, "/api/v1/portal/recents", `{"kind":"asset","id":"mine"}`).Code)
}

func TestStarAndUnstar(t *testing.T) {
	lib := &fakeLibrary{}
	mux := newServer(t, lib, nil)

	assert.Equal(t, http.StatusNoContent, call(mux, other, http.MethodPut, "/api/v1/portal/favorites/asset/shared", "").Code)
	assert.Equal(t, http.StatusNotFound, call(mux, other, http.MethodPut, "/api/v1/portal/favorites/asset/mine", "").Code)
	assert.Equal(t, http.StatusBadRequest, call(mux, other, http.MethodPut, "/api/v1/portal/favorites/view/v1", "").Code)
	assert.Equal(t, []string{"asset:shared"}, lib.starred)

	assert.Equal(t, http.StatusNoContent, call(mux, other, http.MethodDelete, "/api/v1/portal/favorites/asset/mine", "").Code)
	assert.Equal(t, http.StatusBadRequest, call(mux, other, http.MethodDelete, "/api/v1/portal/favorites/view/v1", "").Code)
	assert.Equal(t, http.StatusUnauthorized, call(mux, nil, http.MethodDelete, "/api/v1/portal/favorites/asset/mine", "").Code)
	assert.Equal(t, []string{"asset:mine"}, lib.unstarred)

	lib.err = errBoom
	assert.Equal(t, http.StatusInternalServerError, call(mux, owner, http.MethodPut, "/api/v1/portal/favorites/asset/mine", "").Code)
	assert.Equal(t, http.StatusInternalServerError, call(mux, owner, http.MethodDelete, "/api/v1/portal/favorites/asset/mine", "").Code)
}

// Everyone's lists are an administrator's alone.
func TestOwnerListsScope(t *testing.T) {
	lib := &fakeLibrary{}
	mux := newServer(t, lib, nil)

	rec := call(mux, owner, http.MethodGet, "/api/v1/portal/trash", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, lib.scope.Everyone)
	assert.Equal(t, "u1", lib.scope.Owner.UserID)
	var trash TrashResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &trash))
	assert.Equal(t, 30, trash.RetentionDays)
	assert.Equal(t, 1, trash.Total)

	assert.Equal(t, http.StatusForbidden, call(mux, owner, http.MethodGet, "/api/v1/portal/trash?everyone=true", "").Code)
	assert.Equal(t, http.StatusForbidden, call(mux, owner, http.MethodGet, "/api/v1/portal/unused?everyone=true", "").Code)
	assert.Equal(t, http.StatusForbidden, call(mux, owner, http.MethodGet, "/api/v1/portal/duplicates?everyone=1", "").Code)
	assert.Equal(t, http.StatusUnauthorized, call(mux, nil, http.MethodGet, "/api/v1/portal/trash", "").Code)

	assert.Equal(t, http.StatusOK, call(mux, admin, http.MethodGet, "/api/v1/portal/duplicates?everyone=true", "").Code)
	assert.True(t, lib.scope.Everyone)

	lib.err = errBoom
	assert.Equal(t, http.StatusInternalServerError, call(mux, owner, http.MethodGet, "/api/v1/portal/trash", "").Code)
	assert.Equal(t, http.StatusInternalServerError, call(mux, owner, http.MethodGet, "/api/v1/portal/duplicates", "").Code)
}

func TestUnusedPeriods(t *testing.T) {
	lib := &fakeLibrary{}
	mux := newServer(t, lib, nil)

	rec := call(mux, owner, http.MethodGet, "/api/v1/portal/unused", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC), lib.since, "90 days by default")
	var got UnusedResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 90, got.Days)

	assert.Equal(t, http.StatusOK, call(mux, owner, http.MethodGet, "/api/v1/portal/unused?days=30", "").Code)
	assert.Equal(t, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), lib.since)
	assert.Equal(t, http.StatusBadRequest, call(mux, owner, http.MethodGet, "/api/v1/portal/unused?days=7", "").Code)

	lib.err = errBoom
	assert.Equal(t, http.StatusInternalServerError, call(mux, owner, http.MethodGet, "/api/v1/portal/unused", "").Code)
}

// Restore and Delete permanently are the owner's or an administrator's; an
// item someone else owns answers as one that is not in the trash.
func TestRestoreAndPurge(t *testing.T) {
	lib, purger := &fakeLibrary{}, &fakePurger{}
	mux := newServer(t, lib, purger)

	assert.Equal(t, http.StatusOK, call(mux, owner, http.MethodPost, "/api/v1/portal/trash/asset/gone/restore", "").Code)
	assert.Equal(t, http.StatusOK, call(mux, admin, http.MethodPost, "/api/v1/portal/trash/collection/c-gone/restore", "").Code)
	assert.Equal(t, http.StatusNotFound, call(mux, other, http.MethodPost, "/api/v1/portal/trash/asset/gone/restore", "").Code)
	assert.Equal(t, http.StatusNotFound, call(mux, owner, http.MethodPost, "/api/v1/portal/trash/asset/mine/restore", "").Code, "a live asset is not in the trash")
	assert.Equal(t, http.StatusNotFound, call(mux, owner, http.MethodPost, "/api/v1/portal/trash/collection/c1/restore", "").Code)
	assert.Equal(t, http.StatusBadRequest, call(mux, owner, http.MethodPost, "/api/v1/portal/trash/view/v/restore", "").Code)
	assert.Equal(t, http.StatusUnauthorized, call(mux, nil, http.MethodPost, "/api/v1/portal/trash/asset/gone/restore", "").Code)
	assert.Equal(t, []string{"asset:gone", "collection:c-gone"}, lib.restored)

	lib.restoreErr = library.ErrNotDeleted
	assert.Equal(t, http.StatusNotFound, call(mux, owner, http.MethodPost, "/api/v1/portal/trash/asset/gone/restore", "").Code)
	lib.restoreErr = errBoom
	assert.Equal(t, http.StatusInternalServerError, call(mux, owner, http.MethodPost, "/api/v1/portal/trash/asset/gone/restore", "").Code)

	assert.Equal(t, http.StatusOK, call(mux, owner, http.MethodDelete, "/api/v1/portal/trash/asset/gone", "").Code)
	assert.Equal(t, http.StatusOK, call(mux, owner, http.MethodDelete, "/api/v1/portal/trash/collection/c-gone", "").Code)
	assert.Equal(t, http.StatusNotFound, call(mux, other, http.MethodDelete, "/api/v1/portal/trash/asset/gone", "").Code)
	assert.Equal(t, []string{"asset:gone", "collection:c-gone"}, purger.purged)

	for err, want := range map[error]int{
		portalpurge.ErrNotInTrash:  http.StatusNotFound,
		portalpurge.ErrObjectsKept: http.StatusServiceUnavailable,
		errBoom:                    http.StatusInternalServerError,
	} {
		purger.err = err
		assert.Equal(t, want, call(mux, owner, http.MethodDelete, "/api/v1/portal/trash/asset/gone", "").Code)
	}
}

func TestPurgeWithoutAPurger(t *testing.T) {
	mux := newServer(t, &fakeLibrary{}, nil)
	assert.Equal(t, http.StatusServiceUnavailable, call(mux, owner, http.MethodDelete, "/api/v1/portal/trash/asset/gone", "").Code)
}

func TestDismissals(t *testing.T) {
	lib := &fakeLibrary{}
	mux := newServer(t, lib, nil)
	digest := strings.Repeat("ab", 32)

	assert.Equal(t, http.StatusNoContent, call(mux, owner, http.MethodPost, "/api/v1/portal/duplicates/dismissals", `{"digest":"`+digest+`"}`).Code)
	assert.Equal(t, http.StatusNoContent, call(mux, owner, http.MethodDelete, "/api/v1/portal/duplicates/dismissals/"+digest, "").Code)
	assert.Equal(t, []string{"+" + digest, "-" + digest}, lib.dismissed)

	assert.Equal(t, http.StatusBadRequest, call(mux, owner, http.MethodPost, "/api/v1/portal/duplicates/dismissals", `{"digest":"XYZ"}`).Code)
	assert.Equal(t, http.StatusBadRequest, call(mux, owner, http.MethodDelete, "/api/v1/portal/duplicates/dismissals/"+strings.Repeat("G", 64), "").Code)
	assert.Equal(t, http.StatusUnauthorized, call(mux, nil, http.MethodPost, "/api/v1/portal/duplicates/dismissals", `{}`).Code)
	assert.Equal(t, http.StatusUnauthorized, call(mux, nil, http.MethodDelete, "/api/v1/portal/duplicates/dismissals/"+digest, "").Code)

	lib.err = errBoom
	assert.Equal(t, http.StatusInternalServerError, call(mux, owner, http.MethodPost, "/api/v1/portal/duplicates/dismissals", `{"digest":"`+digest+`"}`).Code)
	assert.Equal(t, http.StatusInternalServerError, call(mux, owner, http.MethodDelete, "/api/v1/portal/duplicates/dismissals/"+digest, "").Code)
}

// A deployment with no collection store answers a collection as missing.
func TestNoCollectionStore(t *testing.T) {
	api := &API{cfg: Config{}}
	assert.False(t, api.canOpen(context.Background(), owner, library.KindCollection, "c1"))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	assert.False(t, api.managesDeleted(req, owner, library.KindCollection, "c1"))
}

func TestFailIgnoresACanceledRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	fail(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody), errRead, context.Canceled)
	assert.Equal(t, http.StatusOK, rec.Code, "nothing is written for a request the caller abandoned")
}

func TestNowDefaultsToTheClock(t *testing.T) {
	before := time.Now()
	assert.False(t, (&API{}).now().Before(before))
}
