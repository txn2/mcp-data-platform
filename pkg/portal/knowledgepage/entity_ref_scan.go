package knowledgepage

import (
	"regexp"
	"strings"
)

// trailingPunct is the set of sentence punctuation trimmed from the tail of an
// undelimited reference token. The id class of an mcp: reference includes ".",
// and the bare-urn class stops only at whitespace or a closing bracket, so a
// reference written in prose immediately before sentence punctuation absorbs
// that punctuation into the token (#704). No real reference id or URN ends in
// these characters, and the parenthesized/dataset forms already terminate at
// ")", so trimming a trailing run is safe and never touches punctuation inside
// a token.
const trailingPunct = ".,;:!?"

// refTokenRe matches a serialized reference token in page-body text: an mcp:
// internal reference (a simple id, or a (kind,name) connection) or a urn:
// external reference (with or without a single parenthesized id group, the form
// DataHub uses). At most one level of parentheses is supported, which covers
// every current reference form. The parenthesized alternatives come first so a
// connection or DataHub token is matched whole rather than truncated at the
// closing paren of an enclosing markdown link.
var refTokenRe = regexp.MustCompile(
	`mcp:[a-z_]+:\([^)]*\)|mcp:[a-z_]+:[A-Za-z0-9_.\-]+|urn:[a-z]+:[A-Za-z0-9]+:\([^)]*\)|urn:[a-z]+:[^\s)\]>]+`)

// codeSpanRe matches fenced code blocks and inline code spans, which are stripped
// before scanning so a URN shown as a documentation example does not become a
// stored reference (the renderer never chips code, so scanning it would diverge).
var codeSpanRe = regexp.MustCompile("(?s)```.*?```|`[^`]*`")

// ScanBodyRefs extracts the entity references mentioned in a page's markdown body.
// It is content-agnostic: a reference is found whether it appears as a markdown
// link href, an autolink, or inline text. It uses ParseCitableRef, so a mention of
// a fetch-only-but-not-citable form (mcp:memory:, mcp:insight:) is skipped exactly
// like an unparseable token rather than producing a reference no page-citation path
// can persist (#699). Unparseable matches are skipped, the result is de-duplicated
// by target, and every ref is marked source=inline so a reconcile can replace only
// the inline set without touching promoted or manual references.
func ScanBodyRefs(body string) []EntityRef {
	body = codeSpanRe.ReplaceAllString(body, " ")
	matches := refTokenRe.FindAllString(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	refs := make([]EntityRef, 0, len(matches))
	for _, m := range matches {
		m = strings.TrimRight(m, trailingPunct)
		ref, err := ParseCitableRef(m)
		if err != nil {
			continue
		}
		if _, dup := seen[ref.identity()]; dup {
			continue
		}
		seen[ref.identity()] = struct{}{}
		ref.Source = RefSourceInline
		refs = append(refs, ref)
	}
	return refs
}
