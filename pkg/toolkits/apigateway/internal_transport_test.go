package apigateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/utilhandler"
)

func TestParseConfig_Handler(t *testing.T) {
	t.Run("internal fills synthetic base URL", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{"handler": HandlerInternal})
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		if cfg.Handler != HandlerInternal {
			t.Errorf("Handler = %q", cfg.Handler)
		}
		if cfg.BaseURL != internalBaseURL {
			t.Errorf("BaseURL = %q; want synthetic %q", cfg.BaseURL, internalBaseURL)
		}
	})
	t.Run("unknown handler refused", func(t *testing.T) {
		if _, err := ParseConfig(map[string]any{"handler": "sidecar", "base_url": "https://x"}); err == nil {
			t.Fatal("expected error for unknown handler value")
		}
	})
	t.Run("internal requires auth_mode none", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{"handler": HandlerInternal, "auth_mode": AuthModeBearer, "credential": "tok"})
		if err == nil || !strings.Contains(err.Error(), "auth_mode=none") {
			t.Fatalf("err = %v; want auth_mode=none requirement", err)
		}
	})
	t.Run("internal incompatible with identity passthrough", func(t *testing.T) {
		_, err := ParseConfig(map[string]any{"handler": HandlerInternal, "identity_passthrough": true})
		if err == nil || !strings.Contains(err.Error(), "identity_passthrough") {
			t.Fatalf("err = %v; want identity_passthrough refusal", err)
		}
	})
	t.Run("empty handler unchanged for normal connections", func(t *testing.T) {
		cfg, err := ParseConfig(map[string]any{"base_url": "https://api.example.com"})
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		if cfg.Handler != "" || cfg.BaseURL != "https://api.example.com" {
			t.Errorf("cfg = %+v; normal connection must be untouched", cfg)
		}
	})
}

func TestAddConnection_InternalRequiresHandlerWired(t *testing.T) {
	tk := New("api")
	err := tk.AddConnection("util", map[string]any{"handler": HandlerInternal})
	if err == nil || !strings.Contains(err.Error(), "SetInternalHandler") {
		t.Fatalf("err = %v; want SetInternalHandler requirement", err)
	}
	tk.SetInternalHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if err := tk.AddConnection("util", map[string]any{"handler": HandlerInternal}); err != nil {
		t.Fatalf("AddConnection after SetInternalHandler: %v", err)
	}
	if !tk.HasConnection("util") {
		t.Fatal("connection not registered")
	}
}

func TestInternalRoundTripper_BasicResponse(t *testing.T) {
	rt := &internalRoundTripper{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/util/fetch" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "15")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"ok":"boiled"}`))
	})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://internal.invalid/util/fetch", http.NoBody)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if resp.ContentLength != 15 {
		t.Errorf("ContentLength = %d; want 15 (export cap check reads this)", resp.ContentLength)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"ok":"boiled"}` {
		t.Errorf("body = %q", body)
	}
}

// TestInternalRoundTripper_StreamsLargeBody proves the response flows
// through the pipe without buffering the whole body: the handler
// writes far more than any single buffer while the reader drains
// concurrently.
func TestInternalRoundTripper_StreamsLargeBody(t *testing.T) {
	const chunk = 64 * 1024
	const chunks = 128 // 8 MiB total
	rt := &internalRoundTripper{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		buf := bytes.Repeat([]byte("x"), chunk)
		for range chunks {
			if _, err := w.Write(buf); err != nil {
				return
			}
		}
	})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal.invalid/big", http.NoBody)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		t.Fatalf("draining: %v", err)
	}
	if n != chunk*chunks {
		t.Errorf("drained %d bytes; want %d", n, chunk*chunks)
	}
}

func TestInternalRoundTripper_HandlerWritesNothing(t *testing.T) {
	rt := &internalRoundTripper{handler: http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal.invalid/", http.NoBody)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want implicit 200", resp.StatusCode)
	}
	if body, _ := io.ReadAll(resp.Body); len(body) != 0 {
		t.Errorf("body = %q; want empty", body)
	}
	if resp.ContentLength != -1 {
		t.Errorf("ContentLength = %d; want -1 (undeclared)", resp.ContentLength)
	}
}

func TestInternalRoundTripper_HandlerPanic(t *testing.T) {
	rt := &internalRoundTripper{handler: http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal.invalid/", http.NoBody)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500 from panicking handler", resp.StatusCode)
	}
	if _, rerr := io.ReadAll(resp.Body); rerr == nil {
		t.Error("expected body read error from aborted handler")
	}
}

func TestInternalRoundTripper_ContextCanceledBeforeHeader(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	rt := &internalRoundTripper{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release // hold the header back until the test releases it
		w.WriteHeader(http.StatusOK)
	})}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://internal.invalid/", http.NoBody)
	go func() {
		<-started
		cancel()
	}()
	if _, err := rt.RoundTrip(req); !errors.Is(err, context.Canceled) { //nolint:bodyclose // RoundTrip returns a nil response together with the context error
		t.Fatalf("err = %v; want context.Canceled", err)
	}
}

func TestContentLengthFromHeader(t *testing.T) {
	tests := []struct {
		value string
		want  int64
	}{
		{"", -1},
		{"1024", 1024},
		{"0", 0},
		{"-5", -1},
		{"notanumber", -1},
	}
	for _, tt := range tests {
		h := http.Header{}
		if tt.value != "" {
			h.Set("Content-Length", tt.value)
		}
		if got := contentLengthFromHeader(h); got != tt.want {
			t.Errorf("contentLengthFromHeader(%q) = %d; want %d", tt.value, got, tt.want)
		}
	}
}

// buildUtilTestToolkit wires the REAL assembled chain: utilhandler
// (with loopback allowed via the operator CIDR knob) dispatched
// through SetInternalHandler + a handler=internal connection — the
// same wiring utilconn performs at boot.
func buildUtilTestToolkit(t *testing.T) *Toolkit {
	t.Helper()
	h, err := utilhandler.New(utilhandler.Options{AllowPrivateCIDRs: []string{"127.0.0.0/8", "::1/128"}})
	if err != nil {
		t.Fatalf("utilhandler.New: %v", err)
	}
	tk := New("api")
	tk.SetInternalHandler(h)
	if err := tk.AddConnection("util", map[string]any{
		"handler":         HandlerInternal,
		"auth_mode":       AuthModeNone,
		"connection_name": "util",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	return tk
}

// TestHandleInvoke_UtilFetchInline is the end-to-end inline path:
// api_invoke_endpoint -> internal transport -> utilhandler ->
// SSRF-guarded fetch of a real HTTP server -> parsed JSON back
// through the normal invoke envelope.
func TestHandleInvoke_UtilFetchInline(t *testing.T) {
	const rawQuery = "sig=abc%2B%2F%3D&se=2026-07-21"
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows":[{"id":1}]}`))
	}))
	t.Cleanup(upstream.Close)

	tk := buildUtilTestToolkit(t)
	res, payload, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "util",
		Method:     http.MethodPost,
		Path:       utilhandler.FetchPath,
		Body:       map[string]any{"url": upstream.URL + "/rows?" + rawQuery},
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", textContent(res))
	}
	out, ok := payload.(InvokeOutput)
	if !ok {
		t.Fatalf("payload type %T", payload)
	}
	if out.Status != http.StatusOK {
		t.Errorf("status = %d (error %q)", out.Status, out.Error)
	}
	if gotQuery != rawQuery {
		t.Errorf("upstream RawQuery = %q; want byte-identical %q", gotQuery, rawQuery)
	}
	body, _ := json.Marshal(out.Body)
	if !strings.Contains(string(body), `"id":1`) {
		t.Errorf("body = %s; want fetched JSON parsed inline", body)
	}
}

// TestHandleInvoke_UtilFetchBlockedDestination proves the refusal
// travels the whole chain: guard 403 -> handler error envelope ->
// invoke output with Status 403.
func TestHandleInvoke_UtilFetchBlockedDestination(t *testing.T) {
	tk := buildUtilTestToolkit(t)
	res, payload, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "util",
		Method:     http.MethodPost,
		Path:       utilhandler.FetchPath,
		Body:       map[string]any{"url": "http://192.168.1.1/router"},
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool-level error: %s", textContent(res))
	}
	out, ok := payload.(InvokeOutput)
	if !ok {
		t.Fatalf("payload type %T; want InvokeOutput", payload)
	}
	if out.Status != http.StatusForbidden {
		t.Errorf("status = %d; want 403 relayed from the guard", out.Status)
	}
}

// TestHandleExport_UtilFetchStreamsToAsset is the end-to-end export
// path: api_export -> internal transport -> utilhandler fetch -> the
// full body streamed into the (fake) asset store, byte-identical.
func TestHandleExport_UtilFetchStreamsToAsset(t *testing.T) {
	payload := bytes.Repeat([]byte("row,val\n1,x\n"), 200_000) // ~2.4 MB
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{}
	ver := &fakeExportVersionStore{}
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(store, ver, s3)
	tk := buildUtilTestToolkit(t)
	tk.SetExportDeps(deps)

	res, out, err := tk.handleExport(context.Background(), nil, exportInput{
		Connection: "util",
		Method:     http.MethodPost,
		Path:       utilhandler.FetchPath,
		Body:       map[string]any{"url": upstream.URL + "/export.csv"},
		Name:       "signed-download.csv",
	})
	if err != nil {
		t.Fatalf("handleExport: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", textContent(res))
	}
	eo, ok := out.(*exportOutput)
	if !ok {
		t.Fatalf("output type %T; want *exportOutput", out)
	}
	if eo.Status != http.StatusOK {
		t.Errorf("upstream_status = %d", eo.Status)
	}
	if eo.ContentType != "text/csv" {
		t.Errorf("content type = %q; want text/csv relayed", eo.ContentType)
	}
	if len(s3.puts) != 1 {
		t.Fatalf("S3 puts = %d; want 1", len(s3.puts))
	}
	if !bytes.Equal(s3.puts[0].Data, payload) {
		t.Errorf("persisted %d bytes; want byte-identical %d", len(s3.puts[0].Data), len(payload))
	}
	if len(store.inserted) != 1 || store.inserted[0].SizeBytes != int64(len(payload)) {
		t.Errorf("asset row = %+v; want one row sized %d", store.inserted, len(payload))
	}
}

// TestHandleExport_UtilFetchTruncatedUpstreamAborts pins the
// all-or-nothing export contract for the internal path: an upstream
// that declares a Content-Length larger than the body it delivers
// (a mid-stream truncation) must abort the export with NO asset row
// and NO S3 object, exactly as the normal proxy export path does.
func TestHandleExport_UtilFetchTruncatedUpstreamAborts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "100000") // promise 100 KB
		_, _ = w.Write([]byte(`{"partial":true}`)) // deliver ~16 bytes, then close
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(upstream.Close)

	store := &fakeExportAssetStore{}
	ver := &fakeExportVersionStore{}
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(store, ver, s3)
	tk := buildUtilTestToolkit(t)
	tk.SetExportDeps(deps)

	res, _, err := tk.handleExport(context.Background(), nil, exportInput{
		Connection: "util",
		Method:     http.MethodPost,
		Path:       utilhandler.FetchPath,
		Body:       map[string]any{"url": upstream.URL + "/truncated"},
		Name:       "truncated.json",
	})
	if err != nil {
		t.Fatalf("handleExport: %v", err)
	}
	if !res.IsError {
		t.Fatal("truncated upstream must fail the export, not land a partial asset")
	}
	if len(store.inserted) != 0 {
		t.Errorf("asset rows = %d; want 0 (all-or-nothing)", len(store.inserted))
	}
}

// TestHandleInvoke_UtilTimeout pins timeout behavior through the full
// chain: the invoke timeout bounds the internal handler's outbound
// fetch, and the caller gets a gateway-timeout outcome rather than a
// hang.
func TestHandleInvoke_UtilTimeout(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(upstream.Close)

	tk := buildUtilTestToolkit(t)
	done := make(chan struct{})
	var out InvokeOutput
	go func() {
		defer close(done)
		_, payload, _ := tk.handleInvoke(context.Background(), nil, InvokeInput{
			Connection:     "util",
			Method:         http.MethodPost,
			Path:           utilhandler.FetchPath,
			Body:           map[string]any{"url": upstream.URL},
			TimeoutSeconds: 1,
		})
		if p, ok := payload.(InvokeOutput); ok {
			out = p
		}
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("invoke did not return; timeout not propagating through internal transport")
	}
	// Either shape is a correct timeout surface: a gateway-level
	// timeout (Status 0 + Error) or the handler's own 504.
	if out.Status != 0 && out.Status != http.StatusGatewayTimeout {
		t.Errorf("status = %d (error %q); want timeout outcome", out.Status, out.Error)
	}
}
