package admin

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

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// --- mock collection store ---

type mockAdminCollectionStore struct {
	getColl   *portal.Collection
	getErr    error
	listRes   []portal.Collection
	listTotal int
	listErr   error
	updateErr error
	deleteErr error
	// getErrAfterUpdate makes the re-read that follows a successful Update
	// fail, which is the only way the handler's 500 on re-read is reachable.
	getErrAfterUpdate bool
	updated           bool
	lastFilter        portal.CollectionFilter
	lastName          string
	lastDesc          string
}

func (*mockAdminCollectionStore) Insert(_ context.Context, _ portal.Collection) error { return nil }

func (m *mockAdminCollectionStore) Get(_ context.Context, _ string) (*portal.Collection, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.updated && m.getErrAfterUpdate {
		return nil, errors.New("collection vanished")
	}
	return m.getColl, nil
}

func (m *mockAdminCollectionStore) List(_ context.Context, f portal.CollectionFilter) (colls []portal.Collection, total int, err error) {
	m.lastFilter = f
	return m.listRes, m.listTotal, m.listErr
}

func (m *mockAdminCollectionStore) Update(_ context.Context, _, name, description string) error {
	m.lastName, m.lastDesc = name, description
	m.updated = true
	return m.updateErr
}

func (*mockAdminCollectionStore) UpdateConfig(_ context.Context, _ string, _ portal.CollectionConfig) error {
	return nil
}

func (*mockAdminCollectionStore) UpdateThumbnail(_ context.Context, _, _ string) error { return nil }

func (m *mockAdminCollectionStore) SoftDelete(_ context.Context, _ string) error { return m.deleteErr }

func (*mockAdminCollectionStore) SetSections(_ context.Context, _ string, _ []portal.CollectionSection) error {
	return nil
}

// collectionSummaryShareStore answers the collection share-summary lookup with
// a fixed map, which the base admin share mock does not.
type collectionSummaryShareStore struct {
	mockAdminShareStore
	summaries map[string]portal.ShareSummary
	err       error
	// lastSummaryIDs records the IDs the handler asked summaries for.
	lastSummaryIDs []string
}

func (s *collectionSummaryShareStore) ListActiveCollectionShareSummaries(_ context.Context, ids []string) (map[string]portal.ShareSummary, error) {
	s.lastSummaryIDs = ids
	return s.summaries, s.err
}

func newCollectionTestHandler(colls portal.CollectionStore, shares portal.ShareStore) *Handler {
	return NewHandler(Deps{CollectionStore: colls, ShareStore: shares}, nil)
}

func testCollection() *portal.Collection {
	now := time.Now()
	return &portal.Collection{
		ID:         "col-1",
		OwnerID:    "apikey:admin",
		OwnerEmail: "admin@apikey.local",
		Name:       "Agent Documentation",
		Sections: []portal.CollectionSection{
			{ID: "sec-1", CollectionID: "col-1", Title: "Overview"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// --- route registration ---

func TestCollectionRoutesRegistered(t *testing.T) {
	h := newCollectionTestHandler(
		&mockAdminCollectionStore{getColl: testCollection()},
		&mockAdminShareStore{},
	)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/collections"},
		{http.MethodGet, "/api/v1/admin/collections/col-1"},
		{http.MethodPut, "/api/v1/admin/collections/col-1"},
		{http.MethodDelete, "/api/v1/admin/collections/col-1"},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), rt.method, rt.path, http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.NotEqual(t, http.StatusNotFound, w.Code,
				"route %s %s should be registered", rt.method, rt.path)
		})
	}
}

func TestCollectionRoutesNotRegisteredWithoutStore(t *testing.T) {
	h := NewHandler(Deps{}, nil) // no CollectionStore

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/collections", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.True(t, w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed,
		"expected 404/405 without collection store, got %d", w.Code)
}

// --- listAllCollections ---

func TestListAllCollectionsCrossesOwners(t *testing.T) {
	agentOwned := *testCollection()
	humanOwned := portal.Collection{
		ID: "col-2", OwnerID: "u-1", OwnerEmail: "alice@example.com", Name: "Q4",
	}
	colls := &mockAdminCollectionStore{
		listRes:   []portal.Collection{agentOwned, humanOwned},
		listTotal: 2,
	}
	shares := &collectionSummaryShareStore{
		summaries: map[string]portal.ShareSummary{"col-1": {HasPublicLink: true}},
	}
	h := newCollectionTestHandler(colls, shares)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/collections", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp adminCollectionListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// The point of the route: no owner filter reaches the store, so a
	// collection owned by a principal nobody can sign in as is listed.
	assert.Empty(t, colls.lastFilter.OwnerID)
	require.Len(t, resp.Data, 2)
	assert.Equal(t, "admin@apikey.local", resp.Data[0].OwnerEmail)
	assert.Equal(t, 2, resp.Total)
	assert.True(t, resp.ShareSummaries["col-1"].HasPublicLink)
	assert.ElementsMatch(t, []string{"col-1", "col-2"}, shares.lastSummaryIDs)
}

func TestListAllCollectionsPassesQueryParams(t *testing.T) {
	colls := &mockAdminCollectionStore{}
	h := newCollectionTestHandler(colls, &mockAdminShareStore{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/collections?search=alice&limit=5&offset=10&sort=name&dir=asc", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "alice", colls.lastFilter.Search)
	assert.Equal(t, 5, colls.lastFilter.Limit)
	assert.Equal(t, 10, colls.lastFilter.Offset)
	assert.Equal(t, "name", colls.lastFilter.SortBy)
	assert.Equal(t, "ASC", colls.lastFilter.SortDir)

	var resp adminCollectionListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// An empty page answers with [] rather than null, and echoes the page it served.
	assert.Empty(t, resp.Data)
	assert.NotNil(t, resp.Data)
	assert.Equal(t, 5, resp.Limit)
	assert.Equal(t, 10, resp.Offset)
}

func TestListAllCollectionsStoreError(t *testing.T) {
	h := newCollectionTestHandler(
		&mockAdminCollectionStore{listErr: errors.New("db down")},
		&mockAdminShareStore{},
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/collections", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListAllCollectionsSurvivesShareSummaryError(t *testing.T) {
	shares := &collectionSummaryShareStore{err: errors.New("summary lookup failed")}
	h := newCollectionTestHandler(
		&mockAdminCollectionStore{listRes: []portal.Collection{*testCollection()}, listTotal: 1},
		shares,
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/collections", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp adminCollectionListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 1)
	assert.Empty(t, resp.ShareSummaries)
}

// --- getAdminCollection ---

func TestGetAdminCollectionAnyOwner(t *testing.T) {
	h := newCollectionTestHandler(
		&mockAdminCollectionStore{getColl: testCollection()},
		&mockAdminShareStore{},
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/collections/col-1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got portal.Collection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "Agent Documentation", got.Name)
	assert.Equal(t, "admin@apikey.local", got.OwnerEmail)
	// A section with no items serializes as [], not null, so the viewer can map it.
	require.Len(t, got.Sections, 1)
	assert.NotNil(t, got.Sections[0].Items)
	assert.Empty(t, got.Sections[0].Items)
}

func TestGetAdminCollectionSectionsNeverNull(t *testing.T) {
	coll := testCollection()
	coll.Sections = nil
	h := newCollectionTestHandler(&mockAdminCollectionStore{getColl: coll}, &mockAdminShareStore{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/collections/col-1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"sections":[]`)
}

func TestGetAdminCollectionNotFound(t *testing.T) {
	h := newCollectionTestHandler(
		&mockAdminCollectionStore{getErr: errors.New("not found")},
		&mockAdminShareStore{},
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/collections/missing", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAdminCollectionDeleted(t *testing.T) {
	coll := testCollection()
	deletedAt := time.Now()
	coll.DeletedAt = &deletedAt
	h := newCollectionTestHandler(&mockAdminCollectionStore{getColl: coll}, &mockAdminShareStore{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/collections/col-1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

// --- updateAdminCollection ---

func TestUpdateAdminCollectionMergesPartialBody(t *testing.T) {
	colls := &mockAdminCollectionStore{getColl: testCollection()}
	colls.getColl.Description = "kept"
	h := newCollectionTestHandler(colls, &mockAdminShareStore{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/collections/col-1", strings.NewReader(`{"name":"Renamed"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Renamed", colls.lastName)
	// An omitted field keeps the stored value: the store's Update writes both.
	assert.Equal(t, "kept", colls.lastDesc)
}

func TestUpdateAdminCollectionValidatesName(t *testing.T) {
	colls := &mockAdminCollectionStore{getColl: testCollection()}
	h := newCollectionTestHandler(colls, &mockAdminShareStore{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/collections/col-1", strings.NewReader(`{"name":""}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, colls.lastName, "an invalid name must never reach the store")
}

func TestUpdateAdminCollectionRejectsUnknownField(t *testing.T) {
	colls := &mockAdminCollectionStore{getColl: testCollection()}
	h := newCollectionTestHandler(colls, &mockAdminShareStore{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/collections/col-1", strings.NewReader(`{"owner_id":"someone-else"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, colls.lastName)
}

func TestUpdateAdminCollectionNotFound(t *testing.T) {
	h := newCollectionTestHandler(
		&mockAdminCollectionStore{getErr: errors.New("not found")},
		&mockAdminShareStore{},
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/collections/missing", strings.NewReader(`{"name":"x"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateAdminCollectionStoreError(t *testing.T) {
	h := newCollectionTestHandler(
		&mockAdminCollectionStore{getColl: testCollection(), updateErr: errors.New("db down")},
		&mockAdminShareStore{},
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/collections/col-1", strings.NewReader(`{"name":"Renamed"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateAdminCollectionRereadError(t *testing.T) {
	h := newCollectionTestHandler(
		&mockAdminCollectionStore{getColl: testCollection(), getErrAfterUpdate: true},
		&mockAdminShareStore{},
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
		"/api/v1/admin/collections/col-1", strings.NewReader(`{"name":"Renamed"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- deleteAdminCollection ---

func TestDeleteAdminCollection(t *testing.T) {
	h := newCollectionTestHandler(
		&mockAdminCollectionStore{getColl: testCollection()},
		&mockAdminShareStore{},
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/collections/col-1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, statusDeleted, resp.Status)
}

func TestDeleteAdminCollectionAlreadyGone(t *testing.T) {
	h := newCollectionTestHandler(
		&mockAdminCollectionStore{deleteErr: errors.New("not found")},
		&mockAdminShareStore{},
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/collections/col-1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
