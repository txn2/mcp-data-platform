package apigateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// supportedMethods is the closed set of HTTP methods api_invoke_endpoint
// accepts. Restricting the set keeps the tool's blast radius bounded and
// avoids servicing exotic methods (TRACE, CONNECT) that the model has
// no legitimate reason to call.
//
//nolint:gochecknoglobals // intentionally a package-level constant set
var supportedMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodDelete: true,
	http.MethodPatch:  true,
	http.MethodHead:   true,
}

// maxTimeoutSeconds caps the per-call timeout the model can request.
// Even an upstream that supports long-running calls is bounded — the
// MCP request lifecycle is not designed for hour-long operations.
const maxTimeoutSeconds = 600

// strconv numeric base / bitSize constants. Pulled out so revive's
// add-constant rule does not flag the literal repetitions across the
// scalar-stringification switch.
const (
	intBase      = 10
	floatBitSize = 64
)

// methodsAllowingBody is the set of methods for which an input body
// is forwarded. GET and HEAD are explicitly excluded; some upstreams
// reject GET-with-body and the model can move parameters to query
// instead.
//
//nolint:gochecknoglobals // intentionally a package-level constant set
var methodsAllowingBody = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodDelete: true,
	http.MethodPatch:  true,
}

// InvokeInput is the parsed argument shape for api_invoke_endpoint.
// Field names match the JSON schema.
type InvokeInput struct {
	Connection     string            `json:"connection"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Query          map[string]any    `json:"query_params,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           any               `json:"body,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

// InvokeOutput is the structured result returned to the model. Errors
// reaching the upstream (DNS, connection refused, TLS failure,
// timeout) are surfaced via Error rather than as MCP tool errors so
// the model can branch on them without losing the rest of the
// response envelope.
type InvokeOutput struct {
	Status        int                 `json:"status"`
	Headers       map[string][]string `json:"headers,omitempty"`
	Body          any                 `json:"body,omitempty"`
	BodyTruncated bool                `json:"body_truncated,omitempty"`
	// Pagination is populated when the upstream response carries a
	// recognizable cursor (RFC 5988 Link rel="next", @odata.nextLink,
	// next_cursor, etc). The model uses this to decide whether to
	// issue a follow-up call. The gateway does NOT auto-follow so
	// each loop iteration stays observable in audit + conversation.
	Pagination *PaginationInfo `json:"pagination,omitempty"`
	// Hint surfaces operator-actionable advice to the model when the
	// response itself can't carry it — most importantly the "use
	// api_export instead" suggestion when the body exceeded
	// max_response_bytes. Distinct from Error: Hint is informational,
	// the call still succeeded.
	Hint       string `json:"hint,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// invocation bundles a connection lookup with its supporting types so
// the call path can be tested without standing up a full Toolkit.
type invocation struct {
	cfg    Config
	auth   Authenticator
	client *http.Client
}

// invoke runs a single api_invoke_endpoint call against a known
// connection. The returned error is reserved for argument-validation
// failures (which the model should fix); upstream failures populate
// InvokeOutput.Error with Status==0.
func invoke(ctx context.Context, inv invocation, in InvokeInput) (InvokeOutput, error) {
	method, err := validateMethod(in.Method)
	if err != nil {
		return InvokeOutput{}, err
	}
	if err := validatePath(in.Path); err != nil {
		return InvokeOutput{}, err
	}
	authHeader := authHeaderForConfig(inv.cfg)
	if err := validateCustomHeaders(in.Headers, authHeader, inv.cfg.StaticHeaders); err != nil {
		return InvokeOutput{}, err
	}

	reqURL, err := buildURL(inv.cfg.BaseURL, in.Path, in.Query)
	if err != nil {
		return InvokeOutput{}, err
	}

	body, contentType, err := encodeBody(method, in.Body)
	if err != nil {
		return InvokeOutput{}, err
	}

	timeout := resolveTimeout(in.TimeoutSeconds, inv.cfg.CallTimeout)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := buildRequest(callCtx, requestSpec{
		method:        method,
		url:           reqURL,
		body:          body,
		contentType:   contentType,
		headers:       in.Headers,
		staticHeaders: inv.cfg.StaticHeaders,
	})
	if err != nil {
		return InvokeOutput{}, err
	}
	if err := inv.auth.Apply(req); err != nil {
		return InvokeOutput{}, fmt.Errorf("apigateway: applying auth: %w", err)
	}

	return executeRequest(inv.client, req, inv.cfg.MaxResponseBytes), nil
}

func validateMethod(method string) (string, error) {
	m := strings.ToUpper(strings.TrimSpace(method))
	if !supportedMethods[m] {
		return "", fmt.Errorf("apigateway: method %q not supported (want GET, POST, PUT, DELETE, PATCH, or HEAD)", method)
	}
	return m, nil
}

func validatePath(p string) error {
	if p == "" {
		return errors.New("apigateway: path is required")
	}
	if !strings.HasPrefix(p, "/") {
		return errors.New("apigateway: path must start with \"/\"")
	}
	// Reject path shapes that, when string-concatenated to a base
	// URL, would let url.Parse interpret the result as a different
	// host (SSRF). Without this check, path="//evil.com/foo" turns
	// "https://api.example.com" + path into a protocol-relative URL
	// pointing at evil.com, and path="@evil.com/foo" injects
	// userinfo so the final Host becomes evil.com. The host pinning
	// in buildURL is the primary defense; this rejection is the
	// up-front diagnostic the model sees.
	if strings.HasPrefix(p, "//") {
		return errors.New("apigateway: path must not start with \"//\" (protocol-relative URLs are rejected)")
	}
	if strings.ContainsAny(p, "@\r\n\x00") {
		return errors.New("apigateway: path contains a disallowed character (@, CR, LF, NUL)")
	}
	// Reject path segments that JoinPath or the upstream would
	// normalize to something different from what filepath.Match
	// sees. Without this check, persona APIRoutes globs do not
	// reliably bound the model:
	//
	//   - Literal "." / "..": "/v1/users/.." matches "/v1/users/*"
	//     but JoinPath resolves to "/v1".
	//   - Empty interior segments ("//"): "/v1//admin/secret" does
	//     NOT match a literal "/v1/admin/*" glob, but JoinPath
	//     collapses the double slash and the upstream sees
	//     "/v1/admin/secret" — bypassing a deny rule scoped to
	//     "/v1/admin/*".
	//   - Percent-encoded dot segments: "%2E%2E" passes a literal
	//     "." / ".." string compare but RFC 3986 says servers MAY
	//     decode %2E for path resolution; many do (Apache default,
	//     several SaaS APIs).
	//
	// All three are refused here so the raw path the policy sees
	// equals the path the upstream will see (after JoinPath but
	// before any server-side decoding).
	return checkPathSegments(p)
}

// checkPathSegments rejects literal "." / ".." segments, interior
// empty segments (collapse vector), and percent-encoded dot
// segments. See validatePath for the security rationale.
func checkPathSegments(p string) error {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		// Leading slash makes parts[0] == ""; allowed.
		// A single trailing slash makes parts[last] == ""; allowed.
		if seg == "" {
			if i == 0 || i == len(parts)-1 {
				continue
			}
			return errors.New("apigateway: path must not contain empty segments (\"//\")")
		}
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			return errors.New("apigateway: path contains a malformed percent-escape")
		}
		if decoded == "." || decoded == ".." {
			return errors.New("apigateway: path must not contain \".\" or \"..\" segments (literal or percent-encoded)")
		}
	}
	return nil
}

// authorizationHeader is the HTTP header bearer-mode auth populates.
// Extracted as a named constant so the same literal isn't repeated
// across the auth dispatch switch and the header-spoof rejection.
const authorizationHeader = "Authorization"

// authHeaderForConfig returns the canonical header name the
// connection's auth mode would set, so validateCustomHeaders can
// reject the model's attempts to spoof or override it. Empty string
// means no header-based auth (mode=none, mode=api_key with query
// placement).
func authHeaderForConfig(c Config) string {
	switch c.AuthMode {
	case AuthModeBearer:
		return authorizationHeader
	case AuthModeAPIKey:
		if c.APIKeyPlacement == APIKeyPlacementHeader {
			return c.APIKeyHeader
		}
	}
	return ""
}

func validateCustomHeaders(headers map[string]string, authHeader string, staticHeaders map[string]string) error {
	for name := range headers {
		if strings.EqualFold(name, authorizationHeader) {
			return errors.New("apigateway: Authorization header is reserved; configure auth via connection")
		}
		if authHeader != "" && strings.EqualFold(name, authHeader) {
			return fmt.Errorf("apigateway: %s header is reserved by this connection's auth_mode", authHeader)
		}
		for staticName := range staticHeaders {
			if strings.EqualFold(name, staticName) {
				return fmt.Errorf("apigateway: %s header is reserved by this connection's static_headers", staticName)
			}
		}
	}
	return nil
}

// buildURL composes the upstream URL from the connection's base
// URL and the model-supplied path + query. Defense against SSRF:
// the base URL is parsed independently of the path; the path is
// joined with url.URL.JoinPath rather than string-concatenated;
// and the resulting URL's scheme + host MUST equal the base's. If
// any of those checks fail (a model-crafted path that escapes the
// base, a future Go change to JoinPath semantics, etc.) the call
// is refused before the request is built.
func buildURL(baseURL, path string, query map[string]any) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("apigateway: parsing base_url: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("apigateway: base_url %q must include scheme and host", baseURL)
	}
	u := base.JoinPath(path)
	if u.Scheme != base.Scheme || u.Host != base.Host {
		return "", fmt.Errorf("apigateway: path %q would change the request host (base=%q, joined=%q); refusing", path, base.Host, u.Host)
	}
	if len(query) > 0 {
		q := u.Query()
		for k, v := range query {
			appendQueryValue(q, k, v)
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// appendQueryValue handles scalars, slices, and arbitrary types by
// stringifying. The output is never sensitive — query string values
// come from the model's tool arguments, not from connection
// credentials.
func appendQueryValue(q url.Values, key string, val any) {
	switch v := val.(type) {
	case nil:
	case string:
		q.Add(key, v)
	case bool:
		q.Add(key, strconv.FormatBool(v))
	case int:
		q.Add(key, strconv.Itoa(v))
	case int64:
		q.Add(key, strconv.FormatInt(v, intBase))
	case float64:
		q.Add(key, strconv.FormatFloat(v, 'f', -1, floatBitSize))
	case []any:
		for _, item := range v {
			appendQueryValue(q, key, item)
		}
	default:
		q.Add(key, fmt.Sprintf("%v", v))
	}
}

func encodeBody(method string, body any) (data []byte, contentType string, err error) {
	if body == nil || !methodsAllowingBody[method] {
		return nil, "", nil
	}
	if s, ok := body.(string); ok {
		return []byte(s), "text/plain; charset=utf-8", nil
	}
	encoded, jerr := json.Marshal(body)
	if jerr != nil {
		return nil, "", fmt.Errorf("apigateway: encoding body as JSON: %w", jerr)
	}
	return encoded, "application/json", nil
}

func resolveTimeout(requested int, defaultTimeout time.Duration) time.Duration {
	if requested <= 0 {
		return defaultTimeout
	}
	if requested > maxTimeoutSeconds {
		requested = maxTimeoutSeconds
	}
	return time.Duration(requested) * time.Second
}

// requestSpec bundles the inputs for buildRequest so the function
// signature stays under revive's argument-limit ceiling without
// losing any of the data the request-building step needs.
type requestSpec struct {
	method        string
	url           string
	body          []byte
	contentType   string
	headers       map[string]string
	staticHeaders map[string]string
}

func buildRequest(ctx context.Context, spec requestSpec) (*http.Request, error) {
	var bodyReader io.Reader
	if spec.body != nil {
		bodyReader = bytes.NewReader(spec.body)
	}
	req, err := http.NewRequestWithContext(ctx, spec.method, spec.url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("apigateway: building request: %w", err)
	}
	for name, value := range spec.headers {
		req.Header.Set(name, value)
	}
	// Static (operator-configured) headers override per-call (model)
	// headers so a connection's mandatory subscription/quota header
	// (e.g. Google's x-goog-user-project) is authoritative.
	// validateCustomHeaders also rejects model attempts at the same
	// header names, so this is belt-and-suspenders.
	for name, value := range spec.staticHeaders {
		req.Header.Set(name, value)
	}
	if spec.contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", spec.contentType)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json, */*;q=0.5")
	}
	return req, nil
}

// scrubTransportError rewrites a transport-level error so its message
// cannot leak credentials carried in the request URL's query string
// (api_key=... when AuthMode is api_key + APIKeyPlacementQuery).
//
// Go's *http.Client returns a *url.Error whose Error() method
// stringifies the full request URL — including any query parameters
// the Authenticator added. Returning that verbatim to the model and
// audit pipeline would violate the toolkit's auth-leak contract
// (see auth.go's Authenticator interface comment). We rebuild the
// URL without RawQuery so the message keeps the operation, host,
// and path useful for diagnostics, but drops the secret.
func scrubTransportError(err error) string {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err.Error()
	}
	parsed, perr := url.Parse(ue.URL)
	if perr != nil {
		// If the URL itself doesn't parse we can't safely include
		// any of it; fall back to op + cause without the URL.
		return fmt.Sprintf("%s: %v", ue.Op, ue.Err)
	}
	parsed.RawQuery = ""
	parsed.User = nil // also strip any embedded userinfo, defensive
	return fmt.Sprintf("%s %q: %v", ue.Op, parsed.String(), ue.Err)
}

func executeRequest(client *http.Client, req *http.Request, maxBytes int64) InvokeOutput {
	start := time.Now()
	// #nosec G107 G704 -- req.URL is constructed by buildURL, which parses the
	// operator-configured base_url independently, joins the model-supplied
	// path via url.URL.JoinPath (no string concatenation), and refuses any
	// result whose scheme/host differs from the base. validatePath additionally
	// rejects path shapes (//, @, CR/LF/NUL) that would let url.Parse be
	// tricked into changing the host. The dynamic URL is therefore pinned to
	// the connection's pre-registered host; SSRF is defeated at the construction
	// site even though gosec's taint analysis cannot see the runtime guards.
	resp, err := client.Do(req)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		return InvokeOutput{Status: 0, Error: scrubTransportError(err), DurationMs: duration}
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	body, truncated, readErr := readBody(resp.Body, maxBytes)
	if readErr != nil {
		return InvokeOutput{
			Status:     resp.StatusCode,
			Headers:    selectResponseHeaders(resp.Header),
			Error:      readErr.Error(),
			DurationMs: time.Since(start).Milliseconds(),
		}
	}
	parsed := decodeBody(resp.Header.Get("Content-Type"), body)
	out := InvokeOutput{
		Status:        resp.StatusCode,
		Headers:       selectResponseHeaders(resp.Header),
		Body:          parsed,
		BodyTruncated: truncated,
		Pagination:    detectPagination(resp.Header, parsed),
		DurationMs:    time.Since(start).Milliseconds(),
	}
	if truncated {
		// The body exceeded the connection's max_response_bytes
		// cap. Steer the model toward api_export, which streams
		// the response directly into a portal asset without
		// returning the bytes through the MCP turn — same path
		// trino_export uses for query results that don't fit.
		out.Hint = "response exceeded max_response_bytes; use api_export to stream the full response into a portal asset (no model-context cost)"
	}
	return out
}

func readBody(r io.Reader, maxBytes int64) (body []byte, truncated bool, err error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxResponseBytes
	}
	limited := io.LimitReader(r, maxBytes+1)
	read, rerr := io.ReadAll(limited)
	if rerr != nil {
		return nil, false, fmt.Errorf("apigateway: reading response body: %w", rerr)
	}
	if int64(len(read)) > maxBytes {
		return read[:maxBytes], true, nil
	}
	return read, false, nil
}

// decodeBody parses a JSON response into a Go value when the
// Content-Type indicates JSON; otherwise returns the body as a
// string. Decoding failure on a JSON-typed response falls back to
// returning the raw text so the model still sees something useful.
func decodeBody(contentType string, body []byte) any {
	if len(body) == 0 {
		return nil
	}
	if !strings.Contains(strings.ToLower(contentType), "json") {
		return string(body)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return string(body)
	}
	return v
}

// passthroughResponseHeaders is the closed set of response headers
// returned to the model. Most upstream headers (Set-Cookie, Server,
// X-Powered-By, Date, etc.) are noise from the model's perspective;
// pagination and content-type headers are useful enough to be worth
// the context cost.
//
//nolint:gochecknoglobals // intentionally a package-level constant set
var passthroughResponseHeaders = map[string]bool{
	"Content-Type":          true,
	"Content-Length":        true,
	"Content-Encoding":      true,
	"Etag":                  true,
	"Last-Modified":         true,
	"Link":                  true,
	"Location":              true, // 3xx target — surfaced so the model can choose to follow manually
	"Retry-After":           true,
	"X-Total-Count":         true,
	"X-Ratelimit-Limit":     true,
	"X-Ratelimit-Remaining": true,
	"X-Ratelimit-Reset":     true,
}

func selectResponseHeaders(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string)
	for name, values := range h {
		if passthroughResponseHeaders[http.CanonicalHeaderKey(name)] {
			out[http.CanonicalHeaderKey(name)] = values
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
