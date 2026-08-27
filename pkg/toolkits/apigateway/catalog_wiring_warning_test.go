package apigateway

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// captureWarnings redirects the default logger to a buffer for the
// duration of the test and returns it. Tests using it must not run in
// parallel: slog.SetDefault is process-wide.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// countUnbackedWarnings counts the catalog-store warnings in captured
// log output.
func countUnbackedWarnings(out string) int {
	return strings.Count(out, "no catalog store wired")
}

// addCatalogConn registers a connection that references a catalog,
// which is the only shape the warning is about.
func addCatalogConn(t *testing.T, tk *Toolkit, name, catalogID string) {
	t.Helper()
	if err := tk.AddConnection(name, map[string]any{
		"base_url":   "https://" + name + ".example.com",
		"catalog_id": catalogID,
	}); err != nil {
		t.Fatalf("AddConnection(%s): %v", name, err)
	}
}

// TestCatalogWiring_SilentWhenTheStoreArrivesDuringStartup is the first
// acceptance criterion of #1509. Connections are built before the
// platform wires the catalog store, so every catalog-backed connection
// passes through buildConnSpecs with a nil store; the platform then
// wires the store and reloads them. A deployment on that path serves
// its specs, and must say nothing about a store it has.
func TestCatalogWiring_SilentWhenTheStoreArrivesDuringStartup(t *testing.T) {
	buf := captureWarnings(t)
	tk := New("api")

	// Construction: no store yet, exactly as NewMulti runs it.
	addCatalogConn(t, tk, "bea", "bea-2026-08")
	addCatalogConn(t, tk, "nws", "nws-2026-08")

	// Platform wiring: the store arrives and every connection is rebuilt.
	setupCatalogWithSpec(t, tk, "bea-2026-08", "default", petstoreSpec)
	for _, name := range []string{"bea", "nws"} {
		if err := tk.ReloadConnection(name); err != nil {
			t.Fatalf("ReloadConnection(%s): %v", name, err)
		}
	}
	tk.MarkCatalogWiringComplete()

	if got := countUnbackedWarnings(buf.String()); got != 0 {
		t.Errorf("got %d catalog-store warnings, want 0; log: %s", got, buf.String())
	}
}

// TestCatalogWiring_ReportsEveryConnectionTheStoreNeverReached is the
// second criterion: the deployment the message was written for, where
// no store is ever wired, still hears about each affected connection.
func TestCatalogWiring_ReportsEveryConnectionTheStoreNeverReached(t *testing.T) {
	buf := captureWarnings(t)
	tk := New("api")

	addCatalogConn(t, tk, "bea", "bea-2026-08")
	addCatalogConn(t, tk, "nws", "nws-2026-08")
	// A connection with no catalog_id has nothing to lose and is not named.
	if err := tk.AddConnection("plain", map[string]any{
		"base_url": "https://plain.example.com",
	}); err != nil {
		t.Fatalf("AddConnection(plain): %v", err)
	}

	tk.MarkCatalogWiringComplete()

	out := buf.String()
	if got := countUnbackedWarnings(out); got != 2 {
		t.Fatalf("got %d catalog-store warnings, want one per catalog-backed connection; log: %s", got, out)
	}
	for _, want := range []string{"connection=bea", "catalog_id=bea-2026-08", "connection=nws", "catalog_id=nws-2026-08"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "connection=plain") {
		t.Errorf("a connection with no catalog_id was reported: %s", out)
	}
}

// TestCatalogWiring_WarnsAtBuildTimeOnceWiringHasPassed covers the
// connection that arrives after startup. The store can no longer be on
// its way, so the state is reported as the connection is built rather
// than being withheld until some later sweep.
func TestCatalogWiring_WarnsAtBuildTimeOnceWiringHasPassed(t *testing.T) {
	buf := captureWarnings(t)
	tk := New("api")
	tk.MarkCatalogWiringComplete()
	if got := countUnbackedWarnings(buf.String()); got != 0 {
		t.Fatalf("a toolkit with no connections warned: %s", buf.String())
	}

	addCatalogConn(t, tk, "late", "late-2026-08")

	out := buf.String()
	if got := countUnbackedWarnings(out); got != 1 {
		t.Fatalf("got %d catalog-store warnings, want 1; log: %s", got, out)
	}
	if !strings.Contains(out, "connection=late") {
		t.Errorf("log does not name the connection: %s", out)
	}
}
