package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// This file drives the full #1001 guest journey through the assembled portal
// handler and its real public mux: landing page, one-time link request, claim,
// guest viewing, replay refusal, and revocation. Nothing calls a handler
// method directly; every request enters through Handler.ServeHTTP exactly as
// it would from a browser.

// memGuestLinks is an in-memory shareguest.LinkStore for the journey.
type memGuestLinks struct {
	mu    sync.Mutex
	links map[string]shareguest.Link
}

func newMemGuestLinks() *memGuestLinks { return &memGuestLinks{links: map[string]shareguest.Link{}} }

func (m *memGuestLinks) Insert(_ context.Context, l shareguest.Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[l.TokenHash] = l
	return nil
}

func (m *memGuestLinks) Claim(_ context.Context, tokenHash, shareID string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[tokenHash]
	if !ok || l.ShareID != shareID || l.UsedAt != nil || !l.ExpiresAt.After(now) {
		return false, nil
	}
	l.UsedAt = &now
	m.links[tokenHash] = l
	return true, nil
}

func (m *memGuestLinks) CountSince(_ context.Context, shareID string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, l := range m.links {
		if l.ShareID == shareID && l.CreatedAt.After(since) {
			n++
		}
	}
	return n, nil
}

// guestMail records the one-time-link emails the journey sends.
type guestMail struct {
	mu    sync.Mutex
	to    []string
	links []string
}

func (g *guestMail) send(_ context.Context, to, link string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.to = append(g.to, to)
	g.links = append(g.links, link)
	return nil
}

const guestJourneyBase = "https://platform.example.com"

// newGuestJourneyHandler assembles the portal handler with the share guest
// service wired the way the composition root wires it: the resolver reads the
// same share store the gate uses, so state changes (revocation) apply to both.
func newGuestJourneyHandler(share *Share) (*Handler, *mockShareStore, *guestMail) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "owner", Name: "Doc", ContentType: "text/plain",
		S3Bucket: "b1", S3Key: "assets/a1",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	shares := &mockShareStore{getByTokenRes: share}
	mail := &guestMail{}

	svc := shareguest.New(shareguest.Config{
		Resolve: func(ctx context.Context, token string) (shareguest.ShareInfo, bool) {
			s, err := shares.GetByToken(ctx, token)
			if err != nil || s == nil || s.Token != token {
				return shareguest.ShareInfo{}, false
			}
			return shareguest.ShareInfo{
				ID:             s.ID,
				Token:          s.Token,
				RecipientEmail: s.SharedWithEmail,
				Public:         s.AccessMode == shareaccess.ModePublic,
				Revoked:        s.Revoked,
				Expired:        s.ExpiresAt != nil && s.ExpiresAt.Before(time.Now()),
			}, true
		},
		Links:        newMemGuestLinks(),
		SendLink:     mail.send,
		SessionKey:   []byte("0123456789abcdef0123456789abcdef"),
		BaseURL:      guestJourneyBase,
		SecureCookie: true,
		Brand:        shareguest.Brand{Name: "ACME Data"},
	})

	deps := Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: shares,
		S3Client:   &mockS3Client{getData: []byte("file content"), getCT: "text/plain"},
		S3Bucket:   "b1",
		RateLimit:  RateLimitConfig{RequestsPerMinute: 6000, BurstSize: 1000},
		ShareGuest: svc,
	}
	return NewHandler(deps, nil), shares, mail
}

// emailShare is the restricted share the journey opens.
func emailShare() *Share {
	return &Share{
		ID: "sh1", AssetID: "a1", Token: "tok1", CreatedBy: "alice@example.com",
		SharedWithEmail: "bob@example.com", Permission: PermissionEditor,
		AccessMode: AccessModeRestricted,
	}
}

// browserGet performs a GET as a browser navigation, carrying cookies.
func browserGet(h *Handler, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestGuestJourney(t *testing.T) {
	h, shares, mail := newGuestJourneyHandler(emailShare())

	// 1. Anonymous browser navigation: branded landing page, not http.Error
	// text. It offers sign-in and the one-time link, and never shows the
	// recipient address.
	w := browserGet(h, "/portal/view/tok1", nil)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "text/html")
	body := w.Body.String()
	assert.Contains(t, body, "This item was shared privately")
	assert.Contains(t, body, "/portal/auth/login?return_to=%2Fportal%2Fview%2Ftok1")
	assert.Contains(t, body, "/portal/view/tok1/request-link")
	assert.NotContains(t, body, "bob@example.com")

	// Subresource fetches keep plain-text refusals.
	wc := httptest.NewRecorder()
	h.ServeHTTP(wc, httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/portal/view/tok1/content", http.NoBody))
	assert.Equal(t, http.StatusForbidden, wc.Code)
	assert.NotContains(t, wc.Header().Get("Content-Type"), "text/html")

	// 2. Request a one-time link: uniform response, email to the stored
	// address only.
	wr := httptest.NewRecorder()
	h.ServeHTTP(wr, httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/portal/view/tok1/request-link", http.NoBody))
	require.Equal(t, http.StatusOK, wr.Code)
	require.Len(t, mail.to, 1)
	assert.Equal(t, "bob@example.com", mail.to[0])
	claimPath := strings.TrimPrefix(mail.links[0], guestJourneyBase)
	require.True(t, strings.HasPrefix(claimPath, "/portal/view/tok1/guest?otk="))

	// 3. Claim the link: guest cookie plus redirect into the viewer.
	wg := browserGet(h, claimPath, nil)
	require.Equal(t, http.StatusSeeOther, wg.Code)
	assert.Equal(t, "/portal/view/tok1", wg.Header().Get("Location"))
	cookies := wg.Result().Cookies()
	require.Len(t, cookies, 1)

	// 4. View as guest: the page renders with the guest indicator, without
	// the portal feedback affordance, and triggers no auto-promotion even
	// though the share grants editor.
	wv := browserGet(h, "/portal/view/tok1", cookies)
	require.Equal(t, http.StatusOK, wv.Code)
	viewer := wv.Body.String()
	assert.Contains(t, viewer, "Viewing as guest")
	assert.NotContains(t, viewer, "Sign in to leave feedback")
	assert.NotContains(t, viewer, "Open this in your portal")
	assert.Nil(t, shares.inserted, "a guest session must never auto-promote to a derived share")

	// 5. Content downloads work for the guest.
	wd := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/portal/view/tok1/content", http.NoBody)
	req.AddCookie(cookies[0])
	h.ServeHTTP(wd, req)
	require.Equal(t, http.StatusOK, wd.Code)
	assert.Equal(t, "file content", wd.Body.String())
	// The guest cookie is the whole credential here, so these bytes may not be
	// stored anywhere a second caller can reach (#1070).
	assert.Equal(t, "private", wd.Header().Get("Cache-Control"))
	assert.Equal(t, "Cookie", wd.Header().Get("Vary"))

	// 6. Replaying the claimed link fails and lands back on the landing
	// page, which explains and re-offers the request button.
	wreplay := browserGet(h, claimPath, nil)
	require.Equal(t, http.StatusSeeOther, wreplay.Code)
	assert.Equal(t, "/portal/view/tok1?link=invalid", wreplay.Header().Get("Location"))
	winvalid := browserGet(h, "/portal/view/tok1?link=invalid", nil)
	assert.Contains(t, winvalid.Body.String(), "already used or has expired")
	assert.Contains(t, winvalid.Body.String(), "/portal/view/tok1/request-link")

	// 7. The guest cookie is not a portal session: the portal API still
	// refuses the caller.
	wapi := httptest.NewRecorder()
	apiReq := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/v1/portal/me", http.NoBody)
	apiReq.AddCookie(cookies[0])
	h.ServeHTTP(wapi, apiReq)
	assert.Equal(t, http.StatusUnauthorized, wapi.Code)

	// 8. The guest session is scoped to its share: another share's token
	// refuses the same cookie.
	other := emailShare()
	other.ID = "sh2"
	other.Token = "tok2"
	shares.getByTokenRes = other
	wother := browserGet(h, "/portal/view/tok2", cookies)
	assert.Equal(t, http.StatusForbidden, wother.Code)
	assert.NotContains(t, wother.Body.String(), "file content")

	// 9. Revoking the share kills the live guest session immediately.
	revoked := emailShare()
	revoked.Revoked = true
	shares.getByTokenRes = revoked
	wrev := browserGet(h, "/portal/view/tok1", cookies)
	assert.Equal(t, http.StatusGone, wrev.Code)
	assert.Contains(t, wrev.Body.String(), "revoked")
}

func TestGuestLandingOmitsLinkOfferForLinkShares(t *testing.T) {
	// An authenticated-mode share names nobody, so the landing page offers
	// sign-in but no one-time link.
	share := emailShare()
	share.SharedWithEmail = ""
	share.AccessMode = AccessModeAuthenticated
	h, _, mail := newGuestJourneyHandler(share)

	w := browserGet(h, "/portal/view/tok1", nil)
	require.Equal(t, http.StatusForbidden, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "/portal/auth/login")
	assert.NotContains(t, body, "request-link")

	// And a posted request quietly sends nothing.
	wr := httptest.NewRecorder()
	h.ServeHTTP(wr, httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/portal/view/tok1/request-link", http.NoBody))
	assert.Equal(t, http.StatusOK, wr.Code)
	assert.Empty(t, mail.to)
}

func TestGuestRoutesAbsentWithoutService(t *testing.T) {
	// Without a wired service the guest routes do not exist and denials stay
	// plain text, exactly as before #1001.
	h := shareGateHandler(emailShare(), nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/portal/view/tok1/request-link", http.NoBody))
	assert.Equal(t, http.StatusNotFound, w.Code)

	wv := browserGet(h, "/portal/view/tok1", nil)
	assert.Equal(t, http.StatusForbidden, wv.Code)
	assert.NotContains(t, wv.Header().Get("Content-Type"), "text/html")
}
