package site

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Limits on a release's _headers file (#2557).
const (
	maxHeaderRules  = 1000
	maxHeaderValue  = 4096
	maxHeadersPerID = 20
)

// HeaderRule is one block of a release's _headers file: a path pattern, as a
// redirect's from is written ("/*" ending matches everything beneath), and
// the headers every answer under it carries (#2557).
type HeaderRule struct {
	Path    string   `json:"path" example:"/*"`
	Headers []Header `json:"headers"`
}

// Header is one header a rule sets.
type Header struct {
	Name  string `json:"name" example:"Content-Security-Policy"`
	Value string `json:"value" example:"default-src 'self'"`
}

// The headers a release may set. Each is a policy the site's own pages are
// held to, and none of them can reach another origin, a cookie, a cache or
// the platform's own answers: Set-Cookie, Cache-Control, the CORS headers and
// everything else stay the platform's.
var allowedHeaders = []string{
	"Content-Security-Policy",
	"Content-Security-Policy-Report-Only",
	"Permissions-Policy",
	"Cross-Origin-Opener-Policy",
	"Cross-Origin-Embedder-Policy",
	"Cross-Origin-Resource-Policy",
	"X-Frame-Options",
}

// HeaderNames is the headers a release's _headers file may set, as a refusal
// lists them.
func HeaderNames() []string { return slices.Clone(allowedHeaders) }

// The names the framing default reads.
const (
	headerCSP          = "Content-Security-Policy"
	headerFrameOptions = "X-Frame-Options"
	frameAncestors     = "frame-ancestors"
)

// FramingDefault is the policy an answer carries when its release declares no
// framing rule of its own: only pages of the same origin may frame it.
const FramingDefault = "frame-ancestors 'self'"

// errHeadersFile prefixes every refusal of the file.
const errHeadersFile = "the _headers file, line %d: %w"

// ParseHeaders reads a release's _headers file: a path line starting with
// "/", then each header the path carries on an indented "Name: value" line,
// with blank lines and # comments skipped. A header outside the allowed set,
// a value holding a control character, or a header with no path above it
// refuses the file, naming the line.
func ParseHeaders(text string) ([]HeaderRule, error) {
	out := []HeaderRule{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		var err error
		if out, err = headerLine(out, line, trimmed); err != nil {
			return nil, fmt.Errorf(errHeadersFile, i+1, err)
		}
		if len(out) > maxHeaderRules {
			return nil, fmt.Errorf("the _headers file holds more than %d paths", maxHeaderRules)
		}
	}
	return out, nil
}

// headerLine reads one line of the file into the rules: a path line starts a
// block, any other line is a header of the block above it.
func headerLine(rules []HeaderRule, line, trimmed string) ([]HeaderRule, error) {
	if strings.HasPrefix(line, "/") {
		rule, err := headerPath(trimmed)
		if err != nil {
			return nil, err
		}
		return append(rules, rule), nil
	}
	if len(rules) == 0 {
		return nil, errors.New("a header comes before any path: start a block with a path such as /*")
	}
	h, err := parseHeader(trimmed)
	if err != nil {
		return nil, err
	}
	last := &rules[len(rules)-1]
	if len(last.Headers) >= maxHeadersPerID {
		return nil, fmt.Errorf("path %s sets more than %d headers", last.Path, maxHeadersPerID)
	}
	last.Headers = append(last.Headers, h)
	return rules, nil
}

// headerPath reads a path line.
func headerPath(line string) (HeaderRule, error) {
	if hasControl(line) {
		return HeaderRule{}, fmt.Errorf("path %q: one path, such as / or /docs/*, with no spaces", line)
	}
	if strings.Contains(strings.TrimSuffix(line, "/*"), "*") {
		return HeaderRule{}, fmt.Errorf("path %q: * is allowed only as the last part, as /docs/*", line)
	}
	return HeaderRule{Path: line, Headers: []Header{}}, nil
}

// parseHeader reads one "Name: value" line.
func parseHeader(line string) (Header, error) {
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return Header{}, errors.New(strconv.Quote(line) + " is not a header: write Name: value under a path")
	}
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	canonical := http.CanonicalHeaderKey(name)
	if !slices.Contains(allowedHeaders, canonical) {
		return Header{}, fmt.Errorf("a release cannot set %s; it may set %s", name, strings.Join(allowedHeaders, ", "))
	}
	switch {
	case value == "":
		return Header{}, errors.New(canonical + " has no value")
	case controlByte(value):
		return Header{}, fmt.Errorf("the value of %s holds a control character", canonical)
	case len(value) > maxHeaderValue:
		return Header{}, fmt.Errorf("the value of %s is longer than %d bytes", canonical, maxHeaderValue)
	}
	return Header{Name: canonical, Value: value}, nil
}

// controlByte reports whether s holds a control character, which could end a
// header and start another. A space is not one: a header value holds spaces.
func controlByte(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// RenderHeaders is a release's header rules as the _headers file that parses
// back to them, which is what an edit of that file reads.
func RenderHeaders(rules []HeaderRule) string {
	var b strings.Builder
	for _, r := range rules {
		fmt.Fprintf(&b, "%s\n", r.Path)
		for _, h := range r.Headers {
			fmt.Fprintf(&b, "  %s: %s\n", h.Name, h.Value)
		}
	}
	return b.String()
}

// HeadersFor is every header the rules set for a path, in file order, a later
// rule's header replacing an earlier one's of the same name; then the framing
// default, unless one of them declares framing (frame-ancestors in an enforced
// Content-Security-Policy, or X-Frame-Options).
func HeadersFor(rules []HeaderRule, p string) []Header {
	var out []Header
	for _, r := range rules {
		if !pathMatches(r.Path, p) {
			continue
		}
		for _, h := range r.Headers {
			out = slices.DeleteFunc(out, func(o Header) bool { return o.Name == h.Name })
			out = append(out, h)
		}
	}
	if !declaresFraming(out) {
		out = append(out, Header{Name: headerCSP, Value: FramingDefault})
	}
	return out
}

// declaresFraming reports whether headers hold a framing rule of their own.
func declaresFraming(headers []Header) bool {
	return slices.ContainsFunc(headers, func(h Header) bool {
		return h.Name == headerFrameOptions || (h.Name == headerCSP && strings.Contains(strings.ToLower(h.Value), frameAncestors))
	})
}

// pathMatches reports whether a rule's path matches p, the way a redirect's
// from does: exactly, ignoring a trailing slash, or everything beneath a path
// ending "/*".
func pathMatches(pattern, p string) bool {
	if prefix, splat := strings.CutSuffix(pattern, "/*"); splat {
		return prefix == "" || p == prefix || strings.HasPrefix(p, prefix+"/")
	}
	return pattern == p || strings.TrimSuffix(pattern, "/") == strings.TrimSuffix(p, "/")
}
