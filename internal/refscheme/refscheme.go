// Package refscheme is the one reader and writer of the scheme platform
// references start with (#2776): mcp:asset:<id>, mcp:dataset:(...), and every
// other kind. A deployment names its scheme once (server.reference_scheme);
// every reference the platform hands out is written under it, every reference
// it reads is accepted under it and under mcp:, and everything stored keeps the
// canonical mcp: form, so changing the setting changes only what is written
// out. With no setting the scheme is mcp and nothing is rewritten.
package refscheme

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
)

// Canonical is the scheme every stored reference uses.
const Canonical = "mcp"

// Prefix is the canonical start of a reference, and URIPrefix of a resource
// URI stored under the default scheme.
const (
	Prefix    = "mcp:"
	URIPrefix = "mcp://"
)

// The canonical start of each kind of reference, written out whole so a
// search of the source for one finds where it is made.
const (
	Asset         = "mcp:asset:"
	Prompt        = "mcp:prompt:"
	Collection    = "mcp:collection:"
	KnowledgePage = "mcp:knowledge_page:"
	Connection    = "mcp:connection:"
	Insight       = "mcp:insight:"
	Resource      = "mcp:resource:"
	Memory        = "mcp:memory:"
	Script        = "mcp:script:"
	Call          = "mcp:call:"
	Session       = "mcp:session:"
	Form          = "mcp:form:"
	Endpoint      = "mcp:endpoint:"
	Site          = "mcp:site:"
	Tag           = "mcp:tag:"
)

// Kinds are every kind a reference names.
var Kinds = []string{
	"asset", "prompt", "collection", "knowledge_page", "connection", "insight", "resource", "memory", "script", "call", "session",
	"dataset", "column", "glossary_term", "glossary_node", "tag", "domain", "data_product", "schema_document",
	"form", "endpoint", "site", "catalog",
}

// Of is the canonical reference of a kind and the rest of it.
func Of(kind, rest string) string { return Prefix + kind + ":" + rest }

// reserved are schemes with another meaning, which a deployment may not use.
var reserved = []string{"http", "https", "file", "data", "javascript", "mailto", "urn", "ftp", "ws", "wss", "blob", "about", "schema", "glossary", "availability"}

var schemeShape = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Validate refuses a scheme a deployment may not use, naming the key.
func Validate(scheme string) error {
	switch {
	case scheme == "":
		return nil
	case !schemeShape.MatchString(scheme):
		return fmt.Errorf("server.reference_scheme %q: a lowercase letter, then lowercase letters, digits or -, at most 32", scheme)
	case slices.Contains(reserved, scheme):
		return fmt.Errorf("server.reference_scheme %q names a scheme with another meaning; choose your deployment's own name", scheme)
	}
	return nil
}

// config is the scheme in force and its rewriters.
type config struct {
	scheme string
	// stored is the scheme resource URIs are stored under:
	// resources.managed.uri_scheme, or mcp.
	stored string
	// resources is true when resource URIs follow the scheme (mcp:// is
	// written as <scheme>://), as they do when resources.managed.uri_scheme
	// is not set.
	resources bool
	toCanon   *regexp.Regexp
	toDisplay *regexp.Regexp
	// prose is the platform's own wording about the reference form
	// (#2818): mcp: before a placeholder (mcp:<type>:<key>), before a kind
	// named as a word (an mcp:dataset reference), or standing alone (an mcp:
	// reference). It is applied only to text the platform writes, never to a
	// tool's data, where "mcp:" may mean something else.
	prose *regexp.Regexp
}

var current atomic.Pointer[config]

func init() { current.Store(&config{scheme: Canonical, stored: Canonical}) }

// kindAlternation matches every kind.
var kindAlternation = strings.Join(Kinds, "|")

// Configure sets the scheme references are written under; "" is mcp.
// uriScheme is resources.managed.uri_scheme: with none set, resource URIs are
// stored as mcp:// and written out as <scheme>:// too.
func Configure(scheme, uriScheme string) error {
	if err := Validate(scheme); err != nil {
		return err
	}
	resources := uriScheme == ""
	stored := cmp.Or(uriScheme, Canonical)
	if scheme == "" || scheme == Canonical {
		current.Store(&config{scheme: Canonical, stored: stored})
		return nil
	}
	uri := ""
	if resources {
		uri = `|//`
	}
	q := regexp.QuoteMeta(scheme)
	current.Store(&config{
		scheme:    scheme,
		stored:    stored,
		resources: resources,
		// A reference starts where no name or path continues before it.
		toCanon:   regexp.MustCompile(`(^|[^A-Za-z0-9_.\-/])` + q + `:((?:` + kindAlternation + `):` + uri + `)`),
		toDisplay: regexp.MustCompile(`(^|[^A-Za-z0-9_.\-/])` + Canonical + `:((?:` + kindAlternation + `):` + uri + `)`),
		prose:     regexp.MustCompile(`(^|[^A-Za-z0-9_.\-/])` + Canonical + `:(<|(?:` + kindAlternation + `)(?:[^:A-Za-z0-9_]|$)|[\s` + "`" + `'"),.;\\]|$)`),
	})
	return nil
}

// Scheme is the scheme references are written under.
func Scheme() string { return current.Load().scheme }

// Rewrites reports whether references are written under a scheme other than
// mcp, so that anything is rewritten at all.
func Rewrites() bool { return current.Load().toCanon != nil }

// Canon is a text with every reference under the deployment's scheme written
// in the canonical form: what the platform reads, before it stores or compares
// it.
func Canon(s string) string {
	c := current.Load()
	if c.toCanon == nil || !strings.Contains(s, c.scheme+":") {
		return s
	}
	return c.toCanon.ReplaceAllString(s, "${1}"+Canonical+":${2}")
}

// Display is a text with every canonical reference written under the
// deployment's scheme: what the platform hands out.
func Display(s string) string {
	c := current.Load()
	if c.toDisplay == nil || !strings.Contains(s, Prefix) {
		return s
	}
	return c.toDisplay.ReplaceAllString(s, "${1}"+c.scheme+":${2}")
}

// Prose is Display for text the platform writes about references (#2818):
// tool descriptions and schemas, instructions and refusals. Besides every
// whole reference it rewrites the generic forms, mcp:<type>:<key>, "an
// mcp:dataset reference" and "an mcp: reference", so what the platform teaches
// is the form the deployment shows.
func Prose(s string) string {
	s = Display(s)
	c := current.Load()
	if c.prose == nil || !strings.Contains(s, Prefix) {
		return s
	}
	return c.prose.ReplaceAllString(s, "${1}"+c.scheme+":${2}")
}

// ProseBytes is Prose over bytes.
func ProseBytes(b []byte) []byte {
	b = DisplayBytes(b)
	c := current.Load()
	if c.prose == nil || !strings.Contains(string(b), Prefix) {
		return b
	}
	return c.prose.ReplaceAll(b, []byte("${1}"+c.scheme+":${2}"))
}

// CanonBytes is Canon over bytes.
func CanonBytes(b []byte) []byte {
	c := current.Load()
	if c.toCanon == nil || !strings.Contains(string(b), c.scheme+":") {
		return b
	}
	return c.toCanon.ReplaceAll(b, []byte("${1}"+Canonical+":${2}"))
}

// DisplayBytes is Display over bytes.
func DisplayBytes(b []byte) []byte {
	c := current.Load()
	if c.toDisplay == nil || !strings.Contains(string(b), Prefix) {
		return b
	}
	return c.toDisplay.ReplaceAll(b, []byte("${1}"+c.scheme+":${2}"))
}

// StoredResourceScheme is the scheme resource URIs are stored under:
// resources.managed.uri_scheme, or mcp. A body is read for this one and mcp://
// both.
func StoredResourceScheme() string { return current.Load().stored }
