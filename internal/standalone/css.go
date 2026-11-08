package standalone

import (
	"context"
	"net/url"
	"regexp"
	"strings"
)

// maxImportDepth bounds how deep stylesheets importing stylesheets are
// followed.
const maxImportDepth = 4

// cssImport's groups: the URL double-quoted, single-quoted or bare (one of the
// three is set), and the media list.
const (
	importDouble = 1 + iota
	importSingle
	importBare
	importMedia
)

var (
	// cssImport is an @import rule: its URL, double-quoted, single-quoted or
	// bare, in url() or not, and its media list. A quoted URL is read whole:
	// a Google Fonts URL carries ";" and "@" (family=Inter:wght@400;700).
	cssImport = regexp.MustCompile(`(?i)@import\s+(?:url\(\s*)?(?:"([^"]+)"|'([^']+)'|([^"'\s);]+))\s*\)?\s*([^;]*);`)
	// cssURL is a url() token, its value quoted or not.
	cssURL = regexp.MustCompile(`(?i)url\(\s*(["']?)([^"')]+)(["']?)\s*\)`)
)

// css returns a stylesheet with every @import replaced by the stylesheet it
// names and every url() by a data: URI, resolved against base (where the
// stylesheet came from). What cannot be fetched stays as written.
func (s *session) css(ctx context.Context, base *url.URL, text string, depth int) string {
	text = cssImport.ReplaceAllStringFunc(text, func(rule string) string {
		return s.importRule(ctx, base, rule, depth)
	})
	return cssURL.ReplaceAllStringFunc(text, func(tok string) string {
		m := cssURL.FindStringSubmatch(tok)
		if m[1] != m[3] {
			return tok
		}
		if data := s.inlineAsset(ctx, base, m[2]); data != "" {
			return `url("` + data + `")`
		}
		return tok
	})
}

// importRule is one @import replaced by the stylesheet it names, wrapped in
// its media list, or the rule as written when it cannot be fetched.
func (s *session) importRule(ctx context.Context, base *url.URL, rule string, depth int) string {
	m := cssImport.FindStringSubmatch(rule)
	ref := m[importDouble] + m[importSingle] + m[importBare]
	if depth >= maxImportDepth {
		s.notice(ref, "a stylesheet imported deeper than the export follows")
		return rule
	}
	f, ok := s.get(ctx, base, ref)
	if !ok {
		return rule
	}
	inner := s.css(ctx, f.u, string(f.body), depth+1)
	if media := strings.TrimSpace(m[importMedia]); media != "" {
		return "@media " + media + "{" + inner + "}"
	}
	return inner
}
