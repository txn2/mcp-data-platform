// Package pathtemplate matches a concrete request path against an OpenAPI
// path template: the one rule the api gateway's router, its WebDAV route
// matcher and its specificity ranking share, so a placeholder means the same
// thing to each (issues #876, #1297). It knows nothing about connections or
// specs; it compares strings.
package pathtemplate

import (
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
