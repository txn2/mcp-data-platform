package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mw "github.com/txn2/mcp-data-platform/pkg/middleware"
)

// A share opened by a signed-in platform user who can open the target in the
// portal lands there instead of on the public page (#1473). These tests drive
// the real route through the gate, so what they exercise is the whole
// admission-then-promotion-then-landing sequence rather than the redirect
// decision on its own.

// redirectFixture assembles the public surface around one asset and one
// collection, both owned by ownerID, with the share the test opens. A non-nil
// viewer is who the authenticator resolves the request to.
type redirectFixture struct {
	handler *Handler
	shares  *mockShareStore
}

const (
	redirectOwnerID    = "u-owner"
	redirectOwnerEmail = "owner@example.com"
)

func newRedirectFixture(share *Share, viewer *User, opts ...func(*Deps, *mockShareStore)) redirectFixture {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: redirectOwnerID, Name: "Doc", ContentType: "text/plain",
		S3Bucket: "b1", S3Key: "assets/a1",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	coll := &Collection{
		ID: "c1", Name: "Coll", OwnerID: redirectOwnerID,
		Sections:  []CollectionSection{{Items: []CollectionItem{{AssetID: "a1"}}}},
		CreatedAt: now, UpdatedAt: now,
	}
	shares := &mockShareStore{getByTokenRes: share}

	deps := Deps{
		AssetStore:      &mockAssetStore{getAsset: asset},
		ShareStore:      shares,
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{getData: []byte("file content"), getCT: "text/plain"},
		S3Bucket:        "b1",
		RateLimit:       RateLimitConfig{RequestsPerMinute: 6000, BurstSize: 1000},
		// The deployment serves a portal application that admits this reader.
		// Every case that must NOT redirect overrides this below.
		PortalAppAdmits: func([]string) bool { return true },
	}
	if viewer != nil {
		deps.Authenticator = NewAuthenticator(&mockAuthenticator{
			info: &mw.UserInfo{UserID: viewer.UserID, Email: viewer.Email},
		})
	}
	for _, opt := range opts {
		opt(&deps, shares)
	}
	return redirectFixture{handler: NewHandler(deps, nil), shares: shares}
}

// open drives one GET on the public surface, signed in when viewer was set.
func (f redirectFixture) open(path string, signedIn bool) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	if signedIn {
		req.Header.Set("X-API-Key", "key")
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

// recipientShare is the restricted asset share bob is the recipient of.
func recipientShare() *Share {
	return &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: redirectOwnerEmail,
		SharedWithUserID: "u-bob", SharedWithEmail: "bob@example.com",
		AccessMode: AccessModeRestricted, NoticeText: defaultNoticeText,
	}
}

func bob() *User { return &User{UserID: "u-bob", Email: "bob@example.com"} }

func TestPublicViewSendsRecipientToTheAssetInTheirPortal(t *testing.T) {
	f := newRedirectFixture(recipientShare(), bob())

	w := f.open("/portal/view/tok1", true)

	require.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/portal/assets/a1?share=tok1", w.Header().Get("Location"))
	// The verdict is the caller's, never the URL's: a stored redirect would
	// answer the anonymous reader the public page exists for.
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestPublicViewSendsPublicLinkViewerToTheirPortalAndGrantsTheShare(t *testing.T) {
	share := &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, bob())

	w := f.open("/portal/view/tok1", true)

	require.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/portal/assets/a1?share=tok1", w.Header().Get("Location"))

	require.NotNil(t, f.shares.inserted, "the derived viewer share is what puts the asset in their portal")
	assert.Equal(t, "a1", f.shares.inserted.AssetID)
	assert.Equal(t, "u-bob", f.shares.inserted.SharedWithUserID)
	assert.Equal(t, PermissionViewer, f.shares.inserted.Permission)
	assert.Equal(t, OriginPublicLinkLogin, f.shares.inserted.Origin)
}

func TestPublicViewSendsOwnerToTheirPortalWithoutCreatingAShare(t *testing.T) {
	owner := &User{UserID: redirectOwnerID, Email: redirectOwnerEmail}
	share := &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, owner)

	w := f.open("/portal/view/tok1", true)

	require.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/portal/assets/a1?share=tok1", w.Header().Get("Location"))
	assert.Nil(t, f.shares.inserted, "the owner needs no share")
}

func TestPublicViewKeepsAnonymousReaderOnThePublicPage(t *testing.T) {
	share := &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, nil)

	w := f.open("/portal/view/tok1", false)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Doc")
	assert.Contains(t, body, htmlNoticeText)
	assert.Contains(t, body, "Sign in to leave feedback")
	assert.Nil(t, f.shares.inserted)
}

func TestPublicViewKeepsViewerOnThePublicPageWhenTheGrantFailed(t *testing.T) {
	// The promotion is best-effort and swallows its failures. A viewer whose
	// share insert failed has no page for this asset in their portal, so
	// sending them there would be sending them to a refusal.
	share := &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, bob(), func(_ *Deps, m *mockShareStore) { m.insertErr = assert.AnError })

	w := f.open("/portal/view/tok1", true)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Doc")
	// With no page of their own to offer, the portal link stays the root.
	assert.Contains(t, w.Body.String(), `href="/portal/"`)
}

func TestPublicViewKeepsViewerOnThePublicPageWhenTheLookupFailed(t *testing.T) {
	share := &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, bob(), func(_ *Deps, m *mockShareStore) { m.activeShareErr = assert.AnError })

	w := f.open("/portal/view/tok1", true)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Doc")
}

func TestPublicViewOffersTheAssetPageToAViewerWhoStaysOnThePublicPage(t *testing.T) {
	// The reader who followed the link back from their portal is signed in and
	// can open the asset there, so the page's portal link names the asset
	// rather than the portal root.
	share := &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, bob())

	w := f.open("/portal/view/tok1?public=1", true)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `href="/portal/assets/a1"`)
	assert.NotNil(t, f.shares.inserted, "asking for the public page does not skip the grant")
}

func TestPublicViewRendersInPlaceForAnEmbeddedRequest(t *testing.T) {
	share := &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, bob())

	w := f.open("/portal/view/tok1?embedded=1", true)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Doc")
}

func TestPublicViewCountsTheOpenItRedirects(t *testing.T) {
	f := newRedirectFixture(recipientShare(), bob())

	w := f.open("/portal/view/tok1", true)
	require.Equal(t, http.StatusFound, w.Code)

	// The counter is bumped from a goroutine detached from the request, so the
	// assertion waits for it rather than reading once.
	assert.Eventually(t, func() bool {
		ids := f.shares.incremented()
		return len(ids) == 1 && ids[0] == "s1"
	}, time.Second, 5*time.Millisecond)
}

func TestPublicCollectionViewSendsRecipientToTheCollectionInTheirPortal(t *testing.T) {
	share := &Share{
		ID: "s2", Token: "tok1", CollectionID: "c1", CreatedBy: redirectOwnerEmail,
		SharedWithUserID: "u-bob", SharedWithEmail: "bob@example.com",
		AccessMode: AccessModeRestricted, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, bob())

	w := f.open("/portal/view/tok1", true)

	require.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/portal/collections/c1?share=tok1", w.Header().Get("Location"))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.NotNil(t, f.shares.inserted)
	assert.Equal(t, "c1", f.shares.inserted.CollectionID)

	assert.Eventually(t, func() bool {
		ids := f.shares.incremented()
		return len(ids) == 1 && ids[0] == "s2"
	}, time.Second, 5*time.Millisecond)
}

func TestPublicCollectionViewKeepsAnonymousReaderOnThePublicPage(t *testing.T) {
	share := &Share{
		ID: "s2", Token: "tok1", CollectionID: "c1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, nil)

	w := f.open("/portal/view/tok1", false)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Coll")
}

func TestPublicCollectionViewKeepsViewerOnThePublicPageWhenTheGrantFailed(t *testing.T) {
	share := &Share{
		ID: "s2", Token: "tok1", CollectionID: "c1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, bob(), func(_ *Deps, m *mockShareStore) { m.insertErr = assert.AnError })

	w := f.open("/portal/view/tok1", true)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Coll")
}

func TestPublicViewKeepsAViewerThePortalWouldRefuseOnThePublicPage(t *testing.T) {
	// A share admits on the share's terms and says nothing about the portal:
	// the shell refuses an account no persona claims. Sending them there would
	// trade the asset they can read for a refusal.
	share := &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, bob(), func(d *Deps, _ *mockShareStore) {
		d.PortalAppAdmits = func([]string) bool { return false }
	})

	w := f.open("/portal/view/tok1", true)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Doc")
	assert.NotNil(t, f.shares.inserted, "the grant still stands for when they are given one")
}

func TestPublicViewKeepsEveryReaderOnThePublicPageWithNoPortalApplication(t *testing.T) {
	// A build with no frontend serves /portal/view/ and nothing at /portal/,
	// so a redirect would send a reader of a working share page to a route this
	// deployment does not have. The same holds for any embedder of this package
	// that serves the share viewer without the portal.
	share := &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: redirectOwnerEmail,
		AccessMode: AccessModePublic, NoticeText: defaultNoticeText,
	}
	f := newRedirectFixture(share, bob(), func(d *Deps, _ *mockShareStore) {
		d.PortalAppAdmits = nil
	})

	w := f.open("/portal/view/tok1", true)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Doc")
	// With no portal to offer, the page's own portal link stays the root.
	assert.Contains(t, w.Body.String(), `href="/portal/"`)
}

func TestPortalTargetPathEscapesTheIdentifierAndCarriesTheToken(t *testing.T) {
	tests := []struct {
		name    string
		section string
		id      string
		token   string
		want    string
	}{
		{"asset with token", portalAssetsSection, "a1", "tok1", "/portal/assets/a1?share=tok1"},
		{"collection with token", portalCollectionsSection, "c1", "tok1", "/portal/collections/c1?share=tok1"},
		{"no token", portalAssetsSection, "a1", "", "/portal/assets/a1"},
		// An identifier is a uuid in practice, but the path is built rather
		// than concatenated so a value carrying a separator cannot name a
		// different route than the one it was resolved for.
		{"identifier with a separator", portalAssetsSection, "a/1?x=2", "t", "/portal/assets/a%2F1%3Fx=2?share=t"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, portalTargetPath(tc.section, tc.id, tc.token))
		})
	}
}

func TestCanEnterPortal(t *testing.T) {
	user := &User{UserID: "u1", Email: "u1@example.com", Roles: []string{"analyst"}}

	t.Run("no portal application served", func(t *testing.T) {
		h := &Handler{deps: Deps{}}
		assert.False(t, h.canEnterPortal(user))
	})

	t.Run("the portal application admits them", func(t *testing.T) {
		h := &Handler{deps: Deps{PortalAppAdmits: func(roles []string) bool {
			return len(roles) > 0
		}}}
		assert.True(t, h.canEnterPortal(user))
	})

	t.Run("the portal application refuses them", func(t *testing.T) {
		h := &Handler{deps: Deps{PortalAppAdmits: func([]string) bool { return false }}}
		assert.False(t, h.canEnterPortal(user))
	})

	t.Run("no user", func(t *testing.T) {
		h := &Handler{deps: Deps{PortalAppAdmits: func([]string) bool { return true }}}
		assert.False(t, h.canEnterPortal(nil))
	})
}

func TestPortalOpenURLFallsBackToTheRootWhenTheTargetIsUnreachable(t *testing.T) {
	assert.Equal(t, portalAppPath, portalOpenURL(false, portalAssetsSection, "a1"))
	assert.Equal(t, "/portal/assets/a1", portalOpenURL(true, portalAssetsSection, "a1"))
}
