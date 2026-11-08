package standalone

import (
	"context"
	"regexp"
)

// quotedURL is an http(s) URL or a root-relative platform path written as a
// whole quoted string: a JavaScript string or template literal, or a JSX
// attribute value. Only a whole string is read, so a URL assembled at run
// time from pieces is left alone rather than fetched half-built.
var quotedURL = regexp.MustCompile("([\"'`])((?:https?://|/portal/)[^\"'`\\s<>${}]+)([\"'`])")

// importSpecifier is the module an import or export statement, or a dynamic
// import(), names.
var importSpecifier = regexp.MustCompile("(?:\\bfrom\\s*|\\bimport\\s*\\(?\\s*)[\"'`]([^\"'`]+)[\"'`]")

// literals returns script text with every quoted URL that names an image, a
// font or a stylesheet replaced by a data: URI. A declared reference the
// script reads as data (a CSV, JSON or any other file it fetches) is a
// platform read and is recorded; a file on another host that is neither shown
// nor importable stays as its URL and is named in a notice.
//
// A module specifier (import ... from "https://...") is left to the bundler,
// which fetches the module rather than inlining it as a file.
func (s *session) literals(ctx context.Context, text string) string {
	specifiers := map[string]bool{}
	for _, m := range importSpecifier.FindAllStringSubmatch(text, -1) {
		specifiers[m[1]] = true
	}
	return quotedURL.ReplaceAllStringFunc(text, func(lit string) string {
		m := quotedURL.FindStringSubmatch(lit)
		if m[1] != m[3] || specifiers[m[2]] {
			return lit
		}
		f, ok := s.get(ctx, s.in.Origin, m[2])
		if !ok {
			return lit
		}
		if data := s.literalData(ctx, f); data != "" {
			return m[1] + data + m[3]
		}
		return lit
	})
}

// literalData is the data: URI a script literal is replaced by: a stylesheet
// with its own files inlined, or an image, font or media file. A declared
// reference read as data is a platform read; any other file is left, named in
// a notice.
func (s *session) literalData(ctx context.Context, f fetchedRef) string {
	switch {
	case isCSS(f.contentType):
		return dataURI("text/css", []byte(s.css(ctx, f.u, string(f.body), 0)))
	case embeddable(f.contentType):
		return dataURI(f.contentType, f.body)
	case f.where == placeOwn && isRef(f.u):
		s.dataRead(f.u, f.contentType)
	default:
		s.notice(displayURL(f.u, f.where), "read while the document runs, which needs the network")
	}
	return ""
}
