package knowledgepage

import "regexp"

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
// link href, an autolink, or inline text. Unparseable matches are skipped, the
// result is de-duplicated by target, and every ref is marked source=inline so a
// reconcile can replace only the inline set without touching promoted or manual
// references.
func ScanBodyRefs(body string) []EntityRef {
	body = codeSpanRe.ReplaceAllString(body, " ")
	matches := refTokenRe.FindAllString(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	refs := make([]EntityRef, 0, len(matches))
	for _, m := range matches {
		ref, err := ParseEntityRef(m)
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
