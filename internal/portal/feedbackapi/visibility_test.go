package feedbackapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// externalServer serves the routes for a caller whose persona is external
// when external is true, over an asset shared with them.
func externalServer(store *mockThreadStore, user *access.User, external bool) http.Handler {
	assets := &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: "owner"}}
	shares := &mockShareStore{listByAsset: []portaldomain.Share{{SharedWithEmail: user.Email, Permission: portaldomain.PermissionViewer}}}
	cfg := Config{Assets: assets, Shares: shares, Threads: store}
	cfg.Access = access.New(access.Config{
		Assets: assets, Shares: shares, AdminRoles: []string{"admin"},
		PortalPermissions: func([]string) persona.PortalAccess { return persona.PortalAccess{External: external} },
	})
	cfg.ExternalAddress = func(_ context.Context, email string) bool { return email == "client@example.org" }
	return newTestServer(cfg, user)
}

var client = &access.User{UserID: "ext:a1", Email: "client@example.org"}

// A thread an external person opens is shared whatever they ask, and they
// list only shared threads (#2227).
func TestExternalPersonSeesAndOpensOnlySharedThreads(t *testing.T) {
	store := &mockThreadStore{}
	h := externalServer(store, client, true)
	w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{
		Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", Body: "x", Visibility: threads.VisibilityInternal,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, threads.VisibilityShared, store.lastCreated.Visibility)

	w = doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?asset_id=asset_1", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, store.lastListFilter.SharedOnly)

	store.getResult = &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", Visibility: threads.VisibilityInternal}
	for _, path := range []string{"/api/v1/portal/threads/thr_1", "/api/v1/portal/threads/thr_1/events"} {
		assert.Equal(t, http.StatusNotFound, doThreadReq(t, h, http.MethodGet, path, nil).Code, path)
	}
	store.getResult.Visibility = threads.VisibilityShared
	assert.Equal(t, http.StatusOK, doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/thr_1", nil).Code)
}

// An internal person chooses the visibility, defaulting to internal, and an
// invalid one is refused.
func TestInternalPersonChoosesTheVisibility(t *testing.T) {
	owner := &access.User{UserID: "owner", Email: "owner@example.com"}
	for _, c := range []struct {
		ask, want string
		status    int
	}{
		{"", threads.VisibilityInternal, http.StatusCreated},
		{threads.VisibilityShared, threads.VisibilityShared, http.StatusCreated},
		{"public", "", http.StatusBadRequest},
	} {
		store := &mockThreadStore{}
		h := externalServer(store, owner, false)
		w := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", createThreadRequest{
			Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", Body: "x", Visibility: c.ask,
		})
		require.Equal(t, c.status, w.Code, c.ask)
		if c.want != "" {
			assert.Equal(t, c.want, store.lastCreated.Visibility)
		}
	}
	store := &mockThreadStore{}
	h := externalServer(store, owner, false)
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads?asset_id=asset_1", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.False(t, store.lastListFilter.SharedOnly)
}

// The author, owner, editors and admins change the visibility; an external
// person cannot.
func TestChangingTheVisibility(t *testing.T) {
	shared := threads.VisibilityShared
	owner := &access.User{UserID: "owner", Email: "owner@example.com"}
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", AuthorID: "owner", Visibility: threads.VisibilityInternal}}
	h := externalServer(store, owner, false)
	w := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", map[string]any{"visibility": shared})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, store.lastUpdate.Visibility)
	assert.Equal(t, shared, *store.lastUpdate.Visibility)
	assert.Equal(t, http.StatusBadRequest, doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", map[string]any{"visibility": "x"}).Code)

	store.getResult = &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1", AuthorID: client.UserID, Visibility: threads.VisibilityShared}
	ext := externalServer(store, client, true)
	assert.Equal(t, http.StatusForbidden, doThreadReq(t, ext, http.MethodPatch, "/api/v1/portal/threads/thr_1", map[string]any{"visibility": "internal"}).Code)
}

type fixedMentions []string

func (m fixedMentions) ResolveMentions(context.Context, string, string, string, string) []string {
	return m
}

// An internal thread mentions nobody with an external account (#2227).
func TestAnInternalThreadMentionsNoExternalPerson(t *testing.T) {
	owner := &access.User{UserID: "owner", Email: "owner@example.com"}
	mentions := fixedMentions{"client@example.org", "colleague@example.com"}
	for vis, want := range map[string][]string{
		threads.VisibilityInternal: {"colleague@example.com"},
		threads.VisibilityShared:   {"client@example.org", "colleague@example.com"},
	} {
		h := &Handler{cfg: Config{Mentions: mentions, ExternalAddress: func(_ context.Context, e string) bool { return e == "client@example.org" }}}
		got := h.resolveMentions(context.Background(), &threads.Thread{Visibility: vis}, "@client @colleague", owner.Email)
		assert.Equal(t, want, got, vis)
	}
}
