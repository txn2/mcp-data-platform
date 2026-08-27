package shareguest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKey is a 32-byte master (browser-session) signing key.
var testKey = []byte("0123456789abcdef0123456789abcdef")

// memLinkStore is an in-memory LinkStore for tests.
type memLinkStore struct {
	mu        sync.Mutex
	links     map[string]*Link // by token hash
	insertErr error
	claimErr  error
	countErr  error
}

func newMemLinkStore() *memLinkStore {
	return &memLinkStore{links: map[string]*Link{}}
}

func (m *memLinkStore) Insert(_ context.Context, l Link) error {
	if m.insertErr != nil {
		return m.insertErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[l.TokenHash] = &l
	return nil
}

func (m *memLinkStore) Claim(_ context.Context, tokenHash, shareID string, now time.Time) (bool, error) {
	if m.claimErr != nil {
		return false, m.claimErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[tokenHash]
	if !ok || l.ShareID != shareID || l.UsedAt != nil || !l.ExpiresAt.After(now) {
		return false, nil
	}
	l.UsedAt = &now
	return true, nil
}

func (m *memLinkStore) CountSince(_ context.Context, shareID string, since time.Time) (int, error) {
	if m.countErr != nil {
		return 0, m.countErr
	}
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

// mailRecorder captures sent one-time-link emails.
type mailRecorder struct {
	mu    sync.Mutex
	to    []string
	links []string
	err   error
}

func (m *mailRecorder) send(_ context.Context, to, link string) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.to = append(m.to, to)
	m.links = append(m.links, link)
	return nil
}

func (m *mailRecorder) sent() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.to)
}

// fixtureShare is the qualifying email share most tests resolve to.
func fixtureShare() ShareInfo {
	return ShareInfo{ID: "sh1", Token: "tok1", RecipientEmail: "bob@example.com"}
}

// newTestService builds a fully enabled service over the given share fixture.
func newTestService(t *testing.T, share ShareInfo, store LinkStore, mail *mailRecorder) *Service {
	t.Helper()
	return New(Config{
		Resolve: func(_ context.Context, token string) (ShareInfo, bool) {
			if token == share.Token {
				return share, true
			}
			return ShareInfo{}, false
		},
		Links:        store,
		SendLink:     mail.send,
		SessionKey:   testKey,
		BaseURL:      "https://platform.example.com",
		SecureCookie: true,
		Brand:        Brand{Name: "ACME Data"},
	})
}

func TestDeriveKeyDomainSeparation(t *testing.T) {
	a := DeriveKey(testKey, "label-a")
	b := DeriveKey(testKey, "label-b")
	assert.Len(t, a, 32)
	assert.NotEqual(t, a, b, "different labels must derive different keys")
	assert.Equal(t, a, DeriveKey(testKey, "label-a"), "derivation must be deterministic")
	assert.NotEqual(t, a, testKey, "derived key must differ from the master")
}

func TestLinksAvailable(t *testing.T) {
	mail := &mailRecorder{}
	full := newTestService(t, fixtureShare(), newMemLinkStore(), mail)
	assert.True(t, full.LinksAvailable())

	var nilSvc *Service
	assert.False(t, nilSvc.LinksAvailable(), "nil service is inert")

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"no store", func(c *Config) { c.Links = nil }},
		{"no mailer", func(c *Config) { c.SendLink = nil }},
		{"no key", func(c *Config) { c.SessionKey = nil }},
		{"no base URL", func(c *Config) { c.BaseURL = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Resolve:    func(context.Context, string) (ShareInfo, bool) { return fixtureShare(), true },
				Links:      newMemLinkStore(),
				SendLink:   mail.send,
				SessionKey: testKey,
				BaseURL:    "https://platform.example.com",
			}
			tt.mutate(&cfg)
			assert.False(t, New(cfg).LinksAvailable())
		})
	}
}

func TestMintOTK(t *testing.T) {
	tok, hash, err := mintOTK()
	require.NoError(t, err)
	assert.Len(t, tok, 64, "32 bytes hex-encoded")
	assert.Equal(t, hashOTK(tok), hash)

	tok2, hash2, err := mintOTK()
	require.NoError(t, err)
	assert.NotEqual(t, tok, tok2)
	assert.NotEqual(t, hash, hash2)
}

func TestGuestSessionRoundTrip(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	signed, err := svc.signGuestSession("sh1", "bob@example.com")
	require.NoError(t, err)

	sid, email, err := svc.verifyGuestSession(signed)
	require.NoError(t, err)
	assert.Equal(t, "sh1", sid)
	assert.Equal(t, "bob@example.com", email)
}

func TestGuestSessionRejectsTampering(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	signed, err := svc.signGuestSession("sh1", "bob@example.com")
	require.NoError(t, err)

	// A token signed under a different master key must not verify.
	other := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	other.guestKey = DeriveKey([]byte("another-master-key-32-bytes-long"), guestKeyLabel)
	_, _, err = other.verifyGuestSession(signed)
	assert.Error(t, err)

	// A mangled token must not verify.
	_, _, err = svc.verifyGuestSession(signed + "x")
	assert.Error(t, err)
}

func TestGuestSessionExpires(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	svc.now = func() time.Time { return time.Now().Add(-GuestSessionTTL - time.Minute) }
	signed, err := svc.signGuestSession("sh1", "bob@example.com")
	require.NoError(t, err)

	svc.now = time.Now
	_, _, err = svc.verifyGuestSession(signed)
	assert.Error(t, err, "a guest session past its signed expiry must not verify")
}

// admitReq builds a request carrying the given guest cookie value.
func admitReq(t *testing.T, cookie string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/view/tok1", http.NoBody)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: guestCookieName, Value: cookie})
	}
	return r
}

func TestAdmit(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	signed, err := svc.signGuestSession("sh1", "bob@example.com")
	require.NoError(t, err)

	guest := svc.Admit(admitReq(t, signed), "sh1")
	require.NotNil(t, guest)
	assert.Equal(t, "bob@example.com", guest.Email)

	assert.Nil(t, svc.Admit(admitReq(t, signed), "other-share"),
		"a guest session admits only the share it was issued for")
	assert.Nil(t, svc.Admit(admitReq(t, ""), "sh1"), "no cookie, no guest")
	assert.Nil(t, svc.Admit(admitReq(t, "garbage"), "sh1"))

	var nilSvc *Service
	assert.Nil(t, nilSvc.Admit(admitReq(t, signed), "sh1"), "nil service admits nobody")
}

// requestLink drives POST /portal/view/{token}/request-link through a mux so
// PathValue is populated, returning the recorder.
func requestLink(svc *Service, token string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /portal/view/{token}/request-link", svc.HandleRequestLink)
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/portal/view/"+token+"/request-link", http.NoBody)
	mux.ServeHTTP(w, r)
	return w
}

func TestHandleRequestLinkUniformResponse(t *testing.T) {
	live := fixtureShare()
	tests := []struct {
		name     string
		share    ShareInfo
		known    bool
		wantSent int
	}{
		{"qualifying email share sends", live, true, 1},
		{"unknown token sends nothing", live, false, 0},
		{"revoked share sends nothing", ShareInfo{ID: "sh1", Token: "tok1", RecipientEmail: "bob@example.com", Revoked: true}, true, 0},
		{"expired share sends nothing", ShareInfo{ID: "sh1", Token: "tok1", RecipientEmail: "bob@example.com", Expired: true}, true, 0},
		{"public share sends nothing", ShareInfo{ID: "sh1", Token: "tok1", RecipientEmail: "bob@example.com", Public: true}, true, 0},
		{"link share with no recipient sends nothing", ShareInfo{ID: "sh1", Token: "tok1"}, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mail := &mailRecorder{}
			svc := newTestService(t, tt.share, newMemLinkStore(), mail)
			token := tt.share.Token
			if !tt.known {
				token = "unknown"
			}
			w := requestLink(svc, token)

			// The response is byte-identical across every case.
			assert.Equal(t, http.StatusOK, w.Code)
			var body map[string]string
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, uniformResponse, body["message"])
			assert.Equal(t, tt.wantSent, mail.sent())
		})
	}
}

func TestHandleRequestLinkSendsOnlyToStoredAddress(t *testing.T) {
	mail := &mailRecorder{}
	store := newMemLinkStore()
	svc := newTestService(t, fixtureShare(), store, mail)

	requestLink(svc, "tok1")
	require.Equal(t, 1, mail.sent())
	assert.Equal(t, "bob@example.com", mail.to[0])
	assert.Contains(t, mail.links[0], "https://platform.example.com/portal/view/tok1/guest?otk=")

	// The plaintext token is never stored; only its hash is.
	otk := strings.TrimPrefix(mail.links[0], "https://platform.example.com/portal/view/tok1/guest?otk=")
	_, plaintextStored := store.links[otk]
	assert.False(t, plaintextStored)
	_, hashStored := store.links[hashOTK(otk)]
	assert.True(t, hashStored)
}

func TestHandleRequestLinkPerShareCap(t *testing.T) {
	mail := &mailRecorder{}
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), mail)

	for range maxLinksPerWindow + 3 {
		w := requestLink(svc, "tok1")
		assert.Equal(t, http.StatusOK, w.Code, "capped requests still answer uniformly")
	}
	assert.Equal(t, maxLinksPerWindow, mail.sent(), "issue cap bounds sends per share per window")
}

func TestHandleRequestLinkStoreFailuresStaySilent(t *testing.T) {
	for name, store := range map[string]*memLinkStore{
		"count fails":  {links: map[string]*Link{}, countErr: errors.New("db down")},
		"insert fails": {links: map[string]*Link{}, insertErr: errors.New("db down")},
	} {
		t.Run(name, func(t *testing.T) {
			mail := &mailRecorder{}
			svc := newTestService(t, fixtureShare(), store, mail)
			w := requestLink(svc, "tok1")
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, 0, mail.sent())
		})
	}
}

func TestHandleRequestLinkSendFailureStaysSilent(t *testing.T) {
	mail := &mailRecorder{err: errors.New("smtp down")}
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), mail)
	w := requestLink(svc, "tok1")
	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, uniformResponse, body["message"])
}

// claimLink drives GET /portal/view/{token}/guest?otk=... through a mux.
func claimLink(svc *Service, token, otk string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /portal/view/{token}/guest", svc.HandleClaim)
	w := httptest.NewRecorder()
	url := "/portal/view/" + token + "/guest"
	if otk != "" {
		url += "?otk=" + otk
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	mux.ServeHTTP(w, r)
	return w
}

// issueOTK requests a link and extracts the plaintext one-time token from the
// recorded email.
func issueOTK(t *testing.T, svc *Service, mail *mailRecorder) string {
	t.Helper()
	requestLink(svc, "tok1")
	require.Equal(t, 1, mail.sent())
	_, otk, found := strings.Cut(mail.links[0], "otk=")
	require.True(t, found)
	return otk
}

func TestHandleClaimOpensGuestSessionOnce(t *testing.T) {
	mail := &mailRecorder{}
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), mail)
	otk := issueOTK(t, svc, mail)

	// First claim: guest cookie + redirect into the viewer.
	w := claimLink(svc, "tok1", otk)
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/portal/view/tok1", w.Header().Get("Location"))
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, guestCookieName, cookies[0].Name)
	assert.True(t, cookies[0].HttpOnly)
	assert.True(t, cookies[0].Secure)
	assert.Equal(t, guestCookiePath, cookies[0].Path)
	assert.Equal(t, 0, cookies[0].MaxAge, "browser-session cookie: expiry lives in the signature")

	// The cookie admits exactly this share.
	guest := svc.Admit(admitReq(t, cookies[0].Value), "sh1")
	require.NotNil(t, guest)

	// Replay: the link is dead, and the caller lands back on the landing page.
	w2 := claimLink(svc, "tok1", otk)
	assert.Equal(t, http.StatusSeeOther, w2.Code)
	assert.Equal(t, "/portal/view/tok1?link=invalid", w2.Header().Get("Location"))
	assert.Empty(t, w2.Result().Cookies(), "a replayed link opens no session")
}

func TestHandleClaimRejectsExpiredOTK(t *testing.T) {
	mail := &mailRecorder{}
	store := newMemLinkStore()
	svc := newTestService(t, fixtureShare(), store, mail)
	otk := issueOTK(t, svc, mail)

	// Advance past the link TTL.
	svc.now = func() time.Time { return time.Now().Add(LinkTTL + time.Minute) }
	w := claimLink(svc, "tok1", otk)
	assert.Equal(t, "/portal/view/tok1?link=invalid", w.Header().Get("Location"))
}

func TestHandleClaimRejectsMissingAndForeignOTK(t *testing.T) {
	mail := &mailRecorder{}
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), mail)
	otk := issueOTK(t, svc, mail)

	w := claimLink(svc, "tok1", "")
	assert.Equal(t, "/portal/view/tok1?link=invalid", w.Header().Get("Location"))

	// A token claimed against a different share's viewer token fails: the
	// resolver does not know that token.
	w = claimLink(svc, "other", otk)
	assert.Equal(t, "/portal/view/other?link=invalid", w.Header().Get("Location"))
}

func TestHandleClaimRejectsDeadShare(t *testing.T) {
	mail := &mailRecorder{}
	store := newMemLinkStore()
	live := fixtureShare()
	svc := newTestService(t, live, store, mail)
	otk := issueOTK(t, svc, mail)

	// Revoke the share after the link was issued: the claim must fail even
	// though the link row itself is valid.
	revoked := live
	revoked.Revoked = true
	svc.resolve = func(context.Context, string) (ShareInfo, bool) { return revoked, true }
	w := claimLink(svc, "tok1", otk)
	assert.Equal(t, "/portal/view/tok1?link=invalid", w.Header().Get("Location"))
}

// denyReq builds a browser navigation (Accept: text/html) for the landing page.
func denyReq(query string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/view/tok1"+query, http.NoBody)
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	return r
}

func TestDenyRendersLandingPageForAnonymous(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), Denial{
		Status:         http.StatusForbidden,
		Message:        "sign in required",
		Token:          "tok1",
		RecipientEmail: "bob@example.com",
	})

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	body := w.Body.String()
	assert.Contains(t, body, "This item was shared privately")
	assert.Contains(t, body, "/portal/auth/login?return_to=%2Fportal%2Fview%2Ftok1")
	assert.Contains(t, body, "/portal/view/tok1/request-link")
	assert.Contains(t, body, "ACME Data")
	assert.NotContains(t, body, "bob@example.com", "the page must never display the recipient address")
}

func TestDenyOmitsLinkOfferWhenShareNamesNobody(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), Denial{Status: http.StatusForbidden, Message: "sign in required", Token: "tok1"})

	body := w.Body.String()
	assert.Contains(t, body, "/portal/auth/login")
	assert.NotContains(t, body, "request-link")
}

func TestDenyOmitsLinkOfferWhenLinksUnavailable(t *testing.T) {
	svc := New(Config{
		Resolve: func(context.Context, string) (ShareInfo, bool) { return fixtureShare(), true },
		Brand:   Brand{Name: "ACME Data"},
	})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), Denial{
		Status:         http.StatusForbidden,
		Message:        "sign in required",
		Token:          "tok1",
		RecipientEmail: "bob@example.com",
	})
	assert.NotContains(t, w.Body.String(), "request-link",
		"without store/mailer/key the page must not offer a link it cannot send")
}

func TestDenyWrongAccountNamesSignedInIdentity(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), Denial{
		Status:         http.StatusForbidden,
		Message:        "not the recipient",
		Token:          "tok1",
		RecipientEmail: "bob@example.com",
		SignedInEmail:  "carol@example.com",
	})

	body := w.Body.String()
	assert.Contains(t, body, "carol@example.com", "naming the signed-in identity makes the wrong-account case self-diagnosing")
	assert.NotContains(t, body, "bob@example.com")
	assert.Contains(t, body, "/portal/auth/logout")
	assert.NotContains(t, body, "request-link", "a signed-in caller is not the guest-link audience")
}

func TestDenyGoneRendersNoActions(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), Denial{
		Status:         http.StatusGone,
		Message:        "This share link has been revoked.",
		Token:          "tok1",
		RecipientEmail: "bob@example.com",
	})

	assert.Equal(t, http.StatusGone, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "no longer available")
	assert.Contains(t, body, "This share link has been revoked.")
	assert.NotContains(t, body, "request-link")
	assert.NotContains(t, body, "/portal/auth/login")
}

func TestDenyShowsInvalidLinkNotice(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq("?link=invalid"), Denial{
		Status:         http.StatusForbidden,
		Message:        "sign in required",
		Token:          "tok1",
		RecipientEmail: "bob@example.com",
	})
	assert.Contains(t, w.Body.String(), "already used or has expired")
}

func TestHandleClaimWithLinksDisabled(t *testing.T) {
	// A service without the link flow (no key/store/mailer) refuses every
	// claim instead of panicking or admitting.
	svc := New(Config{
		Resolve: func(context.Context, string) (ShareInfo, bool) { return fixtureShare(), true },
	})
	w := claimLink(svc, "tok1", "deadbeef")
	assert.Equal(t, "/portal/view/tok1?link=invalid", w.Header().Get("Location"))
}

func TestHandleClaimStoreErrorRefuses(t *testing.T) {
	mail := &mailRecorder{}
	store := newMemLinkStore()
	svc := newTestService(t, fixtureShare(), store, mail)
	otk := issueOTK(t, svc, mail)

	store.claimErr = errors.New("db down")
	w := claimLink(svc, "tok1", otk)
	assert.Equal(t, "/portal/view/tok1?link=invalid", w.Header().Get("Location"))
	assert.Empty(t, w.Result().Cookies())
}

func TestVerifyGuestSessionRejectsMalformedClaims(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})

	// A structurally valid token whose sid claim is empty must not admit.
	signed, err := svc.signGuestSession("", "bob@example.com")
	require.NoError(t, err)
	_, _, err = svc.verifyGuestSession(signed)
	assert.Error(t, err, "a guest session naming no share is invalid")

	// A completely different token format fails to parse.
	_, _, err = svc.verifyGuestSession("aaa.bbb.ccc")
	assert.Error(t, err)
}

func TestDenyDefaultBrandName(t *testing.T) {
	svc := New(Config{Resolve: func(context.Context, string) (ShareInfo, bool) { return fixtureShare(), true }})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), Denial{Status: http.StatusForbidden, Message: "m", Token: "tok1"})
	assert.Contains(t, w.Body.String(), "MCP Data Platform",
		"an unset brand falls back to the platform default, matching the public viewer")
}

// A logo arrives as an <img> sourced at the operator's URL; the landing page
// must insert it as markup, and its policy must admit that origin (#1500).
func TestDenyRendersLinkedLogos(t *testing.T) {
	const brandImg = `<img src="https://cdn.example.com/logo.png" alt="ACME Data">`
	const implImg = `<img src="https://img.example.net/badge.png" alt="ACME Corp">`

	svc := New(Config{
		Resolve: func(context.Context, string) (ShareInfo, bool) { return fixtureShare(), true },
		Brand: Brand{
			Name:                "ACME Data",
			LogoHTML:            brandImg,
			ImplementorName:     "ACME Corp",
			ImplementorLogoHTML: implImg,
			ImageSources:        []string{"https://cdn.example.com", "https://img.example.net"},
		},
	})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), Denial{Status: http.StatusForbidden, Message: "m", Token: "tok1"})

	body := w.Body.String()
	assert.Contains(t, body, brandImg, "brand logo must render as markup")
	assert.Contains(t, body, implImg, "implementor logo must render as markup")
	csp := w.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "img-src data: https://cdn.example.com https://img.example.net;",
		"the policy must admit both logo origins under img-src")
	assert.Contains(t, csp, "connect-src 'self'",
		"the directives after img-src must survive the addition")
}

// A deployment with no logo configured must not have its policy widened.
func TestDenyPolicyUnchangedWithoutLogos(t *testing.T) {
	svc := New(Config{
		Resolve: func(context.Context, string) (ShareInfo, bool) { return fixtureShare(), true },
		Brand:   Brand{Name: "ACME Data"},
	})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), Denial{Status: http.StatusForbidden, Message: "m", Token: "tok1"})

	assert.Contains(t, w.Header().Get("Content-Security-Policy"), "img-src data:; connect-src 'self'",
		"a deployment with no logo must load no remote image")
}

func TestDenyNonHTMLKeepsPlainText(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/view/tok1/content", http.NoBody)
	svc.Deny(w, r, Denial{Status: http.StatusForbidden, Message: "sign in required", Token: "tok1"})

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NotContains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "sign in required\n", w.Body.String())
}

// A deployment that sets an implementor logo and no implementor name gets the
// logo on the landing page, matching the public viewer (#1507). The previous
// gate keyed on the name alone, so the page rendered nothing while its policy
// still admitted the logo's origin.
func TestDenyRendersLogoOnlyImplementor(t *testing.T) {
	const implImg = `<img src="https://img.example.net/badge.png" alt="">`

	svc := New(Config{
		Resolve: func(context.Context, string) (ShareInfo, bool) { return fixtureShare(), true },
		Brand: Brand{
			Name:                "ACME Data",
			ImplementorLogoHTML: implImg,
			ImageSources:        []string{"https://img.example.net"},
			// ImplementorName intentionally empty.
		},
	})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), Denial{Status: http.StatusForbidden, Message: "m", Token: "tok1"})

	body := w.Body.String()
	assert.Contains(t, body, implImg, "the implementor logo must render with no name configured")
	assert.Contains(t, body, `class="brand muted-brand"`, "the implementor block must render")
	// The platform brand also uses brand-name, so constrain the search to the
	// implementor block: an empty name must emit no span at all.
	implBlock := body
	if before, _, found := strings.Cut(body, `<div class="spacer">`); found {
		implBlock = before
	}
	assert.NotContains(t, implBlock, `<span class="brand-name">`,
		"an empty implementor name must not produce a brand-name span")
	assert.Contains(t, w.Header().Get("Content-Security-Policy"), "https://img.example.net",
		"a page whose policy admits the logo's origin is a page that renders it")
}

// The name-only case predates #1507 and must survive the widened gate: a
// rewrite to AND would hide the block for every deployment that sets a name
// and no logo.
func TestDenyRendersNameOnlyImplementor(t *testing.T) {
	svc := New(Config{
		Resolve: func(context.Context, string) (ShareInfo, bool) { return fixtureShare(), true },
		Brand: Brand{
			Name:            "ACME Data",
			ImplementorName: "ACME Corp",
			// ImplementorLogoHTML intentionally empty.
		},
	})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), Denial{Status: http.StatusForbidden, Message: "m", Token: "tok1"})

	body := w.Body.String()
	assert.Contains(t, body, `<span class="brand-name">ACME Corp</span>`,
		"the implementor name must render with no logo configured")
	implBlock := body
	if before, _, found := strings.Cut(body, `<div class="spacer">`); found {
		implBlock = before
	}
	assert.NotContains(t, implBlock, `class="brand-logo"`,
		"an empty implementor logo must not produce a logo slot")
}
