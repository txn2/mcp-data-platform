// Package sessionlogin is the session sign-in an HTTP-based connection kind
// authenticates with under auth_mode session_login (#2015): the keys it is
// configured by, their validation, and the transport that signs in, carries
// the session on every request, signs in again when the upstream rejects it,
// and signs out when the connection is released. internal/upstreamauth wires
// it into the connection's client; it is its own package because the shared
// policy reached its size budget.
package sessionlogin

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// AuthMode signs in to the upstream with a stored credential and
// carries the session token the sign-in returns on every call, signing in
// again when the upstream rejects it (#2015). It is the scheme of Tableau
// (personal access token to /auth/signin, X-Tableau-Auth on every call),
// MicroStrategy (X-MSTR-AuthToken) and Veeva Vault (sessionId as
// Authorization): a login endpoint that takes a vendor-shaped body and
// answers with a token at a vendor-chosen place, which no OAuth grant
// describes.
const AuthMode = "session_login"

// The keys a session_login connection is configured with.
const (
	cfgKeySessionLoginURL         = "session_login_url"
	cfgKeySessionLoginMethod      = "session_login_method"
	cfgKeySessionLoginBody        = "session_login_body"
	cfgKeySessionLoginContentType = "session_login_content_type"
	cfgKeySessionLoginSecret      = "session_login_secret" // #nosec G101 -- map key, not a credential
	cfgKeySessionLoginHeaders     = "session_login_headers"
	cfgKeySessionTokenSource      = "session_token_source" // #nosec G101 -- map key, not a credential
	cfgKeySessionTokenHeader      = "session_token_header" // #nosec G101 -- map key, not a credential
	cfgKeySessionTokenPrefix      = "session_token_prefix" // #nosec G101 -- map key, not a credential
	cfgKeySessionTTL              = "session_ttl"
	cfgKeySessionLogoutURL        = "session_logout_url"
	cfgKeySessionLogoutMethod     = "session_logout_method"
	cfgKeySessionCapture          = "session_capture"
	cfgKeySessionExpiredStatuses  = "session_expired_statuses"
	cfgKeySessionExpiredMarker    = "session_expired_marker"
)

// The keys whose values may name a stored secret as {{secret:<name>}}
// (#2066), which the sign-in fills as it is sent.
const (
	ConfigKeyLoginBody    = cfgKeySessionLoginBody
	ConfigKeyLoginSecret  = cfgKeySessionLoginSecret
	ConfigKeyLoginHeaders = cfgKeySessionLoginHeaders
)

// SessionSecretPlaceholder is where session_login_secret is written into the
// sign-in body. The body itself is not secret, so an operator can read it
// back; the secret is stored apart from it and encrypted.
const SessionSecretPlaceholder = "{{secret}}"

// The places a token or a captured value is read from in the sign-in
// response: a dotted path into its JSON body, or a response header.
const (
	sessionSourceBody   = "body"
	sessionSourceHeader = "header"
)

// maxErrorStatus is the highest status an expired session can be answered with.
const maxErrorStatus = 599

// defaultSessionContentType is what the sign-in body is sent as when the
// connection names nothing else.
const defaultSessionContentType = "application/json"

// defaultSessionTokenPrefix is written before the token when the connection
// names neither a token header nor a prefix: Authorization: Bearer <token>.
const defaultSessionTokenPrefix = "Bearer "

// sessionValuePattern is how a request names a value the sign-in captured:
// {session.<name>} in its path, as written or percent-encoded.
var sessionValuePattern = regexp.MustCompile(`(?i)(?:\{|%7B)session\.([a-z0-9_]+)(?:\}|%7D)`)

// sessionValueName is what a captured value may be called.
var sessionValueName = regexp.MustCompile(`^[a-z0-9_]+$`)

// Config is how a session_login connection signs in and carries
// its session.
type Config struct {
	// LoginURL is the sign-in endpoint, resolved against the connection's
	// base URL when it was written as a path.
	LoginURL string
	// LoginMethod is the sign-in request's method. Defaults to POST.
	LoginMethod string
	// LoginBody is the sign-in request's body, with
	// SessionSecretPlaceholder where the secret goes.
	LoginBody string
	// LoginContentType is the sign-in body's media type. Defaults to
	// application/json. The secret is escaped for it: JSON string, form
	// value, XML text.
	LoginContentType string
	// Secret replaces SessionSecretPlaceholder in the body. Encrypted at
	// rest.
	Secret string
	// LoginHeaders are sent on the sign-in request only.
	LoginHeaders map[string]string
	// TokenSource is where the token is in the sign-in response:
	// body:<dotted json path> or header:<name>.
	TokenSource string
	// TokenHeader is the header the token is sent in on every call.
	TokenHeader string
	// TokenPrefix is written before the token in TokenHeader.
	TokenPrefix string
	// TTL, when positive, is how long a session is used before the
	// platform signs in again without waiting to be rejected.
	TTL time.Duration
	// LogoutURL, when set, ends the session when the connection is
	// removed, replaced or shut down.
	LogoutURL string
	// LogoutMethod is the sign-out request's method. Defaults to POST.
	LogoutMethod string
	// Capture names further values the sign-in response carries, each read
	// from a source in TokenSource's form. A request writes
	// {session.<name>} in its path to have the value put there.
	Capture map[string]string
	// ExpiredStatuses are the statuses that mean the session is no longer
	// accepted. Defaults to 401.
	ExpiredStatuses []int
	// ExpiredMarker, when set, is text whose presence in a response body
	// means the session is no longer accepted, for an upstream that
	// answers an expired session with 200 or 403 and an error body.
	ExpiredMarker string
	// Connection is the connection's name, which a stored secret the
	// sign-in names must be allowed on. Set by the transport's builder.
	Connection string
}

// Parse reads a session_login connection's settings. endpointURL
// is the connection's base URL, which a sign-in or sign-out path resolves
// against.
func Parse(endpointURL string, cfg map[string]any) Config {
	s := Config{
		LoginURL:         resolveAgainst(endpointURL, cfgmap.String(cfg, cfgKeySessionLoginURL)),
		LoginMethod:      strings.ToUpper(cfgmap.StringDefault(cfg, cfgKeySessionLoginMethod, http.MethodPost)),
		LoginBody:        cfgmap.String(cfg, cfgKeySessionLoginBody),
		LoginContentType: cfgmap.StringDefault(cfg, cfgKeySessionLoginContentType, defaultSessionContentType),
		Secret:           cfgmap.String(cfg, cfgKeySessionLoginSecret),
		LoginHeaders:     cfgmap.StringMap(cfg, cfgKeySessionLoginHeaders),
		TokenSource:      strings.TrimSpace(cfgmap.String(cfg, cfgKeySessionTokenSource)),
		TokenHeader:      cfgmap.String(cfg, cfgKeySessionTokenHeader),
		TokenPrefix:      cfgmap.String(cfg, cfgKeySessionTokenPrefix),
		TTL:              cfgmap.Duration(cfg, cfgKeySessionTTL, 0),
		LogoutURL:        resolveAgainst(endpointURL, cfgmap.String(cfg, cfgKeySessionLogoutURL)),
		LogoutMethod:     strings.ToUpper(cfgmap.StringDefault(cfg, cfgKeySessionLogoutMethod, http.MethodPost)),
		Capture:          cfgmap.StringMap(cfg, cfgKeySessionCapture),
		ExpiredStatuses:  intList(cfg[cfgKeySessionExpiredStatuses]),
		ExpiredMarker:    cfgmap.String(cfg, cfgKeySessionExpiredMarker),
	}
	// Both blank is the conventional bearer header. A connection that names
	// Authorization itself and no prefix gets the raw token, which is how
	// Veeva Vault reads its session id.
	if s.TokenHeader == "" && s.TokenPrefix == "" {
		s.TokenPrefix = defaultSessionTokenPrefix
	}
	if s.TokenHeader == "" {
		s.TokenHeader = authorizationHeader
	}
	if len(s.ExpiredStatuses) == 0 {
		s.ExpiredStatuses = []int{http.StatusUnauthorized}
	}
	return s
}

// resolveAgainst resolves ref against base when ref is a path, and returns
// ref unchanged when it is absolute or either does not parse; validation
// reports a URL that is not usable.
func resolveAgainst(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || base == "" {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil || r.IsAbs() {
		return ref
	}
	b, err := url.Parse(base)
	if err != nil || !b.IsAbs() {
		return ref
	}
	if !strings.HasPrefix(ref, "/") {
		// A relative path is relative to the base as a directory, so
		// "auth/signin" under https://host/api/3.22 is
		// https://host/api/3.22/auth/signin rather than a sibling of 3.22.
		b.Path = strings.TrimSuffix(b.Path, "/") + "/"
	}
	return b.ResolveReference(r).String()
}

// intList reads a list of integers from the forms a JSON or YAML round trip
// leaves one in: numbers, numeric strings, or one comma-separated string.
// Anything else is skipped.
func intList(raw any) []int {
	var items []any
	switch v := raw.(type) {
	case []any:
		items = v
	case []int:
		return append([]int(nil), v...)
	case string:
		for part := range strings.SplitSeq(v, ",") {
			items = append(items, strings.TrimSpace(part))
		}
	default:
		return nil
	}
	out := make([]int, 0, len(items))
	for _, item := range items {
		if n, ok := toInt(item); ok {
			out = append(out, n)
		}
	}
	return out
}

// toInt reads one integer out of a decoded value.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), n == float64(int(n))
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	}
	return 0, false
}

// validator checks a Config, speaking in the calling kind's voice.
type validator struct {
	s      Config
	prefix string
}

// errf builds an error in the calling kind's voice.
func (v validator) errf(format string, a ...any) error {
	return fmt.Errorf(v.prefix+": "+format, a...)
}

// Validate refuses a session_login connection that could not sign in, or could
// not find what it signed in for. Each refusal names the key to fix, after
// prefix, the calling kind's name.
func (s Config) Validate(prefix string) error {
	v := validator{s: s, prefix: prefix}
	if err := v.validateSessionURL(cfgKeySessionLoginURL, s.LoginURL, true); err != nil {
		return err
	}
	if err := v.validateSessionURL(cfgKeySessionLogoutURL, s.LogoutURL, false); err != nil {
		return err
	}
	if err := v.validateSessionMethods(); err != nil {
		return err
	}
	if err := v.validateSessionBody(); err != nil {
		return err
	}
	if err := v.validateSessionHeaders(); err != nil {
		return err
	}
	if err := v.validateSessionSources(); err != nil {
		return err
	}
	return v.validateSessionExpiry()
}

// validateSessionURL refuses a sign-in or sign-out URL that is not an
// absolute http(s) address once resolved against the base URL.
func (v validator) validateSessionURL(key, raw string, required bool) error {
	if raw == "" {
		if required {
			return v.errf("%s is required when auth_mode is %q", key, AuthMode)
		}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return v.errf("%s must be an http(s) URL, or a path under the connection's base URL", key)
	}
	return nil
}

// validateSessionMethods refuses a sign-in or sign-out method that is not one
// a login endpoint answers.
func (v validator) validateSessionMethods() error {
	for key, method := range map[string]string{
		cfgKeySessionLoginMethod:  v.s.LoginMethod,
		cfgKeySessionLogoutMethod: v.s.LogoutMethod,
	} {
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return v.errf("invalid %s %q (want GET, POST, PUT, PATCH or DELETE)", key, method)
		}
	}
	return nil
}

// validateSessionBody refuses a sign-in that carries no credential, a secret
// the body has nowhere to put, a placeholder with no secret behind it, and a
// JSON body that does not parse once the secret is in it.
func (v validator) validateSessionBody() error {
	s := v.s
	hasPlaceholder := strings.Contains(s.LoginBody, SessionSecretPlaceholder)
	switch {
	case s.LoginBody == "" && s.Secret == "":
		return v.errf("%s or %s is required when auth_mode is %q",
			cfgKeySessionLoginBody, cfgKeySessionLoginSecret, AuthMode)
	case secretref.Malformed(s.LoginBody):
		return v.errf("%s names a stored secret in a form that is not {{secret:<name>}}, where a name is lower case letters, digits, '.', '_' and '-'",
			cfgKeySessionLoginBody)
	case s.Secret != "" && !hasPlaceholder:
		return v.errf("%s is set but %s does not contain %s, where it is written; a body that names a stored secret as {{secret:<name>}} needs no %s, so clear it",
			cfgKeySessionLoginSecret, cfgKeySessionLoginBody, SessionSecretPlaceholder, cfgKeySessionLoginSecret)
	case hasPlaceholder && s.Secret == "":
		return v.errf("%s contains %s but %s is empty",
			cfgKeySessionLoginBody, SessionSecretPlaceholder, cfgKeySessionLoginSecret)
	}
	if isJSONType(s.LoginContentType) && !json.Valid([]byte(s.renderBody())) {
		return v.errf("%s is not valid JSON once %s is written into it", cfgKeySessionLoginBody, SessionSecretPlaceholder)
	}
	return nil
}

// validateSessionHeaders refuses a token header or a sign-in header a request
// could not carry, or a prefix that would split the header.
func (v validator) validateSessionHeaders() error {
	s := v.s
	if !validHeaderName(s.TokenHeader) || reservedHopHeader(s.TokenHeader) {
		return v.errf("%s %q is not a header a request can carry", cfgKeySessionTokenHeader, s.TokenHeader)
	}
	if strings.ContainsAny(s.TokenPrefix, "\r\n\x00") {
		return v.errf("%s contains CR/LF/NUL", cfgKeySessionTokenPrefix)
	}
	for name, value := range s.LoginHeaders {
		if !validHeaderName(name) || reservedHopHeader(name) {
			return v.errf("%s name %q is not a header a request can carry", cfgKeySessionLoginHeaders, name)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return v.errf("%s[%q] contains CR/LF/NUL", cfgKeySessionLoginHeaders, name)
		}
	}
	return nil
}

// validateSessionSources refuses a token source or a captured value the
// sign-in response could not be read for.
func (v validator) validateSessionSources() error {
	s := v.s
	if s.TokenSource == "" {
		return v.errf("%s is required when auth_mode is %q (body:<json path> or header:<name>)",
			cfgKeySessionTokenSource, AuthMode)
	}
	if _, _, err := parseSessionSource(s.TokenSource); err != nil {
		return v.errf("invalid %s %q: %v", cfgKeySessionTokenSource, s.TokenSource, err)
	}
	for name, source := range s.Capture {
		if !sessionValueName.MatchString(name) {
			return v.errf("%s name %q must be lower-case letters, digits and underscores", cfgKeySessionCapture, name)
		}
		if _, _, err := parseSessionSource(source); err != nil {
			return v.errf("invalid %s[%q] %q: %v", cfgKeySessionCapture, name, source, err)
		}
	}
	return nil
}

// validateSessionExpiry refuses a lifetime below zero and an expiry status
// that is not an error status.
func (v validator) validateSessionExpiry() error {
	if v.s.TTL < 0 {
		return v.errf("%s must not be negative", cfgKeySessionTTL)
	}
	for _, status := range v.s.ExpiredStatuses {
		if status < http.StatusBadRequest || status > maxErrorStatus {
			return v.errf("%s contains %d; an expired session is answered with a 4xx or 5xx status",
				cfgKeySessionExpiredStatuses, status)
		}
	}
	return nil
}

// parseSessionSource splits a source into where it is read from and what is
// read there.
func parseSessionSource(source string) (where, what string, err error) {
	where, what, ok := strings.Cut(source, ":")
	what = strings.TrimSpace(what)
	if !ok || what == "" {
		return "", "", errors.New("want body:<json path> or header:<name>")
	}
	switch strings.ToLower(strings.TrimSpace(where)) {
	case sessionSourceBody:
		if strings.Contains(what, "..") || strings.HasPrefix(what, ".") || strings.HasSuffix(what, ".") {
			return "", "", fmt.Errorf("the json path %q has an empty segment", what)
		}
		return sessionSourceBody, what, nil
	case sessionSourceHeader:
		if !validHeaderName(what) {
			return "", "", fmt.Errorf("header name %q is not valid", what)
		}
		return sessionSourceHeader, what, nil
	default:
		return "", "", errors.New("want body:<json path> or header:<name>")
	}
}

// renderBody is the sign-in body with the secret written in, escaped for the
// body's media type so a secret holding a quote or an ampersand does not
// change the body's shape.
func (s Config) renderBody() string {
	return s.renderBodyWith(s.LoginBody, s.Secret)
}

// renderBodyWith is body with secret written in at SessionSecretPlaceholder.
func (s Config) renderBodyWith(body, secret string) string {
	if !strings.Contains(body, SessionSecretPlaceholder) {
		return body
	}
	return strings.ReplaceAll(body, SessionSecretPlaceholder, escapeFor(s.LoginContentType, secret))
}

// signInBody is the body a sign-in sends: every stored secret the body names
// as {{secret:<name>}} read through lookup and escaped for the body's media
// type (#2066), then session_login_secret, itself read through lookup when it
// names one, written in at SessionSecretPlaceholder. The body's own
// placeholders are filled first, so only text the operator wrote is read as
// a reference.
func (s Config) signInBody(lookup secretref.Lookup) (string, error) {
	body, err := secretref.Fill(s.LoginBody, lookup, func(v string) string { return escapeFor(s.LoginContentType, v) })
	if err != nil {
		return "", err //nolint:wrapcheck // the lookup's refusal, which names the secret and the connection
	}
	secret, err := secretref.Fill(s.Secret, lookup, secretref.Raw)
	if err != nil {
		return "", err //nolint:wrapcheck // as above
	}
	return s.renderBodyWith(body, secret), nil
}

// escapeFor escapes a value for the body of the given media type.
func escapeFor(contentType, value string) string {
	switch {
	case isJSONType(contentType):
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(value) // a string always encodes
		quoted := strings.TrimSuffix(b.String(), "\n")
		return quoted[1 : len(quoted)-1]
	case strings.Contains(strings.ToLower(contentType), "x-www-form-urlencoded"):
		return url.QueryEscape(value)
	case strings.Contains(strings.ToLower(contentType), "xml"):
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(value)) // a strings.Builder never fails
		return b.String()
	default:
		return value
	}
}

// isJSONType reports whether a media type is JSON or a +json suffix type.
func isJSONType(contentType string) bool {
	mt := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}
