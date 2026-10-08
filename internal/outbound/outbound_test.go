package outbound

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/internal/egressguard"
	"github.com/txn2/mcp-data-platform/internal/useragent"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func newMetrics(t *testing.T) *observability.Metrics {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m
}

func scrape(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	return rec.Body.String()
}

func recordingTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
		_ = tp.Shutdown(context.Background())
	})
	return sr
}

// upstream records what it received.
type upstream struct {
	srv     *httptest.Server
	headers chan http.Header
}

func newUpstream(t *testing.T, status int) *upstream {
	t.Helper()
	u := &upstream{headers: make(chan http.Header, 16)}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.headers <- r.Header.Clone()
		w.WriteHeader(status)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) received(t *testing.T) http.Header {
	t.Helper()
	select {
	case h := <-u.headers:
		return h
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream received nothing")
		return nil
	}
}

// TestNewClient_RecordsSpansMetricsAndPropagates: one request through the
// chain yields a client span under the caller's span, a traceparent at the
// upstream carrying the same trace, the platform's User-Agent, and one
// http_client_requests_total sample under its kind and connection.
func TestNewClient_RecordsSpansMetricsAndPropagates(t *testing.T) {
	sr := recordingTracer(t)
	m := newMetrics(t)
	up := newUpstream(t, http.StatusAccepted)

	client := NewClient(Options{Kind: KindAPI, Connection: "crm", Metrics: m, Timeout: 5 * time.Second})
	ctx, parent := otel.Tracer("test").Start(context.Background(), "tools/call api_invoke_endpoint")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, up.srv.URL+"/v1/echo", http.NoBody)
	require.NoError(t, err)
	res, err := client.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	parent.End()

	h := up.received(t)
	require.Equal(t, useragent.Product(), h.Get("User-Agent"))
	tp := h.Get("traceparent")
	require.NotEmpty(t, tp, "the upstream continues the trace")
	require.Contains(t, tp, parent.SpanContext().TraceID().String())

	var client1 sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.SpanKind() == trace.SpanKindClient {
			client1 = s
		}
	}
	require.NotNil(t, client1, "a client span")
	require.Equal(t, "api POST", client1.Name())
	require.Equal(t, parent.SpanContext().SpanID(), client1.Parent().SpanID(), "under the tool call")
	attrs := map[string]string{}
	for _, kv := range client1.Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	require.Equal(t, "api", attrs["upstream.kind"])
	require.Equal(t, "crm", attrs["mcp.connection"])
	require.Equal(t, "202", attrs["http.response.status_code"])

	body := scrape(t, m)
	require.Contains(t, body, `http_client_requests_total{connection="crm",kind="api",status_class="2xx"} 1`)
	require.Contains(t, body, `http_client_request_duration_seconds_count{connection="crm",kind="api",status_class="2xx"} 1`)
}

func TestNewClient_NoPropagationLeavesTheHeadersOff(t *testing.T) {
	recordingTracer(t)
	up := newUpstream(t, http.StatusOK)
	client := NewClient(Options{Kind: KindGraphQL, Connection: "gql", NoPropagation: true})
	ctx, parent := otel.Tracer("test").Start(context.Background(), "parent")
	defer parent.End()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, up.srv.URL, http.NoBody)
	require.NoError(t, err)
	res, err := client.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	h := up.received(t)
	require.Empty(t, h.Get("traceparent"))
	require.Empty(t, h.Get("tracestate"))
	require.Equal(t, useragent.Product(), h.Get("User-Agent"), "the User-Agent is not a trace header")
}

func TestNewClient_RefusesRedirectsByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	client := NewClient(Options{Kind: KindOIDC})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	res, err := client.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode, "the redirect is handed back, not followed")

	followed := false
	client = NewClient(Options{Kind: KindUtil, CheckRedirect: func(*http.Request, []*http.Request) error {
		followed = true
		return http.ErrUseLastResponse
	}})
	req, err = http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	res, err = client.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.True(t, followed, "a caller's policy is used")
}

// TestTransport_RecordsTheDefaultRecorderAndTransportFailures: a client built
// with no recorder records through the default the observability layer
// installed, and a dial failure is status_class other.
func TestTransport_RecordsTheDefaultRecorderAndTransportFailures(t *testing.T) {
	m := newMetrics(t)
	SetDefaultMetrics(m)
	t.Cleanup(func() { SetDefaultMetrics(nil) })

	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closed := l.Addr().String()
	require.NoError(t, l.Close())

	client := NewClient(Options{Kind: KindPromQL, Timeout: 2 * time.Second})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+closed+"/api/v1/query", http.NoBody)
	require.NoError(t, err)
	_, err = client.Do(req) //nolint:bodyclose // the dial fails; there is no body
	require.Error(t, err)
	require.Contains(t, scrape(t, m), `http_client_requests_total{connection="",kind="promql",status_class="other"} 1`)
}

// TestTransport_EgressRefusalIsCountedAndLogged: a guard's refusal below the
// chain is counted under its class and logged with the host, and the error
// reaches the caller as the guard produced it.
func TestTransport_EgressRefusalIsCountedAndLogged(t *testing.T) {
	m := newMetrics(t)
	var logs strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	guard, err := egressguard.New(nil)
	require.NoError(t, err)
	client := NewClient(Options{Kind: KindUtil, Base: guard.Transport(), Metrics: m, Timeout: 2 * time.Second})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:9/", http.NoBody)
	require.NoError(t, err)
	_, err = client.Do(req) //nolint:bodyclose // the dial is refused; there is no body
	var blocked *egressguard.BlockedError
	require.ErrorAs(t, err, &blocked)
	require.Equal(t, egressguard.ClassLoopback, blocked.Class)

	require.Contains(t, scrape(t, m), `egress_blocked_total{reason="loopback"} 1`)
	require.Contains(t, logs.String(), `msg="egress blocked"`)
	require.Contains(t, logs.String(), `host=127.0.0.1`)
	require.Contains(t, logs.String(), `kind=util`)
}

func TestRecordBlocked(t *testing.T) {
	m := newMetrics(t)
	SetDefaultMetrics(m)
	t.Cleanup(func() { SetDefaultMetrics(nil) })
	RecordBlocked(context.Background(), KindSpecFetch, "metadata.google.internal", egressguard.ClassInternalHostname)
	require.Contains(t, scrape(t, m), `egress_blocked_total{reason="internal_hostname"} 1`)
}

func TestTransport_WrapsAndCloseIdleConnections(t *testing.T) {
	base := &http.Transport{}
	rt := Transport(base, Options{Kind: KindMCP, Connection: "up"})
	chain, ok := Wraps(rt)
	require.True(t, ok)
	require.Same(t, base, chain.Base)
	require.Equal(t, KindMCP, chain.Kind)
	require.Equal(t, "up", chain.Connection)
	closer, isCloser := rt.(interface{ CloseIdleConnections() })
	require.True(t, isCloser)
	closer.CloseIdleConnections()
	_, ok = Wraps(http.DefaultTransport)
	require.False(t, ok)

	// a nil base is a fresh transport, never the process-wide one
	fresh, _ := Wraps(Transport(nil, Options{Kind: KindBranding}))
	require.NotSame(t, http.DefaultTransport, fresh.Base)
	require.IsType(t, &http.Transport{}, fresh.Base)
}

// A connection kind's answers are that connection's state (#1898): a refused
// credential, an upstream that cannot answer and a healthy one each leave the
// state the connection gauge reports. A platform-wide kind (oauth) has no
// toolkit connection and records none.
func TestTransport_RecordsTheConnectionsState(t *testing.T) {
	status := http.StatusUnauthorized
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()

	get := func(kind Kind, conn string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
		resp, err := NewClient(Options{Kind: kind, Connection: conn}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, connstate.AuthFailed},
		{http.StatusServiceUnavailable, connstate.Unreachable},
		{http.StatusOK, connstate.Healthy},
	} {
		status = tc.status
		get(KindAPI, "crm")
		if got, _ := connstate.State(string(KindAPI), "crm"); got != tc.want {
			t.Errorf("after HTTP %d the connection is %q, want %q", tc.status, got, tc.want)
		}
	}
	get(KindOAuth, "crm")
	if _, ok := connstate.State(string(KindOAuth), "crm"); ok {
		t.Error("a token endpoint call recorded a connection state")
	}
}
