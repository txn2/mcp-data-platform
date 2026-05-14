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
	if err := doc.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("validating: %w: %w", ErrInvalidContent, err)
	}
	return doc, nil
}

// ValidateContent is a wrapper around ParseSpec for callers that
// only need to assert validity (e.g. the admin upload route).
func ValidateContent(raw string) error {
	_, err := ParseSpec(raw)
	return err
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
	return doFetch(ctx, u, opts)
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
func doFetch(ctx context.Context, u *url.URL, opts FetchOptions) (*FetchResult, error) {
	client := opts.HTTPClient
	if client == nil {
		client = newFetchClient(opts)
	}
	ctx, cancel := context.WithTimeout(ctx, opts.TotalTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/yaml, */*;q=0.5")
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
