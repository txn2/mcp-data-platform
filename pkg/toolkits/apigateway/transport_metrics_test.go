package apigateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func TestInstrumentedTransport_RecordsOutbound(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	// Upstream returns 200 on /ok and 500 on /boom so we can prove
	// the class label is bucketed correctly.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/boom":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	base := http.DefaultTransport
	rt := newInstrumentedTransport(base, "primary", m)
	client := &http.Client{Transport: rt}

	for _, path := range []string{"/ok", "/ok", "/boom"} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL+path, http.NoBody)
		if err != nil {
			t.Fatalf("new req: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("do %s: %v", path, err)
		}
		_ = resp.Body.Close()
	}

	body := scrapeMetricsHandler(t, m.Handler())
	wantSeries := []string{
		`apigateway_outbound_total{connection="primary",http_status_class="2xx",status_category="ok"} 2`,
		`apigateway_outbound_total{connection="primary",http_status_class="5xx",status_category="upstream_err"} 1`,
		`apigateway_outbound_duration_seconds_count{connection="primary",http_status_class="2xx",status_category="ok"} 2`,
	}
	for _, want := range wantSeries {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestInstrumentedTransport_DisabledReturnsBare(t *testing.T) {
	base := http.DefaultTransport
	got := newInstrumentedTransport(base, "primary", nil)
	if got != base {
		t.Errorf("nil-metrics wrap returned a wrapping transport; want bare base")
	}
}

func TestInstrumentClient_NilSafety(t *testing.T) {
	// Nil client + nil metrics: must not panic.
	instrumentClient(nil, "x", nil)
	// Nil metrics + real client: client should be unchanged.
	c := &http.Client{Transport: http.DefaultTransport}
	instrumentClient(c, "x", nil)
	if c.Transport != http.DefaultTransport {
		t.Errorf("nil metrics wrapped transport; want unchanged")
	}
}

func TestInstrumentedTransport_TransportErrorClassified(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	// errorTransport always returns a transport-level error so the
	// recorder sees the "no response + non-nil err" branch.
	base := &errorTransport{}
	rt := newInstrumentedTransport(base, "primary", m)
	client := &http.Client{Transport: rt}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://unreachable.invalid/x", http.NoBody)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	resp, doErr := client.Do(req)
	if doErr == nil {
		t.Fatal("client.Do should have errored")
	}
	if resp != nil {
		_ = resp.Body.Close()
	}

	body := scrapeMetricsHandler(t, m.Handler())
	want := `apigateway_outbound_total{connection="primary",http_status_class="other",status_category="upstream_err"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("scrape missing %q\n--- body ---\n%s", want, body)
	}
}

func TestToolkit_SetMetrics_WrapsExistingConnections(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	// Build a toolkit with a connection added BEFORE metrics arrive.
	// SetMetrics must wrap the connection's existing transport so the
	// retro-wire path emits observations on the next outbound call.
	tk := New("primary")
	cfg := Config{
		BaseURL:        "http://127.0.0.1:1", // unused; we never call out
		CallTimeout:    1,
		ConnectTimeout: 1,
		AuthMode:       AuthModeNone,
	}
	if err := tk.addParsedConnection("primary", cfg); err != nil {
		t.Fatalf("addParsedConnection: %v", err)
	}

	// Before SetMetrics the transport is the bare http.Transport.
	c1 := tk.connections["primary"].client
	bareTransport := c1.Transport
	if _, isInstr := bareTransport.(*instrumentedTransport); isInstr {
		t.Fatal("connection transport pre-SetMetrics should not be wrapped")
	}

	tk.SetMetrics(m)

	// After SetMetrics the transport must be wrapped, and unwrapping
	// it must surface the original bare transport so subsequent
	// SSRF / redirect handling continues to work.
	c2 := tk.connections["primary"].client
	wrapped, ok := c2.Transport.(*instrumentedTransport)
	if !ok {
		t.Fatalf("connection transport post-SetMetrics is %T, want *instrumentedTransport", c2.Transport)
	}
	if wrapped.connection != "primary" {
		t.Errorf("wrapped.connection = %q, want %q", wrapped.connection, "primary")
	}
	if wrapped.base != bareTransport {
		t.Error("wrapped.base differs from the pre-wrap transport; ssrf/redirect guards depend on the original")
	}

	// SetMetrics(nil) is a no-op that does not unwrap (documented).
	tk.SetMetrics(nil)
	if _, stillWrapped := tk.connections["primary"].client.Transport.(*instrumentedTransport); !stillWrapped {
		t.Error("SetMetrics(nil) unwrapped the transport; the contract is no-op")
	}

	// Second call with the same recorder must be idempotent —
	// instrumentClient skips already-wrapped transports for the
	// same (connection, metrics) pair. Otherwise double-wrapping
	// would double-record every outbound call.
	tk.SetMetrics(m)
	tk.SetMetrics(m)
	idempotent, ok := tk.connections["primary"].client.Transport.(*instrumentedTransport)
	if !ok {
		t.Fatalf("expected wrapped transport after repeated SetMetrics, got %T", tk.connections["primary"].client.Transport)
	}
	if _, nested := idempotent.base.(*instrumentedTransport); nested {
		t.Error("repeated SetMetrics(m) double-wrapped the transport; instrumentClient must be idempotent")
	}
}

type errorTransport struct{}

func (*errorTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, errSimulatedTransport
}

var errSimulatedTransport = simulatedError("simulated dial failure")

type simulatedError string

func (e simulatedError) Error() string { return string(e) }

func scrapeMetricsHandler(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("scrape req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup; body fully read above
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(body)
}
