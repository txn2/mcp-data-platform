// Package assetrefs serves and rewrites the managed resources an asset's
// content references (#1474).
//
// It owns three things a reference needs and no single caller could own alone:
// the URL a reference is served under, the rewrite that swaps a declared
// mcp:// URI for that URL as content is served, and the HTTP handler that
// answers the URL with the resource's bytes. The portal's own asset and
// version reads, the public share and collection-item reads, and the admin
// console's reads all pass content through the same rewrite, so a reference
// renders identically wherever the asset is opened.
//
// What it deliberately does not touch is the read an agent makes before it
// patches (manage_asset get_content). That path serves the stored content with
// its mcp:// URIs intact, because an agent handed a rewritten URL would write
// a platform-internal path back into the asset on its next patch and the
// reference would be gone.
package assetrefs

import (
	"net/http"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// PathPrefix is the unauthenticated route prefix references are served under.
// It is its own prefix rather than a path below /api/v1/portal/ because the
// route takes no session: the reader is inside a sandboxed frame or holds a
// public share link, and neither carries the portal's cookie. Being a distinct
// prefix is what lets the composition root mount it outside the portal's
// authentication chain instead of carving an exception out of it.
const PathPrefix = "/portal/refs/"

// URL returns the absolute, credential-free URL a reference is served under.
//
// Absolute because the serving surfaces render an asset inside an iframe whose
// document came from a blob: URL: a root-relative path resolved against a
// blob: base does not name the server at all. base is the origin BaseURL
// resolved, with or without a trailing slash; an empty base yields a
// root-relative URL, which is still correct for a reader whose page is served
// from the origin directly.
func URL(base, assetID, refToken string) string {
	return strings.TrimSuffix(base, "/") + PathPrefix + assetID + "/" + refToken
}

// Scheme values that may appear in a resolved origin. They are also the
// allow-list for X-Forwarded-Proto, so a misbehaving proxy cannot put an
// arbitrary scheme into a URL the platform emits.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// BaseURL resolves the absolute origin a reader reaches this deployment at:
// the configured public base URL when there is one, and otherwise the scheme
// and host of the request being answered.
//
// The fallback is not a nicety. A reference URL has to be absolute, and a
// deployment that never set portal.public_base_url still serves share links,
// which resolve the same way -- so without it references would be the one
// surface that silently produced URLs no reader could follow.
//
// X-Forwarded-Proto is honored only when r.TLS is nil, which is when a reverse
// proxy is plausibly in front. Where the server terminates TLS itself, an
// attacker-supplied header must not be able to downgrade the real scheme. A
// multi-proxy chain may send a comma-separated list; the leftmost token is the
// originating client's scheme. Anything that is not http or https falls back to
// the default, so a malformed header cannot produce a malformed URL.
//
// An empty result (a request with no Host, which is a unit-test shape rather
// than a served one) yields a root-relative URL from URL above.
func BaseURL(r *http.Request, configured string) string {
	if s := strings.TrimRight(configured, "/"); s != "" {
		return s
	}
	if r == nil || r.Host == "" {
		return ""
	}
	if r.TLS != nil {
		return schemeHTTPS + "://" + r.Host
	}
	return forwardedScheme(r) + "://" + r.Host
}

// forwardedScheme reads the originating client's scheme from
// X-Forwarded-Proto, defaulting to http and refusing anything that is not one
// of the two schemes above.
func forwardedScheme(r *http.Request) string {
	forwarded := r.Header.Get("X-Forwarded-Proto")
	if forwarded == "" {
		return schemeHTTP
	}
	if i := strings.IndexByte(forwarded, ','); i >= 0 {
		forwarded = forwarded[:i]
	}
	forwarded = strings.TrimSpace(forwarded)
	if forwarded == schemeHTTP || forwarded == schemeHTTPS {
		return forwarded
	}
	return schemeHTTP
}

// Rewrite returns content with every declared mcp:// URI replaced by the URL
// its reference is served under.
//
// Only the URIs in refs are rewritten. A mcp:// URI that appears in the
// content but was never declared is left exactly as written and resolves to
// nothing, so the grant is always the declaration and never a string that
// happens to appear in the body.
//
// Non-textual content is returned untouched: a stored PNG is bytes, and
// scanning it for URIs could only corrupt it.
func Rewrite(content []byte, contentType, base, assetID string, refs []portaldomain.AssetResourceRef) []byte {
	if len(refs) == 0 || len(content) == 0 || !contenttype.IsTextual(contentType) {
		return content
	}
	pairs := replacements(base, assetID, refs)
	if len(pairs) == 0 {
		return content
	}
	in := string(content)
	out := strings.NewReplacer(pairs...).Replace(in)
	if out == in {
		// Returning the caller's slice rather than a fresh one keeps a read
		// that changed nothing from costing a copy of the whole document.
		return content
	}
	return []byte(out)
}

// replacements builds the flat old/new argument list strings.NewReplacer takes,
// longest URI first.
//
// The order matters and is not cosmetic. One declared URI can be a prefix of
// another -- "mcp://global/brand/logo.png" and
// "mcp://global/brand/logo.png.bak" differ only by a suffix -- and a replacer
// fed the shorter one first would rewrite the head of the longer URI and leave
// its tail stranded in the markup. Sorting longest-first makes the more
// specific URI win, which is the only reading that can be right.
//
// A reference with an empty URI or token contributes nothing: it could only
// match everywhere or resolve to nothing.
func replacements(base, assetID string, refs []portaldomain.AssetResourceRef) []string {
	ordered := make([]portaldomain.AssetResourceRef, 0, len(refs))
	for _, ref := range refs {
		if ref.URI == "" || ref.RefToken == "" {
			continue
		}
		ordered = append(ordered, ref)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return len(ordered[i].URI) > len(ordered[j].URI)
	})

	pairs := make([]string, 0, len(ordered)*2)
	for _, ref := range ordered {
		pairs = append(pairs, ref.URI, URL(base, assetID, ref.RefToken))
	}
	return pairs
}
