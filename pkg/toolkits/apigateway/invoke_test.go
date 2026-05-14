package apigateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestValidateMethod_AcceptsKnownAndRejectsOthers(t *testing.T) {
	known := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD"}
	for _, m := range known {
		if got, err := validateMethod(m); err != nil || got != m {
			t.Errorf("validateMethod(%q) = (%q, %v); want (%q, nil)", m, got, err, m)
		}
	}
	if got, err := validateMethod("get"); err != nil || got != "GET" {
		t.Errorf("validateMethod(lowercase) = (%q, %v); want uppercase", got, err)
	}
	for _, m := range []string{"OPTIONS", "TRACE", "CONNECT", "BANANA"} {
		if _, err := validateMethod(m); err == nil {
			t.Errorf("validateMethod(%q) want error", m)
		}
	}
}

func TestValidatePath(t *testing.T) {
	if err := validatePath("/v1/users"); err != nil {
		t.Errorf("valid path rejected: %v", err)
	}
	if err := validatePath(""); err == nil {
		t.Error("empty path accepted")
	}
	if err := validatePath("v1/users"); err == nil {
		t.Error("missing leading slash accepted")
	}
}

// TestValidatePath_RejectsSSRFShapes is the regression test for the
// gosec G704 SSRF finding. A path that, when string-concatenated to
// a base URL, would let url.Parse interpret the result as a
// different host must be rejected up front. buildURL also defends
// against this via JoinPath + host pinning, but rejecting at
// validate-time gives the model a clearer error.
func TestValidatePath_RejectsSSRFShapes(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"protocol-relative", "//evil.example/foo"},
		{"userinfo injection", "/foo@evil.example/bar"},
		{"userinfo at start", "@evil.example/bar"},
		{"CR injection", "/foo\rEvil-Header: x"},
		{"LF injection", "/foo\nEvil-Header: x"},
		{"NUL injection", "/foo\x00bar"},
		// "/v1/users/.." matches a "/v1/users/*" persona allow rule
		// but JoinPath resolves it to "/v1" — letting the model
		// escape the persona's intended scope. Refuse at the
		// boundary so persona globs bound the model reliably.
		{"parent traversal segment", "/v1/users/.."},
		{"current segment", "/v1/users/."},
		{"nested traversal", "/v1/../etc/passwd"},
		// Empty interior segment ("//") would get collapsed by
		// JoinPath, bypassing literal-pattern persona rules.
		{"interior double-slash", "/v1//admin/secret"},
		{"three slashes", "/v1///admin"},
		// Percent-encoded dot segments: many servers decode
		// %2E during path resolution (RFC 3986 allows it).
		{"percent-encoded ..", "/v1/users/%2E%2E"},
		{"percent-encoded .. lowercase", "/v1/users/%2e%2e"},
		{"percent-encoded . segment", "/v1/users/%2E"},
		{"malformed percent escape", "/v1/users/%2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validatePath(tc.path); err == nil {
				t.Errorf("validatePath(%q) accepted; want rejection", tc.path)
			}
		})
	}
}

// TestBuildURL_PinsHostAgainstAttackerInputs confirms that even
// when adversarial paths bypass validatePath, buildURL preserves
// the connection's base host. JoinPath normalizes embedded schemes
// and protocol-relative prefixes by escaping/treating them as path
// segments rather than letting them change the URL's authority.
// The host-pin check after JoinPath is defense-in-depth against
// any future change to JoinPath semantics.
func TestBuildURL_PinsHostAgainstAttackerInputs(t *testing.T) {
	cases := []struct {
		name string
		base string
		path string
	}{
		{"absolute URL in path", "https://api.example.com", "/https://evil.example/foo"},
		{"scheme-shaped segment", "https://api.example.com", "/http://evil.example/foo"},
		{"backslash separator", "https://api.example.com", "/foo\\bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildURL(tc.base, tc.path, nil)
			if err != nil {
				return // refusal is also acceptable; the contract is "host stays pinned OR refuse"
			}
			parsed, perr := url.Parse(got)
			if perr != nil {
				t.Fatalf("buildURL produced an unparseable URL %q: %v", got, perr)
			}
			if parsed.Host != "api.example.com" {
				t.Errorf("HOST ESCAPED: buildURL(%q, %q) host = %q; want %q", tc.base, tc.path, parsed.Host, "api.example.com")
			}
		})
	}
}

func TestBuildURL_RejectsBaseWithoutSchemeOrHost(t *testing.T) {
	if _, err := buildURL("/no-scheme", "/foo", nil); err == nil {
		t.Error("buildURL accepted base_url with no scheme")
	}
	if _, err := buildURL("https://", "/foo", nil); err == nil {
		t.Error("buildURL accepted base_url with no host")
	}
}

func TestAuthHeaderForConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"none", Config{AuthMode: AuthModeNone}, ""},
		{"bearer", Config{AuthMode: AuthModeBearer}, "Authorization"},
		{"api_key header default", Config{AuthMode: AuthModeAPIKey, APIKeyPlacement: APIKeyPlacementHeader, APIKeyHeader: DefaultAPIKeyHeader}, DefaultAPIKeyHeader},
		{"api_key header custom", Config{AuthMode: AuthModeAPIKey, APIKeyPlacement: APIKeyPlacementHeader, APIKeyHeader: "X-My-Key"}, "X-My-Key"},
		{"api_key query", Config{AuthMode: AuthModeAPIKey, APIKeyPlacement: APIKeyPlacementQuery, APIKeyParam: "key"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := authHeaderForConfig(tc.cfg); got != tc.want {
				t.Errorf("authHeaderForConfig = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestValidateCustomHeaders_RejectsAuthorization(t *testing.T) {
	err := validateCustomHeaders(map[string]string{"AUTHORIZATION": "anything"}, "", nil)
	if err == nil {
		t.Error("Authorization header allowed")
	}
}

func TestValidateCustomHeaders_RejectsConfiguredAPIKeyHeader(t *testing.T) {
	err := validateCustomHeaders(map[string]string{"x-custom-key": "spoof"}, "X-Custom-Key", nil)
	if err == nil {
		t.Error("configured api_key header allowed (case-insensitive check failed)")
	}
}

func TestValidateCustomHeaders_AllowsOtherHeaders(t *testing.T) {
	err := validateCustomHeaders(map[string]string{"Accept-Language": "en"}, "X-API-Key", nil)
	if err != nil {
		t.Errorf("unrelated header rejected: %v", err)
	}
}

func TestValidateCustomHeaders_RejectsStaticHeaderOverride(t *testing.T) {
	staticHeaders := map[string]string{"Bb-Api-Subscription-Key": "secret"}
	err := validateCustomHeaders(map[string]string{"bb-api-subscription-key": "spoof"}, "", staticHeaders)
	if err == nil {
		t.Error("model attempt to override static header allowed (case-insensitive check failed)")
	}
}

func TestBuildURL_NoQuery(t *testing.T) {
	got, err := buildURL("https://api.example.com", "/v1/items", nil)
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}
	if got != "https://api.example.com/v1/items" {
		t.Errorf("got %q", got)
	}
}

func TestBuildURL_WithQuery(t *testing.T) {
	got, err := buildURL("https://api.example.com", "/search", map[string]any{
		"q":     "hello world",
		"limit": 10,
	})
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}
	if !strings.Contains(got, "q=hello+world") {
		t.Errorf("query not encoded: %q", got)
	}
	if !strings.Contains(got, "limit=10") {
		t.Errorf("int param missing: %q", got)
	}
}

func TestBuildURL_QueryArrayExpands(t *testing.T) {
	got, err := buildURL("https://example.com", "/x", map[string]any{
		"tag": []any{"a", "b", "c"},
	})
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}
	for _, want := range []string{"tag=a", "tag=b", "tag=c"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q; want substring %q", got, want)
		}
	}
}

func TestAppendQueryValue_AllScalarTypes(t *testing.T) {
	q := url.Values{}
	appendQueryValue(q, "s", "abc")
	appendQueryValue(q, "b", true)
	appendQueryValue(q, "i", 5)
	appendQueryValue(q, "i64", int64(7))
	appendQueryValue(q, "f", 3.5)
	appendQueryValue(q, "n", nil)
	appendQueryValue(q, "x", struct{ Name string }{"y"})

	if q.Get("s") != "abc" {
		t.Errorf("s = %q", q.Get("s"))
	}
	if q.Get("b") != "true" {
		t.Errorf("b = %q", q.Get("b"))
	}
	if q.Get("i") != "5" {
		t.Errorf("i = %q", q.Get("i"))
	}
	if q.Get("i64") != "7" {
		t.Errorf("i64 = %q", q.Get("i64"))
	}
	if q.Get("f") != "3.5" {
		t.Errorf("f = %q", q.Get("f"))
	}
	if _, ok := q["n"]; ok {
		t.Errorf("nil value added a key: %v", q["n"])
	}
	if q.Get("x") != "{y}" {
		t.Errorf("x = %q", q.Get("x"))
	}
}

func TestEncodeBody_SkipsBodyForGET(t *testing.T) {
	body, ct, err := encodeBody("GET", map[string]any{"hello": "world"})
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if body != nil || ct != "" {
		t.Errorf("body/ct = %v/%q; want nil/empty for GET", body, ct)
	}
}

func TestEncodeBody_StringBody(t *testing.T) {
	body, ct, err := encodeBody("POST", "raw text")
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if string(body) != "raw text" {
		t.Errorf("body = %q", string(body))
	}
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q; want text/plain*", ct)
	}
}

func TestEncodeBody_ObjectAsJSON(t *testing.T) {
	body, ct, err := encodeBody("POST", map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(string(body), `"key":"value"`) {
		t.Errorf("body = %s", string(body))
	}
}

func TestEncodeBody_RejectsUnencodable(t *testing.T) {
	_, _, err := encodeBody("POST", make(chan int))
	if err == nil {
		t.Error("encodeBody: want error for non-JSON-encodable body")
	}
}

func TestResolveTimeout(t *testing.T) {
	if got := resolveTimeout(0, 30*time.Second); got != 30*time.Second {
		t.Errorf("default not used: %v", got)
	}
	if got := resolveTimeout(5, time.Minute); got != 5*time.Second {
		t.Errorf("explicit not honored: %v", got)
	}
	if got := resolveTimeout(9999, time.Minute); got != maxTimeoutSeconds*time.Second {
		t.Errorf("not capped: %v", got)
	}
}

func TestReadBody_BelowCap(t *testing.T) {
	body, truncated, err := readBody(strings.NewReader("hello"), 100)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if string(body) != "hello" || truncated {
		t.Errorf("body=%q truncated=%v", body, truncated)
	}
}

func TestReadBody_AtCap(t *testing.T) {
	body, truncated, err := readBody(strings.NewReader("12345"), 5)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if string(body) != "12345" || truncated {
		t.Errorf("body=%q truncated=%v", body, truncated)
	}
}

func TestReadBody_OverCapTruncates(t *testing.T) {
	body, truncated, err := readBody(strings.NewReader("123456789"), 4)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if string(body) != "1234" {
		t.Errorf("body=%q; want 1234", body)
	}
	if !truncated {
		t.Error("truncated = false; want true")
	}
}

func TestReadBody_ZeroCapDefaults(t *testing.T) {
	_, _, err := readBody(strings.NewReader("hi"), 0)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
}

type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, errors.New("boom") }

func TestReadBody_PropagatesError(t *testing.T) {
	if _, _, err := readBody(errReader{}, 100); err == nil {
		t.Error("readBody: want error from underlying reader")
	}
}

func TestDecodeBody_JSONContentType(t *testing.T) {
	got := decodeBody("application/json", []byte(`{"a":1}`))
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %#v; want map", got)
	}
	val, ok := m["a"].(float64)
	if !ok || val != 1 {
		t.Errorf("m[\"a\"] = %#v; want 1", m["a"])
	}
}

func TestDecodeBody_NonJSONReturnsString(t *testing.T) {
	got := decodeBody("text/html", []byte("<p>hi</p>"))
	if s, ok := got.(string); !ok || s != "<p>hi</p>" {
		t.Errorf("got %#v; want string", got)
	}
}

func TestDecodeBody_JSONFallsBackOnInvalid(t *testing.T) {
	got := decodeBody("application/json", []byte("not json"))
	if s, ok := got.(string); !ok || s != "not json" {
		t.Errorf("got %#v; want raw string fallback", got)
	}
}

func TestDecodeBody_EmptyReturnsNil(t *testing.T) {
	if got := decodeBody("application/json", nil); got != nil {
		t.Errorf("got %#v; want nil", got)
	}
}

func TestSelectResponseHeaders_OnlyPassesAllowList(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Set-Cookie", "session=secret")
	h.Set("X-Custom", "ignored")
	h.Set("Link", "<next>; rel=\"next\"")

	out := selectResponseHeaders(h)
	if out["Content-Type"] == nil {
		t.Error("Content-Type dropped")
	}
	if out["Link"] == nil {
		t.Error("Link dropped")
	}
	if _, ok := out["Set-Cookie"]; ok {
		t.Error("Set-Cookie leaked")
	}
	if _, ok := out["X-Custom"]; ok {
		t.Error("X-Custom leaked")
	}
}

func TestSelectResponseHeaders_NilWhenEmpty(t *testing.T) {
	if out := selectResponseHeaders(http.Header{}); out != nil {
		t.Errorf("got %v; want nil", out)
	}
	h := http.Header{}
	h.Set("X-Nothing-Of-Interest", "1")
	if out := selectResponseHeaders(h); out != nil {
		t.Errorf("got %v; want nil for fully-filtered headers", out)
	}
}

// End-to-end: the integration-style test using httptest.Server. This
// is the test that actually proves the assembled invocation pipeline
// (URL build → request build → auth apply → execute → read → decode)
// works against a real HTTP transport.
func TestInvoke_EndToEnd_BearerAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
			t.Errorf("upstream saw Authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"echo":"ok"}`)
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:          srv.URL,
		AuthMode:         AuthModeBearer,
		Credential:       "tok-123",
		ConnectTimeout:   2 * time.Second,
		CallTimeout:      5 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	out, err := invoke(context.Background(), invocation{
		cfg:    cfg,
		auth:   auth,
		client: newHTTPClient(cfg),
	}, InvokeInput{
		Connection: "test",
		Method:     "GET",
		Path:       "/items",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out.Status != 200 {
		t.Errorf("status = %d", out.Status)
	}
	body, ok := out.Body.(map[string]any)
	if !ok || body["echo"] != "ok" {
		t.Errorf("body = %#v", out.Body)
	}
	if out.DurationMs < 0 {
		t.Errorf("duration_ms = %d", out.DurationMs)
	}
}

// TestInvoke_EndToEnd_StaticHeadersAlongsideBearer proves the
// Blackbaud-shaped requirement: the operator's static header lands on
// the wire alongside the OAuth/Bearer Authorization header, without
// the model being able to inject or override either. The model
// supplies an unrelated Accept-Language header to confirm static
// headers don't displace per-call ones that aren't reserved.
func TestInvoke_EndToEnd_StaticHeadersAlongsideBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok-xyz" {
			t.Errorf("Authorization = %q; want %q", got, "Bearer tok-xyz")
		}
		if got := r.Header.Get("Bb-Api-Subscription-Key"); got != "sub-secret" {
			t.Errorf("Bb-Api-Subscription-Key = %q; want %q", got, "sub-secret")
		}
		if got := r.Header.Get("Accept-Language"); got != "en-US" {
			t.Errorf("Accept-Language = %q; want %q", got, "en-US")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:          srv.URL,
		AuthMode:         AuthModeBearer,
		Credential:       "tok-xyz",
		ConnectTimeout:   2 * time.Second,
		CallTimeout:      5 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
		StaticHeaders: map[string]string{
			"Bb-Api-Subscription-Key": "sub-secret",
		},
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	out, err := invoke(context.Background(), invocation{
		cfg:    cfg,
		auth:   auth,
		client: newHTTPClient(cfg),
	}, InvokeInput{
		Connection: "blackbaud",
		Method:     "GET",
		Path:       "/v1/constituents",
		Headers:    map[string]string{"Accept-Language": "en-US"},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out.Status != http.StatusOK {
		t.Errorf("status = %d", out.Status)
	}
}

// TestInvoke_ModelCannotOverrideStaticHeader proves the operator's
// static_headers entry beats a per-call attempt to spoof the same
// header — validateCustomHeaders should refuse the call.
func TestInvoke_ModelCannotOverrideStaticHeader(t *testing.T) {
	cfg := Config{
		BaseURL:          "https://api.example.com",
		AuthMode:         AuthModeNone,
		ConnectTimeout:   time.Second,
		CallTimeout:      time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
		StaticHeaders:    map[string]string{"Bb-Api-Subscription-Key": "operator-key"},
	}
	auth, _ := NewAuthenticator(cfg)
	_, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}, InvokeInput{
		Connection: "blackbaud",
		Method:     "GET",
		Path:       "/v1/anything",
		Headers:    map[string]string{"bb-api-subscription-key": "spoofed"},
	})
	if err == nil {
		t.Fatal("expected error refusing model override of static_header")
	}
}

func TestInvoke_EndToEnd_PostBodySent(t *testing.T) {
	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL: srv.URL, AuthMode: AuthModeNone,
		ConnectTimeout: time.Second, CallTimeout: 5 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
	auth, _ := NewAuthenticator(cfg)
	out, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}, InvokeInput{
		Connection: "x", Method: "POST", Path: "/things",
		Body: map[string]any{"name": "alice"},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out.Status != http.StatusCreated {
		t.Errorf("status = %d", out.Status)
	}
	if !bytes.Contains(seenBody, []byte(`"name":"alice"`)) {
		t.Errorf("body sent = %s", string(seenBody))
	}
}

// TestInvoke_NetworkError_DoesNotLeakAPIKeyInQueryPlacement is a
// regression test for the api_key + query-placement credential leak.
// When client.Do returns an error, Go's *url.Error stringifies the
// FULL request URL — including the query string the Authenticator
// added. Without scrubbing, the credential would land in
// InvokeOutput.Error, which is returned to the model and recorded
// in audit. The auth.go Authenticator contract explicitly forbids
// any credential appearing in error messages.
func TestInvoke_NetworkError_DoesNotLeakAPIKeyInQueryPlacement(t *testing.T) {
	const secret = "supersecret-credential-9b7"
	cfg := Config{
		BaseURL:          "http://192.0.2.1:80", // RFC 5737 unreachable
		AuthMode:         AuthModeAPIKey,
		Credential:       secret,
		APIKeyPlacement:  APIKeyPlacementQuery,
		APIKeyParam:      "api_key",
		ConnectTimeout:   200 * time.Millisecond,
		CallTimeout:      time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	out, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}, InvokeInput{
		Connection: "x", Method: "GET", Path: "/foo",
	})
	if err != nil {
		t.Fatalf("invoke unexpected error: %v", err)
	}
	if out.Error == "" {
		t.Fatal("expected error envelope, got none")
	}
	if strings.Contains(out.Error, secret) {
		t.Errorf("CREDENTIAL LEAK: secret appears in InvokeOutput.Error: %q", out.Error)
	}
}

func TestScrubTransportError_StripsQueryAndUserInfo(t *testing.T) {
	cases := []struct {
		name string
		in   error
		bad  []string
	}{
		{
			name: "url.Error with credential in query",
			in: &url.Error{
				Op:  "Get",
				URL: "http://host.example/foo?api_key=SECRET&page=1",
				Err: errors.New("dial tcp: timeout"),
			},
			bad: []string{"SECRET", "api_key="},
		},
		{
			name: "url.Error with userinfo",
			in: &url.Error{
				Op:  "Get",
				URL: "http://user:PASSWORD@host.example/foo",
				Err: errors.New("dial tcp: refused"),
			},
			bad: []string{"PASSWORD", "user:"},
		},
		{
			name: "url.Error with malformed URL falls back",
			in: &url.Error{
				Op:  "Get",
				URL: "://broken?api_key=SECRET",
				Err: errors.New("malformed"),
			},
			bad: []string{"SECRET"},
		},
		{
			name: "non-url.Error is passed through",
			in:   errors.New("plain error message"),
			bad:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scrubTransportError(tc.in)
			if got == "" {
				t.Fatal("scrubTransportError returned empty string")
			}
			for _, marker := range tc.bad {
				if strings.Contains(got, marker) {
					t.Errorf("scrubbed message %q still contains %q", got, marker)
				}
			}
		})
	}
}

func TestInvoke_NetworkErrorPopulatesError(t *testing.T) {
	cfg := Config{
		BaseURL: "http://127.0.0.1:1", AuthMode: AuthModeNone,
		ConnectTimeout: 100 * time.Millisecond, CallTimeout: 200 * time.Millisecond,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
	auth, _ := NewAuthenticator(cfg)
	out, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}, InvokeInput{
		Connection: "x", Method: "GET", Path: "/",
	})
	if err != nil {
		t.Fatalf("invoke unexpected error: %v", err)
	}
	if out.Status != 0 || out.Error == "" {
		t.Errorf("got status=%d error=%q; want 0 + error", out.Status, out.Error)
	}
}

func TestInvoke_RejectsInvalidMethod(t *testing.T) {
	cfg := Config{BaseURL: "https://x", AuthMode: AuthModeNone, ConnectTimeout: time.Second, CallTimeout: time.Second, MaxResponseBytes: DefaultMaxResponseBytes}
	auth, _ := NewAuthenticator(cfg)
	_, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}, InvokeInput{
		Connection: "x", Method: "PURGE", Path: "/",
	})
	if err == nil || !strings.Contains(err.Error(), "method") {
		t.Errorf("got %v; want method error", err)
	}
}

func TestInvoke_RejectsConflictingHeader(t *testing.T) {
	cfg := Config{BaseURL: "https://x", AuthMode: AuthModeBearer, Credential: "tok", ConnectTimeout: time.Second, CallTimeout: time.Second, MaxResponseBytes: DefaultMaxResponseBytes}
	auth, _ := NewAuthenticator(cfg)
	_, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}, InvokeInput{
		Connection: "x", Method: "GET", Path: "/",
		Headers: map[string]string{"Authorization": "Bearer evil"},
	})
	if err == nil {
		t.Error("invoke: want error for spoofed Authorization")
	}
}

func TestInvoke_RedirectsAreNotFollowed(t *testing.T) {
	hit := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
		if r.URL.Path == "/start" {
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "should-not-reach")
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, AuthMode: AuthModeNone, ConnectTimeout: time.Second, CallTimeout: 2 * time.Second, MaxResponseBytes: DefaultMaxResponseBytes}
	auth, _ := NewAuthenticator(cfg)
	out, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}, InvokeInput{
		Connection: "x", Method: "GET", Path: "/start",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out.Status != http.StatusFound {
		t.Errorf("status = %d; want 302 (redirect not followed)", out.Status)
	}
	if hit != 1 {
		t.Errorf("upstream hit %d times; redirect was followed", hit)
	}
	// Location header must be surfaced so the model can choose to
	// follow the redirect manually with a fresh api_invoke_endpoint
	// call. Dropping Location would trap the model in a 3xx with no
	// way to know where the upstream wanted to redirect it.
	if loc := out.Headers["Location"]; len(loc) == 0 || loc[0] != "/elsewhere" {
		t.Errorf("Location header missing from response envelope: %v", out.Headers)
	}
}

func TestInvoke_TruncatesLargeResponse(t *testing.T) {
	big := strings.Repeat("x", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, big)
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, AuthMode: AuthModeNone, ConnectTimeout: time.Second, CallTimeout: 2 * time.Second, MaxResponseBytes: 100}
	auth, _ := NewAuthenticator(cfg)
	out, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}, InvokeInput{
		Connection: "x", Method: "GET", Path: "/",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !out.BodyTruncated {
		t.Error("BodyTruncated = false; want true")
	}
	if s, _ := out.Body.(string); len(s) != 100 {
		t.Errorf("body length = %d; want 100", len(s))
	}
}
