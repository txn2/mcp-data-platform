// Package sourcecheck holds the checks a drawn source passes before it is
// stored: a Graphviz DOT source (#2303) and a Vega-Lite specification (#2296).
//
// The portal draws each in the reader's browser, so the source decides what
// that browser does. A DOT source that does not parse would store an asset
// that draws nothing, and a Vega-Lite spec naming a URL would make every
// reader's browser fetch it. Both are refused here, before anything is
// written, by every door a source can be stored through: save_asset,
// manage_asset, the portal's REST writes and a script's platform.export.
package sourcecheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gonum.org/v1/gonum/graph/formats/dot"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// maxNamed caps how many refused values one refusal names, so a spec with a
// thousand bad URLs still produces a readable error.
const maxNamed = 5

// referencePrefixes are the reference forms a Vega-Lite spec may load data
// from: a managed resource by its mcp:// URI and another asset by its
// mcp:asset: reference. Every viewing surface rewrites a declared one to the
// platform URL serving its current bytes, which is the only place the
// portal's loader fetches from.
var referencePrefixes = []string{"mcp://", "mcp:asset:"}

// viewKeys are the top-level keys of which a Vega-Lite spec must carry at
// least one to describe a view: a single mark, or a composition of views.
var viewKeys = []string{"mark", "layer", "concat", "hconcat", "vconcat", "facet", "repeat", "spec"}

// Check refuses a source of a drawn content type that cannot be stored, and
// returns nil for a source that can and for every other content type.
func Check(contentType, body string) error {
	switch contenttype.Normalize(contentType) {
	case contenttype.Graphviz:
		return checkDOT(body)
	case contenttype.VegaLite:
		return checkVegaLite(body)
	default:
		return nil
	}
}

// checkDOT parses a DOT source. The parser's error carries the line and
// column it stopped at, which is what an author needs to fix the source.
func checkDOT(body string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("the DOT source is empty; write a graph such as digraph { a -> b }")
	}
	// The parser refuses a file holding no graph, so a parsed file holds one.
	if _, err := dot.ParseString(body); err != nil {
		return fmt.Errorf("the DOT source does not parse at line:column %w", err)
	}
	return nil
}

// checkVegaLite refuses a spec that is not a JSON object describing a view,
// and one that loads anything other than a declared reference.
func checkVegaLite(body string) error {
	var spec any
	if err := json.Unmarshal([]byte(body), &spec); err != nil {
		return fmt.Errorf("the Vega-Lite spec is not valid JSON: %w", err)
	}
	obj, ok := spec.(map[string]any)
	if !ok {
		return errors.New("the Vega-Lite spec must be a JSON object with a mark, a layer or a composition of views")
	}
	if !slices.ContainsFunc(viewKeys, func(k string) bool { _, has := obj[k]; return has }) {
		return fmt.Errorf("the Vega-Lite spec describes no view: it needs one of %s", strings.Join(viewKeys, ", "))
	}
	var w walker
	w.walk(obj)
	if len(w.urls) > 0 {
		return fmt.Errorf("the Vega-Lite spec loads %s, which is not a managed resource or an asset. "+
			"A spec loads data only from a reference: inline the values, or store the file with manage_resource "+
			"and name its mcp:// URI (or an asset's mcp:asset:<id>) as the url, and declare it in references",
			named(w.urls))
	}
	if len(w.links) > 0 {
		return fmt.Errorf("the Vega-Lite spec links to %s; the portal draws a spec without links, "+
			"so remove the href", named(w.links))
	}
	return nil
}

// walker collects every URL a spec would load and every link it would open.
type walker struct {
	urls  []string
	links []string
}

// walk visits every value in a decoded spec. A url key loads a file, as a
// data source, a lookup's source or an image mark's picture; an href key
// opens a link. Either may be a string or a {"value": ...} channel.
func (w *walker) walk(v any) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			w.visit(key, child)
			w.walk(child)
		}
	case []any:
		for _, child := range node {
			w.walk(child)
		}
	}
}

// visit records what one key of a spec object loads or opens.
func (w *walker) visit(key string, child any) {
	s, ok := literal(child)
	if !ok {
		return
	}
	switch {
	case key == "url" && !isReference(s):
		w.urls = append(w.urls, s)
	case key == "href" && s != "":
		w.links = append(w.links, s)
	}
}

// literal returns the string a url or href key names, whether written
// directly or as a channel's constant {"value": "..."}.
func literal(v any) (string, bool) {
	switch node := v.(type) {
	case string:
		return node, true
	case map[string]any:
		s, ok := node["value"].(string)
		return s, ok
	default:
		return "", false
	}
}

// isReference reports whether a URL is one of the reference forms the
// platform rewrites for every viewing surface.
func isReference(s string) bool {
	return slices.ContainsFunc(referencePrefixes, func(p string) bool { return strings.HasPrefix(s, p) })
}

// named lists the first few values, sorted and quoted, and how many more.
func named(values []string) string {
	slices.Sort(values)
	values = slices.Compact(values)
	shown := values
	if len(shown) > maxNamed {
		shown = shown[:maxNamed]
	}
	quoted := make([]string, len(shown))
	for i, v := range shown {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	out := strings.Join(quoted, ", ")
	if extra := len(values) - len(shown); extra > 0 {
		out += fmt.Sprintf(" and %d more", extra)
	}
	return out
}
