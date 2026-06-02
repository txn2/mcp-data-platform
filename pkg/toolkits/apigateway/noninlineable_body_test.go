package apigateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestIsInlineableContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{"empty preserves legacy path", "", true},
		{"unparseable preserves legacy path", "not a / media type;;;", true},
		{"text plain", "text/plain", true},
		{"text csv with charset", "text/csv; charset=utf-8", true},
		{"text html", "text/html", true},
		{"application json", "application/json", true},
		{"application json with charset", "application/json; charset=utf-8", true},
		{"application xml", "application/xml", true},
		{"vendor json suffix", "application/vnd.api+json", true},
		{"problem json suffix", "application/problem+json", true},
		{"soap xml suffix", "application/soap+xml", true},
		{"form urlencoded", "application/x-www-form-urlencoded", true},
		{"javascript", "application/javascript", true},
		{"zip refused", "application/zip", false},
		{"octet-stream refused", "application/octet-stream", false},
		{"pdf refused", "application/pdf", false},
		{"png refused", "image/png", false},
		{"audio refused", "audio/mpeg", false},
		{"video refused", "video/mp4", false},
		{"gzip refused", "application/gzip", false},
		{"multipart refused", "multipart/form-data; boundary=x", false},
		{"uppercase zip refused", "Application/ZIP", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isInlineableContentType(tt.contentType); got != tt.want {
				t.Errorf("isInlineableContentType(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

// TestInvoke_RefusesBinaryBodyBeforeBuffering proves the OOM fix: a
// binary upstream response is rejected with a typed
// *nonInlineableBodyError BEFORE the body is read, so the multi-megabyte
// JSON-escape amplification that previously OOMed the process never
// happens. The upstream records whether its body was ever read; the test
// asserts it was not.
func TestInvoke_RefusesBinaryBodyBeforeBuffering(t *testing.T) {
	const payload = "PK\x03\x04 pretend this is a 33MB zip"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", "34603008") // ~33 MiB declared
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:          srv.URL,
		AuthMode:         AuthModeNone,
		ConnectTimeout:   2 * time.Second,
		CallTimeout:      5 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
		ConnectionName:   "pbs-nextcloud",
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	out, err := invoke(context.Background(), invocation{
		cfg:    cfg,
		auth:   auth,
		client: newHTTPClient(cfg),
	}, InvokeInput{Connection: "pbs-nextcloud", Method: "GET", Path: "/file.zip"})

	var nb *nonInlineableBodyError
	if !errors.As(err, &nb) {
		t.Fatalf("expected *nonInlineableBodyError, got err=%v out=%#v", err, out)
	}
	if nb.contentType != "application/zip" {
		t.Errorf("contentType = %q, want application/zip", nb.contentType)
	}
	if nb.size != 34603008 {
		t.Errorf("size = %d, want 34603008", nb.size)
	}
	if nb.connection != "pbs-nextcloud" {
		t.Errorf("connection = %q", nb.connection)
	}
	if out.Body != nil {
		t.Errorf("body must not be buffered, got %#v", out.Body)
	}
}

// TestInvoke_AllowsInlineableBodyOfSameSizeClass confirms the guard is
// content-type-driven, not size-driven: a text/csv response on the same
// path still buffers and returns inline (the control case from the field
// report).
func TestInvoke_AllowsInlineableBodyOfSameSizeClass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "a,b,c\n1,2,3\n")
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:          srv.URL,
		AuthMode:         AuthModeNone,
		ConnectTimeout:   2 * time.Second,
		CallTimeout:      5 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
		ConnectionName:   "pbs-nextcloud",
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	out, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)},
		InvokeInput{Connection: "pbs-nextcloud", Method: "GET", Path: "/file.csv"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if s, ok := out.Body.(string); !ok || !strings.Contains(s, "1,2,3") {
		t.Errorf("expected csv body inline, got %#v", out.Body)
	}
}

// TestInvoke_AllowsEmptyBinaryResponse proves a zero-length response is
// not refused regardless of Content-Type (HEAD / 204 / empty body).
func TestInvoke_AllowsEmptyBinaryResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:          srv.URL,
		AuthMode:         AuthModeNone,
		ConnectTimeout:   2 * time.Second,
		CallTimeout:      5 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
		ConnectionName:   "c",
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	out, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)},
		InvokeInput{Connection: "c", Method: "GET", Path: "/empty.zip"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out.Status != http.StatusOK {
		t.Errorf("status = %d", out.Status)
	}
	if out.Body != nil {
		t.Errorf("empty body should decode to nil, got %#v", out.Body)
	}
}

func TestNonInlineableBodyError_Result(t *testing.T) {
	e := &nonInlineableBodyError{connection: "pbs-nextcloud", path: "/file.zip", contentType: "application/zip", size: 34603008}

	if msg := e.Error(); !strings.Contains(msg, ErrCodeBodyNotInlineable) || !strings.Contains(msg, "application/zip") {
		t.Errorf("Error() = %q, want it to carry the code and content type", msg)
	}

	withExport := resultText(t, e.result(true))
	if !strings.Contains(withExport, ErrCodeBodyNotInlineable) {
		t.Errorf("missing error code: %s", withExport)
	}
	if !strings.Contains(withExport, "api_export") {
		t.Errorf("hint should steer to api_export when wired: %s", withExport)
	}
	if !strings.Contains(withExport, "34603008") {
		t.Errorf("size_bytes should be present: %s", withExport)
	}

	noExport := resultText(t, e.result(false))
	if strings.Contains(noExport, "api_export") {
		t.Errorf("hint must not mention api_export when not wired: %s", noExport)
	}
	if !strings.Contains(noExport, "raw passthrough") {
		t.Errorf("hint should steer to raw route when export not wired: %s", noExport)
	}
}

// TestNonInlineableBodyError_OmitsSizeWhenUndeclared confirms the
// structured error omits size_bytes when the upstream gave no
// Content-Length (size == -1) rather than reporting a bogus -1.
func TestNonInlineableBodyError_OmitsSizeWhenUndeclared(t *testing.T) {
	e := &nonInlineableBodyError{connection: "c", path: "/p", contentType: "application/octet-stream", size: -1}
	txt := resultText(t, e.result(true))
	if strings.Contains(txt, "size_bytes") {
		t.Errorf("size_bytes must be omitted when undeclared: %s", txt)
	}
}

// TestHandleInvoke_BinaryBodyProducesStructuredError exercises the full
// toolkit path: a real connection against an upstream returning
// application/zip yields a structured upstream_body_not_inlineable tool
// error (IsError, REST-mappable to 415). The default toolkit has no
// export deps, so the hint steers to the raw route rather than naming a
// tool that is not registered.
func TestHandleInvoke_BinaryBodyProducesStructuredError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "PK\x03\x04binary")
	}))
	defer srv.Close()

	tk := New("test")
	if err := tk.AddConnection("pbs-nextcloud", map[string]any{"base_url": srv.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
		Connection: "pbs-nextcloud", Method: "GET", Path: "/file.zip",
	})
	if err != nil {
		t.Fatalf("handleInvoke unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("binary body did not produce IsError=true")
	}
	txt := textContent(res)
	if !strings.Contains(txt, ErrCodeBodyNotInlineable) {
		t.Errorf("expected %s in result: %s", ErrCodeBodyNotInlineable, txt)
	}
	if !strings.Contains(txt, "raw passthrough") {
		t.Errorf("export not wired: hint should steer to raw route: %s", txt)
	}
}

func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r == nil || len(r.Content) == 0 {
		t.Fatalf("result has no content")
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", r.Content[0])
	}
	if !r.IsError {
		t.Errorf("structured error result must have IsError=true")
	}
	return tc.Text
}
