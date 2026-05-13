package connoauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeIDP is a minimal token-endpoint server for testing the Source.
// Tests control its behavior via the config struct; each request
// increments a counter and the configured handler runs.
type fakeIDP struct {
	server     *httptest.Server
	calls      atomic.Int32
	handleFunc func(http.ResponseWriter, *http.Request)
}

func newFakeIDP(t *testing.T, handle func(http.ResponseWriter, *http.Request)) *fakeIDP {
	t.Helper()
	idp := &fakeIDP{handleFunc: handle}
	idp.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idp.calls.Add(1)
		idp.handleFunc(w, r)
	}))
	t.Cleanup(idp.server.Close)
	return idp
}

func (f *fakeIDP) tokenURL() string { return f.server.URL + "/token" }

// readForm decodes the POST form. Used by handlers to inspect the
// grant_type / refresh_token / etc.
func readForm(r *http.Request) url.Values {
	body, _ := io.ReadAll(r.Body)
	v, _ := url.ParseQuery(string(body))
	return v
}

func writeTokenJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// TestSource_CachedTokenStillValid verifies that Token() returns the
// cached access token without hitting the IdP when ExpiresAt is in
// the future.
func TestSource_CachedTokenStillValid(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("token endpoint should not be called when cached token is valid")
	})
	store := NewMemoryStore()
	key := Key{Kind: KindMCP, Name: "cached"}
	_ = store.Set(context.Background(), PersistedToken{
		Key:          key,
		AccessToken:  "still-valid",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	src := NewSource(store, key, Config{TokenURL: idp.tokenURL(), ClientID: "c"})
	tok, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "still-valid" {
		t.Fatalf("expected cached token, got %q", tok)
	}
	if idp.calls.Load() != 0 {
		t.Fatalf("idp should not have been called")
	}
}

// TestSource_NoTokenReturnsNeedsReauth — Token() on an empty store
// returns ErrNeedsReauth so the caller can surface the Connect prompt.
func TestSource_NoTokenReturnsNeedsReauth(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	src := NewSource(store, Key{Kind: KindMCP, Name: "never-connected"}, Config{})
	_, err := src.Token(context.Background())
	if !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("expected ErrNeedsReauth, got %v", err)
	}
}

// TestSource_RefreshRotatesAndPersists — the bug-#3 regression. When
// the IdP returns a NEW refresh_token on refresh (rotation), the new
// refresh_token MUST land in the store. The prior MCP implementation
// had subtle paths where rotation could fail to persist; this test
// proves the new path does not.
func TestSource_RefreshRotatesAndPersists(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, r *http.Request) {
		form := readForm(r)
		if form.Get("grant_type") != "refresh_token" {
			t.Errorf("expected refresh_token grant, got %q", form.Get("grant_type"))
		}
		if form.Get("refresh_token") != "rt-original" {
			t.Errorf("expected original refresh token, got %q", form.Get("refresh_token"))
		}
		writeTokenJSON(w, map[string]any{
			"access_token":  "at-fresh",
			"refresh_token": "rt-rotated",
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	})

	store := NewMemoryStore()
	key := Key{Kind: KindMCP, Name: "rotates"}
	_ = store.Set(context.Background(), PersistedToken{
		Key:          key,
		AccessToken:  "at-old",
		RefreshToken: "rt-original",
		ExpiresAt:    time.Now().Add(-time.Minute), // expired
	})
	src := NewSource(store, key, Config{
		TokenURL:          idp.tokenURL(),
		ClientID:          "c",
		ClientSecret:      "s",
		EndpointAuthStyle: oauth2.AuthStyleInHeader,
	})
	tok, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "at-fresh" {
		t.Fatalf("expected fresh access token, got %q", tok)
	}
	// Critical: the rotated refresh token must be persisted.
	got, _ := store.Get(context.Background(), key)
	if got.RefreshToken != "rt-rotated" {
		t.Fatalf("BUG #3: rotated refresh_token did not persist. got %q want %q",
			got.RefreshToken, "rt-rotated")
	}
	if got.AccessToken != "at-fresh" {
		t.Fatalf("access token did not persist: got %q", got.AccessToken)
	}
}

// TestSource_RefreshPreservesOldRefreshWhenOmitted — the OTHER bug-#3
// subcase. RFC 6749 §6 allows IdPs to OMIT refresh_token from a
// refresh response, meaning "the prior one is still valid." A naive
// implementation that overwrites with whatever the response carries
// would NULL the field. This test ensures the prior refresh_token
// is preserved.
func TestSource_RefreshPreservesOldRefreshWhenOmitted(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTokenJSON(w, map[string]any{
			"access_token": "at-fresh",
			"expires_in":   3600,
			"token_type":   "Bearer",
			// No refresh_token — IdP says "keep using the existing one".
		})
	})

	store := NewMemoryStore()
	key := Key{Kind: KindAPI, Name: "preserves"}
	_ = store.Set(context.Background(), PersistedToken{
		Key:          key,
		AccessToken:  "at-old",
		RefreshToken: "rt-keep",
		ExpiresAt:    time.Now().Add(-time.Minute),
	})
	src := NewSource(store, key, Config{
		TokenURL:          idp.tokenURL(),
		ClientID:          "c",
		ClientSecret:      "s",
		EndpointAuthStyle: oauth2.AuthStyleInHeader,
	})
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	got, _ := store.Get(context.Background(), key)
	if got.RefreshToken != "rt-keep" {
		t.Fatalf("BUG #3: refresh_token was NULLed when IdP omitted it. got %q want %q",
			got.RefreshToken, "rt-keep")
	}
	if got.AccessToken != "at-fresh" {
		t.Fatalf("access token did not refresh: got %q", got.AccessToken)
	}
}

// TestSource_RefreshExpiresInPersists — when the IdP includes
// refresh_expires_in (Keycloak-style), it must reach the store and
// be visible on Status. Zero when absent.
func TestSource_RefreshExpiresInPersists(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTokenJSON(w, map[string]any{
			"access_token":       "at-fresh",
			"refresh_token":      "rt-rotated",
			"expires_in":         3600,
			"refresh_expires_in": 7200,
			"token_type":         "Bearer",
		})
	})
	store := NewMemoryStore()
	key := Key{Kind: KindMCP, Name: "refresh-expires"}
	_ = store.Set(context.Background(), PersistedToken{
		Key:          key,
		AccessToken:  "at-old",
		RefreshToken: "rt-original",
		ExpiresAt:    time.Now().Add(-time.Minute),
	})
	src := NewSource(store, key, Config{
		TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s",
	})
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	got, _ := store.Get(context.Background(), key)
	if got.RefreshExpiresAt.IsZero() {
		t.Fatalf("expected RefreshExpiresAt to be set from refresh_expires_in")
	}
	wantRoughly := time.Now().Add(7200 * time.Second)
	if delta := got.RefreshExpiresAt.Sub(wantRoughly); delta > 5*time.Second || delta < -5*time.Second {
		t.Fatalf("RefreshExpiresAt off by %v from expected", delta)
	}
}

// TestSource_RefreshRevokedReturnsNeedsReauth — RFC 6749 §5.2
// invalid_grant at HTTP 400 means the refresh token is definitively
// dead. The Source must delete the row and return ErrNeedsReauth.
func TestSource_RefreshRevokedReturnsNeedsReauth(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Refresh token expired"}`))
	})
	store := NewMemoryStore()
	key := Key{Kind: KindMCP, Name: "revoked"}
	_ = store.Set(context.Background(), PersistedToken{
		Key:          key,
		AccessToken:  "at-old",
		RefreshToken: "rt-dead",
		ExpiresAt:    time.Now().Add(-time.Minute),
	})
	src := NewSource(store, key, Config{
		TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s",
	})
	_, err := src.Token(context.Background())
	if !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("expected ErrNeedsReauth for revoked refresh, got %v", err)
	}
	// Persisted row must be deleted so a process restart doesn't
	// replay the dead refresh token.
	if _, gerr := store.Get(context.Background(), key); !errors.Is(gerr, ErrTokenNotFound) {
		t.Fatalf("expected row to be deleted after revoked refresh, still present")
	}
}

// TestSource_RefreshTransientErrorPreservesRow — a 5xx from the IdP
// is transient; the persisted row MUST NOT be deleted. The error
// surfaces so the caller can retry.
func TestSource_RefreshTransientErrorPreservesRow(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	store := NewMemoryStore()
	key := Key{Kind: KindAPI, Name: "transient"}
	_ = store.Set(context.Background(), PersistedToken{
		Key:          key,
		AccessToken:  "at-old",
		RefreshToken: "rt-still-good",
		ExpiresAt:    time.Now().Add(-time.Minute),
	})
	src := NewSource(store, key, Config{
		TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s",
	})
	_, err := src.Token(context.Background())
	if err == nil {
		t.Fatalf("expected error on transient 503")
	}
	if errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("transient 503 must not surface as ErrNeedsReauth: %v", err)
	}
	// Row must still be present so the next call can retry.
	got, gerr := store.Get(context.Background(), key)
	if gerr != nil {
		t.Fatalf("row should still exist after transient error: %v", gerr)
	}
	if got.RefreshToken != "rt-still-good" {
		t.Fatalf("refresh token must not be lost on transient error")
	}
}

// TestSource_NoRefreshTokenReturnsNeedsReauth — when a row exists
// but has no refresh_token (e.g., IdP never issued one because the
// scope didn't include offline_access), Token() must surface
// ErrNeedsReauth rather than hang on a doomed refresh attempt.
func TestSource_NoRefreshTokenReturnsNeedsReauth(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("token endpoint should not be called when no refresh token is present")
	})
	store := NewMemoryStore()
	key := Key{Kind: KindMCP, Name: "no-refresh"}
	_ = store.Set(context.Background(), PersistedToken{
		Key:          key,
		AccessToken:  "at-old",
		RefreshToken: "", // none
		ExpiresAt:    time.Now().Add(-time.Minute),
	})
	src := NewSource(store, key, Config{TokenURL: idp.tokenURL(), ClientID: "c"})
	_, err := src.Token(context.Background())
	if !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("expected ErrNeedsReauth, got %v", err)
	}
}

// TestSource_RefreshExpiredReturnsNeedsReauth — when the
// IdP-disclosed RefreshExpiresAt has passed, skip the round trip
// and return ErrNeedsReauth immediately.
func TestSource_RefreshExpiredReturnsNeedsReauth(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("token endpoint should not be called when refresh deadline has passed")
	})
	store := NewMemoryStore()
	key := Key{Kind: KindMCP, Name: "refresh-expired"}
	_ = store.Set(context.Background(), PersistedToken{
		Key:              key,
		RefreshToken:     "rt",
		RefreshExpiresAt: time.Now().Add(-time.Minute), // already past
		ExpiresAt:        time.Now().Add(-time.Hour),
	})
	src := NewSource(store, key, Config{TokenURL: idp.tokenURL(), ClientID: "c"})
	_, err := src.Token(context.Background())
	if !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("expected ErrNeedsReauth, got %v", err)
	}
}

// TestSource_Reacquire — the admin "Reacquire" button forces a
// refresh even if the access token is still valid. Useful for
// testing whether the persisted refresh token works.
func TestSource_Reacquire(t *testing.T) {
	t.Parallel()
	called := atomic.Int32{}
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		called.Add(1)
		writeTokenJSON(w, map[string]any{
			"access_token":  "at-fresh",
			"refresh_token": "rt-fresh",
			"expires_in":    3600,
		})
	})
	store := NewMemoryStore()
	key := Key{Kind: KindMCP, Name: "reacquire"}
	_ = store.Set(context.Background(), PersistedToken{
		Key:          key,
		AccessToken:  "at-still-valid",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	src := NewSource(store, key, Config{TokenURL: idp.tokenURL(), ClientID: "c"})
	if err := src.Reacquire(context.Background()); err != nil {
		t.Fatalf("Reacquire: %v", err)
	}
	if called.Load() != 1 {
		t.Fatalf("Reacquire should hit the IdP, calls=%d", called.Load())
	}
}

// TestSource_Status_NoRow — when the store has no row for the key,
// Status returns Configured + NeedsReauth (the UI surfaces a Connect
// button) without a LastError (the absence is normal).
func TestSource_Status_NoRow(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	src := NewSource(store, Key{Kind: KindMCP, Name: "fresh"}, Config{
		TokenURL: "https://idp.example/token",
		Scopes:   []string{"openid", "offline_access"},
	})
	st := src.Status(context.Background())
	if !st.Configured || !st.NeedsReauth {
		t.Fatalf("expected Configured + NeedsReauth, got %+v", st)
	}
	if st.LastError != "" {
		t.Fatalf("absent row is normal, LastError should be empty: %q", st.LastError)
	}
	if !strings.Contains(st.Scope, "offline_access") {
		t.Fatalf("scope from cfg should be surfaced: %q", st.Scope)
	}
}

// TestSource_Status_StoreError — when the store fails (DB unreachable,
// etc.), Status surfaces the error on LastError so operators see the
// real cause rather than a misleading "Connect needed" prompt.
func TestSource_Status_StoreError(t *testing.T) {
	t.Parallel()
	src := NewSource(failingStore{}, Key{Kind: KindMCP, Name: "x"}, Config{
		TokenURL: "https://idp.example/token",
	})
	st := src.Status(context.Background())
	if !st.Configured {
		t.Fatalf("Configured must be true even on store error: %+v", st)
	}
	if st.LastError == "" {
		t.Fatalf("store error should surface on LastError")
	}
}

// TestSource_Status_Healthy — happy path: a valid persisted token
// produces a TokenAcquired status without NeedsReauth.
func TestSource_Status_Healthy(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	key := Key{Kind: KindMCP, Name: "healthy"}
	now := time.Now()
	_ = store.Set(context.Background(), PersistedToken{
		Key:             key,
		AccessToken:     "at",
		RefreshToken:    "rt",
		ExpiresAt:       now.Add(time.Hour),
		AuthenticatedBy: "user@example.com",
		AuthenticatedAt: now,
	})
	src := NewSource(store, key, Config{TokenURL: "https://idp/token"})
	st := src.Status(context.Background())
	if !st.TokenAcquired || st.NeedsReauth {
		t.Fatalf("healthy token should be acquired and not need reauth: %+v", st)
	}
}

// TestSource_Reacquire_NoRow — Reacquire on an absent row returns
// ErrNeedsReauth (matches Token() behavior; the admin button is a
// no-op when there's nothing to refresh against).
func TestSource_Reacquire_NoRow(t *testing.T) {
	t.Parallel()
	src := NewSource(NewMemoryStore(), Key{Kind: KindMCP, Name: "fresh"}, Config{})
	if err := src.Reacquire(context.Background()); !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("expected ErrNeedsReauth, got %v", err)
	}
}

// TestSource_Reacquire_Revoked — Reacquire against a refresh-token-
// revoked IdP must delete the row and surface ErrNeedsReauth.
func TestSource_Reacquire_Revoked(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	store := NewMemoryStore()
	key := Key{Kind: KindAPI, Name: "revoked-reacquire"}
	_ = store.Set(context.Background(), PersistedToken{
		Key: key, AccessToken: "at", RefreshToken: "rt-dead",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	src := NewSource(store, key, Config{TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s"})
	if err := src.Reacquire(context.Background()); !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("expected ErrNeedsReauth, got %v", err)
	}
	if _, gerr := store.Get(context.Background(), key); !errors.Is(gerr, ErrTokenNotFound) {
		t.Fatal("row should be deleted after revoked-refresh on Reacquire")
	}
}

// TestSource_Reacquire_Transient — Reacquire on a transient failure
// surfaces the error WITHOUT deleting the row (so a retry can run).
func TestSource_Reacquire_Transient(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	store := NewMemoryStore()
	key := Key{Kind: KindMCP, Name: "transient-reacquire"}
	_ = store.Set(context.Background(), PersistedToken{
		Key: key, AccessToken: "at", RefreshToken: "rt",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	src := NewSource(store, key, Config{TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s"})
	err := src.Reacquire(context.Background())
	if err == nil {
		t.Fatal("expected transient error from Reacquire")
	}
	if errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("transient must not surface as NeedsReauth: %v", err)
	}
	if _, gerr := store.Get(context.Background(), key); gerr != nil {
		t.Fatalf("row should be preserved after transient error: %v", gerr)
	}
}

// failingStore returns a transport-shaped error on every operation,
// for exercising the Status/Reacquire/Token store-error branches.
type failingStore struct{}

func (failingStore) Get(_ context.Context, _ Key) (*PersistedToken, error) {
	return nil, errors.New("store: connection refused")
}

func (failingStore) Set(_ context.Context, _ PersistedToken) error {
	return errors.New("store: connection refused")
}

func (failingStore) Delete(_ context.Context, _ Key) error {
	return errors.New("store: connection refused")
}

func (failingStore) List(_ context.Context) ([]PersistedToken, error) {
	return nil, errors.New("store: connection refused")
}

// TestSource_Token_StoreError — when the store returns a transient
// error (not ErrTokenNotFound), Token() must surface it as a wrapped
// error rather than misclassifying it as NeedsReauth.
func TestSource_Token_StoreError(t *testing.T) {
	t.Parallel()
	src := NewSource(failingStore{}, Key{Kind: KindMCP, Name: "x"}, Config{})
	_, err := src.Token(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("store error must not become NeedsReauth: %v", err)
	}
}

func TestStatusFromPersisted_TokenAcquired(t *testing.T) {
	t.Parallel()
	now := time.Now()
	p := &PersistedToken{
		Key:              Key{Kind: KindMCP, Name: "x"},
		AccessToken:      "at",
		RefreshToken:     "rt",
		ExpiresAt:        now.Add(time.Hour),
		RefreshExpiresAt: now.Add(24 * time.Hour),
		Scope:            "openid",
		AuthenticatedBy:  "user@example.com",
		AuthenticatedAt:  now,
		UpdatedAt:        now,
	}
	st := statusFromPersisted(p, Config{TokenURL: "https://idp.example/token"})
	if !st.TokenAcquired || !st.HasRefreshToken {
		t.Fatalf("expected token acquired and refresh present: %+v", st)
	}
	if st.NeedsReauth {
		t.Fatalf("should not need reauth for healthy token")
	}
	if st.AuthenticatedBy != "user@example.com" {
		t.Fatalf("authenticated_by missing")
	}
}

func TestStatusFromPersisted_RefreshExpired(t *testing.T) {
	t.Parallel()
	p := &PersistedToken{
		Key:              Key{Kind: KindMCP, Name: "x"},
		RefreshToken:     "rt",
		RefreshExpiresAt: time.Now().Add(-time.Hour),
	}
	st := statusFromPersisted(p, Config{})
	if !st.NeedsReauth {
		t.Fatalf("expected NeedsReauth when RefreshExpiresAt is past")
	}
}

func TestStatusFromPersisted_NoRefreshNoAccess(t *testing.T) {
	t.Parallel()
	p := &PersistedToken{Key: Key{Kind: KindMCP, Name: "x"}}
	st := statusFromPersisted(p, Config{Scopes: []string{"openid", "offline_access"}})
	if !st.NeedsReauth {
		t.Fatalf("expected NeedsReauth")
	}
	if !strings.Contains(st.Scope, "offline_access") {
		t.Fatalf("scope from cfg should be exposed: %q", st.Scope)
	}
}

func TestRefreshDeadlineFromToken(t *testing.T) {
	t.Parallel()
	now := time.Now()
	cases := []struct {
		name  string
		extra map[string]any
		want  bool // non-zero?
	}{
		{"absent", nil, false},
		{"zero", map[string]any{"refresh_expires_in": 0}, false},
		{"negative", map[string]any{"refresh_expires_in": -10}, false},
		{"float", map[string]any{"refresh_expires_in": float64(3600)}, true},
		{"int", map[string]any{"refresh_expires_in": int(3600)}, true},
		{"int64", map[string]any{"refresh_expires_in": int64(3600)}, true},
		{"wrong type", map[string]any{"refresh_expires_in": "3600"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tok := &oauth2.Token{}
			if tc.extra != nil {
				// oauth2.Token.Extra requires the token to be built from
				// a TokenSource — we use a synthetic raw map by marshaling.
				b, _ := json.Marshal(tc.extra)
				_ = json.Unmarshal(b, tok)
				tok = tok.WithExtra(tc.extra)
			}
			got := refreshDeadlineFromToken(tok, now)
			if (tc.want && got.IsZero()) || (!tc.want && !got.IsZero()) {
				t.Fatalf("refreshDeadlineFromToken: want non-zero=%v got %v", tc.want, got)
			}
		})
	}
}

func TestIsRevokedRefresh(t *testing.T) {
	t.Parallel()
	if !isRevokedRefresh(errNoRefreshToken) {
		t.Fatal("errNoRefreshToken should be revoked")
	}
	if !isRevokedRefresh(errRefreshExpired) {
		t.Fatal("errRefreshExpired should be revoked")
	}
	if !isRevokedRefresh(errRefreshTokenRevoked) {
		t.Fatal("errRefreshTokenRevoked should be revoked")
	}
	if isRevokedRefresh(errors.New("network down")) {
		t.Fatal("arbitrary error should not be revoked")
	}
}

func TestTokenFetchError_RedactsURL(t *testing.T) {
	t.Parallel()
	src := &url.Error{Op: "Post", URL: "https://user:secret@idp.example/token?client_secret=leak", Err: errors.New("boom")}
	got := tokenFetchError(src)
	msg := got.Error()
	if strings.Contains(msg, "secret") {
		t.Fatalf("error must not leak userinfo or query: %q", msg)
	}
	if strings.Contains(msg, "leak") {
		t.Fatalf("error must not leak query: %q", msg)
	}
}

func TestAccessTokenStillValid(t *testing.T) {
	t.Parallel()
	if accessTokenStillValid(nil) {
		t.Fatal("nil token should be invalid")
	}
	if accessTokenStillValid(&PersistedToken{}) {
		t.Fatal("zero token should be invalid")
	}
	if accessTokenStillValid(&PersistedToken{AccessToken: "x"}) {
		t.Fatal("zero ExpiresAt should be invalid")
	}
	past := &PersistedToken{AccessToken: "x", ExpiresAt: time.Now().Add(-time.Hour)}
	if accessTokenStillValid(past) {
		t.Fatal("past ExpiresAt should be invalid")
	}
	soon := &PersistedToken{AccessToken: "x", ExpiresAt: time.Now().Add(time.Second)}
	if accessTokenStillValid(soon) {
		t.Fatal("ExpiresAt within buffer should be invalid")
	}
	future := &PersistedToken{AccessToken: "x", ExpiresAt: time.Now().Add(time.Hour)}
	if !accessTokenStillValid(future) {
		t.Fatal("future ExpiresAt should be valid")
	}
}
