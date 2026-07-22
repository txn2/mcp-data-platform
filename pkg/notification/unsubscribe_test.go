package notification

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var unsubKey = []byte("unsubscribe-test-key-32-bytes-ok")

func TestUnsubTokenRoundTrip(t *testing.T) {
	tok := UnsubToken(unsubKey, "Bob@Example.com ")
	email, ok := VerifyUnsubToken(unsubKey, tok)
	require.True(t, ok)
	assert.Equal(t, "bob@example.com", email, "the token canonicalizes to the prefs store's keying")
}

func TestVerifyUnsubTokenRejectsForgery(t *testing.T) {
	tok := UnsubToken(unsubKey, "bob@example.com")

	_, ok := VerifyUnsubToken([]byte("a-different-key-32-bytes-long!!!"), tok)
	assert.False(t, ok, "a token minted under another key must not verify")

	_, ok = VerifyUnsubToken(unsubKey, tok+"x")
	assert.False(t, ok)

	_, ok = VerifyUnsubToken(unsubKey, "no-separator")
	assert.False(t, ok)

	_, ok = VerifyUnsubToken(unsubKey, "!!!.###")
	assert.False(t, ok, "undecodable segments must not verify")

	// Swapping in a different address under the same MAC must fail.
	other := UnsubToken(unsubKey, "carol@example.com")
	_, macPart, _ := strings.Cut(tok, ".")
	otherEmailPart, _, _ := strings.Cut(other, ".")
	_, ok = VerifyUnsubToken(unsubKey, otherEmailPart+"."+macPart)
	assert.False(t, ok)
}

// memPrefsStore records Set calls for handler tests.
type memPrefsStore struct {
	mu     sync.Mutex
	set    map[string]Prefs
	setErr error
}

func newMemPrefsStore() *memPrefsStore { return &memPrefsStore{set: map[string]Prefs{}} }

func (m *memPrefsStore) Get(_ context.Context, email string) (Prefs, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.set[email]; ok {
		return p, nil
	}
	return DefaultPrefs(email), nil
}

func (m *memPrefsStore) Set(ctx context.Context, email string, u PrefsUpdate) (Prefs, error) {
	if m.setErr != nil {
		return Prefs{}, m.setErr
	}
	current, _ := m.Get(ctx, email)
	if u.Mode != nil {
		current.Mode = *u.Mode
	}
	if u.SharesEnabled != nil {
		current.SharesEnabled = *u.SharesEnabled
	}
	if u.CommentsEnabled != nil {
		current.CommentsEnabled = *u.CommentsEnabled
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.set[email] = current
	return current, nil
}

func unsubRequest(tok string) *http.Request {
	url := "/portal/notifications/unsubscribe"
	if tok != "" {
		url += "?tok=" + tok
	}
	return httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
}

// confirmRequest builds the POST the confirmation page's form submits: same
// URL as the GET, no one-click body.
func confirmRequest(tok string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/portal/notifications/unsubscribe?tok="+tok, strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// TestUnsubscribeHandlerGetDoesNotMutate pins the scanner-prefetch guard
// (#1022): mail security layers GET every URL in a message body, so the GET
// must render the confirmation form and write nothing.
func TestUnsubscribeHandlerGetDoesNotMutate(t *testing.T) {
	prefs := newMemPrefsStore()
	h := &UnsubscribeHandler{Prefs: prefs, Key: unsubKey, BrandName: "ACME Data"}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, unsubRequest(UnsubToken(unsubKey, "bob@example.com")))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `<form method="post"`)
	assert.Contains(t, w.Body.String(), "bob@example.com", "the page names the address it would opt out")
	assert.Contains(t, w.Body.String(), "ACME Data")
	assert.Contains(t, w.Header().Get("Content-Security-Policy"), "form-action 'self'")
	assert.Empty(t, prefs.set, "a bare GET must never record the opt-out")
}

// TestUnsubscribeHandlerConfirmPostOptsOut proves the deliberate click works:
// the form POST records the opt-out and renders the confirmation page.
func TestUnsubscribeHandlerConfirmPostOptsOut(t *testing.T) {
	prefs := newMemPrefsStore()
	h := &UnsubscribeHandler{Prefs: prefs, Key: unsubKey, BrandName: "ACME Data"}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, confirmRequest(UnsubToken(unsubKey, "bob@example.com")))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "You are unsubscribed")
	assert.Contains(t, w.Body.String(), "ACME Data")
	assert.NotContains(t, w.Body.String(), "<form", "the confirmation page offers no second form")

	stored, err := prefs.Get(context.Background(), "bob@example.com")
	require.NoError(t, err)
	assert.Equal(t, ModeOff, stored.Mode, "a confirmed form POST writes delivery mode off")
}

func TestUnsubscribeHandlerConfirmPostRejectsBadToken(t *testing.T) {
	prefs := newMemPrefsStore()
	h := &UnsubscribeHandler{Prefs: prefs, Key: unsubKey}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, confirmRequest("not-a-token"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not valid", "a browser POST gets a page, not a bare status")
	assert.Empty(t, prefs.set, "an invalid token writes nothing")
}

func TestUnsubscribeHandlerRejectsBadToken(t *testing.T) {
	prefs := newMemPrefsStore()
	h := &UnsubscribeHandler{Prefs: prefs, Key: unsubKey}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, unsubRequest("not-a-token"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not valid")
	assert.Empty(t, prefs.set, "an invalid token writes nothing")
}

// oneClickRequest builds the RFC 8058 POST a mail provider sends: the token
// URL from the List-Unsubscribe header with the fixed form body.
func oneClickRequest(tok string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/portal/notifications/unsubscribe?tok="+tok,
		strings.NewReader("List-Unsubscribe=One-Click"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// TestUnsubscribeHandlerOneClickPost proves the RFC 8058 path records the
// opt-out with no page: the caller is a mail provider, not a browser.
func TestUnsubscribeHandlerOneClickPost(t *testing.T) {
	prefs := newMemPrefsStore()
	h := &UnsubscribeHandler{Prefs: prefs, Key: unsubKey, BrandName: "ACME Data"}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, oneClickRequest(UnsubToken(unsubKey, "bob@example.com")))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "<html", "one-click must not return a page requiring interaction")

	stored, err := prefs.Get(context.Background(), "bob@example.com")
	require.NoError(t, err)
	assert.Equal(t, ModeOff, stored.Mode, "a one-click POST writes delivery mode off")
}

func TestUnsubscribeHandlerOneClickRejectsBadToken(t *testing.T) {
	prefs := newMemPrefsStore()
	h := &UnsubscribeHandler{Prefs: prefs, Key: unsubKey}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, oneClickRequest("not-a-token"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, prefs.set, "an invalid token writes nothing")
}

func TestUnsubscribeHandlerOneClickReportsStoreFailure(t *testing.T) {
	prefs := newMemPrefsStore()
	prefs.setErr = errors.New("db down")
	h := &UnsubscribeHandler{Prefs: prefs, Key: unsubKey}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, oneClickRequest(UnsubToken(unsubKey, "bob@example.com")))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUnsubscribeHandlerReportsStoreFailure(t *testing.T) {
	prefs := newMemPrefsStore()
	prefs.setErr = errors.New("db down")
	h := &UnsubscribeHandler{Prefs: prefs, Key: unsubKey}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, confirmRequest(UnsubToken(unsubKey, "bob@example.com")))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "could not be recorded")
}
