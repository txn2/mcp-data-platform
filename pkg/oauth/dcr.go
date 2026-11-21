package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/publicsuffix"
)

// Redirect URI schemes with special handling in the scheme gate.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// DCR credential size constants.
const (
	// clientIDByteLength is the byte length for generated client IDs.
	clientIDByteLength = 32

	// clientSecretByteLength is the byte length for generated client secrets.
	clientSecretByteLength = 48

	// idByteLength is the byte length for generated unique IDs.
	idByteLength = 16
)

// DCRConfig configures Dynamic Client Registration.
type DCRConfig struct {
	// Enabled enables DCR.
	Enabled bool

	// AllowedRedirectPatterns are regex patterns for allowed redirect URIs.
	// Each must match the whole URI (it is anchored at both ends), and it must
	// pin the host: a pattern that would still match with the URI's host
	// replaced by one nobody controls admits nothing (see hostPinned).
	// The registration endpoint is unauthenticated, so leaving this empty
	// denies all registrations unless AllowAllRedirectURIs is set.
	AllowedRedirectPatterns []string

	// AllowAllRedirectURIs explicitly opts out of redirect URI pattern
	// matching, accepting any HTTPS (or loopback HTTP) redirect URI. An
	// attacker-controlled redirect URI is the setup for authorization-code
	// interception, so this is an explicit escape hatch rather than the
	// default for an empty pattern list.
	AllowAllRedirectURIs bool

	// DefaultGrantTypes are the default grant types for new clients.
	DefaultGrantTypes []string

	// RequirePKCE requires PKCE for all clients.
	RequirePKCE bool
}

// DCRService handles Dynamic Client Registration.
type DCRService struct {
	storage  Storage
	config   DCRConfig
	patterns []*regexp.Regexp
}

// NewDCRService creates a new DCR service.
func NewDCRService(storage Storage, config DCRConfig) (*DCRService, error) {
	patterns := make([]*regexp.Regexp, 0, len(config.AllowedRedirectPatterns))
	for _, p := range config.AllowedRedirectPatterns {
		// Anchored: an operator's pattern names a whole redirect URI, and an
		// unanchored one matched any URI that merely contained it.
		re, err := regexp.Compile("^(?:" + p + ")$")
		if err != nil {
			return nil, fmt.Errorf("invalid redirect pattern %q: %w", p, err)
		}
		patterns = append(patterns, re)
	}

	if len(config.DefaultGrantTypes) == 0 {
		config.DefaultGrantTypes = []string{grantTypeAuthCode, grantTypeRefreshToken}
	}

	return &DCRService{
		storage:  storage,
		config:   config,
		patterns: patterns,
	}, nil
}

// DCRRequest is a Dynamic Client Registration request.
type DCRRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

// DCRResponse is a Dynamic Client Registration response.
type DCRResponse struct {
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret,omitempty"` // #nosec G117 -- required by OAuth 2.0 DCR spec (RFC 7591)
	ClientName            string   `json:"client_name"`
	RedirectURIs          []string `json:"redirect_uris"`
	GrantTypes            []string `json:"grant_types"`
	ClientSecretExpiresAt int      `json:"client_secret_expires_at"` // 0 means never
}

// Register registers a new OAuth client.
func (s *DCRService) Register(ctx context.Context, req DCRRequest) (*DCRResponse, error) {
	if !s.config.Enabled {
		return nil, errors.New("dynamic client registration is disabled")
	}

	if len(s.patterns) == 0 && !s.config.AllowAllRedirectURIs {
		return nil, errors.New("dynamic client registration is not configured: set allowed_redirect_patterns to the redirect URIs your clients use, or set allow_all_redirect_uris: true to explicitly accept any redirect URI")
	}

	if err := s.validateRedirectURIs(req.RedirectURIs); err != nil {
		return nil, err
	}

	// Generate client credentials
	clientID, err := generateSecureToken(clientIDByteLength)
	if err != nil {
		return nil, fmt.Errorf("generating client ID: %w", err)
	}

	clientSecret, err := generateSecureToken(clientSecretByteLength)
	if err != nil {
		return nil, fmt.Errorf("generating client secret: %w", err)
	}

	// Hash the client secret
	hashedSecret, err := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing client secret: %w", err)
	}

	// Determine grant types
	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = s.config.DefaultGrantTypes
	}

	// Create client
	client := &Client{
		ID:                    generateID(),
		ClientID:              clientID,
		ClientSecret:          string(hashedSecret),
		Name:                  req.ClientName,
		RedirectURIs:          req.RedirectURIs,
		GrantTypes:            grantTypes,
		RequirePKCE:           s.config.RequirePKCE,
		CreatedAt:             time.Now(),
		Active:                true,
		DynamicallyRegistered: true,
	}

	if err := s.storage.CreateClient(ctx, client); err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}

	return &DCRResponse{
		ClientID:              clientID,
		ClientSecret:          clientSecret, // Return unhashed secret to client
		ClientName:            req.ClientName,
		RedirectURIs:          req.RedirectURIs,
		GrantTypes:            grantTypes,
		ClientSecretExpiresAt: 0,
	}, nil
}

// validateRedirectURIs returns an error naming the first requested redirect URI
// that the configured policy does not permit.
func (s *DCRService) validateRedirectURIs(uris []string) error {
	for _, uri := range uris {
		if s.isAllowedRedirectURI(uri) {
			continue
		}
		for _, pattern := range s.patterns {
			if pattern.MatchString(uri) {
				return fmt.Errorf("redirect URI not allowed: %s: the pattern that matches it would also match "+
					"other hosts; the operator should write the host exactly (e.g. https://claude\\.ai/api/mcp/auth_callback)", uri)
			}
		}
		return fmt.Errorf("redirect URI not allowed: %s", uri)
	}
	return nil
}

// AllowsRedirectURI reports whether the configured policy permits uri. The
// authorization endpoint asks it again for a dynamically registered client's
// redirect URI, so a client registered under an earlier, looser policy is held
// to the current one.
func (s *DCRService) AllowsRedirectURI(uri string) bool {
	return s != nil && s.isAllowedRedirectURI(uri)
}

// isAllowedRedirectURI checks if a redirect URI is allowed.
//
// Shape rules (redirectShape): an absolute URI with no user info, no fragment,
// no backslash, whitespace or control character, and no escape in its
// authority, so the host a browser goes to is the host read here.
//
// Scheme rules: plain HTTP to a non-loopback host is interceptable
// transport and is never allowed regardless of configuration. HTTPS and
// loopback HTTP (RFC 8252 Section 7.3) are eligible everywhere.
// Private-use schemes (RFC 8252 Section 7.1 native apps, e.g.
// com.example.app:/callback) are eligible only through an explicitly
// configured pattern; AllowAllRedirectURIs does not cover them, so the
// unauthenticated registration endpoint never hands out arbitrary
// scheme hijacking by default.
//
// Pattern rules: a pattern matches the whole URI and, unless the URI is
// loopback, must pin its host (hostPinned).
func (s *DCRService) isAllowedRedirectURI(uri string) bool {
	u, ok := redirectShape(uri)
	if !ok {
		return false
	}
	if u.Scheme == schemeHTTP && !isLoopbackURI(uri) {
		return false
	}

	if len(s.patterns) == 0 {
		return s.config.AllowAllRedirectURIs && (u.Scheme == schemeHTTPS || u.Scheme == schemeHTTP)
	}

	// A loopback URI sends the code to the person's own machine, so a pattern
	// need only match it; any other host must be pinned.
	loopback := isLoopbackURI(uri)
	for _, pattern := range s.patterns {
		if pattern.MatchString(uri) && (loopback || hostPinned(pattern, uri, u)) {
			return true
		}
	}
	return false
}

// redirectShape parses uri and reports whether it has a shape whose host is
// unambiguous: absolute, no user info, no fragment, no backslash, whitespace
// or control character anywhere, and no percent escape in the authority. A
// browser reads "https://a.example\@b.example/" and an escaped host
// differently from a parser, and a redirect URI has no use for either.
func redirectShape(uri string) (*url.URL, bool) {
	if strings.ContainsFunc(uri, ambiguousRune) {
		return nil, false
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme == "" || u.User != nil || strings.Contains(uri, "#") {
		return nil, false
	}
	if auth, ok := authority(uri); ok && strings.Contains(auth, "%") {
		return nil, false
	}
	web := u.Scheme == schemeHTTP || u.Scheme == schemeHTTPS
	return u, !web || u.Hostname() != ""
}

// asciiDEL is the ASCII delete control character.
const asciiDEL = 0x7f

// ambiguousRune is a rune a redirect URI never carries: whitespace, a control
// character, or a backslash, which a browser reads as a slash.
func ambiguousRune(r rune) bool {
	return r <= ' ' || r == asciiDEL || r == '\\'
}

// authority returns the authority of a hierarchical URI ("scheme://authority/
// ..."), and false for one without ("com.example.app:/callback").
func authority(uri string) (string, bool) {
	_, rest, ok := strings.Cut(uri, "://")
	if !ok {
		return "", false
	}
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		return rest[:end], true
	}
	return rest, true
}

// hostCanary is a host no one can register (RFC 6761 reserves .invalid).
const hostCanary = "host-check.invalid"

// hostPinned reports whether pattern pins uri's host: the host is matched in
// full, so no host an outsider could register is matched in its place. The
// same URI with its host altered in each way an outsider could exploit must
// not match:
//
//   - the host replaced by hostCanary: a wildcard reaching across the
//     authority ("https://.*\.example\.com/cb" against
//     "https://attacker.test/x.example.com/cb") matches whatever the host;
//   - a label appended (host.host-check.invalid) or a letter appended to the
//     last label: a wildcard after the host lets "example.com.attacker.test"
//     or another top-level domain through;
//   - a letter put in front of the registrable domain (www.xexample.com): a
//     wildcard before it lets "attackerexample.com" through. A wildcard for
//     subdomains of a domain the operator names ("[a-z]+\.example\.com")
//     still passes, since every host it matches is under that domain.
//
// A URI with no authority (a private-use scheme) is pinned by its scheme.
func hostPinned(pattern *regexp.Regexp, uri string, u *url.URL) bool {
	auth, ok := authority(uri)
	if !ok {
		return true
	}
	host := u.Hostname()
	if strings.HasPrefix(host, "[") || strings.Contains(host, ":") {
		// An IP literal (only loopback reaches here over http) has no
		// labels to extend; the replacement probe still applies.
		return !matchesWithHost(pattern, uri, auth, u.Port(), hostCanary)
	}
	probes := []string{hostCanary, host + "." + hostCanary, host + "x"}
	if reg, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		probes = append(probes, strings.TrimSuffix(host, reg)+"x"+reg)
	}
	for _, probe := range probes {
		if matchesWithHost(pattern, uri, auth, u.Port(), probe) {
			return false
		}
	}
	return true
}

// matchesWithHost reports whether pattern matches uri with its authority
// (auth) replaced by host and the original port.
func matchesWithHost(pattern *regexp.Regexp, uri, auth, port, host string) bool {
	if port != "" {
		host += ":" + port
	}
	start := strings.Index(uri, "://") + len("://")
	return pattern.MatchString(uri[:start] + host + uri[start+len(auth):])
}

// generateSecureToken generates a cryptographically secure token.
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// generateID generates a unique ID.
func generateID() string {
	bytes := make([]byte, idByteLength)
	_, _ = rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes)
}
