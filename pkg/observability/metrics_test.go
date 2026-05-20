package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewDisabledReturnsNoOpRecorder(t *testing.T) {
	m, err := New(Config{Enabled: false})
	if err != nil {
		t.Fatalf("New(disabled) err = %v", err)
	}
	if m != nil {
		t.Fatalf("New(disabled) should return nil recorder, got %+v", m)
	}
	// All Record methods must be nil-safe.
	m.RecordToolCall(context.Background(), ToolCallAttrs{}, time.Millisecond)
	m.IncInflightToolCalls(context.Background())
	m.DecInflightToolCalls(context.Background())
	m.RecordAPIGatewayOutbound(context.Background(), APIGatewayAttrs{}, time.Millisecond)

	if got := m.Enabled(); got {
		t.Errorf("Enabled() on nil = %v, want false", got)
	}
	if got := m.Handler(); got == nil {
		t.Errorf("Handler() on nil returned nil; want a non-nil 404 handler")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown() on nil = %v, want nil", err)
	}
}

func TestNewEnabledRecordsAllInstruments(t *testing.T) {
	m, err := New(Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatalf("New(enabled) err = %v", err)
	}
	if m == nil {
		t.Fatal("New(enabled) returned nil recorder")
	}
	defer func() {
		if err := m.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown err = %v", err)
		}
	}()

	ctx := context.Background()
	m.RecordToolCall(ctx, ToolCallAttrs{
		Tool: "trino_query", ToolkitKind: "trino", Persona: "analyst", StatusCategory: StatusOK,
	}, 250*time.Millisecond)
	m.IncInflightToolCalls(ctx)
	m.IncInflightToolCalls(ctx)
	m.DecInflightToolCalls(ctx)
	m.RecordAPIGatewayOutbound(ctx, APIGatewayAttrs{
		Connection: "primary", HTTPStatusClass: StatusClass2xx, StatusCategory: StatusOK,
	}, 50*time.Millisecond)
	m.RecordAPIGatewayOutbound(ctx, APIGatewayAttrs{
		Connection: "primary", HTTPStatusClass: StatusClass5xx, StatusCategory: StatusUpstreamErr,
	}, 1200*time.Millisecond)

	body := scrapeMetrics(t, m.Handler())

	mustContain := []string{
		"mcp_tool_calls_total",
		`tool="trino_query"`,
		`toolkit_kind="trino"`,
		`persona="analyst"`,
		`status_category="ok"`,
		"mcp_tool_call_duration_seconds",
		"mcp_inflight_tool_calls",
		"apigateway_outbound_total",
		`connection="primary"`,
		`http_status_class="2xx"`,
		`http_status_class="5xx"`,
		"apigateway_outbound_duration_seconds",
		// Go runtime collectors registered by New.
		"go_goroutines",
		"process_cpu_seconds_total",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing %q\n--- body ---\n%s", want, body)
		}
	}

	// Inflight should be 1 after two increments and one decrement.
	if !strings.Contains(body, "mcp_inflight_tool_calls 1") {
		t.Errorf("expected inflight gauge to read 1; body:\n%s", body)
	}
}

func TestNewListenerNilWhenDisabled(t *testing.T) {
	m, _ := New(Config{Enabled: false})
	if l := NewListener(m); l != nil {
		t.Errorf("NewListener(nil metrics) = %+v, want nil", l)
	}
}

// TestShutdownIdempotent guards the shutdown path that the platform's
// double-Close protocol relies on. The OTel meter provider returns
// "reader is shutdown" on a second Shutdown call; without the
// sync.Once guard, Platform.Close() would surface that error on every
// invocation after the first, breaking the documented "Close is safe
// to call multiple times" contract.
func TestShutdownIdempotent(t *testing.T) {
	m, err := New(Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatalf("New(enabled) err = %v", err)
	}
	if m == nil {
		t.Fatal("New(enabled) returned nil recorder")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("first Shutdown err = %v", err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown err = %v, want nil (idempotent)", err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Errorf("third Shutdown err = %v, want nil (idempotent)", err)
	}
}

// TestShutdownErrorIsCachedAndReturned exercises the error branch
// inside the sync.Once closure: when the underlying provider shutdown
// fails, the wrapped error is captured once and returned by every
// subsequent call. Without caching, the second call would return nil
// and mask a failed first shutdown.
//
// The shutdownFn field is overridden so the test does not depend on
// the SDK being able to fail on demand.
func TestShutdownErrorIsCachedAndReturned(t *testing.T) {
	m, err := New(Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatalf("New(enabled) err = %v", err)
	}
	if m == nil {
		t.Fatal("New(enabled) returned nil recorder")
	}

	sentinel := errors.New("provider boom")
	callCount := 0
	m.shutdownFn = func(_ context.Context) error {
		callCount++
		return sentinel
	}

	firstErr := m.Shutdown(context.Background())
	if firstErr == nil || !errors.Is(firstErr, sentinel) {
		t.Fatalf("first Shutdown err = %v, want wrapped %v", firstErr, sentinel)
	}
	if !strings.Contains(firstErr.Error(), "meter provider shutdown") {
		t.Errorf("first Shutdown err = %v, want wrapped meter-provider error", firstErr)
	}

	secondErr := m.Shutdown(context.Background())
	if secondErr == nil || secondErr.Error() != firstErr.Error() {
		t.Errorf("second Shutdown err = %v, want cached first error %v", secondErr, firstErr)
	}

	if callCount != 1 {
		t.Errorf("shutdownFn invocations = %d, want 1 (idempotent)", callCount)
	}
}

func TestListenerStartShutdown(t *testing.T) {
	m, err := New(Config{Enabled: true, ListenAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("New err = %v", err)
	}
	// Use NewListener even though the listener will pick its own
	// port via the net.Listen path. We can't readily verify the
	// bound port without exposing it, so we exercise start/stop
	// for lifecycle correctness only.
	defer func() {
		_ = m.Shutdown(context.Background())
	}()

	l := NewListener(m)
	if l == nil {
		t.Fatal("NewListener returned nil for enabled metrics")
	}
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start err = %v", err)
	}
	// Double-start should error so a misuse is caught early.
	if err := l.Start(context.Background()); err == nil {
		t.Errorf("second Start should error")
	}
	if err := l.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown err = %v", err)
	}
	// Idempotent Shutdown when not running.
	if err := (&Listener{}).Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown on unstarted = %v, want nil", err)
	}
}

func TestListenerStartFailsOnBusyAddr(t *testing.T) {
	// Hold an ephemeral port so the second listener fails fast on bind.
	hold, err := netListen()
	if err != nil {
		t.Fatalf("test setup: %v", err)
	}
	defer hold.Close() //nolint:errcheck // best-effort

	m, err := New(Config{Enabled: true, ListenAddr: hold.Addr().String()})
	if err != nil {
		t.Fatalf("New err = %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	l := NewListener(m)
	if err := l.Start(context.Background()); err == nil {
		t.Error("Start on busy addr should error")
		_ = l.Shutdown(context.Background())
	}
}

// scrapeMetrics drives the Prometheus handler with a real HTTP
// request and returns the response body. Using httptest.NewServer
// exercises the same handler chain that a real Prometheus scraper
// hits, including content-negotiation headers.
func scrapeMetrics(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape get: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup; body fully read above
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// netListen reserves an ephemeral port via the OS so the
// busy-addr test can target a guaranteed-occupied address.
func netListen() (net.Listener, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("observability test: listen: %w", err)
	}
	return ln, nil
}
