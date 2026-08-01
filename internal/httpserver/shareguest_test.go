package httpserver

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/internal/httpserver/unsubhttp"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// testSigningKeyB64 is a base64-encoded 32-byte browser-session signing key.
var testSigningKeyB64 = base64.StdEncoding.EncodeToString(
	[]byte("0123456789abcdef0123456789abcdef"))

// guestTestConfig returns a platform config with browser sessions and a
// public base URL, the two inputs the guest and unsubscribe wiring key off.
func guestTestConfig() *platform.Config {
	cfg := &platform.Config{}
	cfg.Auth.BrowserSession.Enabled = true
	cfg.Auth.BrowserSession.SigningKey = testSigningKeyB64
	cfg.Portal.PublicBaseURL = "https://platform.example.com"
	return cfg
}

func TestBrowserSessionSigningKey(t *testing.T) {
	if browserSessionSigningKey(nil) != nil {
		t.Error("nil platform must yield no key")
	}

	p := newTestPlatform(t, &platform.Config{})
	defer func() { _ = p.Close() }()
	if browserSessionSigningKey(p) != nil {
		t.Error("disabled browser sessions must yield no key")
	}

	p2 := newTestPlatform(t, guestTestConfig())
	defer func() { _ = p2.Close() }()
	key := browserSessionSigningKey(p2)
	if string(key) != "0123456789abcdef0123456789abcdef" {
		t.Errorf("unexpected decoded key: %q", key)
	}

	bad := guestTestConfig()
	bad.Auth.BrowserSession.SigningKey = "%%% not base64 %%%"
	p3 := newTestPlatform(t, bad)
	defer func() { _ = p3.Close() }()
	if browserSessionSigningKey(p3) != nil {
		t.Error("an undecodable key must yield nil, not garbage")
	}
}

// fakeShareReader answers GetByToken from a fixed share.
type fakeShareReader struct {
	share *portal.Share
	err   error
}

func (f *fakeShareReader) GetByToken(context.Context, string) (*portal.Share, error) {
	return f.share, f.err
}

func TestShareGuestResolver(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	share := &portal.Share{
		ID: "sh1", Token: "tok1", SharedWithEmail: "bob@example.com",
		AccessMode: shareaccess.ModeRestricted, ExpiresAt: &past, Revoked: true,
	}
	resolve := shareGuestResolver(&fakeShareReader{share: share})

	info, ok := resolve(context.Background(), "tok1")
	if !ok {
		t.Fatal("expected resolution")
	}
	if info.ID != "sh1" || info.RecipientEmail != "bob@example.com" || !info.Revoked || !info.Expired || info.Public {
		t.Errorf("unexpected info: %+v", info)
	}

	pub := shareGuestResolver(&fakeShareReader{share: &portal.Share{ID: "sh2", Token: "t", AccessMode: shareaccess.ModePublic}})
	if info, _ := pub(context.Background(), "t"); !info.Public {
		t.Error("public mode must map to Public")
	}

	if _, ok := shareGuestResolver(&fakeShareReader{err: errors.New("db down")})(context.Background(), "t"); ok {
		t.Error("a store error must resolve to not-found")
	}
	if _, ok := shareGuestResolver(&fakeShareReader{})(context.Background(), "t"); ok {
		t.Error("a nil share must resolve to not-found")
	}
}

func TestNewShareGuestService(t *testing.T) {
	p := newTestPlatform(t, guestTestConfig())
	defer func() { _ = p.Close() }()

	// Without a DB and notification handle, the service renders pages but
	// cannot issue links.
	svc := newShareGuestService(p, nil, &fakeShareReader{}, nil)
	if svc == nil {
		t.Fatal("expected a service")
	}
	if svc.LinksAvailable() {
		t.Error("no DB and no mailer must disable the link flow")
	}

	// With a DB (link store), a notification handle (mailer), the signing
	// key, and a base URL, the link flow is fully enabled.
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	handle, err := notifydelivery.New(notifydelivery.Config{DB: db, Encryptor: passthroughStringEncryptor{}})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Stop()

	full := newShareGuestService(p, handle, &fakeShareReader{}, db)
	if !full.LinksAvailable() {
		t.Error("DB + handle + key + base URL must enable the link flow")
	}
}

func TestUnsubscribeURLFn(t *testing.T) {
	p := newTestPlatform(t, guestTestConfig())
	defer func() { _ = p.Close() }()

	fn := unsubscribeURLFn(p)
	if fn == nil {
		t.Fatal("key + base URL must yield a builder")
	}
	link := fn("bob@example.com")
	if !strings.HasPrefix(link, "https://platform.example.com/portal/notifications/unsubscribe?tok=") {
		t.Errorf("unexpected link: %s", link)
	}

	// The minted token verifies under the same derived key the mounted
	// handler uses.
	tok := strings.TrimPrefix(link, "https://platform.example.com/portal/notifications/unsubscribe?tok=")
	key := deriveUnsubKey(p)
	email, ok := unsubhttp.VerifyUnsubToken(key, tok)
	if !ok || email != "bob@example.com" {
		t.Errorf("footer token must verify at the endpoint: ok=%v email=%q", ok, email)
	}

	// Missing key or base URL disables the footer.
	noKey := guestTestConfig()
	noKey.Auth.BrowserSession.Enabled = false
	pNoKey := newTestPlatform(t, noKey)
	defer func() { _ = pNoKey.Close() }()
	if unsubscribeURLFn(pNoKey) != nil {
		t.Error("no signing key must disable the unsubscribe footer")
	}

	noBase := guestTestConfig()
	noBase.Portal.PublicBaseURL = ""
	pNoBase := newTestPlatform(t, noBase)
	defer func() { _ = pNoBase.Close() }()
	if unsubscribeURLFn(pNoBase) != nil {
		t.Error("no base URL must disable the unsubscribe footer")
	}
}

// deriveUnsubKey mirrors the wiring's derivation for assertions.
func deriveUnsubKey(p *platform.Platform) []byte {
	return shareguest.DeriveKey(browserSessionSigningKey(p), unsubscribeKeyLabel)
}

func TestMountNotificationUnsubscribe(t *testing.T) {
	p := newTestPlatform(t, guestTestConfig())
	defer func() { _ = p.Close() }()

	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	handle, err := notifydelivery.New(notifydelivery.Config{DB: db, Encryptor: passthroughStringEncryptor{}})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Stop()

	mux := http.NewServeMux()
	mountNotificationUnsubscribe(mux, p, handle)

	// An invalid token reaches the handler and is refused with the branded
	// 400 page, proving the route is mounted and keyed.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		unsubscribePath+"?tok=garbage", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for a bad token, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not valid") {
		t.Error("expected the branded refusal page")
	}

	// The RFC 8058 one-click POST route is mounted on the same path and
	// reaches the handler (a bad token is refused with a bare status, no
	// page).
	postReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		unsubscribePath+"?tok=garbage", strings.NewReader("List-Unsubscribe=One-Click"))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wPost := httptest.NewRecorder()
	mux.ServeHTTP(wPost, postReq)
	if wPost.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for a bad one-click token, got %d", wPost.Code)
	}
	if strings.Contains(wPost.Body.String(), "<html") {
		t.Error("one-click refusal must not render a page")
	}

	// Without a key, nothing mounts.
	mux2 := http.NewServeMux()
	pNoKey := newTestPlatform(t, &platform.Config{})
	defer func() { _ = pNoKey.Close() }()
	mountNotificationUnsubscribe(mux2, pNoKey, handle)
	w2 := httptest.NewRecorder()
	mux2.ServeHTTP(w2, httptest.NewRequestWithContext(context.Background(), http.MethodGet, unsubscribePath, http.NoBody))
	if w2.Code != http.StatusNotFound {
		t.Errorf("expected no route without a key, got %d", w2.Code)
	}

	// Without a notification handle, nothing mounts.
	mux3 := http.NewServeMux()
	mountNotificationUnsubscribe(mux3, p, nil)
	w3 := httptest.NewRecorder()
	mux3.ServeHTTP(w3, httptest.NewRequestWithContext(context.Background(), http.MethodGet, unsubscribePath, http.NoBody))
	if w3.Code != http.StatusNotFound {
		t.Errorf("expected no route without a handle, got %d", w3.Code)
	}
}

// fakePrefsStore is an in-memory notification.PrefsStore for the guest
// opt-out wiring tests.
type fakePrefsStore struct {
	modes     map[string]string
	lastEmail string
	lastSet   notification.PrefsUpdate
	getErr    error
	setErr    error
}

func (f *fakePrefsStore) Get(_ context.Context, email string) (notification.Prefs, error) {
	if f.getErr != nil {
		return notification.Prefs{}, f.getErr
	}
	p := notification.DefaultPrefs(email)
	if mode, ok := f.modes[email]; ok {
		p.Mode = mode
	}
	return p, nil
}

func (f *fakePrefsStore) Set(_ context.Context, email string, u notification.PrefsUpdate) (notification.Prefs, error) {
	if f.setErr != nil {
		return notification.Prefs{}, f.setErr
	}
	f.lastEmail = email
	f.lastSet = u
	return notification.DefaultPrefs(email), nil
}

func TestOptOutStatusFn(t *testing.T) {
	prefs := &fakePrefsStore{modes: map[string]string{"bob@example.com": notification.ModeOff}}
	fn := optOutStatusFn(prefs)

	// Mixed-case recipient must find the canonically keyed row.
	out, err := fn(context.Background(), " Bob@Example.com ")
	if err != nil || !out {
		t.Errorf("opted-out address must read true: out=%v err=%v", out, err)
	}

	out, err = fn(context.Background(), "carol@example.com")
	if err != nil || out {
		t.Errorf("default prefs must read not opted out: out=%v err=%v", out, err)
	}

	if _, err := optOutStatusFn(&fakePrefsStore{getErr: errors.New("db down")})(context.Background(), "x@y.z"); err == nil {
		t.Error("a store error must propagate")
	}
}

func TestResubscribeFn(t *testing.T) {
	prefs := &fakePrefsStore{}
	fn := resubscribeFn(prefs)

	if err := fn(context.Background(), " Bob@Example.com "); err != nil {
		t.Fatalf("resubscribe: %v", err)
	}
	if prefs.lastEmail != "bob@example.com" {
		t.Errorf("write must use the canonical address, got %q", prefs.lastEmail)
	}
	if prefs.lastSet.Mode == nil || *prefs.lastSet.Mode != notification.ModeImmediate {
		t.Errorf("resubscribe must restore immediate delivery, got %+v", prefs.lastSet)
	}
	if prefs.lastSet.SharesEnabled != nil || prefs.lastSet.CommentsEnabled != nil {
		t.Error("resubscribe must touch nothing but the mode")
	}

	if err := resubscribeFn(&fakePrefsStore{setErr: errors.New("db down")})(context.Background(), "x@y.z"); err == nil {
		t.Error("a store error must propagate")
	}
}
