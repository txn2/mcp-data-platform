package epbinding

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// A catch-all segment (#2553): the last segment of a path template written
// {name...} matches the rest of a request's path at any depth, including none,
// so an application's own address can answer every path it does not reserve.
// The handler reads it as req["params"]["name"], with no leading slash.
var catchAllSegment = regexp.MustCompile(`^\{([a-z][a-z0-9_]*)\.{3}\}$`)

// What a catch-all hands a handler is bounded: its length, and its segments.
const (
	maxRouteBytes    = 2048
	maxRouteSegments = 64
)

// catchAll is the name of the catch-all a template's last segment declares,
// "" for none.
func catchAll(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	if m := catchAllSegment.FindStringSubmatch(segs[len(segs)-1]); m != nil {
		return m[1]
	}
	return ""
}

// validateCatchAll refuses a catch-all anywhere but the last segment.
func validateCatchAll(path string, segs []string) error {
	for i, seg := range segs {
		if catchAllSegment.MatchString(seg) && i != len(segs)-1 {
			return fmt.Errorf("path %q: only the last segment may be a catch-all {name...}", path)
		}
	}
	return nil
}

// Match reports whether rest, the part of a request path after
// /endpoints/{name} or the page origin's root, is the binding's template, and
// the parameter values it carries. rest is the decoded path, as the platform's
// router and the page origin's request both hold it: a segment is not decoded
// again, so %25 in an address reaches a handler as the % it stands for, and a
// doubly encoded dot never turns into "..".
//
// A segment is matched only when it is a name a handler can use: not empty,
// not "." or "..", and holding no backslash or control character. A catch-all
// takes the remaining segments under the same rule, joined with "/", so what
// it hands a handler is always a clean relative path.
func (b Binding) Match(rest string) (map[string]string, bool) {
	rest = strings.TrimSuffix(rest, pathSep)
	if rest != "" && !strings.HasPrefix(rest, pathSep) {
		return nil, false
	}
	want, got := splitPath(b.Path), splitPath(rest)
	name := catchAll(want)
	if name != "" {
		want = want[:len(want)-1]
	}
	params, ok := matchFixed(want, got, name != "")
	if !ok || name == "" {
		return params, ok
	}
	route, ok := routeOf(got[len(want):])
	if !ok {
		return nil, false
	}
	params[name] = route
	return params, true
}

// matchFixed matches the template's fixed segments want against the first of
// got, which must be all of got unless open (a catch-all takes the rest).
func matchFixed(want, got []string, open bool) (map[string]string, bool) {
	if len(got) < len(want) || (!open && len(got) != len(want)) {
		return nil, false
	}
	params := map[string]string{}
	for i, seg := range want {
		if !matchSegment(seg, got[i], params) {
			return nil, false
		}
	}
	return params, true
}

// splitPath is a path's segments, none for "".
func splitPath(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(p, pathSep), pathSep)
}

// matchSegment reports whether one request path segment is the template's
// seg, recording a parameter's value in params.
func matchSegment(seg, got string, params map[string]string) bool {
	if !usableSegment(got) {
		return false
	}
	if m := paramSegment.FindStringSubmatch(seg); m != nil {
		params[m[1]] = got
		return true
	}
	return got == seg
}

// routeOf is what a catch-all hands its handler for segs, and whether every
// segment is usable and the whole within bounds.
func routeOf(segs []string) (string, bool) {
	if len(segs) > maxRouteSegments {
		return "", false
	}
	for _, s := range segs {
		if !usableSegment(s) {
			return "", false
		}
	}
	route := strings.Join(segs, pathSep)
	return route, len(route) <= maxRouteBytes
}

// usableSegment reports whether a decoded path segment is one a handler can
// build a lookup from: not empty, not a relative step, no backslash and no
// control character.
func usableSegment(s string) bool {
	if s == "" || s == "." || s == ".." || strings.ContainsRune(s, '\\') {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}
