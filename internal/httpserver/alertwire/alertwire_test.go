package alertwire

import (
	"context"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func newTestPlatform(t *testing.T, cfg *platform.Config) *platform.Platform {
	t.Helper()
	p, err := platform.New(platform.WithConfig(cfg))
	if err != nil {
		t.Fatalf("failed to create test platform: %v", err)
	}
	return p
}

// TestBuildReviewAlert_NoDatabase covers the degraded path of the scheduled
// review-queue check (#803). Its database-present path needs a live Postgres
// (Platform wires p.db only from a real DSN) and is covered by
// TestBuildReviewAlert_RealDB in dbmounts_realdb_integration_test.go.
func TestBuildReviewAlert_NoDatabase(t *testing.T) {
	if got := BuildReviewAlert(nil, nil); got != nil {
		t.Error("nil platform must yield no checker")
	}
	if got := ReviewAlertSettings(nil, reviewalert.KnowledgeTarget()); got != nil {
		t.Error("nil platform must yield no settings store")
	}

	p := newTestPlatform(t, &platform.Config{})
	defer func() { _ = p.Close() }()

	if got := BuildReviewAlert(p, nil); got != nil {
		t.Error("no database must yield no checker")
	}
	if got := ReviewAlertSettings(p, reviewalert.KnowledgeTarget()); got != nil {
		t.Error("no database must yield no settings store")
	}
	// The composition root brackets Start/Stop on whatever it got back.
	BuildReviewAlert(p, nil).Start(context.Background())
	BuildReviewAlert(p, nil).Stop()
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

	if got := ReviewAlertSettings(p, reviewalert.KnowledgeTarget()); got != nil {
		t.Error("disabled notifications must yield no settings store")
	}
}

// TestAlertSettings_NoDatabase: with no database none of the alerts can
// exist, so none of their admin routes may mount.
func TestAlertSettings_NoDatabase(t *testing.T) {
	if ConnAlertSettings(nil) != nil || ScriptRunAlertSettings(nil) != nil || ConnAlertStore(nil) != nil {
		t.Error("nil platform yielded a settings store")
	}
	p := newTestPlatform(t, &platform.Config{})
	defer func() { _ = p.Close() }()
	if ConnAlertSettings(p) != nil || ScriptRunAlertSettings(p) != nil {
		t.Error("no database yielded a settings store")
	}
	if WireConnRevocationAlert(p, nil) != nil {
		t.Error("no database yielded an escalator")
	}
	var deps admin.Deps
	AdminDeps(&deps, p, nil)
	if deps.ScriptRunAlert != nil || deps.ConnectionAlert != nil || deps.ReviewQueueAlert != nil || deps.NotificationChannels != nil {
		t.Errorf("no database wired admin notification routes: %+v", deps)
	}
}

func TestEnvInterval(t *testing.T) {
	cases := map[string]time.Duration{"": 0, "15s": 15 * time.Second, "soon": 0, "-1m": 0}
	for _, name := range []string{EnvReviewAlertInterval, EnvConnAlertInterval} {
		for raw, want := range cases {
			t.Setenv(name, raw)
			if got := envInterval(name); got != want {
				t.Errorf("%s=%q: interval = %v, want %v", name, raw, got, want)
			}
		}
	}
}
