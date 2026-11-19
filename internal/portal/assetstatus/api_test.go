package assetstatus

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
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// fakeAssets is the asset store the gate and the checker read.
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

// fakeShares answers the share lookups the checker makes.
type fakeShares struct {
	portaldomain.ShareStore
	byAsset map[string][]portaldomain.Share
}

func (f *fakeShares) ListByAsset(_ context.Context, id string) ([]portaldomain.Share, error) {
	return f.byAsset[id], nil
}

func (*fakeShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

// fakeStatus is a Store that applies changes to the fake assets.
type fakeStatus struct {
	assets  *fakeAssets
	events  []Event
	failSet error
	failHis error
}

func (f *fakeStatus) Set(_ context.Context, id string, c Change) error {
	if f.failSet != nil {
		return f.failSet
	}
	c, err := c.Normalize()
	if err != nil {
		return err
	}
	f.assets.rows[id].Status = &portaldomain.AssetStatus{Value: c.Status, Note: c.Note, SetBy: c.Actor}
	f.events = append(f.events, Event{Kind: KindStatus, To: c.Status, Actor: c.Actor})
	return nil
}

func (f *fakeStatus) Clear(_ context.Context, id, actor string) (bool, error) {
	had := f.assets.rows[id].Status != nil
	f.assets.rows[id].Status = nil
	f.events = append(f.events, Event{Kind: KindStatus, Actor: actor})
	return had, nil
}

func (f *fakeStatus) History(context.Context, string, int) ([]Event, error) {
	if f.failHis != nil {
		return nil, f.failHis
	}
	return f.events, nil
}

const (
	ownerEmail  = "owner@example.com"
	editorEmail = "editor@example.com"
	viewerEmail = "viewer@example.com"
)

func fixture() (*fakeAssets, *fakeStatus, *http.ServeMux) {
	assets := &fakeAssets{rows: map[string]*portaldomain.Asset{
		"a1": {ID: "a1", OwnerID: "u-owner", OwnerEmail: ownerEmail, Name: "Q3 report", CurrentVersion: 2},
		"gone": {
			ID: "gone", OwnerID: "u-owner", OwnerEmail: ownerEmail,
			DeletedAt: func() *time.Time { t := time.Now(); return &t }(),
		},
	}}
	shares := &fakeShares{byAsset: map[string][]portaldomain.Share{
		"a1": {
			{AssetID: "a1", SharedWithEmail: editorEmail, Permission: portaldomain.PermissionEditor},
			{AssetID: "a1", SharedWithEmail: viewerEmail, Permission: portaldomain.PermissionViewer},
		},
	}}
	store := &fakeStatus{assets: assets}
	mux := http.NewServeMux()
	Mount(store, assets)(mux, access.New(access.Config{Assets: assets, Shares: shares}))
	return assets, store, mux
}

func call(mux *http.ServeMux, method, path, email, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if email != "" {
		req = req.WithContext(access.ContextWithUser(req.Context(), &access.User{UserID: "u-" + email, Email: email}))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestGateRefusals(t *testing.T) {
	_, _, mux := fixture()
	cases := map[string]struct {
		path, email string
		want        int
	}{
		"no caller":         {"/api/v1/portal/assets/a1/status", "", http.StatusUnauthorized},
		"missing asset":     {"/api/v1/portal/assets/nope/status", ownerEmail, http.StatusNotFound},
		"deleted asset":     {"/api/v1/portal/assets/gone/status", ownerEmail, http.StatusNotFound},
		"cannot open":       {"/api/v1/portal/assets/a1/status", "stranger@example.com", http.StatusNotFound},
		"viewer cannot set": {"/api/v1/portal/assets/a1/status", viewerEmail, http.StatusForbidden},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := call(mux, http.MethodPut, tc.path, tc.email, `{"status":"draft"}`)
			assert.Equal(t, tc.want, rec.Code, rec.Body.String())
		})
	}
}

func TestOwnerAndEditorSetTheStatus(t *testing.T) {
	for _, who := range []string{ownerEmail, editorEmail} {
		t.Run(who, func(t *testing.T) {
			assets, store, mux := fixture()
			rec := call(mux, http.MethodPut, "/api/v1/portal/assets/a1/status", who, `{"status":"deprecated","note":"see Q4"}`)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var got portaldomain.Asset
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.NotNil(t, got.Status)
			assert.Equal(t, "deprecated", got.Status.Value)
			assert.Equal(t, "see Q4", got.Status.Note)
			assert.Equal(t, who, assets.rows["a1"].Status.SetBy, "recorded under the caller's address")
			require.Len(t, store.events, 1)
		})
	}
}

func TestSetStatusRefusesABadBodyAndAnUnknownStatus(t *testing.T) {
	_, store, mux := fixture()
	rec := call(mux, http.MethodPut, "/api/v1/portal/assets/a1/status", ownerEmail, `{not json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), errBadBody)

	rec = call(mux, http.MethodPut, "/api/v1/portal/assets/a1/status", ownerEmail, `{"status":"archived"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "draft, published or deprecated")
	assert.Empty(t, store.events, "nothing recorded for a refused change")
}

func TestSetStatusStoreFailureIs500(t *testing.T) {
	_, store, mux := fixture()
	store.failSet = errors.New("db down")
	rec := call(mux, http.MethodPut, "/api/v1/portal/assets/a1/status", ownerEmail, `{"status":"draft"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "db down", "the store's error is not repeated to the caller")
}

func TestClearStatus(t *testing.T) {
	assets, _, mux := fixture()
	assets.rows["a1"].Status = &portaldomain.AssetStatus{Value: "draft"}
	rec := call(mux, http.MethodDelete, "/api/v1/portal/assets/a1/status", viewerEmail, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "a viewer cannot clear it")
	require.NotNil(t, assets.rows["a1"].Status)

	rec = call(mux, http.MethodDelete, "/api/v1/portal/assets/a1/status", ownerEmail, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var got portaldomain.Asset
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Nil(t, got.Status)
}

func TestHistoryIsReadableByAnyoneWhoCanOpenIt(t *testing.T) {
	_, store, mux := fixture()
	store.events = []Event{{Kind: KindStatus, From: "draft", To: "published", Actor: ownerEmail}}
	rec := call(mux, http.MethodGet, "/api/v1/portal/assets/a1/status/history", viewerEmail, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var got historyResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Events, 1)
	assert.Equal(t, "published", got.Events[0].To)

	rec = call(mux, http.MethodGet, "/api/v1/portal/assets/a1/status/history", "stranger@example.com", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	store.failHis = errors.New("boom")
	rec = call(mux, http.MethodGet, "/api/v1/portal/assets/a1/status/history", ownerEmail, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestActorPrefersTheAddress(t *testing.T) {
	assert.Equal(t, "a@x", Actor(&access.User{UserID: "u1", Email: "a@x"}))
	assert.Equal(t, "u1", Actor(&access.User{UserID: "u1"}))
}

func TestWriteAssetReadFailureIs500(t *testing.T) {
	assets, store, _ := fixture()
	api := &API{store: store, gate: Gate{Assets: assets}}
	rec := httptest.NewRecorder()
	api.writeAsset(context.Background(), rec, "missing")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
