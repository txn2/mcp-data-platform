package apigateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// captureSink is an in-memory RawSink for exercising the raw
// passthrough handler without an http.ResponseWriter.
type captureSink struct {
	headers   http.Header
	status    int
	statusSet bool
	body      bytes.Buffer
}

func newCaptureSink() *captureSink { return &captureSink{headers: http.Header{}} }

func (s *captureSink) AddHeader(key, value string) { s.headers.Add(key, value) }

func (s *captureSink) SetStatus(code int) {
	if !s.statusSet {
		s.statusSet = true
		s.status = code
	}
}

func (s *captureSink) Write(p []byte) (int, error) {
	if !s.statusSet {
		s.SetStatus(http.StatusOK)
	}
	n, _ := s.body.Write(p) // bytes.Buffer.Write never errors
	return n, nil
}

func TestReserveBodyBudget(t *testing.T) {
	tests := []struct {
		name          string
		max           int64
		preAcquire    int64
		contentLength int64
		readCap       int64
		wantReserved  int64
		wantOK        bool
	}{
		{name: "unknown length reserves the cap", max: 1000, contentLength: -1, readCap: 200, wantReserved: 200, wantOK: true},
		{name: "empty body reserves zero", max: 1000, contentLength: 0, readCap: 200, wantReserved: 0, wantOK: true},
		{name: "small content-length reserves only itself", max: 1000, contentLength: 50, readCap: 200, wantReserved: 50, wantOK: true},
		{name: "content-length above cap reserves the cap", max: 1000, contentLength: 5000, readCap: 200, wantReserved: 200, wantOK: true},
		{name: "refused when over budget", max: 100, preAcquire: 80, contentLength: -1, readCap: 50, wantReserved: 50, wantOK: false},
		{name: "exact fit admitted", max: 100, preAcquire: 50, contentLength: 50, readCap: 200, wantReserved: 50, wantOK: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := NewMemBudget(tc.max)
			if tc.preAcquire > 0 && !b.Acquire(tc.preAcquire) {
				t.Fatalf("pre-acquire %d failed", tc.preAcquire)
			}
			reserved, ok := reserveBodyBudget(b, tc.contentLength, tc.readCap)
			if reserved != tc.wantReserved {
				t.Errorf("reserved = %d; want %d", reserved, tc.wantReserved)
			}
			if ok != tc.wantOK {
				t.Errorf("ok = %v; want %v", ok, tc.wantOK)
			}
		})
	}
}

func TestReserveBodyBudgetNilBudgetAlwaysGrants(t *testing.T) {
	reserved, ok := reserveBodyBudget(nil, -1, 1<<30)
	if !ok {
		t.Fatal("nil budget must grant the reservation")
	}
	if reserved != 1<<30 {
		t.Errorf("reserved = %d; want the cap", reserved)
	}
}

func TestStructuredErrorResultCarriesCodeAndFields(t *testing.T) {
	r := structuredErrorResult("some_code", map[string]any{"limit_bytes": int64(42)})
	if !r.IsError {
		t.Fatal("structured error result must set IsError")
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(textContent(r)), &env); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if env["error"] != "some_code" {
		t.Errorf("error = %v; want some_code", env["error"])
	}
	if lb, _ := env["limit_bytes"].(float64); lb != 42 {
		t.Errorf("limit_bytes = %v; want 42", env["limit_bytes"])
	}
}

func TestBudgetErrorResultAndMessage(t *testing.T) {
	be := &budgetError{limit: 1000, requested: 200, inUse: 900, connection: "crm", path: "/x"}
	if !strings.Contains(be.Error(), ErrCodeBudgetExhausted) {
		t.Errorf("Error() = %q; want it to contain %q", be.Error(), ErrCodeBudgetExhausted)
	}
	r := be.result()
	var env map[string]any
	_ = json.Unmarshal([]byte(textContent(r)), &env)
	if env["error"] != ErrCodeBudgetExhausted {
		t.Errorf("error = %v; want %q", env["error"], ErrCodeBudgetExhausted)
	}
	if env["connection"] != "crm" {
		t.Errorf("connection = %v; want crm", env["connection"])
	}
}

func TestBodyTooLargeResult(t *testing.T) {
	r := bodyTooLargeResult("crm", "/big", 100, 5000)
	if !r.IsError {
		t.Fatal("body too large must be an error result")
	}
	var env map[string]any
	_ = json.Unmarshal([]byte(textContent(r)), &env)
	if env["error"] != ErrCodeBodyTooLarge {
		t.Errorf("error = %v; want %q", env["error"], ErrCodeBodyTooLarge)
	}
	if ab, _ := env["actual_bytes"].(float64); ab != 5000 {
		t.Errorf("actual_bytes = %v; want 5000", env["actual_bytes"])
	}
}

// TestInvoke_BudgetExhausted proves the core OOM guard: when the shared
// budget cannot accommodate the response buffer, invoke refuses with a
// *budgetError BEFORE reading the body, rather than allocating and
// risking OOM under concurrency.
func TestInvoke_BudgetExhausted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 100))
	}))
	t.Cleanup(upstream.Close)

	cfg := Config{
		BaseURL:          upstream.URL,
		AuthMode:         AuthModeNone,
		ConnectTimeout:   2 * time.Second,
		CallTimeout:      5 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
		ConnectionName:   "crm",
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	budget := NewMemBudget(1000)
	if !budget.Acquire(950) {
		t.Fatal("pre-acquire failed")
	}
	_, err = invoke(context.Background(), invocation{
		cfg: cfg, auth: auth, client: newHTTPClient(cfg), budget: budget,
	}, InvokeInput{Connection: "crm", Method: "GET", Path: "/items"})

	var be *budgetError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v; want *budgetError", err)
	}
	if be.limit != 1000 {
		t.Errorf("budgetError.limit = %d; want 1000", be.limit)
	}
	// The refused reservation must not have been committed: only the
	// pre-acquired 950 remains in use.
	if budget.InUse() != 950 {
		t.Errorf("InUse = %d; want 950 (refused reservation not committed)", budget.InUse())
	}
}

// TestInvoke_BudgetReleasedAfterCall proves the reservation is released
// once the buffered read completes, so the budget is reusable.
func TestInvoke_BudgetReleasedAfterCall(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(upstream.Close)

	cfg := Config{
		BaseURL: upstream.URL, AuthMode: AuthModeNone,
		ConnectTimeout: 2 * time.Second, CallTimeout: 5 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes, ConnectionName: "crm",
	}
	auth, _ := NewAuthenticator(cfg)
	budget := NewMemBudget(1 << 20)
	out, err := invoke(context.Background(), invocation{
		cfg: cfg, auth: auth, client: newHTTPClient(cfg), budget: budget,
	}, InvokeInput{Connection: "crm", Method: "GET", Path: "/x"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out.Status != 200 {
		t.Errorf("status = %d", out.Status)
	}
	if budget.InUse() != 0 {
		t.Errorf("InUse after call = %d; want 0 (reservation released)", budget.InUse())
	}
}

// TestHandleInvoke_BudgetExhaustedReturns429Envelope proves the
// toolkit-level handler renders a budget rejection as the structured
// ErrCodeBudgetExhausted envelope (which the REST shim maps to 429).
func TestHandleInvoke_BudgetExhaustedReturns429Envelope(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("y", 100))
	}))
	t.Cleanup(upstream.Close)

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": upstream.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	budget := NewMemBudget(1000)
	_ = budget.Acquire(950)
	tk.SetMemBudget(budget)

	r, _, _ := tk.handleInvoke(context.Background(), &mcp.CallToolRequest{}, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/items",
	})
	if r == nil || !r.IsError {
		t.Fatalf("want IsError result; got %+v", r)
	}
	if !strings.Contains(textContent(r), ErrCodeBudgetExhausted) {
		t.Errorf("payload = %s; want it to contain %q", textContent(r), ErrCodeBudgetExhausted)
	}
}

// TestHandleExport_BudgetExhausted proves api_export is no longer an
// independent OOM vector: its buffer reserves against the same budget,
// and a refused reservation aborts the export BEFORE the S3 upload.
func TestHandleExport_BudgetExhausted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("z", 100))
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{}
	ver := &fakeExportVersionStore{}
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(store, ver, s3)
	tk := buildExportTestToolkit(t, upstream.URL, &deps)

	budget := NewMemBudget(1000)
	_ = budget.Acquire(950)
	tk.SetMemBudget(budget)

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/items", Name: "dump",
	})
	if r == nil || !r.IsError {
		t.Fatalf("want IsError result; got %+v", r)
	}
	if !strings.Contains(textContent(r), ErrCodeBudgetExhausted) {
		t.Errorf("payload = %s; want it to contain %q", textContent(r), ErrCodeBudgetExhausted)
	}
	if len(s3.puts) != 0 {
		t.Errorf("S3 PutObject called %d times; budget rejection must abort before upload", len(s3.puts))
	}
}

// TestHandleInvokeRaw_StreamsBodyAndInjectsCredential proves the raw
// passthrough streams the upstream body verbatim to the sink with the
// upstream status and Content-Type, AND that the gateway still injects
// the held credential (the constraint that the caller never holds it).
func TestHandleInvokeRaw_StreamsBodyAndInjectsCredential(t *testing.T) {
	payload := bytes.Repeat([]byte{0x00, 0x01, 0x02, 0xff}, 64) // binary, not JSON
	var sawAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="blob.bin"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(upstream.Close)

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{
		"base_url": upstream.URL, "auth_mode": AuthModeBearer, "credential": "tok-raw",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	sink := newCaptureSink()
	ctx := WithRawPassthrough(context.Background(), &RawPassthrough{Sink: sink, MaxBytes: 1 << 20})
	r, _, _ := tk.handleInvoke(ctx, &mcp.CallToolRequest{}, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/blob",
	})
	if r == nil || r.IsError {
		t.Fatalf("raw stream returned error result: %+v", r)
	}
	if sawAuth != "Bearer tok-raw" {
		t.Errorf("upstream saw Authorization=%q; credential not injected on raw path", sawAuth)
	}
	if sink.status != http.StatusOK {
		t.Errorf("sink status = %d; want 200", sink.status)
	}
	if !bytes.Equal(sink.body.Bytes(), payload) {
		t.Errorf("streamed body mismatch: got %d bytes, want %d", sink.body.Len(), len(payload))
	}
	if got := sink.headers.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q; want application/octet-stream", got)
	}
	if got := sink.headers.Get("Content-Disposition"); !strings.Contains(got, "blob.bin") {
		t.Errorf("Content-Disposition = %q; want it to carry the filename", got)
	}
}

// TestHandleInvokeRaw_BodyTooLargeRejectsBeforeStreaming proves the
// 413 path: an upstream Content-Length over the raw cap is refused
// before any bytes (or status) are written to the sink.
func TestHandleInvokeRaw_BodyTooLargeRejectsBeforeStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := bytes.Repeat([]byte("A"), 5000)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(upstream.Close)

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": upstream.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	sink := newCaptureSink()
	ctx := WithRawPassthrough(context.Background(), &RawPassthrough{Sink: sink, MaxBytes: 1000})
	r, _, _ := tk.handleInvoke(ctx, &mcp.CallToolRequest{}, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/big",
	})
	if r == nil || !r.IsError {
		t.Fatalf("want IsError result for over-limit body; got %+v", r)
	}
	if !strings.Contains(textContent(r), ErrCodeBodyTooLarge) {
		t.Errorf("payload = %s; want %q", textContent(r), ErrCodeBodyTooLarge)
	}
	if sink.statusSet || sink.body.Len() != 0 {
		t.Errorf("413 must reject before streaming; sink wrote status=%v bytes=%d", sink.statusSet, sink.body.Len())
	}
}

// TestHandleInvokeRaw_TransportErrorBeforeStreaming proves a transport
// failure surfaces as an error result with nothing streamed, so the
// REST shim can map it to 502/504.
func TestHandleInvokeRaw_TransportErrorBeforeStreaming(t *testing.T) {
	// Point at a closed server so Do() fails immediately.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": deadURL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	sink := newCaptureSink()
	ctx := WithRawPassthrough(context.Background(), &RawPassthrough{Sink: sink, MaxBytes: 0})
	r, _, _ := tk.handleInvoke(ctx, &mcp.CallToolRequest{}, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/x",
	})
	if r == nil || !r.IsError {
		t.Fatalf("want IsError transport result; got %+v", r)
	}
	if sink.statusSet {
		t.Error("transport error must not have written a status to the sink")
	}
}

// TestHandleInvokeRaw_ChunkedExceedsLimit covers the case the
// Content-Length pre-check cannot catch: a chunked (undeclared length)
// body that runs past the cap. Headers are already flushed, so the
// stream is cut and a non-error sentinel returned (the client got a
// truncated body, which is the unavoidable chunked tradeoff).
func TestHandleInvokeRaw_ChunkedExceedsLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		// No Content-Length => chunked. Flush so it streams.
		flusher, _ := w.(http.Flusher)
		for range 10 {
			_, _ = w.Write(bytes.Repeat([]byte("Q"), 100))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(upstream.Close)

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": upstream.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	sink := newCaptureSink()
	ctx := WithRawPassthrough(context.Background(), &RawPassthrough{Sink: sink, MaxBytes: 250})
	r, _, _ := tk.handleInvoke(ctx, &mcp.CallToolRequest{}, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/stream",
	})
	if r == nil || r.IsError {
		t.Fatalf("chunked-over-limit returns a streamed sentinel, not an error: %+v", r)
	}
	// Status + some bytes were streamed; the copy stopped at limit+1.
	if !sink.statusSet {
		t.Error("status should have been streamed before truncation")
	}
	if int64(sink.body.Len()) > 251 {
		t.Errorf("streamed %d bytes; want it bounded at limit+1 (251)", sink.body.Len())
	}
}

// connForTest pulls the materialized conn for a registered connection
// so buildRawRequest's validation branches can be exercised directly.
func connForTest(t *testing.T, baseURL string) *conn {
	t.Helper()
	tk := New("p")
	if err := tk.AddConnection("crm", map[string]any{"base_url": baseURL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.mu.RLock()
	defer tk.mu.RUnlock()
	return tk.connections["crm"]
}

func TestBuildUpstreamRequest(t *testing.T) {
	c := connForTest(t, "https://api.example.com")
	tests := []struct {
		name    string
		in      InvokeInput
		wantErr bool
	}{
		{name: "ok", in: InvokeInput{Method: "GET", Path: "/items"}, wantErr: false},
		{name: "bad method", in: InvokeInput{Method: "TRACE", Path: "/x"}, wantErr: true},
		{name: "bad path", in: InvokeInput{Method: "GET", Path: "no-slash"}, wantErr: true},
		{name: "reserved header", in: InvokeInput{Method: "GET", Path: "/x", Headers: map[string]string{"Authorization": "x"}}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := buildUpstreamRequest(context.Background(), c.cfg, c.auth, c.specs, tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildUpstreamRequest: %v", err)
			}
			if req == nil {
				t.Fatal("nil request on success")
			}
		})
	}
}

func TestStreamRaw_NoLimitCopiesAll(t *testing.T) {
	body := strings.Repeat("Z", 4096)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	sink := newCaptureSink()
	n, err := streamRaw(sink, resp, 0)
	if err != nil {
		t.Fatalf("streamRaw: %v", err)
	}
	if n != int64(len(body)) || sink.body.String() != body {
		t.Errorf("streamed %d bytes; want %d", n, len(body))
	}
	if sink.status != http.StatusOK {
		t.Errorf("status = %d", sink.status)
	}
}

// TestStreamRaw_MaxInt64LimitDoesNotOverflow guards the limit+1
// overflow: with an effectively-unlimited cap (math.MaxInt64), the
// stream must copy the whole body, not zero bytes (which a wrapped
// negative LimitReader bound would cause).
func TestStreamRaw_MaxInt64LimitDoesNotOverflow(t *testing.T) {
	body := strings.Repeat("M", 2048)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	sink := newCaptureSink()
	n, err := streamRaw(sink, resp, math.MaxInt64)
	if err != nil {
		t.Fatalf("streamRaw: %v", err)
	}
	if n != int64(len(body)) || sink.body.Len() != len(body) {
		t.Errorf("streamed %d bytes; want %d (limit+1 overflow must not truncate)", n, len(body))
	}
}

func TestCopyRawHeaders_OnlyForwardsAllowlistedSet(t *testing.T) {
	in := http.Header{}
	in.Set("Content-Type", "image/png")
	in.Set("Set-Cookie", "session=secret") // must NOT be forwarded
	in.Set("Etag", `"abc"`)
	sink := newCaptureSink()
	copyRawHeaders(in, sink)
	if sink.headers.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type not forwarded: %q", sink.headers.Get("Content-Type"))
	}
	if sink.headers.Get("Etag") != `"abc"` {
		t.Errorf("Etag not forwarded: %q", sink.headers.Get("Etag"))
	}
	if sink.headers.Get("Set-Cookie") != "" {
		t.Error("Set-Cookie must not be forwarded on the raw path")
	}
}
