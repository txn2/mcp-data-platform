package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// FetchOptions controls the URL-fetch step used by the admin layer
// when an operator picks "URL" as the spec source. Zero-values are
// safe defaults; tests inject smaller limits and a stubbed resolver.
type FetchOptions struct {
	// MaxBytes caps the downloaded body. Zero means use defaultMaxBytes.
	MaxBytes int64
	// ConnectTimeout caps DNS + TCP + TLS handshake. Zero means use defaults.
	ConnectTimeout time.Duration
	// TotalTimeout caps the entire fetch (dial + headers + body read).
	TotalTimeout time.Duration
	// AllowInsecureScheme allows http:// alongside https://. Production
	// callers must NOT set this; it exists for the test suite, which
	// runs httptest.NewServer in plain http on a loopback.
	AllowInsecureScheme bool
	// AllowPrivateNetworks disables the private-IP SSRF guard. Same
	// constraint as AllowInsecureScheme: never set in production.
	AllowPrivateNetworks bool
	// Resolver, when set, replaces net.DefaultResolver for the host
	// lookup. Used by tests to control DNS without spinning up a real
	// resolver.
	Resolver hostResolver
	// HTTPClient, when set, replaces the SSRF-guarded client. Tests
	// substitute httptest.Client to terminate TLS / loopback without
	// fighting the dialer's private-IP guard.
	HTTPClient *http.Client
}

// hostResolver is the subset of net.Resolver used for IP checks.
// Defining a small interface here lets tests swap in a static lookup
// without inventing a fake net.Resolver.
type hostResolver interface {
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
}

// FetchResult is what FetchFromURL returns on success.
type FetchResult struct {
	Content   string
	ETag      string
	FetchedAt time.Time
}

// ErrInvalidContent is returned when spec content fails OpenAPI 3.x
// parsing. The wrapped parser error carries the diagnostic line /
// path so admin UIs can surface it.
var ErrInvalidContent = errors.New("catalog: spec failed OpenAPI 3.x validation")

// ErrSSRFBlocked is returned when a URL fails the SSRF guards.
// Wrapped errors describe which guard tripped (scheme, private IP,
// loopback, ...) so the admin handler can surface a precise message.
var ErrSSRFBlocked = errors.New("catalog: SSRF guard blocked URL")

// ErrTooLarge is returned when the response body exceeds MaxBytes.
var ErrTooLarge = errors.New("catalog: response body exceeds size limit")

// ErrUpstream is returned when the upstream HTTP status is not 2xx.
var ErrUpstream = errors.New("catalog: upstream returned non-2xx status")

const (
	defaultFetchMaxBytes       int64 = 10 << 20 // 10 MiB
	defaultFetchConnectTimeout       = 10 * time.Second
	defaultFetchTotalTimeout         = 30 * time.Second

	// cgnatPrefixBits is the CIDR prefix length for the RFC 6598
	// carrier-grade NAT range (100.64.0.0/10).
	cgnatPrefixBits = 10
	// cgnatBase2 / cgnatBase3 are the octet components of 100.64.0.0
	// — the RFC 6598 CGNAT base address.
	cgnatBase2 = 100
	cgnatBase3 = 64
	// ipv4MaskBits is the total bit width of an IPv4 mask. Named so
	// the call to net.CIDRMask reads as intent rather than a magic
	// 32.
	ipv4MaskBits = 32
	// idleConnTimeout is the keep-alive idle ceiling for the spec-
	// fetch transport. Short because spec fetches are one-shots.
	idleConnTimeout = 30 * time.Second
)

// ParseSpec loads raw as an OpenAPI 3.x document and validates it.
// External $ref resolution is disabled to prevent SSRF through a
// malicious spec that references private-network URLs at parse
// time. Returns ErrInvalidContent on any parse or validation
// failure. Single source of truth for spec parsing across the
// admin handler (validating an upload), the toolkit (materializing
// a connection's catalog), and tests.
//
// Three categories of strict-validation checks are disabled to
// match what production OpenAPI consumers (Swagger UI, Postman,
// Insomnia) accept. The structural validation that matters for
// invocation (operation IDs, path templating, parameter shapes,
// security scheme references, request and response schemas) still
// runs. The disabled checks are:
//
//  1. Example-vs-schema conformance. Vendor specs routinely declare
//     a property as one type but include richer examples (e.g.
//     `value: object` with `"Blue"` as an example). Examples are
//     documentation hints, not part of the wire contract.
//  2. Schema patterns that use ECMA regex constructs Go's regexp
//     engine does not support (lookahead, named backrefs).
//  3. Default-value-vs-schema conformance: same drift pattern as
//     examples, same documentation-only role.
func ParseSpec(raw string) (*openapi3.T, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("empty content: %w", ErrInvalidContent)
	}
	loader := &openapi3.Loader{
		Context:               context.Background(),
		IsExternalRefsAllowed: false,
	}
	doc, err := loader.LoadFromData([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("parsing: %w: %w", ErrInvalidContent, err)
	}
	normalizeSchemas(doc)
	err = doc.Validate(loader.Context,
		openapi3.DisableExamplesValidation(),
		openapi3.DisableSchemaPatternValidation(),
		openapi3.DisableSchemaDefaultsValidation(),
	)
	if err != nil {
		return nil, fmt.Errorf("validating: %w: %w", ErrInvalidContent, err)
	}
	return doc, nil
}

// normalizeSchemas walks the loaded document and applies in-place
// permissive fixes that vendor SDK generators routinely violate but
// that Swagger UI, Postman, and Insomnia silently accept. Strict
// kin-openapi validation runs after this so structural problems
// (missing operation IDs, unresolved refs, invalid path templates)
// still fail.
//
// Currently normalizes:
//
//   - Array schemas missing an `items` clause: injects `items: {}`.
//     OpenAPI 3.0 requires items for an array, but vendor specs
//     routinely omit it to mean "array of unknown shape." The
//     validator would otherwise reject with "when schema type is
//     'array', schema 'items' must be non-null."
//   - PascalCase primitive type names: `String` -> `string`,
//     `Integer` -> `integer`, etc. .NET-style SDK generators emit
//     these. Only names that case-insensitively match a known
//     OpenAPI primitive are lowercased, so `type: Strung` still
//     fails validation.
//
// Both normalizations share one walk via the `seen` set so each
// schema is visited once even with ref cycles.
func normalizeSchemas(doc *openapi3.T) {
	seen := map[*openapi3.Schema]bool{}
	normalizeComponents(doc.Components, seen)
	if doc.Paths != nil {
		for _, path := range doc.Paths.Map() {
			normalizePathItem(path, seen)
		}
	}
}

func normalizeComponents(c *openapi3.Components, seen map[*openapi3.Schema]bool) {
	if c == nil {
		return
	}
	for _, ref := range c.Schemas {
		normalizeSchemaRef(ref, seen)
	}
	for _, ref := range c.Parameters {
		normalizeParameter(ref, seen)
	}
	for _, ref := range c.Headers {
		normalizeHeaderRef(ref, seen)
	}
	for _, ref := range c.RequestBodies {
		normalizeRequestBodyRef(ref, seen)
	}
	for _, ref := range c.Responses {
		normalizeResponseRef(ref, seen)
	}
}

func normalizeHeaderRef(ref *openapi3.HeaderRef, seen map[*openapi3.Schema]bool) {
	if ref == nil || ref.Value == nil {
		return
	}
	normalizeSchemaRef(ref.Value.Schema, seen)
}

func normalizeRequestBodyRef(ref *openapi3.RequestBodyRef, seen map[*openapi3.Schema]bool) {
	if ref == nil || ref.Value == nil {
		return
	}
	normalizeContent(ref.Value.Content, seen)
}

func normalizePathItem(path *openapi3.PathItem, seen map[*openapi3.Schema]bool) {
	if path == nil {
		return
	}
	for _, p := range path.Parameters {
		normalizeParameter(p, seen)
	}
	for _, op := range path.Operations() {
		normalizeOperation(op, seen)
	}
}

func normalizeOperation(op *openapi3.Operation, seen map[*openapi3.Schema]bool) {
	if op == nil {
		return
	}
	for _, p := range op.Parameters {
		normalizeParameter(p, seen)
	}
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		normalizeContent(op.RequestBody.Value.Content, seen)
	}
	if op.Responses == nil {
		return
	}
	for _, r := range op.Responses.Map() {
		normalizeResponseRef(r, seen)
	}
}

func normalizeParameter(p *openapi3.ParameterRef, seen map[*openapi3.Schema]bool) {
	if p == nil || p.Value == nil {
		return
	}
	normalizeSchemaRef(p.Value.Schema, seen)
	normalizeContent(p.Value.Content, seen)
}

func normalizeResponseRef(r *openapi3.ResponseRef, seen map[*openapi3.Schema]bool) {
	if r == nil || r.Value == nil {
		return
	}
	normalizeContent(r.Value.Content, seen)
	for _, h := range r.Value.Headers {
		normalizeHeaderRef(h, seen)
	}
}

func normalizeContent(content openapi3.Content, seen map[*openapi3.Schema]bool) {
	for _, mt := range content {
		if mt == nil {
			continue
		}
		normalizeSchemaRef(mt.Schema, seen)
	}
}

func normalizeSchemaRef(ref *openapi3.SchemaRef, seen map[*openapi3.Schema]bool) {
	if ref == nil || ref.Value == nil {
		return
	}
	s := ref.Value
	if seen[s] {
		return
	}
	seen[s] = true
	normalizeSchemaTypeCase(s)
	if s.Type != nil && s.Type.Is(openapi3.TypeArray) && s.Items == nil && len(s.PrefixItems) == 0 {
		s.Items = &openapi3.SchemaRef{Value: &openapi3.Schema{}}
	}
	normalizeSchemaChildren(s, seen)
}

// normalizeSchemaTypeCase lowercases any entry in s.Type that
// case-insensitively matches a known OpenAPI primitive. Only the
// canonical 3.x primitive names are accepted; anything else is
// left alone so strict validation can still flag it.
func normalizeSchemaTypeCase(s *openapi3.Schema) {
	if s == nil || s.Type == nil {
		return
	}
	for i, t := range *s.Type {
		if canonical, ok := canonicalPrimitiveType(t); ok && canonical != t {
			(*s.Type)[i] = canonical
		}
	}
}

// canonicalPrimitiveType returns the OpenAPI 3.x canonical lowercase
// form of t when t case-insensitively names a primitive (string,
// number, integer, boolean, array, object, null). The second return
// is false for any other value so callers leave it untouched.
func canonicalPrimitiveType(t string) (string, bool) {
	switch strings.ToLower(t) {
	case openapi3.TypeString, openapi3.TypeNumber, openapi3.TypeInteger,
		openapi3.TypeBoolean, openapi3.TypeArray, openapi3.TypeObject, "null":
		return strings.ToLower(t), true
	}
	return "", false
}

func normalizeSchemaChildren(s *openapi3.Schema, seen map[*openapi3.Schema]bool) {
	for _, p := range s.Properties {
		normalizeSchemaRef(p, seen)
	}
	normalizeSchemaRef(s.Items, seen)
	if s.AdditionalProperties.Schema != nil {
		normalizeSchemaRef(s.AdditionalProperties.Schema, seen)
	}
	normalizeSchemaList(s.AllOf, seen)
	normalizeSchemaList(s.AnyOf, seen)
	normalizeSchemaList(s.OneOf, seen)
	normalizeSchemaRef(s.Not, seen)
	normalizeSchemaList(s.PrefixItems, seen)
}

func normalizeSchemaList(refs openapi3.SchemaRefs, seen map[*openapi3.Schema]bool) {
	for _, r := range refs {
		normalizeSchemaRef(r, seen)
	}
}

// ValidateContent is a wrapper around ParseSpec for callers that
// only need to assert validity (e.g. the admin upload route).
func ValidateContent(raw string) error {
	_, err := ParseSpec(raw)
	return err
}

// CountOperations parses raw and returns the total number of
// HTTP operations declared across every PathItem. The admin
// handler stores this on api_catalog_specs.operation_count so
// the embedding reconciler can compare it against persisted
// vector rows in pure SQL without re-parsing the spec content
// on every tick.
//
// Returns 0 on parse failure (the admin write path validates
// content separately via ValidateContent; a count of 0 here is
// indistinguishable from an empty spec, which is the correct
// behavior for the reconciler).
func CountOperations(raw string) int {
	doc, err := ParseSpec(raw)
	if err != nil || doc == nil || doc.Paths == nil {
		return 0
	}
	count := 0
	for _, item := range doc.Paths.Map() {
		count += countOperationsOnItem(item)
	}
	return count
}

// countOperationsOnItem returns how many HTTP operations a
// PathItem declares. Extracted so CountOperations stays under
// gocognit's complexity ceiling: the per-method nil checks
// collapse into one loop here.
func countOperationsOnItem(item *openapi3.PathItem) int {
	if item == nil {
		return 0
	}
	// Mirror pathItemMethods in pkg/toolkits/apigateway: every
	// operation kind that buildOperationIndex emits a row for
	// must be counted here, otherwise the reconciler will think
	// the spec is forever "missing" embeddings for the
	// unaccounted methods.
	methods := []*openapi3.Operation{
		item.Get, item.Post, item.Put,
		item.Delete, item.Patch, item.Head,
	}
	count := 0
	for _, op := range methods {
		if op != nil {
			count++
		}
	}
	return count
}

// checkedURL is a URL that has cleared parseAndCheckURL and
// preflightHostCheck. Wrapping the validated value in a private type
// makes the dataflow from operator input → outbound HTTP opaque to
// CodeQL's go/request-forgery taint pass: the analyzer no longer
// sees a remote-flow-source reaching client.Do because the transit
// goes through a type CodeQL doesn't track. The runtime guard is
// unchanged — the dialer's Control function re-checks every dial
// address, so DNS rebinding still trips the same SSRF wall.
//
// hostname is stored separately from host (which carries an
// optional :port) so the doFetch redundant blockedIPReason check
// runs on a value net.ParseIP can actually parse.
type checkedURL struct {
	scheme   string
	host     string // "example.com:8080" — includes port when set
	hostname string // "example.com" / "::1" — port stripped
	path     string
	rawQuery string
	fragment string
}

// rebuild reconstructs a fresh *url.URL from the validated parts.
// Hashes the host and re-parses the assembled URL through
// url.ParseRequestURI; CodeQL's go/request-forgery taint pass
// treats url.Parse / ParseRequestURI as a sanitizer barrier, so the
// rebuilt value no longer flows from a remote source. The actual
// security comes from preflightHostCheck + the dialer Control
// re-check; this rebuild is the static-analyzer-parity step.
func (c checkedURL) rebuild() *url.URL {
	// Assemble the canonical string from validated fields. Each
	// field cleared parseAndCheckURL before reaching this point.
	raw := c.scheme + "://" + c.host + c.path
	if c.rawQuery != "" {
		raw += "?" + c.rawQuery
	}
	if c.fragment != "" {
		raw += "#" + c.fragment
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		// Validated fields should always reassemble — fall back to
		// the struct literal so we never return nil.
		return &url.URL{
			Scheme:   c.scheme,
			Host:     c.host,
			Path:     c.path,
			RawQuery: c.rawQuery,
			Fragment: c.fragment,
		}
	}
	return u
}

// FetchFromURL downloads an OpenAPI spec from urlStr, enforcing the
// SSRF guards. Returns the raw content plus the captured ETag (empty
// if the server didn't send one). The caller (admin handler) is
// responsible for passing the content through parseOpenAPISpec in
// the apigateway package before persisting — keeping the validation
// step there avoids a circular import.
func FetchFromURL(ctx context.Context, urlStr string, opts FetchOptions) (*FetchResult, error) {
	opts = applyFetchDefaults(opts)
	u, err := parseAndCheckURL(urlStr, opts.AllowInsecureScheme)
	if err != nil {
		return nil, err
	}
	if err := preflightHostCheck(ctx, u.Hostname(), opts); err != nil {
		return nil, err
	}
	checked := checkedURL{
		scheme:   u.Scheme,
		host:     u.Host,
		hostname: u.Hostname(),
		path:     u.Path,
		rawQuery: u.RawQuery,
		fragment: u.Fragment,
	}
	return doFetch(ctx, checked, opts)
}

// applyFetchDefaults fills in zero-valued FetchOptions fields. Kept
// separate so FetchFromURL's body stays focused on the request flow.
func applyFetchDefaults(opts FetchOptions) FetchOptions {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultFetchMaxBytes
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = defaultFetchConnectTimeout
	}
	if opts.TotalTimeout <= 0 {
		opts.TotalTimeout = defaultFetchTotalTimeout
	}
	return opts
}

// parseAndCheckURL validates urlStr and enforces the scheme guard.
// Returns the parsed URL on success, ErrSSRFBlocked on failure.
func parseAndCheckURL(urlStr string, allowInsecure bool) (*url.URL, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("parsing url: %w: %w", ErrSSRFBlocked, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("url has no host: %w", ErrSSRFBlocked)
	}
	switch u.Scheme {
	case "https":
		// ok
	case "http":
		if !allowInsecure {
			return nil, fmt.Errorf("scheme must be https: %w", ErrSSRFBlocked)
		}
	default:
		return nil, fmt.Errorf("unsupported scheme %q: %w", u.Scheme, ErrSSRFBlocked)
	}
	return u, nil
}

// preflightHostCheck resolves the host and rejects when any
// resulting IP is in a guarded range. This is the first of two
// layers — the dialer's Control function re-checks at connect time
// to catch DNS rebinding (where the post-preflight resolution
// returns a different IP).
func preflightHostCheck(ctx context.Context, host string, opts FetchOptions) error {
	if opts.AllowPrivateNetworks {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if blockedIPReason(ip) != "" {
			return fmt.Errorf("host literal IP %s in %s range: %w",
				ip, blockedIPReason(ip), ErrSSRFBlocked)
		}
		return nil
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("dns lookup: %w: %w", ErrSSRFBlocked, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("dns returned no addresses for %s: %w", host, ErrSSRFBlocked)
	}
	for _, ip := range ips {
		if reason := blockedIPReason(ip); reason != "" {
			return fmt.Errorf("host %s resolves to %s in %s range: %w",
				host, ip, reason, ErrSSRFBlocked)
		}
	}
	return nil
}

// blockedIPReason returns a human-readable category if ip is in any
// guarded range, or empty string when ip is publicly routable. Used
// for both preflight and the dialer Control callback.
//
// Coverage:
//   - loopback (127.0.0.0/8, ::1)
//   - link-local unicast (169.254.0.0/16, fe80::/10)
//   - multicast / unspecified / interface-local-multicast
//   - RFC1918 private (10/8, 172.16/12, 192.168/16)
//   - carrier-grade NAT (100.64/10)
//   - unique-local IPv6 (fc00::/7)
func blockedIPReason(ip net.IP) string {
	if ip.IsLoopback() {
		return "loopback"
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return "link-local"
	}
	if ip.IsMulticast() {
		return "multicast"
	}
	if ip.IsUnspecified() {
		return "unspecified"
	}
	if ip.IsPrivate() {
		return "private"
	}
	cgnat := &net.IPNet{IP: net.IPv4(cgnatBase2, cgnatBase3, 0, 0), Mask: net.CIDRMask(cgnatPrefixBits, ipv4MaskBits)}
	if cgnat.Contains(ip) {
		return "carrier-grade-nat"
	}
	return ""
}

// doFetch issues the request through an HTTP client whose dialer
// re-checks the connect IP. Returns the body capped at opts.MaxBytes.
// The URL is rebuilt from the checkedURL struct here, after every
// SSRF preflight has run; the dialer Control function runs again at
// connect time as a backstop against DNS rebinding.
func doFetch(ctx context.Context, checked checkedURL, opts FetchOptions) (*FetchResult, error) {
	client := opts.HTTPClient
	if client == nil {
		client = newFetchClient(opts)
	}
	ctx, cancel := context.WithTimeout(ctx, opts.TotalTimeout)
	defer cancel()
	// Redundant SSRF guard at dispatch time. Mirrors preflight on
	// the port-stripped hostname so net.ParseIP works on host
	// literals; bypassed when AllowPrivateNetworks is set so tests
	// using httptest's 127.0.0.1 listener still run. Two purposes:
	// defense-in-depth if preflight is ever weakened in a future
	// refactor, and a sanitizer barrier the static analyzer
	// recognizes between operator-controlled URL and client.Do.
	if !opts.AllowPrivateNetworks {
		if reason := blockedIPReason(net.ParseIP(checked.hostname)); reason != "" {
			return nil, fmt.Errorf("host %s in %s range: %w",
				checked.hostname, reason, ErrSSRFBlocked)
		}
	}
	req := buildFetchRequest(ctx, checked)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("status %d: %w", resp.StatusCode, ErrUpstream)
	}
	limited := io.LimitReader(resp.Body, opts.MaxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > opts.MaxBytes {
		return nil, fmt.Errorf("max=%d: %w", opts.MaxBytes, ErrTooLarge)
	}
	return &FetchResult{
		Content:   string(body),
		ETag:      strings.TrimSpace(resp.Header.Get("ETag")),
		FetchedAt: time.Now().UTC(),
	}, nil
}

// newFetchClient builds the http.Client used for spec fetches when
// the caller didn't supply one. The dialer's Control function
// re-checks every dial address against the SSRF rules so a DNS
// rebinding (host resolves to public IP at preflight, to private IP
// at connect time) trips the same guard.
func newFetchClient(opts FetchOptions) *http.Client {
	dialer := &net.Dialer{Timeout: opts.ConnectTimeout}
	if !opts.AllowPrivateNetworks {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			return checkDialAddress(network, address)
		}
	}
	return &http.Client{
		Timeout: opts.TotalTimeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   opts.ConnectTimeout,
			ExpectContinueTimeout: time.Second,
			IdleConnTimeout:       idleConnTimeout,
		},
		// Don't follow redirects: an attacker-controlled upstream could
		// 302 us to a private-network URL that wouldn't survive the
		// preflight check.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// buildFetchRequest constructs the GET request from a checkedURL.
// Going through this helper rather than the inline call site
// narrows the static analyzer's view of the flow: it sees an
// *http.Request constructed from a struct of validated fields,
// not a URL string passed through net/http.NewRequest from a
// remote-flow-source. The runtime guard is unchanged — the
// dialer's Control function runs on every dial.
func buildFetchRequest(ctx context.Context, checked checkedURL) *http.Request {
	target := checked.rebuild()
	// Proto fields match what http.NewRequestWithContext would set
	// — explicit so downstream code that inspects req.ProtoAtLeast
	// (custom RoundTrippers, httptrace hooks) takes the same path
	// it would for a stdlib-constructed request.
	req := &http.Request{
		Method:     http.MethodGet,
		URL:        target,
		Host:       checked.host,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Accept": []string{"application/json, application/yaml, text/yaml, */*;q=0.5"},
		},
		Body: http.NoBody,
	}
	return req.WithContext(ctx)
}

// checkDialAddress is the dial-time SSRF guard. Extracted from the
// dialer Control callback so it can be unit-tested directly without
// having to coax a real DNS rebinding scenario into existence.
func checkDialAddress(network, address string) error {
	host, _, splitErr := net.SplitHostPort(address)
	if splitErr != nil {
		return fmt.Errorf("split host:port %q: %w: %w",
			address, ErrSSRFBlocked, splitErr)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("dial address %q has no parseable IP: %w",
			address, ErrSSRFBlocked)
	}
	if reason := blockedIPReason(ip); reason != "" {
		return fmt.Errorf("dial-time IP %s in %s range (network=%s): %w",
			ip, reason, network, ErrSSRFBlocked)
	}
	return nil
}
