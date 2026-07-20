package httpserver

// The database-present happy path of buildNotifications requires a live
// Postgres (Platform only wires p.db from a real DSN) and is exercised by
// TestBuildNotifications_RealDB in dbmounts_realdb_integration_test.go,
// following the dbmounts.go coverage convention.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

func TestBuildNotifications_NilPlatform(t *testing.T) {
	if got := buildNotifications(nil); got != nil {
		t.Error("nil platform must yield nil handle")
	}
}

func TestBuildNotifications_Disabled(t *testing.T) {
	cfg := &platform.Config{}
	off := false
	cfg.Notifications.Enabled = &off
	p := newTestPlatform(t, cfg)
	defer func() { _ = p.Close() }()

	if got := buildNotifications(p); got != nil {
		t.Error("disabled notifications must yield nil handle")
	}
}

func TestBuildNotifications_NoDatabase(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{})
	defer func() { _ = p.Close() }()

	if got := buildNotifications(p); got != nil {
		t.Error("no database must yield nil handle")
	}
}

func TestWirePortalNotifications(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{})
	defer func() { _ = p.Close() }()

	// Nil handle: the portal deps stay unset.
	var deps portal.Deps
	wirePortalNotifications(&deps, p, nil)
	if deps.Notifier != nil || deps.NotificationRegistrar != nil {
		t.Fatal("nil handle must leave the portal deps unset")
	}

	// Live handle (sqlmock DB): both the trigger bridge and the prefs
	// registrar are wired.
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

	wirePortalNotifications(&deps, p, handle)
	if deps.Notifier == nil {
		t.Error("live handle must wire the trigger bridge")
	}
	if deps.NotificationRegistrar == nil {
		t.Fatal("live handle must wire the prefs registrar")
	}

	// The registered prefs routes resolve the caller through portal's user
	// context: anonymous requests get 401, authenticated ones reach the
	// store (sqlmock without expectations surfaces as a 500, proving the
	// email made it through the closure).
	mux := http.NewServeMux()
	deps.NotificationRegistrar(mux)
	anon := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/notification-prefs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, anon)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous prefs request = %d; want 401", rec.Code)
	}
	authed := anon.WithContext(portal.ContextWithUser(anon.Context(), &portal.User{Email: "a@b.io"}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, authed)
	if rec.Code == http.StatusUnauthorized {
		t.Error("authenticated prefs request must pass the user closure")
	}
}

// passthroughStringEncryptor satisfies notification.StringEncryptor.
type passthroughStringEncryptor struct{}

func (passthroughStringEncryptor) Encrypt(s string) (string, error) { return s, nil }
func (passthroughStringEncryptor) Decrypt(s string) (string, error) { return s, nil }
