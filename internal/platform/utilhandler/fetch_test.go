package utilhandler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newTestHandler builds the util handler with loopback exempted from
// the SSRF block so tests can fetch from httptest servers — exactly
// the operator allow_private_cidrs mechanism, not a test-only door.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := New(Options{AllowPrivateCIDRs: []string{"127.0.0.0/8", "::1/128"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

// doFetch posts a fetch request document to the handler and returns
// the recorded response.
func doFetch(t *testing.T, h http.Handler, in fetchRequest) *httptest.ResponseRecorder {
	t.Helper()
	return doFetchCtx(context.Background(), t, h, in)
}

func doFetchCtx(ctx context.Context, t *testing.T, h http.Handler, in fetchRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, FetchPath, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorField(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("error envelope not JSON: %v (body %q)", err, rec.Body.String())
	}
	return out["error"]
}

// TestHandleFetch_HappyPath_PreservesRawQuery is the core contract:
// the destination sees the query string byte-for-byte (a presigned
// signature survives), and the body, status, and Content-Type relay
// verbatim.
func TestHandleFetch_HappyPath_PreservesRawQuery(t *testing.T) {
	const rawQuery = "X-Amz-Signature=abc%2Bdef%3D%3D&se=2026-07-21T00%3A00%3A00Z&a=b+c"
	const wantBody = `{"rows":[1,2,3]}`
	var gotQuery, gotUA string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(wantBody))
	}))
	t.Cleanup(upstream.Close)

	rec := doFetch(t, newTestHandler(t), fetchRequest{URL: upstream.URL + "/report?" + rawQuery})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if gotQuery != rawQuery {
		t.Errorf("upstream RawQuery = %q; want byte-identical %q", gotQuery, rawQuery)
	}
	if rec.Body.String() != wantBody {
		t.Errorf("body = %q; want %q", rec.Body.String(), wantBody)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if gotUA != fetchUserAgent {
		t.Errorf("User-Agent = %q; want default %q", gotUA, fetchUserAgent)
	}
}

func TestHandleFetch_RelaysUpstreamErrorStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	t.Cleanup(upstream.Close)

	rec := doFetch(t, newTestHandler(t), fetchRequest{URL: upstream.URL})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 relayed", rec.Code)
	}
}

func TestHandleFetch_HeadAndCustomHeaders(t *testing.T) {
	var gotMethod, gotHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	rec := doFetch(t, newTestHandler(t), fetchRequest{
		URL:     upstream.URL,
		Method:  "head",
		Headers: map[string]string{"X-Custom": "v1"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if gotMethod != http.MethodHead {
		t.Errorf("method = %q; want HEAD", gotMethod)
	}
	if gotHeader != "v1" {
		t.Errorf("X-Custom = %q; want opt-in header forwarded", gotHeader)
	}
}

// TestHandleFetch_RelaysLinkHeader pins #1544: a fetched document's Link
// header reaches the caller, because it is the pagination signal a walk over
// the util connection reads. Every Link value is relayed, in order.
func TestHandleFetch_RelaysLinkHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Link", `<https://example.com/items?page=2>; rel="next"`)
		w.Header().Add("Link", `<https://example.com/items?page=10>; rel="last"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(upstream.Close)

	rec := doFetch(t, newTestHandler(t), fetchRequest{URL: upstream.URL})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	links := rec.Header().Values("Link")
	if len(links) != 2 || links[0] != `<https://example.com/items?page=2>; rel="next"` {
		t.Fatalf("Link = %q; want both upstream values relayed in order", links)
	}
}

func TestHandleFetch_BadInputs(t *testing.T) {
	manyHeaders := make(map[string]string, maxCustomHeaders+1)
	for i := 0; i <= maxCustomHeaders; i++ {
		manyHeaders[fmt.Sprintf("X-H-%d", i)] = "v"
	}
	tests := []struct {
		name    string
		in      fetchRequest
		wantMsg string
	}{
		{"missing url", fetchRequest{}, "url is required"},
		{"bad scheme", fetchRequest{URL: "ftp://example.com/f"}, "scheme"},
		{"userinfo", fetchRequest{URL: "https://user:pass@example.com/"}, "userinfo"},
		{"no host", fetchRequest{URL: "https:///path"}, "host"},
		{"write method", fetchRequest{URL: "https://example.com", Method: "POST"}, "read-only"},
		{"too many headers", fetchRequest{URL: "https://example.com", Headers: manyHeaders}, "too many headers"},
		{"transport-owned header", fetchRequest{URL: "https://example.com", Headers: map[string]string{"Host": "evil"}}, "transport-owned"},
		{"empty header name", fetchRequest{URL: "https://example.com", Headers: map[string]string{"": "v"}}, "empty header name"},
		{"header smuggling", fetchRequest{URL: "https://example.com", Headers: map[string]string{"X-A": "v\r\nInjected: x"}}, "CR/LF"},
	}
	h := newTestHandler(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doFetch(t, h, tt.in)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400 (body %q)", rec.Code, rec.Body.String())
			}
			if msg := errorField(t, rec); !strings.Contains(msg, tt.wantMsg) {
				t.Errorf("error = %q; want substring %q", msg, tt.wantMsg)
			}
		})
	}
}

func TestHandleFetch_InvalidJSONBody(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, FetchPath, strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}

func TestHandleFetch_BlockedDestinationIs403(t *testing.T) {
	rec := doFetch(t, newTestHandler(t), fetchRequest{URL: "http://169.254.169.254/latest/meta-data/"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403 (body %q)", rec.Code, rec.Body.String())
	}
	if msg := errorField(t, rec); !strings.Contains(msg, "refused") {
		t.Errorf("error = %q; want guard refusal text", msg)
	}
}

// TestHandleFetch_NAT64LiteralRefused pins Finding 1: an IPv6 literal
// that embeds an internal IPv4 (NAT64 well-known prefix) is refused,
// and a v4 allow_private_cidrs grant (127.0.0.0/8, set by newTestHandler)
// does NOT leak through to its v6-encoded form.
func TestHandleFetch_NAT64LiteralRefused(t *testing.T) {
	for _, target := range []string{
		"http://[64:ff9b::a9fe:a9fe]/latest/meta-data/", // -> 169.254.169.254
		"http://[64:ff9b::7f00:1]/",                     // -> 127.0.0.1
		"http://[2002:a00:1::]/",                        // 6to4 -> 10.0.0.1
	} {
		rec := doFetch(t, newTestHandler(t), fetchRequest{URL: target})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d; want 403", target, rec.Code)
		}
	}
}

func TestHandleFetch_Redirects(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			_, _ = w.Write([]byte("landed"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	h := newTestHandler(t)

	// Default: follow.
	rec := doFetch(t, h, fetchRequest{URL: upstream.URL + "/start"})
	if rec.Code != http.StatusOK || rec.Body.String() != "landed" {
		t.Fatalf("follow: status %d body %q; want 200 landed", rec.Code, rec.Body.String())
	}

	// Opt out: the 302 relays as-is.
	noFollow := false
	rec = doFetch(t, h, fetchRequest{URL: upstream.URL + "/start", FollowRedirects: &noFollow})
	if rec.Code != http.StatusFound {
		t.Fatalf("no-follow: status %d; want 302 relayed", rec.Code)
	}
}

// TestHandleFetch_RedirectToBlockedHostRefused pins that the guard
// applies per hop: a public destination redirecting to internal
// address space is refused mid-chain.
func TestHandleFetch_RedirectToBlockedHostRefused(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.99.0.1/secret", http.StatusFound)
	}))
	t.Cleanup(upstream.Close)

	rec := doFetch(t, newTestHandler(t), fetchRequest{URL: upstream.URL})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403 (body %q)", rec.Code, rec.Body.String())
	}
}

func TestHandleFetch_ExpectedContentType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>error page</html>"))
	}))
	t.Cleanup(upstream.Close)
	h := newTestHandler(t)

	rec := doFetch(t, h, fetchRequest{URL: upstream.URL, ExpectedContentType: "application/json"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("mismatch: status = %d; want 502", rec.Code)
	}
	// Parameters (charset) are ignored in the comparison.
	rec = doFetch(t, h, fetchRequest{URL: upstream.URL, ExpectedContentType: "text/html"})
	if rec.Code != http.StatusOK {
		t.Fatalf("match: status = %d; want 200", rec.Code)
	}
}

func TestHandleFetch_TimeoutIs504(t *testing.T) {
	blocker := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-blocker:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { close(blocker) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	rec := doFetchCtx(ctx, t, newTestHandler(t), fetchRequest{URL: upstream.URL})
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d; want 504 (body %q)", rec.Code, rec.Body.String())
	}
}

// TestHandleFetch_ErrorTextRedactsQuery pins the redaction contract:
// a failure against an unreachable destination must not echo the
// query string (the presigned credential) back in the error text.
func TestHandleFetch_ErrorTextRedactsQuery(t *testing.T) {
	// Reserved TEST-NET-1 address: never routable, dial fails fast
	// with the connect timeout bounded by the request context.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	rec := doFetchCtx(ctx, t, newTestHandler(t), fetchRequest{
		URL: "http://192.0.2.1/file?X-Amz-Signature=SUPERSECRET",
	})
	if rec.Code == http.StatusOK {
		t.Fatal("fetch to TEST-NET-1 should fail")
	}
	if body := rec.Body.String(); strings.Contains(body, "SUPERSECRET") {
		t.Errorf("error body leaked the signature: %q", body)
	}
}

func TestHandler_UnknownRoute(t *testing.T) {
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/util/nope", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown path status = %d; want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, FetchPath, http.NoBody))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET %s status = %d; want 405", FetchPath, rec.Code)
	}
}

func TestValidateFetchMethod(t *testing.T) {
	if m, err := validateFetchMethod(""); err != nil || m != http.MethodGet {
		t.Errorf("empty method = %q, %v; want GET default", m, err)
	}
	if m, err := validateFetchMethod(" get "); err != nil || m != http.MethodGet {
		t.Errorf("' get ' = %q, %v; want GET", m, err)
	}
	for _, bad := range []string{"POST", "PUT", "DELETE", "PROPFIND"} {
		if _, err := validateFetchMethod(bad); err == nil {
			t.Errorf("method %q accepted; want refusal", bad)
		}
	}
}

func TestRedactedURL(t *testing.T) {
	u, err := url.Parse("https://blob.example.net/container/file.json?sig=SECRET&se=2026")
	if err != nil {
		t.Fatal(err)
	}
	got := redactedURL(u)
	if got != "https://blob.example.net/container/file.json" {
		t.Errorf("redactedURL = %q", got)
	}
	if redactedURL(nil) != "" {
		t.Error("redactedURL(nil) should be empty")
	}
}

func TestInnermostErrorText(t *testing.T) {
	inner := errors.New("connection refused")
	wrapped := &url.Error{Op: "Get", URL: "https://h/p?sig=SECRET", Err: inner}
	if got := innermostErrorText(wrapped); got != "connection refused" {
		t.Errorf("innermostErrorText = %q", got)
	}
	plain := errors.New("plain")
	if got := innermostErrorText(plain); got != "plain" {
		t.Errorf("plain error = %q", got)
	}
}

func TestExpectedContentTypeMismatch(t *testing.T) {
	if msg := expectedContentTypeMismatch("", "text/html"); msg != "" {
		t.Errorf("no expectation should pass, got %q", msg)
	}
	if msg := expectedContentTypeMismatch("application/json", "application/json; charset=utf-8"); msg != "" {
		t.Errorf("charset param should be ignored, got %q", msg)
	}
	if msg := expectedContentTypeMismatch("application/json", "text/html"); msg == "" {
		t.Error("mismatch should be reported")
	}
	if msg := expectedContentTypeMismatch("application/json", ""); msg == "" {
		t.Error("missing upstream Content-Type with an expectation should be reported")
	}
}

// TestRelayResponse_StreamsWithoutFullBuffer relays a body larger
// than any single pipe/copy buffer and checks byte fidelity plus the
// Content-Length passthrough the export cap check depends on.
func TestRelayResponse_ContentLengthPassthrough(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefgh"), 512*1024) // 4 MiB
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	}))
	t.Cleanup(upstream.Close)

	rec := doFetch(t, newTestHandler(t), fetchRequest{URL: upstream.URL})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Length"); got != fmt.Sprint(len(payload)) {
		t.Errorf("Content-Length = %q; want %d", got, len(payload))
	}
	if !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Errorf("body mismatch: got %d bytes, want %d", rec.Body.Len(), len(payload))
	}
}
