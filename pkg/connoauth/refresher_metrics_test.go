package connoauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// TestRefresherTick_RecordsCredentialStatesAndRefreshResults: a pass counts
// each kind's connections by credential state, every state written so a
// state no connection is in reads 0, and a transient refresh failure is
// counted as failed and leaves its connection expiring (#1897).
func TestRefresherTick_RecordsCredentialStatesAndRefreshResults(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	bgloop.SetDefaultMetrics(m)
	t.Cleanup(func() {
		bgloop.SetDefaultMetrics(nil)
		_ = m.Shutdown(context.Background())
	})
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	_ = store.Set(ctx, PersistedToken{Key: Key{Kind: KindMCP, Name: "no-refresh"}, AccessToken: "at"})
	_ = store.Set(ctx, PersistedToken{
		Key: Key{Kind: KindMCP, Name: "current"}, AccessToken: "at",
		RefreshToken: "rt", ExpiresAt: now.Add(24 * time.Hour),
	})
	_ = store.Set(ctx, PersistedToken{
		Key: Key{Kind: KindMCP, Name: "due"}, AccessToken: "at",
		RefreshToken: "rt", ExpiresAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	r := NewRefresher(store, stubConfigResolver{cfg: Config{TokenURL: "http://127.0.0.1:1/never-listens"}},
		nil, NoopLocker{}, RefresherConfig{})
	r.now = func() time.Time { return now }

	if err := r.tick(ctx); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	b, _ := io.ReadAll(rec.Body)
	body := string(b)
	for _, want := range []string{
		`connection_oauth_credentials{kind="mcp",state="missing"} 1`,
		`connection_oauth_credentials{kind="mcp",state="ok"} 1`,
		`connection_oauth_credentials{kind="mcp",state="expiring"} 1`,
		`connection_oauth_credentials{kind="mcp",state="revoked"} 0`,
		`connection_oauth_refresh_total{kind="mcp",result="failed"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
}

// switchableResolver resolves every key until gone is set, as if the
// connection were deleted.
type switchableResolver struct {
	cfg  Config
	gone bool
}

func (s *switchableResolver) ResolveConfig(context.Context, Key) (Config, error) {
	if s.gone {
		return Config{}, ErrConfigNotResolvable
	}
	return s.cfg, nil
}

func (*switchableResolver) MaxLifetime(context.Context, Key) time.Duration { return 0 }

// TestRefresherTick_ARevokedCredentialStaysRevoked: the IdP refusing a
// credential deletes its row, and the connection is still counted revoked on
// every later pass -- it needs an administrator -- until the connection stops
// resolving. A kind left with no connections then reads 0 in every state
// rather than its last count.
func TestRefresherTick_ARevokedCredentialStaysRevoked(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	bgloop.SetDefaultMetrics(m)
	t.Cleanup(func() {
		bgloop.SetDefaultMetrics(nil)
		_ = m.Shutdown(context.Background())
	})
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Token is not active"}`)
	}))
	defer idp.Close()

	ctx := context.Background()
	store := NewMemoryStore()
	key := Key{Kind: KindMCP, Name: "crm"}
	_ = store.Set(ctx, PersistedToken{
		Key: key, AccessToken: "at", RefreshToken: "rt",
		ExpiresAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	resolver := &switchableResolver{cfg: Config{TokenURL: idp.URL, ClientID: "c"}}
	r := NewRefresher(store, resolver, nil, NoopLocker{}, RefresherConfig{})
	scrape := func() string {
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/metrics", http.NoBody))
		return rec.Body.String()
	}
	revoked := func(n int) string {
		return `connection_oauth_credentials{kind="mcp",state="revoked"} ` + strconv.Itoa(n)
	}

	for pass := range 2 {
		if err := r.tick(ctx); err != nil {
			t.Fatal(err)
		}
		if body := scrape(); !strings.Contains(body, revoked(1)) {
			t.Fatalf("pass %d: the revoked connection is not counted revoked", pass+1)
		}
	}
	if _, err := store.Get(ctx, key); err == nil {
		t.Fatal("the revoked credential's row was not deleted, so this test does not hold the case")
	}

	resolver.gone = true
	if err := r.tick(ctx); err != nil {
		t.Fatal(err)
	}
	body := scrape()
	for _, state := range []string{"ok", "expiring", "revoked", "missing"} {
		want := `connection_oauth_credentials{kind="mcp",state="` + state + `"} 0`
		if !strings.Contains(body, want) {
			t.Errorf("after the connection was deleted, missing %s", want)
		}
	}
}
