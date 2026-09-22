package exportrefs

import (
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// namedAssetPrefix is the mcp:asset:<id> form a content body names another
// asset by, built from the one reference vocabulary so it is the string a
// search hit carries.
var namedAssetPrefix = knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetAsset}.URN()

// uriTerminators end a reference string written into a body: whitespace, the
// quotes and brackets markup and code put around a URL, and the backslash a
// string literal escapes with. A reference contains none of them, so the first
// one is where the reference the author wrote stops.
const uriTerminators = " \t\r\n\"'`<>()[]{}\\"

// uriTrailing is trimmed off the end of a candidate because prose puts it
// there: "see mcp://global/brand/logo.svg." names the file, not the period.
const uriTrailing = ".,;:!?"

// Named returns every distinct reference string a textual body names: each
// managed resource by its mcp:// URI, then each asset by its mcp:asset:<id>
// reference, both in the order they first appear.
//
// It recognizes the default resource scheme only. A deployment that renamed
// its scheme (resources.managed.uri_scheme) writes URIs this does not see, so a
// caller must treat an empty result as "found none", never as "there are
// none". Declaration is unaffected: it is manage_asset's, which takes the
// deployment's scheme.
//
// It is the reading the serving rewrite would need a declaration for, so a
// surface can tell an author which references in a document will be served as
// written and resolve to nothing (#1834). It grants nothing: a string found
// here is a string, and only a declaration makes it load.
func Named(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, prefix := range []string{resource.DefaultURIScheme + "://", namedAssetPrefix} {
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
