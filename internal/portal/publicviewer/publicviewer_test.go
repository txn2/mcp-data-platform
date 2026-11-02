package publicviewer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/publicviewer"
)

// requiredDirectives are the policy fragments the public viewers depend on. A
// CSP is only ever wrong in production — the browser silently blocks whatever
// the policy forgot — so each directive a viewer needs is asserted by name.
var requiredDirectives = []string{
	"default-src 'none'",
	// Audio and video render from the same-origin raw content endpoint; without
	// media-src the browser refuses the element's source outright.
	"media-src 'self' blob: data:",
	// The PDF viewer embeds the content endpoint.
	"object-src 'self'",
	"script-src",
	"style-src",
	"img-src",
	"font-src",
	"connect-src",
}

func TestAssetCSP(t *testing.T) {
	t.Parallel()

	csp := publicviewer.AssetCSP(nil)
	for _, directive := range requiredDirectives {
		require.Containsf(t, csp, directive, "asset CSP is missing %q", directive)
	}
	require.Contains(t, csp, "frame-src blob: data: 'self'")
}

func TestCollectionCSP(t *testing.T) {
	t.Parallel()

	csp := publicviewer.CollectionCSP(nil)
	for _, directive := range requiredDirectives {
		require.Containsf(t, csp, directive, "collection CSP is missing %q", directive)
	}
	// The collection viewer loads each item's viewer in a same-origin iframe.
	require.Contains(t, csp, "frame-src 'self' blob: data:")
}

// directives splits a policy into directive name → source list.
func directives(csp string) map[string][]string {
	out := map[string][]string{}
	for part := range strings.SplitSeq(csp, ";") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		out[fields[0]] = fields[1:]
	}
	return out
}

// TestActiveDirectivesDenyPlaintextAndEval pins the two properties that make
// this policy worth having. The active surface is script, the fetches script
// makes, and the frames that get to run script under this policy by
// inheritance: a source list that admits plain http: lets a network attacker
// on a plaintext deployment supply code to a page rendering someone else's
// asset, and 'unsafe-eval' hands every artifact a runtime compiler. Both are
// asserted on the parsed directive rather than on the whole string so a
// permissive source in a passive directive (img-src, font-src) cannot
// satisfy them.
func TestActiveDirectivesDenyPlaintextAndEval(t *testing.T) {
	t.Parallel()

	for name, csp := range map[string]string{
		"asset":      publicviewer.AssetCSP(nil),
		"collection": publicviewer.CollectionCSP(nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			parsed := directives(csp)
			for _, directive := range []string{"script-src", "connect-src", "frame-src"} {
				sources := parsed[directive]
				require.NotEmptyf(t, sources, "%s carries no sources", directive)
				require.NotContainsf(t, sources, "http:", "%s admits script over plaintext http", directive)
				require.NotContainsf(t, sources, "*", "%s admits any source at all", directive)
			}
			require.NotContains(t, parsed["script-src"], "'unsafe-eval'",
				"the JSX pipeline transforms in the parent page and runs the result as a module; nothing evaluates source at runtime")
			require.Contains(t, parsed["script-src"], "'unsafe-inline'",
				"the viewer page's own scripts are inline, and a stored HTML asset's inline script is the artifact")
			require.Contains(t, parsed["script-src"], "https:",
				"the JSX import map resolves from esm.sh and HTML artifacts reference CDN libraries")
			require.Contains(t, parsed["script-src"], "blob:",
				"worker-src falls back through child-src to script-src, so dropping blob: refuses an artifact its own worker")
			require.Contains(t, parsed["script-src"], "'wasm-unsafe-eval'",
				"an HTML asset drawing with a WebAssembly library (Graphviz, dotLottie) compiles its module under this source (#2373)")
		})
	}
}

func TestCSPsShareOneBase(t *testing.T) {
	t.Parallel()

	// The two policies differ only in frame-src. Anything else that drifts
	// between them means one viewer quietly has a capability the other lacks.
	strip := func(csp string) []string {
		var out []string
		for part := range strings.SplitSeq(csp, ";") {
			part = strings.TrimSpace(part)
			if part == "" || strings.HasPrefix(part, "frame-src") {
				continue
			}
			out = append(out, part)
		}
		return out
	}
	require.Equal(t, strip(publicviewer.AssetCSP(nil)), strip(publicviewer.CollectionCSP(nil)))
}

func TestTemplatesParse(t *testing.T) {
	t.Parallel()

	// template.Must would have panicked at init on a parse error; naming the
	// templates here proves the embed globs still resolve to real files after a
	// move, which a panic-at-init cannot distinguish from a missing package.
	require.NotNil(t, publicviewer.AssetTemplate)
	require.NotNil(t, publicviewer.CollectionTemplate)
	require.Contains(t, publicviewer.AssetTemplate.DefinedTemplates(), "public_viewer.html")
	require.Contains(t, publicviewer.CollectionTemplate.DefinedTemplates(), "public_collection_viewer.html")
}

func TestDefaultLogoIsSelfContainedSVG(t *testing.T) {
	t.Parallel()

	// The logo is injected into the page as trusted HTML, so it must be inert
	// markup: no script, no external reference.
	logo := publicviewer.DefaultLogoSVG
	require.True(t, strings.HasPrefix(logo, "<svg"))
	require.True(t, strings.HasSuffix(logo, "</svg>"))
	require.NotContains(t, logo, "<script")

	// The SVG namespace is a URI, not a resource the browser fetches; drop it
	// before checking that nothing else in the markup points off-origin.
	body := strings.ReplaceAll(logo, "http://www.w3.org/2000/svg", "")
	require.NotContains(t, body, "http://")
	require.NotContains(t, body, "https://")
}

// TestCSPCarriesTheEmbedHosts is #2069 on the share viewer: the embed hosts are
// sources of frame-src and media-src and of nothing else, and an empty list
// leaves both at the platform's own origin.
func TestCSPCarriesTheEmbedHosts(t *testing.T) {
	t.Parallel()
	hosts := []string{"https://fast.wistia.net", "cdn.example.com"}
	for name, csp := range map[string]string{
		"asset":      publicviewer.AssetCSP(hosts),
		"collection": publicviewer.CollectionCSP(hosts),
	} {
		d := directives(csp)
		for _, dir := range []string{"frame-src", "media-src"} {
			assert.Contains(t, d[dir], "https://fast.wistia.net", "%s %s", name, dir)
			assert.Contains(t, d[dir], "https://cdn.example.com", "%s %s, a bare host gains https", name, dir)
		}
		for dir, sources := range d {
			if dir == "frame-src" || dir == "media-src" {
				continue
			}
			assert.NotContains(t, sources, "https://fast.wistia.net", "%s %s widened", name, dir)
		}
	}
	off := directives(publicviewer.AssetCSP([]string{}))
	assert.Equal(t, []string{"blob:", "data:", "'self'"}, off["frame-src"])
	assert.Equal(t, []string{"'self'", "blob:", "data:"}, off["media-src"])
}

// TestCSPRefusesAHostThatWouldInjectADirective: a value that never passed
// through the admin route's check still cannot end a directive in the header.
func TestCSPRefusesAHostThatWouldInjectADirective(t *testing.T) {
	t.Parallel()
	csp := publicviewer.AssetCSP([]string{"https://ok.example.com", "evil.example.com; script-src *"})
	assert.NotContains(t, csp, "script-src *")
	assert.NotContains(t, csp, "evil.example.com")
	assert.Equal(t, []string{"blob:", "data:", "'self'"}, directives(csp)["frame-src"],
		"an invalid list adds nothing rather than part of itself")
}
