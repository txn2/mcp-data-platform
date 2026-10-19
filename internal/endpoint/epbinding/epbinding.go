// Package epbinding is an endpoint's binding (#2171): the HTTP route a
// managed script answers at /endpoints/{name}/..., who may call it, and the
// limits each request runs under. It holds the record, its validation, the
// store contract and the matching of a request path against the binding's
// template; the store is internal/endpoint/epstore and the request surface
// internal/endpoint/ephttp.
package epbinding

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Auth modes. A public endpoint answers anyone who passes the deployment's
// bot check; a signed-in one answers a browser session or an API key.
const (
	AuthPublic   = "public"
	AuthSignedIn = "signed_in"
)

// PathPrefix is where every endpoint is served: /endpoints/{name}/...
const PathPrefix = "/endpoints/"

// pathSep separates a path's segments.
const pathSep = "/"

// Limits a binding is held to, and the values one that names none gets.
const (
	DefaultTimeoutSeconds = 10
	MaxTimeoutSeconds     = 60

	DefaultMaxMemoryBytes int64 = 64 << 20
	MinMaxMemoryBytes     int64 = 1 << 20
	MaxMaxMemoryBytes     int64 = 1 << 30

	DefaultMaxBodyBytes = 1 << 20
	MaxMaxBodyBytes     = 10 << 20

	DefaultRateLimit       = 600
	DefaultCallerRateLimit = 120
	MaxRateLimit           = 100000

	maxNameLen       = 63
	maxPathSegments  = 8
	maxOrigins       = 20
	maxOriginLen     = 253
	maxSegmentLength = 64
)

// Methods is every HTTP method a binding may answer.
var Methods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}

var (
	namePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	literalSegment = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
	paramSegment   = regexp.MustCompile(`^\{([a-z][a-z0-9_]*)\}$`)
)

// Errors a binding is refused or looked up with.
var (
	// ErrNotFound reports a script with no binding, or a name no binding has.
	ErrNotFound = errors.New("this script is not bound to an endpoint")
	// ErrNameTaken reports a name another script's binding already has.
	ErrNameTaken = errors.New("another script's endpoint has this name")
	// ErrNotAllowed reports a caller who may not create or change a binding:
	// everyone but an administrator, the script's owner included.
	ErrNotAllowed = errors.New("only an administrator can bind a script to an endpoint")
)

// Binding is one script's endpoint.
type Binding struct {
	ID       string `json:"id"`
	ScriptID string `json:"script_id"`
	// Name is the endpoint's segment of /endpoints/{name}, unique across the
	// deployment.
	Name string `json:"name" example:"order-lookup"`
	// Methods are the HTTP methods it answers.
	Methods []string `json:"methods" example:"GET"`
	// Path is the template under /endpoints/{name}: "" for the name alone,
	// "/{id}" for one parameter a request reads as req.params["id"].
	Path string `json:"path" example:"/{id}"`
	// AuthMode is public or signed_in.
	AuthMode string `json:"auth_mode" example:"signed_in"`
	// OpenReads lets GET and HEAD on a public endpoint through without a bot
	// check.
	OpenReads bool `json:"open_reads"`
	// TimeoutSeconds, MaxMemoryBytes and MaxBodyBytes bound one request.
	TimeoutSeconds int   `json:"timeout_seconds"`
	MaxMemoryBytes int64 `json:"max_memory_bytes"`
	MaxBodyBytes   int   `json:"max_body_bytes"`
	// AllowedOrigins are the browser origins a cross-origin request may come
	// from; empty answers no preflight.
	AllowedOrigins []string `json:"allowed_origins"`
	// RateLimit is the endpoint's requests per minute on each replica, and
	// CallerRateLimit one signed-in caller's.
	RateLimit       int       `json:"rate_limit"`
	CallerRateLimit int       `json:"caller_rate_limit"`
	Enabled         bool      `json:"enabled"`
	CreatedBy       string    `json:"created_by,omitempty"`
	UpdatedBy       string    `json:"updated_by,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// URLPath is the path a request reaches the endpoint at, with the template's
// parameters in braces.
func (b Binding) URLPath() string { return PathPrefix + b.Name + b.Path }

// Answers reports whether the binding answers method. HEAD is answered
// wherever GET is.
func (b Binding) Answers(method string) bool {
	return slices.Contains(b.Methods, method) || (method == "HEAD" && slices.Contains(b.Methods, "GET"))
}

// Request is a binding set on a script. A nil field keeps what the binding
// had, or takes the default on a new one.
type Request struct {
	Name            string
	Methods         []string
	Path            *string
	AuthMode        string
	OpenReads       *bool
	TimeoutSeconds  int
	MaxMemoryBytes  int64
	MaxBodyBytes    int
	AllowedOrigins  []string
	RateLimit       int
	CallerRateLimit int
	Enabled         *bool
	Actor           string
	// PublicAllowed is whether the deployment lets a public endpoint be
	// bound: it has a bot check, or an administrator set the override.
	PublicAllowed bool
}

// Build is the binding req sets on sc over prev, which is nil for a script
// not bound yet. It refuses a library, which nothing calls, and everything
// Validate refuses.
func Build(sc *script.Script, prev *Binding, req Request, now time.Time) (*Binding, error) {
	if sc.Library {
		return nil, errors.New("a library is loaded by other scripts and is never called, so it cannot be bound to an endpoint")
	}
	b := Binding{
		ScriptID: sc.ID, Methods: []string{"GET"}, TimeoutSeconds: DefaultTimeoutSeconds,
		MaxMemoryBytes: DefaultMaxMemoryBytes, MaxBodyBytes: DefaultMaxBodyBytes, AllowedOrigins: []string{},
		RateLimit: DefaultRateLimit, CallerRateLimit: DefaultCallerRateLimit, Enabled: true,
		CreatedBy: req.Actor, CreatedAt: now,
	}
	if prev != nil {
		b = *prev
	}
	b.UpdatedBy, b.UpdatedAt = req.Actor, now
	apply(&b, req)
	if err := b.Validate(); err != nil {
		return nil, err
	}
	if b.AuthMode == AuthPublic && !req.PublicAllowed {
		return nil, errors.New("a public endpoint needs a bot check, and this deployment has none: an administrator " +
			"chooses one, or allows public endpoints without one, under Admin > Settings > Bot check")
	}
	return &b, nil
}

// apply sets on b what req names.
func apply(b *Binding, req Request) {
	setString(&b.Name, strings.TrimSpace(req.Name))
	setString(&b.AuthMode, req.AuthMode)
	if req.Methods != nil {
		b.Methods = normalizeMethods(req.Methods)
	}
	if req.Path != nil {
		b.Path = strings.TrimSuffix(strings.TrimSpace(*req.Path), pathSep)
	}
	if req.AllowedOrigins != nil {
		b.AllowedOrigins = req.AllowedOrigins
	}
	setBool(&b.OpenReads, req.OpenReads)
	setBool(&b.Enabled, req.Enabled)
	setInt(&b.TimeoutSeconds, req.TimeoutSeconds)
	setInt(&b.MaxBodyBytes, req.MaxBodyBytes)
	setInt(&b.RateLimit, req.RateLimit)
	setInt(&b.CallerRateLimit, req.CallerRateLimit)
	if req.MaxMemoryBytes != 0 {
		b.MaxMemoryBytes = req.MaxMemoryBytes
	}
}

func setString(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func setBool(dst, v *bool) {
	if v != nil {
		*dst = *v
	}
}

func setInt(dst *int, v int) {
	if v != 0 {
		*dst = v
	}
}

// normalizeMethods upper-cases and de-duplicates methods, in order.
func normalizeMethods(in []string) []string {
	out := make([]string, 0, len(in))
	for _, m := range in {
		m = strings.ToUpper(strings.TrimSpace(m))
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// Validate reports the first thing wrong with the binding.
func (b Binding) Validate() error {
	for _, check := range []func() error{b.validateName, b.validateMethods, b.validatePath, b.validateAuth, b.validateLimits, b.validateOrigins} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func (b Binding) validateName() error {
	if b.Name == "" {
		return errors.New("an endpoint needs a name: the segment of /endpoints/{name} it answers at")
	}
	if len(b.Name) > maxNameLen || !namePattern.MatchString(b.Name) {
		return fmt.Errorf("endpoint name %q must be at most %d lowercase letters, digits, hyphens and underscores, "+
			"starting with a letter or digit", b.Name, maxNameLen)
	}
	return nil
}

func (b Binding) validateMethods() error {
	if len(b.Methods) == 0 {
		return errors.New("an endpoint answers at least one method")
	}
	for _, m := range b.Methods {
		if !slices.Contains(Methods, m) {
			return fmt.Errorf("method %q is not one an endpoint answers (%s)", m, strings.Join(Methods, ", "))
		}
	}
	return nil
}

func (b Binding) validatePath() error {
	if b.Path == "" {
		return nil
	}
	if !strings.HasPrefix(b.Path, pathSep) {
		return fmt.Errorf("path %q must start with /", b.Path)
	}
	segs := strings.Split(b.Path[1:], pathSep)
	if len(segs) > maxPathSegments {
		return fmt.Errorf("path %q has more than %d segments", b.Path, maxPathSegments)
	}
	seen := map[string]bool{}
	for _, seg := range segs {
		if len(seg) > maxSegmentLength {
			return fmt.Errorf("path segment %q is longer than %d characters", seg, maxSegmentLength)
		}
		if m := paramSegment.FindStringSubmatch(seg); m != nil {
			if seen[m[1]] {
				return fmt.Errorf("path %q names the parameter %q twice", b.Path, m[1])
			}
			seen[m[1]] = true
			continue
		}
		if !literalSegment.MatchString(seg) {
			return fmt.Errorf("path segment %q must be letters, digits and ._~- or a whole {parameter}", seg)
		}
	}
	return nil
}

func (b Binding) validateAuth() error {
	switch b.AuthMode {
	case AuthPublic, AuthSignedIn:
		return nil
	case "":
		return fmt.Errorf("an endpoint needs an auth mode: %s or %s", AuthPublic, AuthSignedIn)
	}
	return fmt.Errorf("auth mode %q is not %s or %s", b.AuthMode, AuthPublic, AuthSignedIn)
}

func (b Binding) validateLimits() error {
	switch {
	case outside(int64(b.TimeoutSeconds), 1, MaxTimeoutSeconds):
		return fmt.Errorf("timeout_seconds must be from 1 to %d", MaxTimeoutSeconds)
	case outside(b.MaxMemoryBytes, MinMaxMemoryBytes, MaxMaxMemoryBytes):
		return fmt.Errorf("max_memory_bytes must be from %d to %d", MinMaxMemoryBytes, MaxMaxMemoryBytes)
	case outside(int64(b.MaxBodyBytes), 1, MaxMaxBodyBytes):
		return fmt.Errorf("max_body_bytes must be from 1 to %d", MaxMaxBodyBytes)
	case outside(int64(b.RateLimit), 1, MaxRateLimit):
		return fmt.Errorf("rate_limit must be from 1 to %d requests a minute", MaxRateLimit)
	case outside(int64(b.CallerRateLimit), 1, int64(b.RateLimit)):
		return fmt.Errorf("caller_rate_limit must be from 1 to the endpoint's rate_limit (%d)", b.RateLimit)
	}
	return nil
}

// outside reports whether v is not from lo to hi.
func outside(v, lo, hi int64) bool { return v < lo || v > hi }

func (b Binding) validateOrigins() error {
	if len(b.AllowedOrigins) > maxOrigins {
		return fmt.Errorf("an endpoint lists at most %d allowed origins", maxOrigins)
	}
	for _, o := range b.AllowedOrigins {
		if err := validateOrigin(o); err != nil {
			return err
		}
	}
	return nil
}

// validateOrigin accepts a browser origin: scheme, host and an optional port,
// nothing else.
func validateOrigin(o string) error {
	u, err := url.Parse(o)
	if err != nil || len(o) > maxOriginLen || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("allowed origin %q must be a scheme and host such as https://forms.example.com", o)
	}
	return nil
}

// Match reports whether rest, the part of a request path after
// /endpoints/{name}, is the binding's template, and the parameter values it
// carries.
func (b Binding) Match(rest string) (map[string]string, bool) {
	rest = strings.TrimSuffix(rest, pathSep)
	params := map[string]string{}
	if b.Path == "" || rest == "" {
		return params, b.Path == "" && rest == ""
	}
	want, got := strings.Split(b.Path[1:], pathSep), strings.Split(strings.TrimPrefix(rest, pathSep), pathSep)
	if len(want) != len(got) || !strings.HasPrefix(rest, pathSep) {
		return nil, false
	}
	for i, seg := range want {
		if !matchSegment(seg, got[i], params) {
			return nil, false
		}
	}
	return params, true
}

// matchSegment reports whether one request path segment is the template's
// seg, recording a parameter's value in params.
func matchSegment(seg, got string, params map[string]string) bool {
	value, err := url.PathUnescape(got)
	if err != nil || value == "" {
		return false
	}
	if m := paramSegment.FindStringSubmatch(seg); m != nil {
		params[m[1]] = value
		return true
	}
	return value == seg
}

// Callable refuses a source an endpoint cannot call: one that defines no
// top-level handle(req) taking one parameter. It is asked when a script is
// bound and when a bound script is saved (internal/platform/scriptsave).
func Callable(source string) error {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	if err != nil {
		return fmt.Errorf("the source does not parse: %w", err)
	}
	if scriptdialect.HandlerOf(file) == nil {
		return fmt.Errorf("an endpoint calls %s(req) with each request, and the source defines no %s(req) taking "+
			"one parameter", scriptdialect.HandlerName, scriptdialect.HandlerName)
	}
	return nil
}
