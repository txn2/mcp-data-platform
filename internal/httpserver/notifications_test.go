package httpserver

// The database-present happy path of buildNotifications requires a live
// Postgres (Platform only wires p.db from a real DSN) and is exercised by
// TestBuildNotifications_RealDB in dbmounts_realdb_integration_test.go,
// following the dbmounts.go coverage convention.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
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

// TestBuildReviewAlert_NoDatabase covers the degraded path of the scheduled
// review-queue check (#803). Its database-present path needs a live Postgres
// (Platform wires p.db only from a real DSN) and is covered by
// TestBuildReviewAlert_RealDB in dbmounts_realdb_integration_test.go.
func TestBuildReviewAlert_NoDatabase(t *testing.T) {
	if got := buildReviewAlert(nil, nil); got != nil {
		t.Error("nil platform must yield no checker")
	}
	if got := buildScriptReviewAlert(nil, nil); got != nil {
		t.Error("nil platform must yield no script review checker")
	}
	if got := reviewAlertSettings(nil, reviewalert.KnowledgeTarget()); got != nil {
		t.Error("nil platform must yield no settings store")
	}

	p := newTestPlatform(t, &platform.Config{})
	defer func() { _ = p.Close() }()

	if got := buildReviewAlert(p, nil); got != nil {
		t.Error("no database must yield no checker")
	}
	if got := buildScriptReviewAlert(p, nil); got != nil {
		t.Error("no database must yield no script review checker")
	}
	for _, target := range []reviewalert.Target{
		reviewalert.KnowledgeTarget(), reviewalert.ScriptTarget(),
	} {
		if got := reviewAlertSettings(p, target); got != nil {
			t.Errorf("no database must yield no %s settings store", target.Queue)
		}
	}
	// The composition root brackets Start/Stop on whatever it got back.
	buildReviewAlert(p, nil).Start(context.Background())
	buildReviewAlert(p, nil).Stop()
	buildScriptReviewAlert(p, nil).Start(context.Background())
	buildScriptReviewAlert(p, nil).Stop()
}

// TestReviewAlertSettings_NotificationsDisabled: with notifications off in
// YAML the admin routes must not mount, so an operator cannot configure an
// alert that nothing will ever send. This mirrors the SMTP section, whose
// store is likewise absent in that state.
func TestReviewAlertSettings_NotificationsDisabled(t *testing.T) {
	cfg := &platform.Config{}
	off := false
	cfg.Notifications.Enabled = &off
	p := newTestPlatform(t, cfg)
	defer func() { _ = p.Close() }()

	for _, target := range []reviewalert.Target{
		reviewalert.KnowledgeTarget(), reviewalert.ScriptTarget(),
	} {
		if got := reviewAlertSettings(p, target); got != nil {
			t.Errorf("disabled notifications must yield no %s settings store", target.Queue)
		}
	}
}

// TestEmailLogo covers the startup resolve. A missing or broken logo must
// degrade to the text wordmark rather than block notifications: the asset is
// decoration, and a 404 on it is not a reason to stop sending email.
func TestEmailLogo(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfake-raster-bytes")

	t.Run("unset URL resolves to no logo", func(t *testing.T) {
		if got := emailLogo(""); got != nil {
			t.Errorf("got %q, want nil", got)
		}
	})

	t.Run("fetches configured PNG", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		}))
		defer srv.Close()

		if got := emailLogo(srv.URL + "/logo.png"); !bytes.Equal(got, png) {
			t.Errorf("got %q, want %q", got, png)
		}
	})

	t.Run("unreachable URL degrades to no logo", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		if got := emailLogo(srv.URL + "/missing.png"); got != nil {
			t.Errorf("got %q, want nil", got)
		}
	})
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

func TestEmailReplyTo(t *testing.T) {
	if got := emailReplyTo(""); got != "" {
		t.Errorf("unset reply_to must stay empty, got %q", got)
	}
	if got := emailReplyTo("support@example.com"); got != "support@example.com" {
		t.Errorf("valid reply_to must pass through, got %q", got)
	}
	if got := emailReplyTo("not an address"); got != "" {
		t.Errorf("invalid reply_to must be dropped with a warning, got %q", got)
	}
}
