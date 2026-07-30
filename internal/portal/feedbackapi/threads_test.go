package feedbackapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// mockThreadStore is a controllable ThreadStore for handler tests.
type mockThreadStore struct {
	created           *threads.Thread
	createErr         error
	listResult        []threads.ThreadWithMeta
	listTotal         int
	listErr           error
	lastListFilter    threads.ThreadFilter
	getResult         *threads.Thread
	getErr            error
	events            []threads.ThreadEvent
	eventsErr         error
	appended          *threads.ThreadEvent
	appendErr         error
	updateErr         error
	lastUpdate        *threads.ThreadUpdate
	deleteErr         error
	counts            map[string]int
	countErr          error
	lastCountIDs      []string
	linkedThreadIDs   []string
	linkedInsightID   string
	linkedReturn      []string
	linkErr           error
	validatedThreadID string
	validateErr       error
	respondedThreadID string
	respondedResult   string
	respondErr        error
	signoffCount      int
	signoffErr        error
	lastCreated       *threads.Thread
	lastFirstEvent    *threads.ThreadEvent
	lastAppended      *threads.ThreadEvent
}

func (m *mockThreadStore) CreateThread(_ context.Context, t threads.Thread, first threads.ThreadEvent) (*threads.Thread, error) {
	m.lastCreated = &t
	m.lastFirstEvent = &first
	if m.createErr != nil {
		return nil, m.createErr
	}
	if m.created != nil {
		return m.created, nil
	}
	return &t, nil
}

func (m *mockThreadStore) ListThreads(_ context.Context, f threads.ThreadFilter) ([]threads.ThreadWithMeta, int, error) {
	m.lastListFilter = f
	return m.listResult, m.listTotal, m.listErr
}

func (m *mockThreadStore) GetThread(_ context.Context, _ string) (*threads.Thread, error) {
	return m.getResult, m.getErr
}

func (m *mockThreadStore) ListEvents(_ context.Context, _ string) ([]threads.ThreadEvent, error) {
	return m.events, m.eventsErr
}

func (m *mockThreadStore) AppendEvent(_ context.Context, e threads.ThreadEvent) (*threads.ThreadEvent, error) {
	m.lastAppended = &e
	if m.appendErr != nil {
		return nil, m.appendErr
	}
	if m.appended != nil {
		return m.appended, nil
	}
	return &e, nil
}

func (m *mockThreadStore) UpdateThread(_ context.Context, _ string, u threads.ThreadUpdate, _, _ string) error {
	m.lastUpdate = &u
	return m.updateErr
}

func (m *mockThreadStore) SoftDeleteThread(_ context.Context, _ string) error { return m.deleteErr }

func (m *mockThreadStore) CountOpenByTargets(_ context.Context, _ string, ids []string) (map[string]int, error) {
	m.lastCountIDs = ids
	return m.counts, m.countErr
}

func (m *mockThreadStore) CountSignoffs(_ context.Context, _, _ string) (int, error) {
	return m.signoffCount, m.signoffErr
}

func (m *mockThreadStore) LinkInsight(_ context.Context, threadIDs []string, insightID, _, _ string) ([]string, error) {
	m.linkedThreadIDs = threadIDs
	m.linkedInsightID = insightID
	if m.linkErr != nil {
		return nil, m.linkErr
	}
	if m.linkedReturn != nil {
		return m.linkedReturn, nil
	}
	return threadIDs, nil
}

func (m *mockThreadStore) RequestValidation(_ context.Context, id, _, _ string) error {
	m.validatedThreadID = id
	return m.validateErr
}

func (m *mockThreadStore) RespondValidation(_ context.Context, id string, resp threads.ValidationResponse, _, _ string) error {
	m.respondedThreadID = id
	m.respondedResult = resp.Result
	return m.respondErr
}

func newThreadTestHandler(store *mockThreadStore, assets *mockAssetStore, shares *mockShareStore, user *access.User) http.Handler {
	return newTestServer(Config{Assets: assets, Shares: shares, Threads: store}, user)
}

func doThreadReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, rdr)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func ownedAsset(owner string) *mockAssetStore {
	return &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: owner}}
}

// --- create ---

func TestCreateAssetThreadAsOwner(t *testing.T) {
	store := &mockThreadStore{}
	h := newThreadTestHandler(store, ownedAsset("u1"), &mockShareStore{}, &access.User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{
		Kind: threads.ThreadKindCorrection, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1",
		RequiresResolution: true, Body: "we don't use that term", TargetVersion: 3,
		Anchor: json.RawMessage(`{"type":"text_quote","exact":"churn"}`),
	})

	require.Equal(t, http.StatusCreated, w.Code)
	require.NotNil(t, store.lastCreated)
	assert.Equal(t, "u1", store.lastCreated.AuthorID)
	assert.Equal(t, threads.ThreadStatusOpen, store.lastCreated.Status)
	assert.True(t, store.lastCreated.RequiresResolution)
	assert.Equal(t, 3, store.lastCreated.TargetVersion)
}

func TestCreateThreadInvalidKind(t *testing.T) {
	h := newThreadTestHandler(&mockThreadStore{}, ownedAsset("u1"), &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{Kind: "bogus", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateThreadBadScope(t *testing.T) {
	h := newThreadTestHandler(&mockThreadStore{}, ownedAsset("u1"), &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{
		Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", CollectionID: "col_1",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateAssetThreadNonOwnerDenied(t *testing.T) {
	// Asset owned by someone else, requester has no share → 403.
	h := newThreadTestHandler(&mockThreadStore{}, &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: "other"}}, &mockShareStore{}, &access.User{UserID: "u1", Email: "u1@example.com"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", Body: "x"})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreateStandaloneThreadAnyUser(t *testing.T) {
	store := &mockThreadStore{}
	// No asset access configured; standalone must still succeed for any authed user.
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u2", Email: "u2@example.com"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{Kind: threads.ThreadKindSuggestion, TargetType: portaldomain.TargetTypeStandalone, Body: "idea"})
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, portaldomain.TargetTypeStandalone, store.lastCreated.TargetType)
}

// --- list ---

func TestListThreadsAssetScope(t *testing.T) {
	store := &mockThreadStore{listResult: []threads.ThreadWithMeta{{Thread: threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1"}, EventCount: 2}}, listTotal: 1}
	h := newThreadTestHandler(store, ownedAsset("u1"), &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?asset_id=asset_1", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var resp pagedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Total)
}

func TestListThreadsNoScopeRejected(t *testing.T) {
	h := newThreadTestHandler(&mockThreadStore{}, ownedAsset("u1"), &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListStandaloneThreadsAnyUser(t *testing.T) {
	store := &mockThreadStore{listResult: []threads.ThreadWithMeta{}, listTotal: 0}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u9"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?target_type=standalone", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

// --- get / events ---

func TestGetThreadStandalone(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/thr_1", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListThreadEvents(t *testing.T) {
	store := &mockThreadStore{
		getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone},
		events:    []threads.ThreadEvent{{ID: "evt_1", ThreadID: "thr_1", EventType: threads.EventTypeComment}},
	}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/thr_1/events", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Data []threads.ThreadEvent `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
}

func TestAppendThreadEventDefaultsToComment(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1", Email: "u1@example.com"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/events", appendEventRequest{Body: "thanks"})
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestAppendThreadEventInvalidType(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/events", appendEventRequest{EventType: threads.EventTypeStatusChange, Body: "x"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- patch / delete ---

func TestUpdateThreadByAuthor(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u1"}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	resolved := threads.ThreadStatusResolved
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", updateThreadRequest{Status: &resolved})
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, store.lastUpdate)
	require.NotNil(t, store.lastUpdate.Status)
	assert.Equal(t, threads.ThreadStatusResolved, *store.lastUpdate.Status)
}

func TestUpdateThreadInvalidStatus(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u1"}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	bogus := "bogus"
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", updateThreadRequest{Status: &bogus})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateThreadRejectsValidationState(t *testing.T) {
	// The generic moderator PATCH must not set validation_state; that bypasses
	// the author-only validation gate, the validation_result event, and re-open.
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u1"}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	vs := threads.ValidationStateValidated
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", updateThreadRequest{ValidationState: &vs})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Nil(t, store.lastUpdate) // never reached the store
}

func TestUpdateThreadNonModeratorDenied(t *testing.T) {
	// Standalone thread authored by someone else; requester is not admin → 403.
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone, AuthorID: "other"}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	resolved := threads.ThreadStatusResolved
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", updateThreadRequest{Status: &resolved})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUpdateThreadByAdmin(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone, AuthorID: "other"}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "admin1", Roles: []string{"admin"}})
	ack := threads.ThreadStatusAcknowledged
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", updateThreadRequest{Status: &ack})
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestDeleteThreadByAuthor(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u1"}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodDelete, "/api/v1/portal/threads/thr_1", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

// --- counts ---

func TestThreadCountsOwnedAssets(t *testing.T) {
	store := &mockThreadStore{counts: map[string]int{"asset_1": 3}}
	h := newThreadTestHandler(store, &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: "u1"}}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/counts?target_type=asset&ids=asset_1,asset_2", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]int
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, 3, got["asset_1"])
}

func TestThreadCountsFiltersUnownedAssets(t *testing.T) {
	// Requested asset is owned by someone else → filtered out before counting.
	store := &mockThreadStore{counts: map[string]int{}}
	h := newThreadTestHandler(store, &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_other", OwnerID: "someone"}}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/counts?target_type=asset&ids=asset_other", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, store.lastCountIDs, "unowned id must not reach the count query")
}

func TestThreadCountsAdminUnfiltered(t *testing.T) {
	store := &mockThreadStore{counts: map[string]int{"asset_x": 1}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "admin1", Roles: []string{"admin"}})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/counts?target_type=asset&ids=asset_x", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]int
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, 1, got["asset_x"])
}

func TestThreadCountsTooManyIDs(t *testing.T) {
	store := &mockThreadStore{counts: map[string]int{}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	ids := make([]string, maxThreadCountIDs+1)
	for i := range ids {
		ids[i] = "a"
	}
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/counts?target_type=asset&ids="+strings.Join(ids, ","), nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Nil(t, store.lastCountIDs, "oversized request must not reach the count query")
}

func TestThreadCountsNilAssetStore(t *testing.T) {
	// A non-admin asset-count request with no AssetStore wired must not panic;
	// ownedAssetIDs returns no owned ids, so the count query sees an empty set.
	store := &mockThreadStore{counts: map[string]int{}}
	h := newTestServer(Config{Threads: store, Shares: &mockShareStore{}}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/counts?target_type=asset&ids=a1,a2", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, store.lastCountIDs, "no asset store means no owned ids reach the count query")
}

func TestThreadCountsBadTargetType(t *testing.T) {
	h := newThreadTestHandler(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/counts?target_type=prompt&ids=x", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestThreadCountsStoreError(t *testing.T) {
	store := &mockThreadStore{countErr: assert.AnError}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "admin1", Roles: []string{"admin"}})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/counts?target_type=asset&ids=a", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestThreadCountsRequiresAuth(t *testing.T) {
	h := newThreadTestHandler(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, nil)
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/counts?target_type=asset&ids=a", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestThreadCountsOwnedCollections(t *testing.T) {
	store := &mockThreadStore{counts: map[string]int{"col_1": 2}}
	colls := &mockCollectionStore{getResult: &portaldomain.Collection{ID: "col_1", OwnerID: "u1"}}
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, colls, nil, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/counts?target_type=collection&ids=col_1", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]int
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, 2, got["col_1"])
}

// --- access via share (non-owner with viewer share can author) ---

func TestCreateAssetThreadWithViewerShare(t *testing.T) {
	store := &mockThreadStore{}
	shares := &mockShareStore{listByAsset: []portaldomain.Share{{
		ID: "s1", AssetID: "asset_1", SharedWithUserID: "u1", Permission: portaldomain.PermissionViewer,
		CreatedAt: time.Now(),
	}}}
	h := newThreadTestHandler(store, &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: "other"}}, shares, &access.User{UserID: "u1", Email: "u1@example.com"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", Body: "looks good"})
	assert.Equal(t, http.StatusCreated, w.Code)
}

// --- collection / prompt target access ---

//nolint:revive // test helper aggregating mock stores for a Handler
func newThreadHandlerFull(store *mockThreadStore, assets *mockAssetStore, shares *mockShareStore, colls portaldomain.CollectionStore, prompts prompt.Store, user *access.User) http.Handler {
	return newTestServer(Config{
		Assets:      assets,
		Shares:      shares,
		Threads:     store,
		Collections: colls,
		Prompts:     prompts,
	}, user)
}

func TestCreateCollectionThreadAsOwner(t *testing.T) {
	store := &mockThreadStore{}
	colls := &mockCollectionStore{getResult: &portaldomain.Collection{ID: "col_1", OwnerID: "u1"}}
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, colls, nil, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{
		Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypeCollection, CollectionID: "col_1", Body: "x",
		Anchor: json.RawMessage(`{"type":"section","section_id":"s1"}`),
	})
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCreateCollectionThreadNonOwnerDenied(t *testing.T) {
	colls := &mockCollectionStore{getResult: &portaldomain.Collection{ID: "col_1", OwnerID: "other"}}
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, colls, nil, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypeCollection, CollectionID: "col_1", Body: "x"})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreatePromptThreadGlobalPromptAnyUser(t *testing.T) {
	ps := newMockPromptStore()
	ps.prompts["g1"] = &prompt.Prompt{ID: "p1", Name: "g1", Scope: prompt.ScopeGlobal}
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, nil, ps, &access.User{UserID: "u2", Email: "u2@example.com"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{Kind: threads.ThreadKindQuestion, TargetType: portaldomain.TargetTypePrompt, PromptID: "p1", Body: "q"})
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCreatePromptThreadPersonalNonOwnerDenied(t *testing.T) {
	ps := newMockPromptStore()
	ps.prompts["p"] = &prompt.Prompt{ID: "p1", Name: "p", Scope: prompt.ScopePersonal, OwnerEmail: "owner@example.com"}
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, nil, ps, &access.User{UserID: "u2", Email: "u2@example.com"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypePrompt, PromptID: "p1", Body: "x"})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreatePromptThreadPersonalOwnerAllowed(t *testing.T) {
	ps := newMockPromptStore()
	ps.prompts["p"] = &prompt.Prompt{ID: "p1", Name: "p", Scope: prompt.ScopePersonal, OwnerEmail: "owner@example.com"}
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, nil, ps, &access.User{UserID: "owner", Email: "owner@example.com"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{Kind: threads.ThreadKindCorrection, TargetType: portaldomain.TargetTypePrompt, PromptID: "p1", Body: "fix"})
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestUpdateAssetThreadByAssetOwner(t *testing.T) {
	// Thread authored by SME; asset owner (not author) may still moderate.
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", AuthorID: "sme"}}
	h := newThreadTestHandler(store, ownedAsset("owner1"), &mockShareStore{}, &access.User{UserID: "owner1"})
	resolved := threads.ThreadStatusResolved
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", updateThreadRequest{Status: &resolved})
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestDeleteCollectionThreadByCollectionOwner(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeCollection, CollectionID: "col_1", AuthorID: "sme"}}
	colls := &mockCollectionStore{getResult: &portaldomain.Collection{ID: "col_1", OwnerID: "owner1"}}
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, colls, nil, &access.User{UserID: "owner1"})
	w := doThreadReq(t, h, http.MethodDelete, "/api/v1/portal/threads/thr_1", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestThreadNotFound(t *testing.T) {
	store := &mockThreadStore{getErr: assert.AnError}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/missing", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- error paths ---

func TestCreateThreadInvalidBody(t *testing.T) {
	h := newThreadTestHandler(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/threads", bytes.NewReader([]byte("{bad")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateThreadStoreError(t *testing.T) {
	store := &mockThreadStore{createErr: assert.AnError}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypeStandalone, Body: "x"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListThreadsStoreError(t *testing.T) {
	store := &mockThreadStore{listErr: assert.AnError}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?target_type=standalone", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListThreadEventsStoreError(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "t", TargetType: portaldomain.TargetTypeStandalone}, eventsErr: assert.AnError}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/t/events", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAppendThreadEventInvalidBody(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "t", TargetType: portaldomain.TargetTypeStandalone}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/threads/t/events", bytes.NewReader([]byte("{bad")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAppendThreadEventStoreError(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "t", TargetType: portaldomain.TargetTypeStandalone}, appendErr: assert.AnError}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/t/events", appendEventRequest{Body: "x"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateThreadInvalidBody(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "t", TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u1"}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/portal/threads/t", bytes.NewReader([]byte("{bad")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateThreadStoreError(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "t", TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u1"}, updateErr: assert.AnError}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	resolved := threads.ThreadStatusResolved
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/t", updateThreadRequest{Status: &resolved})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDeleteThreadStoreError(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "t", TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u1"}, deleteErr: assert.AnError}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodDelete, "/api/v1/portal/threads/t", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestThreadHandlersRequireAuth(t *testing.T) {
	store := &mockThreadStore{}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, nil) // nil user → unauthenticated
	for _, path := range []string{
		"/api/v1/portal/threads?target_type=standalone",
		"/api/v1/portal/threads/thr_1",
	} {
		w := doThreadReq(t, h, http.MethodGet, path, nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code, path)
	}
}

func TestUpdateThreadInvalidValidationState(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{ID: "t", TargetType: portaldomain.TargetTypeStandalone, AuthorID: "u1"}}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	bad := "garbage"
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/t", updateThreadRequest{ValidationState: &bad})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateAssetThreadByEditor(t *testing.T) {
	// Asset thread authored by an SME; a non-owner EDITOR may moderate it.
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", AuthorID: "sme"}}
	shares := &mockShareStore{listByAsset: []portaldomain.Share{{
		ID: "s1", AssetID: "asset_1", SharedWithUserID: "ed", Permission: portaldomain.PermissionEditor, CreatedAt: time.Now(),
	}}}
	h := newThreadTestHandler(store, &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: "owner"}}, shares, &access.User{UserID: "ed", Email: "ed@example.com"})
	resolved := threads.ThreadStatusResolved
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", updateThreadRequest{Status: &resolved})
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUpdateAssetThreadByViewerDenied(t *testing.T) {
	// A viewer (not editor, not owner, not author) cannot moderate.
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", AuthorID: "sme"}}
	shares := &mockShareStore{listByAsset: []portaldomain.Share{{
		ID: "s1", AssetID: "asset_1", SharedWithUserID: "vw", Permission: portaldomain.PermissionViewer, CreatedAt: time.Now(),
	}}}
	h := newThreadTestHandler(store, &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: "owner"}}, shares, &access.User{UserID: "vw", Email: "vw@example.com"})
	resolved := threads.ThreadStatusResolved
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", updateThreadRequest{Status: &resolved})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListThreadsStandaloneWithObjectIDRejected(t *testing.T) {
	h := newThreadTestHandler(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?target_type=standalone&asset_id=secret", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestThreadAssetAccessAssetNotFound(t *testing.T) {
	store := &mockThreadStore{}
	h := newThreadTestHandler(store, &mockAssetStore{getErr: assert.AnError}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?asset_id=missing", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestNewPostgresThreadStoreNonNil(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	assert.NotNil(t, threads.NewPostgresThreadStore(db))
}

func TestListThreadsWithKindAndStatusFilters(t *testing.T) {
	store := &mockThreadStore{listResult: []threads.ThreadWithMeta{}, listTotal: 0}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?target_type=standalone&kind=question&status=open&limit=5&offset=0", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestThreadPromptAccessStoreUnavailable(t *testing.T) {
	// No PromptStore configured → prompt target is unavailable (503).
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?prompt_id=p1", nil)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestThreadPromptAccessNotFound(t *testing.T) {
	ps := newMockPromptStore() // empty → GetByID returns nil
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, nil, ps, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?prompt_id=missing", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestThreadCollectionAccessStoreUnavailable(t *testing.T) {
	h := newThreadHandlerFull(&mockThreadStore{}, &mockAssetStore{}, &mockShareStore{}, nil, nil, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?collection_id=c1", nil)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestPromptThreadAccessViaShareGrant(t *testing.T) {
	ps := newMockPromptStore()
	ps.prompts["p"] = &prompt.Prompt{ID: "p1", Name: "p", Scope: prompt.ScopePersonal, OwnerEmail: "owner@example.com"}
	shares := &mockShareStore{promptRefs: []portaldomain.SharedPromptRef{{PromptID: "p1"}}}
	h := newThreadHandlerFull(&mockThreadStore{listResult: []threads.ThreadWithMeta{}}, &mockAssetStore{}, shares, nil, ps, &access.User{UserID: "u2", Email: "u2@example.com"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?prompt_id=p1", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListCollectionThreadsAsOwner(t *testing.T) {
	store := &mockThreadStore{listResult: []threads.ThreadWithMeta{}, listTotal: 0}
	colls := &mockCollectionStore{getResult: &portaldomain.Collection{ID: "col_1", OwnerID: "u1"}}
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, colls, nil, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?collection_id=col_1", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetThreadAccessDenied(t *testing.T) {
	// Asset thread the requester cannot view → 403.
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1"}}
	h := newThreadTestHandler(store, &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: "other"}}, &mockShareStore{}, &access.User{UserID: "u1", Email: "u1@example.com"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/thr_1", nil)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDeletePromptThreadByPromptOwner(t *testing.T) {
	ps := newMockPromptStore()
	ps.prompts["p"] = &prompt.Prompt{ID: "p1", Name: "p", Scope: prompt.ScopePersonal, OwnerEmail: "owner@example.com"}
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypePrompt, PromptID: "p1", AuthorID: "sme"}}
	h := newThreadHandlerFull(store, &mockAssetStore{}, &mockShareStore{}, nil, ps, &access.User{UserID: "owner", Email: "owner@example.com"})
	w := doThreadReq(t, h, http.MethodDelete, "/api/v1/portal/threads/thr_1", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUpdateThreadNotFound(t *testing.T) {
	store := &mockThreadStore{getErr: assert.AnError}
	h := newThreadTestHandler(store, &mockAssetStore{}, &mockShareStore{}, &access.User{UserID: "u1"})
	resolved := threads.ThreadStatusResolved
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/missing", updateThreadRequest{Status: &resolved})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- routes absent when no thread store ---

func TestThreadRoutesAbsentWithoutStore(t *testing.T) {
	h := newTestServer(Config{Assets: &mockAssetStore{}, Shares: &mockShareStore{}}, &access.User{UserID: "u1"})
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?target_type=standalone", nil)
	// Without a thread store the route is unregistered → 404.
	assert.Equal(t, http.StatusNotFound, w.Code)
}
