// Package contentrefs reads what a document body says about references: the
// mcp:// URIs and mcp:asset:<id> references it names, which of those its asset
// does not declare, and which it uses as a link target (#1834, #1875).
//
// It is a lexical reading of text and grants nothing. Declaring a reference,
// and serving one, belong to internal/portal/assetrefs; a managed script's
// export and an agent's save both read a body through this package, so the two
// report the same references in the same words.
package contentrefs

import (
	"regexp"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// namedAssetPrefix is the mcp:asset:<id> form a content body names another
// asset by, built from the one reference vocabulary so it is the string a
// search hit carries.
var namedAssetPrefix = knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetAsset}.URN()

// namedPrefixes are the reference forms a body is read for: a managed resource
// by its default-scheme URI, then an asset by its mcp:asset:<id> reference.
var namedPrefixes = []string{resource.DefaultURIScheme + "://", namedAssetPrefix}

// uriTerminators end a reference string written into a body: whitespace, the
// quotes and brackets markup and code put around a URL, and the backslash a
// string literal escapes with. A reference contains none of them, so the first
// one is where the reference the author wrote stops.
const uriTerminators = " \t\r\n\"'`<>()[]{}\\"

// uriTrailing is trimmed off the end of a candidate because prose puts it
// there: "see mcp://global/brand/logo.svg." names the file, not the period.
const uriTrailing = ".,;:!?"

// UndeclaredConsequence is what happens to references a body names and its
// asset does not declare, in the words every surface that reports them uses
// (a script's run log, a save's result): the serving rewrite leaves them as
// written, so whatever loads one gets nothing.
const UndeclaredConsequence = "they are served as written and resolve to nothing"

// Named returns every distinct reference string a textual body names: each
// managed resource by its mcp:// URI, then each asset by its mcp:asset:<id>
// reference, both in the order they first appear.
//
// It recognizes the default resource scheme only. A deployment that renamed
// its scheme (resources.managed.uri_scheme) writes URIs this does not see, so a
// caller must treat an empty result as "found none", never as "there are
// none". Declaration is unaffected: it is the Declarer's, which takes the
// deployment's scheme.
//
// It is the reading the serving rewrite would need a declaration for, so a
// surface can tell an author which references in a document will be served as
// written and resolve to nothing (#1834, #1875). It grants nothing: a string
// found here is a string, and only a declaration makes it load.
func Named(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, prefix := range namedPrefixes {
		rest := body
		for {
			i := strings.Index(rest, prefix)
			if i < 0 {
				break
			}
			rest = rest[i:]
			end := strings.IndexAny(rest, uriTerminators)
			if end < 0 {
				end = len(rest)
			}
			uri := strings.TrimRight(rest[:end], uriTrailing)
			rest = rest[end:]
			if len(uri) > len(prefix) && !seen[uri] {
				seen[uri] = true
				out = append(out, uri)
			}
		}
	}
	return out
}

// Undeclared returns the reference strings body names that declared does not
// list, in the order Named finds them. Each declared entry is trimmed first, as
// a declaration is recorded.
func Undeclared(body string, declared []string) []string {
	listed := make(map[string]bool, len(declared))
	for _, d := range declared {
		listed[strings.TrimSpace(d)] = true
	}
	var out []string
	for _, uri := range Named(body) {
		if !listed[uri] {
			out = append(out, uri)
		}
	}
	return out
}

// refAt returns the reference string s starts with, or "" when s does not
// start with one: a known prefix followed by at least one character before a
// terminator, with trailing prose punctuation removed.
func refAt(s string) string {
	for _, prefix := range namedPrefixes {
		if !strings.HasPrefix(s, prefix) {
			continue
		}
		end := strings.IndexAny(s, uriTerminators)
		if end < 0 {
			end = len(s)
		}
		uri := strings.TrimRight(s[:end], uriTrailing)
		if len(uri) > len(prefix) {
			return uri
		}
	}
	return ""
}

var (
	// anchorTag is an HTML, JSX or SVG element a reader follows: <a> and an
	// image map's <area>. Everything up to the tag's closing '>' is its
	// attributes.
	anchorTag = regexp.MustCompile(`(?is)<(?:a|area)\s[^>]*>`)

	// hrefValue is where an anchor's href value starts, in markup (href="x",
	// href='x', href=x), in SVG (xlink:href) and in JSX (href={"x"},
	// href={`x`}). The value itself is read by refAt from the match's end.
	hrefValue = regexp.MustCompile("(?is)(?:^|\\s)(?:xlink:)?href\\s*=\\s*\\{?\\s*[\"'`]?\\s*")

	// markdownLink is the opening of an inline Markdown link or image up to
	// its destination: "[text](" or "![alt](", with an optional '<' the
	// destination may be wrapped in. The '!' is part of the match, because an
	// image loads its destination and a link is followed.
	markdownLink = regexp.MustCompile(`!?\[[^\[\]\n]*\]\(\s*<?`)

	// markdownFence and markdownCode are a fenced code block and an inline
	// code span. What they hold is shown as text, so a reference written in
	// one is an example and is not read.
	markdownFence = regexp.MustCompile("(?s)(?:^|\\n)[ \\t]*(?:```|~~~).*?\\n[ \\t]*(?:```|~~~)[ \\t]*(?:\\n|$)")
	markdownCode  = regexp.MustCompile("`[^`\\n]+`")
)

// Linked returns each distinct reference string a body uses as a link target,
// in the order they first appear: an <a> or <area> href in HTML, XHTML, JSX,
// SVG or the markup a Markdown document embeds, and a Markdown link or
// autolink. Any other content type has no links and returns nil.
//
// A reference is served as the bytes of what it names, so it can be loaded
// (an <img src>, a stylesheet, a fetch) and it cannot be followed: the frame a
// document renders in blocks navigation, and a destination that is a file's
// raw bytes is not a page. The platform has no link between assets, and a
// write that makes one is refused rather than stored as a link that goes
// nowhere (#1875). A Markdown image, ![alt](ref), loads its destination and is
// not a link.
func Linked(body, contentType string) []string {
	switch contenttype.Normalize(contentType) {
	case contenttype.HTML, contenttype.XHTML, contenttype.JSX, contenttype.SVG:
		return dedupe(anchorRefs(body))
	case contenttype.Markdown:
		prose := markdownCode.ReplaceAllString(markdownFence.ReplaceAllString(body, ""), "")
		return dedupe(append(anchorRefs(prose), markdownRefs(prose)...))
	default:
		return nil
	}
}

// anchorRefs returns the reference each anchor in body links to.
func anchorRefs(body string) []string {
	var out []string
	for _, tag := range anchorTag.FindAllString(body, -1) {
		for _, loc := range hrefValue.FindAllStringIndex(tag, -1) {
			if uri := refAt(tag[loc[1]:]); uri != "" {
				out = append(out, uri)
			}
		}
	}
	return out
}

// markdownRefs returns the reference each Markdown link and autolink in body
// is a link to, leaving out images.
func markdownRefs(body string) []string {
	var out []string
	for _, loc := range markdownLink.FindAllStringIndex(body, -1) {
		// A match opens with the '!' exactly when it is an image.
		isImage := body[loc[0]] == '!'
		if uri := refAt(body[loc[1]:]); uri != "" && !isImage {
			out = append(out, uri)
		}
	}
	// A Markdown autolink, <reference>, renders as a link to what it wraps.
	// The closing '>' must follow the reference directly, or this is markup
	// and the anchor reading has it.
	for rest := body; ; {
		i := strings.IndexByte(rest, '<')
		if i < 0 {
			return out
		}
		rest = rest[i+1:]
		if uri := refAt(rest); uri != "" && strings.HasPrefix(rest[len(uri):], ">") {
			out = append(out, uri)
		}
	}
}

// dedupe keeps the first occurrence of each string, in order.
func dedupe(in []string) []string {
	var out []string
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
