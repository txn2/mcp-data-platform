package apigwmetrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func TestTransport_RecordsOutbound(t *testing.T) {
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

	client := &http.Client{Transport: New(http.DefaultTransport, "primary", m)}
	for _, path := range []string{"/ok", "/ok", "/boom"} {
		req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL+path, http.NoBody)
		if reqErr != nil {
			t.Fatalf("new req: %v", reqErr)
		}
		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("do %s: %v", path, doErr)
		}
		_ = resp.Body.Close()
	}

	body := scrapeMetricsHandler(t, m.Handler())
	// No tool call stamped a persona on these contexts, so the counter
	// records the one fixed unresolved value; the histogram carries no
	// persona dimension at all (#1615).
	wantSeries := []string{
		`apigateway_outbound_total{connection="primary",http_status_class="2xx",persona="unknown",status_category="ok"} 2`,
		`apigateway_outbound_total{connection="primary",http_status_class="5xx",persona="unknown",status_category="upstream_err"} 1`,
		`apigateway_outbound_duration_seconds_count{connection="primary",http_status_class="2xx",status_category="ok"} 2`,
	}
	for _, want := range wantSeries {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestNew_DisabledReturnsBare(t *testing.T) {
	base := http.DefaultTransport
	if got := New(base, "primary", nil); got != base {
		t.Errorf("nil-metrics wrap returned a wrapping transport; want bare base")
	}
}

func TestInstrument_NilSafety(t *testing.T) {
	// Nil client + nil metrics: must not panic.
	Instrument(nil, "x", nil)
	// Nil metrics + real client: client should be unchanged.
	c := &http.Client{Transport: http.DefaultTransport}
	Instrument(c, "x", nil)
	if c.Transport != http.DefaultTransport {
		t.Errorf("nil metrics wrapped transport; want unchanged")
	}
}

// TestInstrument_IsIdempotent holds the contract the toolkit's SetMetrics
// depends on: a second wrap for the same (connection, recorder) pair must not
// nest a second recorder, which would double-record every outbound call.
func TestInstrument_IsIdempotent(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	bare := http.DefaultTransport
	c := &http.Client{Transport: bare}
	Instrument(c, "primary", m)
	base, ok := Wraps(c, "primary")
	if !ok {
		t.Fatalf("client transport is %T after Instrument; want the recorder", c.Transport)
	}
	if base != bare {
		t.Error("the recorder wrapped something other than the original transport; ssrf/redirect guards depend on the original")
	}

	Instrument(c, "primary", m)
	base, ok = Wraps(c, "primary")
	if !ok {
		t.Fatalf("client transport is %T after a repeated Instrument; want the recorder", c.Transport)
	}
	if _, nested := base.(*transport); nested {
		t.Error("a repeated Instrument double-wrapped the transport")
	}
}

// TestWraps_RejectsOtherTransports keeps the reporter honest: an unwrapped
// client, and one wrapped for a different connection, are both a miss.
func TestWraps_RejectsOtherTransports(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	if _, ok := Wraps(nil, "primary"); ok {
		t.Error("Wraps(nil) reported a recorder")
	}
	if _, ok := Wraps(&http.Client{Transport: http.DefaultTransport}, "primary"); ok {
		t.Error("Wraps reported a recorder on a bare transport")
	}
	c := &http.Client{Transport: http.DefaultTransport}
	Instrument(c, "primary", m)
	if _, ok := Wraps(c, "secondary"); ok {
		t.Error("Wraps matched a recorder bound to a different connection")
	}
}

func TestTransport_TransportErrorClassified(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	// errorTransport always returns a transport-level error so the
	// recorder sees the "no response + non-nil err" branch.
	client := &http.Client{Transport: New(&errorTransport{}, "primary", m)}
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
	want := `apigateway_outbound_total{connection="primary",http_status_class="other",persona="unknown",status_category="upstream_err"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("scrape missing %q\n--- body ---\n%s", want, body)
	}
}

// TestTransport_LabelsPersonaFromContext proves the transport reads the persona
// the tool-call middleware stamped on the call context, so two principals
// sharing one connection are two series (#1615).
func TestTransport_LabelsPersonaFromContext(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	client := &http.Client{Transport: New(http.DefaultTransport, "shared", m)}
	for _, persona := range []string{"ingest-service", "ingest-service", "analyst"} {
		ctx := mcpcontext.WithPersona(context.Background(), persona)
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL, http.NoBody)
		if reqErr != nil {
			t.Fatalf("new req: %v", reqErr)
		}
		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("do (%s): %v", persona, doErr)
		}
		_ = resp.Body.Close()
	}

	body := scrapeMetricsHandler(t, m.Handler())
	wantSeries := []string{
		`apigateway_outbound_total{connection="shared",http_status_class="2xx",persona="ingest-service",status_category="ok"} 2`,
		`apigateway_outbound_total{connection="shared",http_status_class="2xx",persona="analyst",status_category="ok"} 1`,
	}
	for _, want := range wantSeries {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n--- body ---\n%s", want, body)
		}
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
