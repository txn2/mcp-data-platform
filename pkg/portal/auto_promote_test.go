package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoPromoteViewerCreatesViewerShare(t *testing.T) {
	shares := &mockShareStore{activeShare: nil}
	h := &Handler{deps: Deps{ShareStore: shares}}

	h.autoPromoteViewer(context.Background(), promoteTarget{targetTypeAsset, "asset_1", "owner", "owner@example.com"},
		&User{UserID: "u1", Email: "u1@example.com"})

	require.NotNil(t, shares.inserted)
	assert.Equal(t, "asset_1", shares.inserted.AssetID)
	assert.Equal(t, PermissionViewer, shares.inserted.Permission)
	assert.Equal(t, OriginPublicLinkLogin, shares.inserted.Origin)
	assert.Equal(t, "u1", shares.inserted.SharedWithUserID)
}

func TestAutoPromoteViewerSkipsWhenShareExists(t *testing.T) {
	// An existing share (e.g. editor) must never be downgraded → no insert.
	shares := &mockShareStore{activeShare: &Share{ID: "s1", Permission: PermissionEditor}}
	h := &Handler{deps: Deps{ShareStore: shares}}

	h.autoPromoteViewer(context.Background(), promoteTarget{targetTypeAsset, "asset_1", "owner", "owner@example.com"},
		&User{UserID: "u1", Email: "u1@example.com"})

	assert.Nil(t, shares.inserted)
}

func TestAutoPromoteViewerSkipsOwner(t *testing.T) {
	shares := &mockShareStore{}
	h := &Handler{deps: Deps{ShareStore: shares}}

	h.autoPromoteViewer(context.Background(), promoteTarget{targetTypeAsset, "asset_1", "u1", "u1@example.com"},
		&User{UserID: "u1", Email: "u1@example.com"})

	assert.Nil(t, shares.inserted)
}

func TestAutoPromoteViewerCollectionTarget(t *testing.T) {
	shares := &mockShareStore{}
	h := &Handler{deps: Deps{ShareStore: shares}}

	h.autoPromoteViewer(context.Background(), promoteTarget{targetTypeCollection, "col_1", "owner", "owner@example.com"},
		&User{UserID: "u1", Email: "u1@example.com"})

	require.NotNil(t, shares.inserted)
	assert.Equal(t, "col_1", shares.inserted.CollectionID)
}

func TestAutoPromoteViewerNilUserNoop(t *testing.T) {
	shares := &mockShareStore{}
	h := &Handler{deps: Deps{ShareStore: shares}}
	h.autoPromoteViewer(context.Background(), promoteTarget{targetTypeAsset, "asset_1", "owner", ""}, nil)
	assert.Nil(t, shares.inserted)
}

func TestAutoPromoteViewerLookupError(t *testing.T) {
	// A lookup failure is logged and skipped; no share is created.
	shares := &mockShareStore{activeShareErr: assert.AnError}
	h := &Handler{deps: Deps{ShareStore: shares}}
	h.autoPromoteViewer(context.Background(), promoteTarget{targetTypeAsset, "asset_1", "owner", "owner@example.com"},
		&User{UserID: "u1", Email: "u1@example.com"})
	assert.Nil(t, shares.inserted)
}

func TestAutoPromoteViewerInsertError(t *testing.T) {
	// Insert failure is best-effort: logged, never surfaced.
	shares := &mockShareStore{insertErr: assert.AnError}
	h := &Handler{deps: Deps{ShareStore: shares}}
	h.autoPromoteViewer(context.Background(), promoteTarget{targetTypeAsset, "asset_1", "owner", "owner@example.com"},
		&User{UserID: "u1", Email: "u1@example.com"})
	assert.Nil(t, shares.inserted) // mock records inserted only on success
}

func TestMaybeAutoPromoteViewerAnonymousNoop(t *testing.T) {
	// No authenticator → anonymous viewer → no promotion.
	shares := &mockShareStore{}
	h := &Handler{deps: Deps{ShareStore: shares}}
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok", http.NoBody)
	h.maybeAutoPromoteViewer(req, promoteTarget{targetTypeAsset, "asset_1", "owner", "owner@example.com"})
	assert.Nil(t, shares.inserted)
}

func TestResolvePublicViewerNilAuthenticator(t *testing.T) {
	h := &Handler{deps: Deps{}}
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok", http.NoBody)
	assert.Nil(t, h.resolvePublicViewer(req))
}

func TestSignInToLeaveFeedbackURL(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/abc123", http.NoBody)
	url := signInToLeaveFeedbackURL(req)
	assert.Contains(t, url, "/portal/auth/login?return_to=")
	assert.Contains(t, url, "%2Fportal%2Fview%2Fabc123")
}
