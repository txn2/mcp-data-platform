package platform

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

func TestObservability_EnabledByDefault(t *testing.T) {
	// Bind the listener to an ephemeral port so this test does not collide
	// with the package's fixed :9090 default when other tests run in parallel.
	t.Setenv("OTEL_METRICS_ADDR", "127.0.0.1:0")

	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()

	if p.Metrics() == nil {
		t.Fatal("Metrics() = nil with default env; want non-nil (enabled by default)")
	}
	// Wire call is idempotent and safe with no apigateway toolkit registered.
	p.WireAPIGatewayMetrics()
}

func TestObservability_ExplicitDisable(t *testing.T) {
	t.Setenv("OTEL_METRICS_ENABLED", "false")

	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()

	if p.Metrics() != nil {
		t.Errorf("Metrics() = non-nil with OTEL_METRICS_ENABLED=false; want nil")
	}
	// Start/Shutdown must be safe even when disabled.
	if err := p.StartMetricsListener(context.Background()); err != nil {
		t.Errorf("StartMetricsListener (disabled) err = %v", err)
	}
	if err := p.ShutdownMetricsListener(context.Background()); err != nil {
		t.Errorf("ShutdownMetricsListener (disabled) err = %v", err)
	}
	// Wire call is a no-op; must not panic on toolkit walk.
	p.WireAPIGatewayMetrics()
}

func TestObservability_EnabledStartsListener(t *testing.T) {
	// Find an ephemeral port and release it so the listener can bind.
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ephemeral port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	t.Setenv("OTEL_METRICS_ENABLED", "true")
	t.Setenv("OTEL_METRICS_ADDR", addr)

	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()

	if p.Metrics() == nil {
		t.Fatal("Metrics() = nil with env enabled; want non-nil")
	}
	if err := p.StartMetricsListener(context.Background()); err != nil {
		t.Fatalf("StartMetricsListener: %v", err)
	}
	defer func() {
		_ = p.ShutdownMetricsListener(context.Background())
	}()

	// Wire call is idempotent and safe even with no apigateway toolkit.
	p.WireAPIGatewayMetrics()

	// Verify the listener bound and serves /metrics. We don't assert
	// the body shape here — that's covered by the observability
	// package's own tests; this proves the platform wiring put the
	// listener on the configured address.
	url := "http://" + addr + "/metrics"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec,bodyclose // test code; URL is a literal ephemeral http://127.0.0.1, body closed below
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup; body fully read above
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /metrics status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "go_goroutines") {
		t.Errorf("expected go_goroutines in /metrics body; got:\n%s", string(body))
	}
}

// TestWireAPIGatewayMetrics_InstrumentsRegisteredToolkit exercises the
// loop body of WireAPIGatewayMetrics: with a real apigateway toolkit
// registered in the platform's registry, the wire step must thread
// the platform's metrics recorder through the toolkit so subsequent
// outbound calls record observations.
func TestWireAPIGatewayMetrics_InstrumentsRegisteredToolkit(t *testing.T) {
	t.Setenv("OTEL_METRICS_ENABLED", "true")
	t.Setenv("OTEL_METRICS_ADDR", "127.0.0.1:0") // not started in this test

	// Build a registry with an apigateway toolkit pre-registered so
	// the platform constructor consumes it via WithToolkitRegistry.
	reg := registry.NewRegistry()
	api := apigatewaykit.New("primary")
	if err := reg.Register(api); err != nil {
		t.Fatalf("register apigateway: %v", err)
	}

	p := newTestPlatform(t, WithToolkitRegistry(reg))
	defer func() { _ = p.Close() }()

	if p.Metrics() == nil {
		t.Fatal("Metrics() = nil with env enabled; want non-nil")
	}
	// Pre-wire: toolkit metrics is nil. Wire then confirm the toolkit
	// would emit on a subsequent outbound (we cannot easily invoke
	// the round-trip without standing up a server; the SetMetrics
	// unit test in pkg/toolkits/apigateway covers transport wrapping
	// directly, so here we only verify the wire call does not error
	// and the platform path exercises the loop body).
	p.WireAPIGatewayMetrics()
}
