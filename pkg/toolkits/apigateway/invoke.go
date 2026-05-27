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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/txn2/mcp-data-platform/pkg/observability"
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
	"PROPFIND":        true,
	"MKCOL":           true,
	"MOVE":            true,
	"COPY":            true,
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
	"PROPFIND":        true,
	"MOVE":            true,
	"COPY":            true,
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

// InvokeOutput is the structured result returned to the model and to
// the REST gateway shim. Outcomes are distinguished at the MCP-result
// layer (see buildInvokeResult and issue #432):
//
//   - Upstream responded with anything (2xx-5xx): the gateway
//     succeeded at proxying. Status carries the upstream HTTP code,
//     Error is normally empty, and the wrapping CallToolResult has
//     IsError=false. Wire-level HTTP status from the REST shim stays
//     200; HTTP clients read the upstream code from Status and
//     branch on it.
//   - Upstream responded with a status but the body could not be
//     read in full (mid-stream drop, body exceeded the buffer
//     allocator): Status carries the upstream code, Error carries
//     the read-failure text, IsError stays false. The partial body
//     is still returned in Body so callers can inspect what arrived.
//   - Gateway could not reach upstream OR the upstream call timed
//     out: gateway-level failure. Status is 0, Error carries the
//     scrubbed transport-error text, the wrapping CallToolResult
//     has IsError=true, and the REST shim maps this to wire 502
//     (transport) or 504 (timeout).
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
//
// specs is the connection's parsed OpenAPI catalog keyed by component
// spec name. It is consulted by encodeBody to drive Content-Type
// negotiation from the operation's declared requestBody.content (see
// issue #453); leaving it nil falls back to today's type-driven
// heuristic and is the path test-only invocations take.
type invocation struct {
	cfg    Config
	auth   Authenticator
	client *http.Client
	specs  map[string]*specState
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

	declaredContentTypes := resolveDeclaredContentTypes(inv.specs, method, in.Path)
	body, contentType, err := encodeBody(method, in.Body, declaredContentTypes, in.Headers)
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
		return "", fmt.Errorf("apigateway: method %q not supported (want GET, POST, PUT, DELETE, PATCH, HEAD, PROPFIND, MKCOL, MOVE, or COPY)", method)
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

// applicationJSON is the canonical content-type literal used across
// the spec-driven negotiation logic; named so the same string isn't
// repeated across encodeBody's branches and the catalog matcher.
const applicationJSON = "application/json"

// textPlainUTF8 is today's fallback Content-Type for string bodies
// without a JSON signal. Pulled out as a constant for the same reason
// as applicationJSON.
const textPlainUTF8 = "text/plain; charset=utf-8"

// encodeBody serializes the body for an outbound HTTP request and
// returns the Content-Type the gateway proposes to set. Selection
// rules, in order (issue #453):
//
//  1. Method does not allow a body, or body is nil: nothing to send.
//  2. The caller's headers already contain a Content-Type: emit the
//     bytes using today's type-driven encoder; buildRequest will keep
//     the caller's header so the catalog hint is irrelevant.
//  3. The catalog declares application/json on the resolved operation
//     and the caller did NOT set Content-Type:
//     - object/array/scalar bodies marshal as JSON (unchanged from
//     today's behavior),
//     - string bodies that parse as JSON pass through verbatim with
//     Content-Type: application/json (the new behavior, which closes
//     the case where a tool-call layer pre-serialized the argument),
//     - string bodies that do NOT parse as JSON fall back to
//     text/plain (today's behavior preserved).
//  4. The catalog declares a single non-JSON media type and the body
//     is a string: send verbatim with that media type.
//  5. Anything else (no catalog match, no caller header): today's
//     type-driven behavior, i.e. string to text/plain, anything else
//     to application/json via json.Marshal.
//
// declaredContentTypes is the sorted slice returned by
// resolveDeclaredContentTypes; an empty/nil slice means the operation
// could not be located in the catalog. callerHeaders is the model's
// per-call headers map; case-insensitive lookup is required because
// the model can send "content-type" in any casing.
func encodeBody(method string, body any, declaredContentTypes []string, callerHeaders map[string]string) (data []byte, contentType string, err error) {
	if body == nil || !methodsAllowingBody[method] {
		return nil, "", nil
	}
	if callerSetsContentType(callerHeaders) {
		return encodeBodyTypeDriven(body)
	}
	pick := preferredContentType(declaredContentTypes)
	if pick == "" {
		return encodeBodyTypeDriven(body)
	}
	if pick == applicationJSON {
		return encodeBodyForJSONOperation(body)
	}
	if s, ok := body.(string); ok {
		return []byte(s), pick, nil
	}
	return encodeBodyTypeDriven(body)
}

// encodeBodyTypeDriven implements the pre-issue-#453 behavior: string
// bodies emit text/plain verbatim, every other type marshals as JSON.
// Reused on the "no catalog hint" and "caller-supplied Content-Type"
// branches so the legacy behavior is preserved character-for-character.
func encodeBodyTypeDriven(body any) (data []byte, contentType string, err error) {
	if s, ok := body.(string); ok {
		return []byte(s), textPlainUTF8, nil
	}
	encoded, jerr := json.Marshal(body)
	if jerr != nil {
		return nil, "", fmt.Errorf("apigateway: encoding body as JSON: %w", jerr)
	}
	return encoded, applicationJSON, nil
}

// encodeBodyForJSONOperation handles the "catalog declares JSON, no
// caller header" branch. Non-string bodies marshal as JSON (today's
// behavior). String bodies are probed with json.Unmarshal: a successful
// parse means the caller already serialized JSON and the gateway should
// hand the bytes through with application/json; a failed parse falls
// back to text/plain so the existing fixture 3 case (literal text
// payload that happens to be a string) is unchanged.
func encodeBodyForJSONOperation(body any) (data []byte, contentType string, err error) {
	if s, ok := body.(string); ok {
		var probe any
		if err := json.Unmarshal([]byte(s), &probe); err == nil {
			return []byte(s), applicationJSON, nil
		}
		return []byte(s), textPlainUTF8, nil
	}
	encoded, jerr := json.Marshal(body)
	if jerr != nil {
		return nil, "", fmt.Errorf("apigateway: encoding body as JSON: %w", jerr)
	}
	return encoded, applicationJSON, nil
}

// callerSetsContentType reports whether the model's per-call headers
// already pin Content-Type. Lookup is case-insensitive because the
// model can write "content-type", "Content-Type", or any other casing
// and Go's http header set treats them the same.
func callerSetsContentType(h map[string]string) bool {
	for name := range h {
		if strings.EqualFold(name, "Content-Type") {
			return true
		}
	}
	return false
}

// resolveDeclaredContentTypes returns the sorted requestBody content
// types the connection's OpenAPI catalog declares for the operation
// matching (method, path), or nil when no operation can be matched.
// The matcher walks each component spec on the connection and
// compares the model-supplied concrete path against the
// effectiveBasePath-prefixed path template segment-by-segment;
// literal segments must match exactly, bracketed placeholder segments
// match any non-empty segment.
//
// When the spec contains both a literal and a templated path that
// match the same concrete path (e.g. "/users/me" and "/users/{id}"),
// the literal entry (the template with fewer placeholder segments)
// wins. Without that tie-breaker the chosen template would vary
// across calls because Go's map iteration is randomized.
//
// nil specs (test-only invocations) and operations without a
// requestBody both return nil so encodeBody falls back to its
// type-driven encoder.
func resolveDeclaredContentTypes(specs map[string]*specState, method, path string) []string {
	if len(specs) == 0 {
		return nil
	}
	upperMethod := strings.ToUpper(method)
	for _, st := range specs {
		if cts := resolveDeclaredContentTypesInSpec(st, upperMethod, path); cts != nil {
			return cts
		}
	}
	return nil
}

// resolveDeclaredContentTypesInSpec is the per-spec slice of
// resolveDeclaredContentTypes. Extracted so the outer function stays
// under the cognitive-complexity ceiling and so unit tests can target
// a single parsed document directly.
func resolveDeclaredContentTypesInSpec(st *specState, method, path string) []string {
	if st == nil || st.doc == nil || st.doc.Paths == nil {
		return nil
	}
	item := findMostSpecificPathMatch(st, path)
	if item == nil {
		return nil
	}
	op := operationForMethod(item, method)
	if op == nil || op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	return sortedContentTypes(op.RequestBody.Value.Content)
}

// findMostSpecificPathMatch returns the PathItem whose template
// matches path with the fewest placeholder segments. nil when no
// template matches. See resolveDeclaredContentTypes for the
// motivation.
func findMostSpecificPathMatch(st *specState, path string) *openapi3.PathItem {
	var (
		bestItem  *openapi3.PathItem
		bestHoles int
	)
	for rawPath, item := range st.doc.Paths.Map() {
		if item == nil {
			continue
		}
		template := st.effectiveBasePath + rawPath
		if !pathMatchesTemplate(path, template) {
			continue
		}
		holes := countTemplatePlaceholders(template)
		if bestItem == nil || holes < bestHoles {
			bestItem = item
			bestHoles = holes
		}
	}
	return bestItem
}

// operationForMethod returns the Operation registered on item for the
// given HTTP method, or nil when the path item declares no operation
// for that verb. Method comparison is exact (caller upper-cases).
func operationForMethod(item *openapi3.PathItem, method string) *openapi3.Operation {
	for _, m := range pathItemMethods {
		if m.method == method {
			return m.get(item)
		}
	}
	return nil
}

// sortedContentTypes returns the keys of an openapi3.Content map as a
// sorted slice so the caller's content-type preference picks
// deterministically. Empty input returns nil so the caller can
// short-circuit on "no declared media type".
func sortedContentTypes(content openapi3.Content) []string {
	if len(content) == 0 {
		return nil
	}
	out := make([]string, 0, len(content))
	for ct := range content {
		out = append(out, ct)
	}
	sort.Strings(out)
	return out
}

// pathMatchesTemplate reports whether concrete (e.g. "/v1/users/42")
// matches an OpenAPI path template (e.g. "/v1/users/{id}"). Both
// strings are split on "/" and compared segment-by-segment; bracketed
// placeholder segments match any non-empty segment, literal segments
// must match exactly. Trailing slashes are normalized away so
// "/v1/users/" and "/v1/users" both match the same template.
func pathMatchesTemplate(concrete, template string) bool {
	cs := strings.Split(strings.TrimSuffix(concrete, pathSep), pathSep)
	ts := strings.Split(strings.TrimSuffix(template, pathSep), pathSep)
	if len(cs) != len(ts) {
		return false
	}
	for i, seg := range ts {
		if isPlaceholderSegment(seg) {
			if cs[i] == "" {
				return false
			}
			continue
		}
		if cs[i] != seg {
			return false
		}
	}
	return true
}

// isPlaceholderSegment reports whether a path-template segment is an
// OpenAPI parameter placeholder (e.g. "{datasetId}"). A two-character
// minimum length guards against the degenerate "{}" segment, which
// no spec generator emits but a hand-edited spec might contain.
func isPlaceholderSegment(seg string) bool {
	return len(seg) >= 2 && seg[0] == '{' && seg[len(seg)-1] == '}'
}

// countTemplatePlaceholders returns the number of placeholder segments
// in an OpenAPI path template; used by findMostSpecificPathMatch to
// prefer literal paths over templated ones when both match the same
// concrete path.
func countTemplatePlaceholders(template string) int {
	count := 0
	for seg := range strings.SplitSeq(template, pathSep) {
		if isPlaceholderSegment(seg) {
			count++
		}
	}
	return count
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

// httpStatus4xxLo / httpStatus5xxLo / httpStatus6xxLo bound the
// upstream status ranges used by ClassifyInvokeOutcome. They mirror
// the constants in pkg/observability/status.go but live here so this
// file's switch reads as plain numbers without an import alias gymnastics.
const (
	httpStatus4xxLo = 400
	httpStatus5xxLo = 500
	httpStatus6xxLo = 600
)

// ClassifyInvokeOutcome maps an InvokeOutput to one of the bounded
// outcome categories defined in pkg/observability. The audit middleware
// reads this category off the CallToolResult's _meta to populate
// audit_logs.error_category and to derive success without relying on
// the MCP IsError flag, which is reserved for gateway-level failures
// (transport / timeout) per the corrected gateway semantics described
// in issue #432.
//
// Mapping:
//   - Status 0 with a timeout-like error message → upstream_timeout
//   - Status 0 with any other transport error    → transport_err
//   - 4xx upstream response                      → upstream_4xx
//   - 5xx upstream response                      → upstream_5xx
//   - 2xx / 3xx upstream response                → ok
//
// Pattern matching on the scrubbed error string is acceptable here
// because scrubTransportError above normalizes the upstream error
// shape; the substrings checked below are the stable phrases Go's
// net/http and net/url error types produce.
func ClassifyInvokeOutcome(out InvokeOutput) string {
	if out.Status == 0 {
		if isTimeoutErrorMessage(out.Error) {
			return observability.OutcomeUpstreamTimeout
		}
		return observability.OutcomeTransportErr
	}
	if out.Status >= httpStatus5xxLo && out.Status < httpStatus6xxLo {
		return observability.OutcomeUpstream5xx
	}
	if out.Status >= httpStatus4xxLo && out.Status < httpStatus5xxLo {
		return observability.OutcomeUpstream4xx
	}
	return observability.OutcomeOK
}

// isTimeoutErrorMessage reports whether the scrubbed transport-error
// string carries a timeout signature. Strings checked are the stable
// phrases Go's net/http, context, and net/url error types emit:
//   - "context deadline exceeded" for context.DeadlineExceeded
//   - "Client.Timeout exceeded" for http.Client when its own Timeout fires
//   - "i/o timeout" for net.OpError on read/write timeouts
//
// All three are case-insensitive so the message can come from any
// wrapper layer. A plain "timeout" substring is intentionally NOT
// matched because that word can appear in legitimate body content
// surfaced via scrubTransportError; the more specific phrases above
// are unambiguous.
func isTimeoutErrorMessage(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "client.timeout exceeded") ||
		strings.Contains(lower, "i/o timeout")
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
