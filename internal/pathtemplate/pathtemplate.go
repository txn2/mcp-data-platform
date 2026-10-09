// Package pathtemplate matches a concrete request path against an OpenAPI
// path template: the one rule the api gateway's router, its WebDAV route
// matcher and its specificity ranking share, so a placeholder means the same
// thing to each (issues #876, #1297). It knows nothing about connections or
// specs; it compares strings.
package pathtemplate

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// sep is the path separator.
const sep = "/"

// Match reports whether concrete (e.g. "/v1/users/42")
// matches an OpenAPI path template (e.g. "/v1/users/{id}"). Both
// strings are split on "/" and compared segment-by-segment; bracketed
// placeholder segments match any non-empty segment, literal segments
// must match exactly. Trailing slashes are normalized away so
// "/v1/users/" and "/v1/users" both match the same template.
func Match(concrete, template string) bool {
	cs := Split(concrete)
	ts := Split(template)
	if len(cs) != len(ts) {
		return false
	}
	for i, seg := range ts {
		if !SegmentMatches(cs[i], seg) {
			return false
		}
	}
	return true
}

// Split splits a leading-slash path or template into segments
// after trimming a single trailing slash, so "/a/b/" and "/a/b" both
// yield ["", "a", "b"] and align with template segment counts. Shared by
// Match and the WebDAV route matcher so both derive
// segments identically (issue #876).
func Split(p string) []string {
	return strings.Split(strings.TrimSuffix(p, sep), sep)
}

// Placeholder matches one OpenAPI path-template placeholder.
// The name class excludes braces so a segment carrying two placeholders
// ("{latitude},{longitude}") yields two matches rather than one spanning
// both, which is what made such a segment resolve to a parameter named
// "latitude},{longitude" that no caller could supply (issue #1297).
//
//nolint:gochecknoglobals // compiled once; matched on every path resolve
var Placeholder = regexp.MustCompile(`\{([^{}]+)\}`)

// SegmentMatches reports whether one concrete path segment satisfies one
// template segment. Three cases, cheapest first: a literal ("users") must
// match exactly; a whole-segment placeholder ("{id}") matches any
// non-empty segment; a partially-templated segment ("{lat},{lon}",
// "{name}.json") matches when the literal text around its placeholders
// lines up. The single per-segment rule shared by the exact-length
// matcher (Match) and the catch-all-tail matcher
// (webdavRoute.matches) so their placeholder/literal semantics cannot
// drift (issues #876, #1297).
func SegmentMatches(concrete, template string) bool {
	if !strings.Contains(template, "{") {
		return concrete == template
	}
	if IsPlaceholderSegment(template) {
		return concrete != ""
	}
	return templatedSegmentMatches(concrete, template)
}

// templatedSegmentMatches matches a segment that carries at least one
// placeholder alongside literal text. Splitting the template on its
// placeholders yields the literal runs between them — "{lat},{lon}"
// yields ["", ",", ""] — and the concrete segment must start with the
// first run, end with the last, and contain the interior runs in order,
// with every placeholder consuming at least one character. Each
// placeholder takes the shortest run that still lets the next literal
// match, mirroring leftmost router matching. A template whose braces
// form no well-formed placeholder ("{", "{}") is compared literally.
func templatedSegmentMatches(concrete, template string) bool {
	lits := Placeholder.Split(template, -1)
	if len(lits) < 2 {
		return concrete == template
	}
	if !strings.HasPrefix(concrete, lits[0]) {
		return false
	}
	rest := concrete[len(lits[0]):]
	for _, lit := range lits[1 : len(lits)-1] {
		if rest == "" {
			return false
		}
		// rest[1:] skips the one character the preceding placeholder is
		// required to consume, so an empty capture cannot satisfy it.
		idx := strings.Index(rest[1:], lit)
		if idx < 0 {
			return false
		}
		rest = rest[1+idx+len(lit):]
	}
	tail := lits[len(lits)-1]
	return len(rest) > len(tail) && strings.HasSuffix(rest, tail)
}

// IsPlaceholderSegment reports whether a path-template segment is
// entirely one OpenAPI parameter placeholder (e.g. "{datasetId}"). A
// segment that merely contains a placeholder ("{name}.json") is not one:
// it has literal text that must still match, so it routes through
// templatedSegmentMatches instead. The interior brace check and the
// three-character minimum reject the degenerate "{a}{b}" and "{}"
// segments, which no spec generator emits but a hand-edited spec might.
func IsPlaceholderSegment(seg string) bool {
	return len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}' &&
		!strings.ContainsAny(seg[1:len(seg)-1], "{}")
}

// IsTemplated reports whether a path-template segment carries at
// least one placeholder, whether or not it also carries literal text.
// Distinct from IsPlaceholderSegment: this is the "not a fixed segment"
// test specificity ranking needs, where "{lat},{lon}" must count as
// templated even though it is not a whole-segment placeholder.
func IsTemplated(seg string) bool {
	return Placeholder.MatchString(seg)
}

// CountPlaceholders returns the number of placeholders in an
// OpenAPI path template; used by findMostSpecificPathMatch to prefer
// literal paths over templated ones when both match the same concrete
// path. Occurrences are counted rather than segments, so a template with
// a two-placeholder segment ranks as less specific than one that spends
// a whole segment per placeholder (issue #1297).
func CountPlaceholders(template string) int {
	return len(Placeholder.FindAllStringIndex(template, -1))
}

// ValidateCallerPath refuses a path a caller supplies for the api gateway to
// append to a connection's base URL, when its shape would let it reach another
// host or match a route rule the upstream would not see it match. The messages
// carry the gateway's prefix: they are the gateway's refusals.
func ValidateCallerPath(p string) error {
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
// segments. See ValidateCallerPath for the security rationale.
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
