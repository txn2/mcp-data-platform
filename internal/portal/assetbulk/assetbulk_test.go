package assetbulk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalnoop"
)

// assets is an asset store holding a fixed set.
type assets struct {
	portaldomain.AssetStore
	rows map[string]*portaldomain.Asset
}

func (a assets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if row, ok := a.rows[id]; ok {
		return row, nil
	}
	return nil, errors.New("not found")
}

// routes records the single-asset requests served and answers them the way
// the portal does: the owner may act, a viewer of a shared asset is refused
// with the route's sentence, and anyone else is refused too.
type routes struct {
	mu     sync.Mutex
	served []string
	bodies map[string]map[string]any
	owner  string
}

func (rt *routes) handler(store assets) *http.ServeMux {
	mux := http.NewServeMux()
	act := func(w http.ResponseWriter, r *http.Request) {
		rt.mu.Lock()
		rt.served = append(rt.served, r.Method+" "+r.URL.Path)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if rt.bodies == nil {
			rt.bodies = map[string]map[string]any{}
		}
		rt.bodies[r.Method+" "+r.URL.Path] = body
		rt.mu.Unlock()
		id := r.PathValue("id")
		if r.PathValue("cid") != "" {
			id, _ = body["asset_id"].(string)
		}
		row, ok := store.rows[id]
		switch {
		case !ok:
			httpjson.WriteError(w, http.StatusNotFound, "asset not found")
		case row.OwnerID != access.GetUser(r.Context()).UserID:
			httpjson.WriteError(w, http.StatusForbidden, "only the owner can do that")
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	}
	for _, p := range []string{
		"PUT /api/v1/portal/assets/{id}", "DELETE /api/v1/portal/assets/{id}",
		"PUT /api/v1/portal/assets/{id}/status", "DELETE /api/v1/portal/assets/{id}/status",
		"POST /api/v1/portal/assets/{id}/shares", "POST /api/v1/portal/collections/{cid}/items",
	} {
		mux.HandleFunc(p, act)
	}
	return mux
}

func fixture() (assets, *access.User) {
	store := assets{rows: map[string]*portaldomain.Asset{
		"mine1":  {ID: "mine1", OwnerID: "u1", Name: "Mine one", Tags: []string{"Finance", "q3"}},
		"mine2":  {ID: "mine2", OwnerID: "u1", Name: "Mine two"},
		"theirs": {ID: "theirs", OwnerID: "u2", Name: "Hidden report", Tags: []string{"secret"}},
	}}
	return store, &access.User{UserID: "u1", Email: "ann@example.com"}
}

func newAPI(t *testing.T) (*API, *routes, *access.User) {
	t.Helper()
	store, user := fixture()
	rt := &routes{owner: "u1"}
	checker := access.New(access.Config{Assets: store, Shares: portalnoop.NewShareStore()})
	return New(rt.handler(store), store, checker), rt, user
}

func TestApply_AddTagsMergesWithoutCaseAndSkipsHidden(t *testing.T) {
	api, rt, user := newAPI(t)
	res := api.Apply(context.Background(), user, Request{Action: ActionAddTags, AssetIDs: []string{"mine1", "mine2", "theirs", "gone"}, Tags: []string{"finance", "board"}})
	assert.Equal(t, 4, res.Requested)
	assert.Equal(t, []string{"mine1", "mine2"}, res.Applied)
	require.Len(t, res.Skipped, 2)
	for _, s := range res.Skipped {
		assert.Equal(t, ReasonHidden, s.Reason, "an asset the caller cannot open and one that does not exist read the same")
	}
	assert.Equal(t, []any{"Finance", "q3", "board"}, rt.bodies["PUT /api/v1/portal/assets/mine1"]["tags"], "the asset's own spelling is kept")
	assert.Equal(t, []any{"finance", "board"}, rt.bodies["PUT /api/v1/portal/assets/mine2"]["tags"])
	assert.NotContains(t, rt.served, "PUT /api/v1/portal/assets/gone", "an asset that cannot be read is never written")
}

func TestApply_ViewableButRefusedReportsTheRoutesSentence(t *testing.T) {
	store, user := fixture()
	store.rows["shared"] = &portaldomain.Asset{ID: "shared", OwnerID: "u2", OwnerEmail: "bob@example.com"}
	rt := &routes{}
	checker := access.New(access.Config{Assets: store, Shares: sharedWithAnn{ShareStore: portalnoop.NewShareStore()}})
	api := New(rt.handler(store), store, checker)
	res := api.Apply(context.Background(), user, Request{
		Action: ActionShare, AssetIDs: []string{"mine1", "shared"},
		Share: &ShareSpec{SharedWithEmail: "carol@example.com"},
	})
	assert.Equal(t, []string{"mine1"}, res.Applied)
	require.Len(t, res.Skipped, 1)
	assert.Equal(t, Skipped{AssetID: "shared", Reason: "only the owner can do that"}, res.Skipped[0])
	assert.Equal(t, false, rt.bodies["POST /api/v1/portal/assets/mine1/shares"]["notify"], "a bulk share sends no email")
	assert.Equal(t, "carol@example.com", rt.bodies["POST /api/v1/portal/assets/mine1/shares"]["shared_with_email"])
}

// sharedWithAnn grants ann a viewer share on every asset.
type sharedWithAnn struct{ portaldomain.ShareStore }

func (sharedWithAnn) ListByAsset(_ context.Context, assetID string) ([]portaldomain.Share, error) {
	return []portaldomain.Share{{AssetID: assetID, SharedWithEmail: "ann@example.com", Permission: portaldomain.PermissionViewer}}, nil
}

func TestApply_EveryActionUsesItsSingleRoute(t *testing.T) {
	cases := []struct {
		req  Request
		want string
	}{
		{Request{Action: ActionRemoveTags, Tags: []string{"FINANCE"}}, "PUT /api/v1/portal/assets/mine1"},
		{Request{Action: ActionSetStatus, Status: "draft"}, "PUT /api/v1/portal/assets/mine1/status"},
		{Request{Action: ActionClearStatus}, "DELETE /api/v1/portal/assets/mine1/status"},
		{Request{Action: ActionDelete}, "DELETE /api/v1/portal/assets/mine1"},
		{Request{Action: ActionAddToCollection, CollectionID: "c1", SectionID: "s1"}, "POST /api/v1/portal/collections/c1/items"},
	}
	for _, tc := range cases {
		t.Run(tc.req.Action, func(t *testing.T) {
			api, rt, user := newAPI(t)
			tc.req.AssetIDs = []string{"mine1"}
			res := api.Apply(context.Background(), user, tc.req)
			assert.Equal(t, []string{"mine1"}, res.Applied)
			assert.Equal(t, []string{tc.want}, rt.served)
		})
	}
}

func TestRetagged(t *testing.T) {
	assert.Equal(t, []string{"q3"}, Retagged([]string{"Finance", "q3"}, []string{"finance"}, false))
	assert.Equal(t, []string{"Finance", "q3", "new"}, Retagged([]string{"Finance", "q3"}, []string{"FINANCE", " new ", ""}, true))
	assert.Equal(t, []string{}, Retagged(nil, []string{"x"}, false))
}

func TestDistinct(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, Distinct([]string{" a", "b", "a", "", "b "}))
}

func TestValidate(t *testing.T) {
	ids := []string{"a"}
	tooMany := make([]string, MaxAssets+1)
	for i := range tooMany {
		tooMany[i] = strings.Repeat("x", i+1)
	}
	bad := []Request{
		{Action: ActionDelete},
		{Action: ActionDelete, AssetIDs: tooMany},
		{Action: "explode", AssetIDs: ids},
		{Action: ActionAddTags, AssetIDs: ids},
		{Action: ActionAddToCollection, AssetIDs: ids},
		{Action: ActionSetStatus, AssetIDs: ids},
		{Action: ActionShare, AssetIDs: ids},
		{Action: ActionShare, AssetIDs: ids, Share: &ShareSpec{SharedWithEmail: "a@x", SharedWithPersona: "p"}},
	}
	for _, r := range bad {
		assert.Error(t, r.Validate(), "%+v", r.Action)
	}
	good := []Request{
		{Action: ActionDelete, AssetIDs: ids},
		{Action: ActionClearStatus, AssetIDs: ids},
		{Action: ActionShare, AssetIDs: ids, Share: &ShareSpec{SharedWithPersona: "finance"}},
		{Action: ActionAddTags, AssetIDs: ids, Tags: []string{"t"}},
	}
	for _, r := range good {
		assert.NoError(t, r.Validate(), "%+v", r.Action)
	}
	err := Request{Action: ActionDelete, AssetIDs: tooMany}.Validate()
	assert.Contains(t, err.Error(), "at most 500 assets")
}

func TestBulkRoute(t *testing.T) {
	store, user := fixture()
	rt := &routes{}
	mux := rt.handler(store)
	Mount(store)(mux, access.New(access.Config{Assets: store, Shares: portalnoop.NewShareStore()}))

	do := func(body string, u *access.User) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, Path, strings.NewReader(body))
		if u != nil {
			req = req.WithContext(access.ContextWithUser(req.Context(), u))
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := do(`{"action":"delete","asset_ids":["mine1","theirs","mine1"]}`, user)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var res Result
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, 2, res.Requested, "repeated ids are acted on once")
	assert.Equal(t, []string{"mine1"}, res.Applied)
	assert.NotContains(t, rec.Body.String(), "Hidden report", "nothing about an asset the caller cannot open is returned")
	assert.NotContains(t, rec.Body.String(), "secret")

	assert.Equal(t, http.StatusUnauthorized, do(`{}`, nil).Code)
	assert.Equal(t, http.StatusBadRequest, do(`not json`, user).Code)
	assert.Equal(t, http.StatusBadRequest, do(`{"action":"delete","asset_ids":[]}`, user).Code)
	assert.Equal(t, http.StatusBadRequest, do(`{"action":"delete","asset_ids":"mine1"}`, user).Code, "a string is not a list of ids")
}

func TestRecorder(t *testing.T) {
	r := &recorder{header: http.Header{}}
	assert.Equal(t, http.StatusOK, r.statusCode())
	_, _ = r.Write(bytes.Repeat([]byte("x"), maxAnswer+10))
	assert.Equal(t, maxAnswer, r.body.Len(), "only the start of an answer is kept")
	r.WriteHeader(http.StatusTeapot)
	assert.Equal(t, http.StatusOK, r.statusCode(), "the first status stands")
	r.Header().Set("X", "y")
	assert.Equal(t, "y", r.header.Get("X"))
}

func TestReason_FallsBackToStatusText(t *testing.T) {
	api, _, user := newAPI(t)
	assert.Equal(t, "conflict", api.reason(context.Background(), user, "mine1", http.StatusConflict, []byte("not json")))
	assert.Equal(t, ReasonHidden, api.reason(context.Background(), user, "mine1", http.StatusGone, nil))
}

func TestServe_PassesIdentityAndBody(t *testing.T) {
	var got string
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /x", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = access.GetUser(r.Context()).UserID + " " + strings.TrimSpace(string(b))
		w.WriteHeader(http.StatusAccepted)
	})
	api := New(mux, assets{}, nil)
	ctx := access.ContextWithUser(context.Background(), &access.User{UserID: "u9"})
	status, _ := api.serve(ctx, call{http.MethodPut, "/x", map[string]int{"n": 1}})
	assert.Equal(t, http.StatusAccepted, status)
	assert.Equal(t, `u9 {"n":1}`, got)
	status, _ = api.serve(ctx, call{http.MethodPut, "/x", func() {}})
	assert.Equal(t, http.StatusInternalServerError, status, "a body that cannot be encoded is not sent")
}
