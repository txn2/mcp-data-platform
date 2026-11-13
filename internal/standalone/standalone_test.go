package standalone

import (
	"context"
	"encoding/base64"
	"errors"
	"maps"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// file is one answer a fake fetcher gives.
type file struct {
	body string
	ct   string
	err  error
}

// fakeFetcher answers from a map keyed by URL (path and query for the
// platform's own routes, the whole URL for another host) and records what it
// was asked.
type fakeFetcher struct {
	mu    sync.Mutex
	files map[string]file
	asked []string
}

func (f *fakeFetcher) Fetch(_ context.Context, u *url.URL, limit int64) (body []byte, contentType string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := u.String()
	f.asked = append(f.asked, key)
	got, ok := f.files[key]
	if !ok {
		return nil, "", errors.New("404 Not Found")
	}
	if got.err != nil {
		return nil, "", got.err
	}
	if int64(len(got.body)) > limit {
		return nil, "", ErrTooLarge
	}
	return []byte(got.body), got.ct, nil
}

var origin = &url.URL{Scheme: "http", Host: "platform.example"}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// result is what Build answered, and what each fetcher was asked.
type result struct {
	HTML     []byte
	Notices  []Notice
	own, ext *fakeFetcher
}

// fixtures are what the fetchers answer from, and the export's bounds.
type fixtures struct {
	own, ext map[string]file
	limits   Limits
}

// exportDoc runs Build over doc with fetchers answering from the fixtures.
func exportDoc(t *testing.T, doc, ct string, fx fixtures) (*result, error) {
	t.Helper()
	own, ext, limits := fx.own, fx.ext, fx.limits
	res := &result{own: &fakeFetcher{files: own}, ext: &fakeFetcher{files: ext}}
	out, notices, err := Build(context.Background(), Input{
		Document: []byte(doc), ContentType: ct, Title: "Quarterly <deck>", Origin: origin,
		Own: res.own, External: res.ext, Limits: limits,
	})
	res.HTML, res.Notices = out, notices
	return res, err
}

// TestBuild_RevealDeckInlinesTheServedRuntime is the issue's deck: the served
// reveal.js, its stylesheet and theme and the markdown plugin end up inside
// the file, and the theme's font with them.
func TestBuild_RevealDeckInlinesTheServedRuntime(t *testing.T) {
	doc := `<!doctype html><html><head>
<link rel="stylesheet" href="/portal/vendor/reveal/reveal.css">
<link rel="stylesheet" href="http://platform.example/portal/vendor/reveal/theme/black.css" media="screen">
</head><body><div class="reveal"><div class="slides"><section>One</section><section>Two</section></div></div>
<script src="/portal/vendor/reveal/reveal.js"></script>
<script src="/portal/vendor/reveal/plugin/markdown.js"></script>
<script>Reveal.initialize({ plugins: [RevealMarkdown] });</script>
</body></html>`
	own := map[string]file{
		"/portal/vendor/reveal/reveal.css":              {body: ".reveal{color:red}", ct: "text/css"},
		"/portal/vendor/reveal/theme/black.css":         {body: "@font-face{src:url(fonts/source.woff)}", ct: "text/css; charset=utf-8"},
		"/portal/vendor/reveal/theme/fonts/source.woff": {body: "WOFF", ct: "font/woff"},
		"/portal/vendor/reveal/reveal.js":               {body: "var Reveal={initialize:function(){}};/* </script> */", ct: "text/javascript"},
		"/portal/vendor/reveal/plugin/markdown.js":      {body: "var RevealMarkdown={};", ct: "text/javascript"},
	}
	res, err := exportDoc(t, doc, "text/html; charset=utf-8", fixtures{own: own, ext: nil, limits: Limits{}})
	require.NoError(t, err)
	out := string(res.HTML)
	assert.Empty(t, res.Notices)
	assert.Empty(t, res.ext.asked, "nothing on another host was named")
	assert.NotContains(t, out, "/portal/vendor/", "every served file is inside the document")
	assert.Contains(t, out, "<style>.reveal{color:red}</style>")
	assert.Contains(t, out, `<style media="screen">@font-face{src:url("data:font/woff;base64,`+b64("WOFF")+`")}</style>`)
	assert.Contains(t, out, `var Reveal={initialize:function(){}};/* <\/script> */`)
	assert.Contains(t, out, "var RevealMarkdown={};")
	assert.Contains(t, out, "Reveal.initialize({ plugins: [RevealMarkdown] });")
}

// TestBuild_FilesOnOtherHostsAreFetchedAndInlined: a Google Fonts stylesheet
// and the font it names, and an image on a public host.
func TestBuild_FilesOnOtherHostsAreFetchedAndInlined(t *testing.T) {
	doc := `<html><head><style>@import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;700&display=swap');
body{font-family:Inter}</style></head><body><img src="https://images.example.org/logo.png" srcset="https://images.example.org/logo.png 1x, https://images.example.org/logo@2x.png 2x"></body></html>`
	ext := map[string]file{
		"https://fonts.googleapis.com/css2?family=Inter:wght@400;700&display=swap": {body: "@font-face{font-family:Inter;src:url(https://fonts.gstatic.com/inter.woff2) format('woff2')}", ct: "text/css"},
		"https://fonts.gstatic.com/inter.woff2":                                    {body: "W2", ct: "font/woff2"},
		"https://images.example.org/logo.png":                                      {body: "PNG", ct: "image/png"},
		"https://images.example.org/logo@2x.png":                                   {body: "PNG2", ct: "image/png"},
	}
	res, err := exportDoc(t, doc, "text/html", fixtures{own: nil, ext: ext, limits: Limits{}})
	require.NoError(t, err)
	out := string(res.HTML)
	assert.Empty(t, res.Notices)
	assert.NotContains(t, out, "https://")
	assert.Contains(t, out, "data:font/woff2;base64,"+b64("W2"))
	assert.Contains(t, out, `src="data:image/png;base64,`+b64("PNG")+`"`)
	assert.Contains(t, out, "data:image/png;base64,"+b64("PNG2")+" 2x")
}

// TestBuild_RefusedAddressesStayAndAreNamed: a fetch the guard refuses, a
// missing file and a file past the bound are left as their URLs and named.
func TestBuild_RefusedAddressesStayAndAreNamed(t *testing.T) {
	doc := `<html><body><img src="http://127.0.0.1/secret.png"><img src="http://169.254.169.254/latest/meta-data/">
<img src="https://cdn.example.org/huge.png"><img src="https://cdn.example.org/missing.png"><img src="logo.png"></body></html>`
	refused := errors.New("egress guard: address 127.0.0.1 is loopback")
	ext := map[string]file{
		"http://127.0.0.1/secret.png":              {err: refused},
		"http://169.254.169.254/latest/meta-data/": {err: errors.New("egress guard: address 169.254.169.254 is link-local")},
		"https://cdn.example.org/huge.png":         {body: strings.Repeat("x", 64), ct: "image/png"},
	}
	res, err := exportDoc(t, doc, "text/html", fixtures{own: nil, ext: ext, limits: Limits{PerFile: 16}})
	require.NoError(t, err)
	out := string(res.HTML)
	for _, u := range []string{"http://127.0.0.1/secret.png", "http://169.254.169.254/latest/meta-data/", "https://cdn.example.org/huge.png", "https://cdn.example.org/missing.png", "logo.png"} {
		assert.Contains(t, out, u, "left as its URL")
		assert.True(t, noticed(res, u), "named in a notice: %s (%v)", u, res.Notices)
	}
}

func noticed(res *result, u string) bool {
	for _, n := range res.Notices {
		if n.URL == u {
			return true
		}
	}
	return false
}

// TestBuild_DeclaredReferencesAreInlinedAndDataIsRefused: a declared logo is
// inlined; a declared CSV the document fetches is a platform read.
func TestBuild_DeclaredReferencesAreInlinedAndDataIsRefused(t *testing.T) {
	own := map[string]file{
		"/portal/refs/a1/tok-logo": {body: "<svg/>", ct: "image/svg+xml"},
		"/portal/refs/a1/tok-csv":  {body: "region,total\nnorth,1\n", ct: "text/csv"},
	}
	logo := `<html><body><img src="http://platform.example/portal/refs/a1/tok-logo"></body></html>`
	res, err := exportDoc(t, logo, "text/html", fixtures{own: own, ext: nil, limits: Limits{}})
	require.NoError(t, err)
	assert.Contains(t, string(res.HTML), "data:image/svg+xml;base64,"+b64("<svg/>"))
	assert.NotContains(t, string(res.HTML), "tok-logo")

	dashboard := `<html><body><script>fetch("http://platform.example/portal/refs/a1/tok-csv").then(r => r.text())</script></body></html>`
	_, err = exportDoc(t, dashboard, "text/html", fixtures{own: own, ext: nil, limits: Limits{}})
	var data *PlatformDataError
	require.ErrorAs(t, err, &data)
	assert.Equal(t, []string{"it loads a text/csv file from the platform while it renders"}, data.Reasons)
}

// TestBuild_FormsAndTheBasemapArchiveAreRefused before anything is fetched.
func TestBuild_FormsAndTheBasemapArchiveAreRefused(t *testing.T) {
	cases := map[string]string{
		"a form reference":              `<div>mcp:form:signup</div>`,
		"a rewritten form":              `<iframe src="http://platform.example/forms/signup?embed=x"></iframe>`,
		"the basemap archive":           `<script>const REGION = "/portal/maps/us.pmtiles";</script>`,
		"an endpoint reference (#2457)": `<script>fetch("mcp:endpoint:log-search")</script>`,
		"a rewritten endpoint (#2457)":  `<script>fetch("http://platform.example/endpoints/log-search?endpoint_embed=m")</script>`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := exportDoc(t, doc, "text/html", fixtures{own: nil, ext: nil, limits: Limits{}})
			var data *PlatformDataError
			require.ErrorAs(t, err, &data)
			assert.NotEmpty(t, data.Reasons)
			assert.Empty(t, res.own.asked)
			assert.Empty(t, res.ext.asked)
		})
	}
}

// TestBuild_PublishedDataRegionNeedsNothing: numbers spliced into an id="data"
// region are inline already.
func TestBuild_PublishedDataRegionNeedsNothing(t *testing.T) {
	doc := `<html><body><script type="application/json" id="data">{"total": 42, "url": "https://example.org/x"}</script><p id="n"></p>
<script>document.getElementById('n').textContent = JSON.parse(document.getElementById('data').textContent).total</script></body></html>`
	res, err := exportDoc(t, doc, "text/html", fixtures{own: nil, ext: nil, limits: Limits{}})
	require.NoError(t, err)
	assert.Contains(t, string(res.HTML), `{"total": 42, "url": "https://example.org/x"}`)
	assert.Empty(t, res.ext.asked, "a JSON island's text is data, not a script to read URLs from")
}

// TestBuild_DownloadLinkCarriesTheFile and a plain link stays a link.
func TestBuild_DownloadLinkCarriesTheFile(t *testing.T) {
	own := map[string]file{"/portal/refs/a1/tok?download=1": {body: "a,b\n", ct: "text/csv"}}
	doc := `<html><body><a href="http://platform.example/portal/refs/a1/tok?download=1">csv</a><a href="https://example.org/about">about</a></body></html>`
	res, err := exportDoc(t, doc, "text/html", fixtures{own: own, ext: nil, limits: Limits{}})
	require.NoError(t, err)
	assert.Contains(t, string(res.HTML), `href="data:text/csv;base64,`+b64("a,b\n")+`"`)
	assert.Contains(t, string(res.HTML), `href="https://example.org/about"`)
	assert.Empty(t, res.ext.asked)
}

// TestBuild_BudgetsBoundTheExport: past the file count, a file is not fetched
// and is named.
func TestBuild_BudgetsBoundTheExport(t *testing.T) {
	ext := map[string]file{
		"https://img.example.org/1.png": {body: "1", ct: "image/png"},
		"https://img.example.org/2.png": {body: "2", ct: "image/png"},
		"https://img.example.org/3.png": {body: "3", ct: "image/png"},
	}
	doc := `<img src="https://img.example.org/1.png"><img src="https://img.example.org/2.png"><img src="https://img.example.org/3.png">`
	res, err := exportDoc(t, doc, "text/html", fixtures{own: nil, ext: ext, limits: Limits{Files: 2}})
	require.NoError(t, err)
	assert.Len(t, res.ext.asked, 2)
	assert.True(t, noticed(res, "https://img.example.org/3.png"))

	res, err = exportDoc(t, doc, "text/html", fixtures{own: nil, ext: ext, limits: Limits{Total: 2}})
	require.NoError(t, err)
	assert.True(t, noticed(res, "https://img.example.org/3.png"), "past the total: %v", res.Notices)
}

// fakeVendor is a stand-in for the UI build's vendor modules: each exports
// what the test asset uses, and the React entry points share one module.
var fakeVendor = map[string]file{
	jsxVendorPath + "react-all.mjs":         {body: `const React={createElement:(t,p)=>({t,p}),useState:v=>[v,()=>{}]};export const React_=React;export{React as React};export const ReactDOMClient={createRoot:el=>({render:v=>{window.__rendered=v}})};export const JSXRuntime={jsx:(t,p)=>({t,p}),jsxs:(t,p)=>({t,p}),Fragment:"f"};`, ct: "text/javascript"},
	jsxVendorPath + "react.mjs":             {body: `import {React as ns} from "platform-react-all";export default ns;export const {createElement,useState}=ns;`, ct: "text/javascript"},
	jsxVendorPath + "react-dom-client.mjs":  {body: `import {ReactDOMClient as ns} from "platform-react-all";export default ns;export const {createRoot}=ns;`, ct: "text/javascript"},
	jsxVendorPath + "react-jsx-runtime.mjs": {body: `import {JSXRuntime as ns} from "platform-react-all";export default ns;export const {jsx,jsxs,Fragment}=ns;`, ct: "text/javascript"},
	jsxVendorPath + "recharts.mjs":          {body: `import {createElement} from "react";export const LineChart=p=>createElement("svg",p);export const Unused=()=>"recharts-unused-marker";`, ct: "text/javascript"},
	jsxVendorPath + "lucide-react.mjs":      {body: `export const Star=()=>"star";export const Moon=()=>"lucide-unused-marker";`, ct: "text/javascript"},
}

// TestBuild_JSXCompilesWithTheServedLibraries: the component is compiled and
// bundled with the platform's modules, mounted as the viewer mounts it, its
// declared logo inlined, and nothing it does not use is carried.
func TestBuild_JSXCompilesWithTheServedLibraries(t *testing.T) {
	own := map[string]file{"/portal/refs/a1/tok-logo": {body: "PNG", ct: "image/png"}}
	maps.Copy(own, fakeVendor)
	src := `import { useState } from "react";
import { LineChart } from "recharts";
import { Star } from "lucide-react";
export default function Dashboard() {
  const [n] = useState(3);
  return <div><img src="http://platform.example/portal/refs/a1/tok-logo" /><LineChart data={[n]} /><Star /></div>;
}`
	res, err := exportDoc(t, src, "text/jsx", fixtures{own: own, ext: nil, limits: Limits{}})
	require.NoError(t, err)
	out := string(res.HTML)
	assert.Empty(t, res.ext.asked)
	assert.Contains(t, out, "<title>Quarterly &lt;deck&gt;</title>")
	assert.Contains(t, out, `<div id="root"></div>`)
	assert.Contains(t, out, "data:image/png;base64,"+b64("PNG"))
	assert.NotContains(t, out, "esm.sh")
	assert.NotContains(t, out, "import ", "one classic script, nothing left to resolve")
	assert.NotContains(t, out, "recharts-unused-marker", "what the asset does not use is not carried")
	assert.NotContains(t, out, "lucide-unused-marker")
	assert.Contains(t, out, "function showError")
}

// TestBuild_JSXImportingAnythingElseDoesNotBuild, as it does not render in
// the viewer.
func TestBuild_JSXImportingAnythingElseDoesNotBuild(t *testing.T) {
	src := `import _ from "lodash"; export default function A() { return <p>{_.x}</p>; }`
	_, err := exportDoc(t, src, "text/jsx", fixtures{own: fakeVendor, ext: nil, limits: Limits{}})
	var be *BuildError
	require.ErrorAs(t, err, &be)
	assert.Contains(t, be.Error(), `imports "lodash"`)
}

// TestBuild_JSXModuleImportedByURLIsBundled: a library imported from esm.sh,
// and the module it imports by root-relative path, are fetched through the
// external fetcher and bundled; a bare react inside it is the served React;
// and the import is not reported as a file left behind.
func TestBuild_JSXModuleImportedByURLIsBundled(t *testing.T) {
	ext := map[string]file{
		"https://esm.sh/flowlib@1.0.0":                    {body: `export { draw } from "/flowlib@1.0.0/es2022/flowlib.mjs";`, ct: "application/javascript"},
		"https://esm.sh/flowlib@1.0.0/es2022/flowlib.mjs": {body: `import { createElement } from "react"; export const draw = () => createElement("i", null, "flowlib-drawn");`, ct: "application/javascript"},
	}
	src := `import { draw } from "https://esm.sh/flowlib@1.0.0";
export default function Flow() { return <div>{draw()}</div>; }`
	res, err := exportDoc(t, src, "text/jsx", fixtures{own: fakeVendor, ext: ext, limits: Limits{}})
	require.NoError(t, err)
	assert.Contains(t, string(res.HTML), "flowlib-drawn")
	assert.Empty(t, res.Notices, "an imported module is bundled, not left behind")
	assert.ElementsMatch(t, []string{"https://esm.sh/flowlib@1.0.0", "https://esm.sh/flowlib@1.0.0/es2022/flowlib.mjs"}, res.ext.asked)

	_, err = exportDoc(t, `import x from "http://cdn.example.org/x.js"; export default function A(){ return <p>{x}</p> }`, "text/jsx", fixtures{own: fakeVendor, ext: nil, limits: Limits{}})
	var be *BuildError
	require.ErrorAs(t, err, &be, "a plain-http import is not fetched")
}

func TestBuild_OtherTypesAreNotExported(t *testing.T) {
	_, err := exportDoc(t, "a,b", "text/csv", fixtures{own: nil, ext: nil, limits: Limits{}})
	assert.ErrorIs(t, err, ErrUnsupported)
}

func TestComponentName_MatchesTheViewer(t *testing.T) {
	assert.Equal(t, "App", componentName("export default function App() {}"))
	assert.Equal(t, "Board", componentName("const Board = () => null;\nexport default Board;"))
	assert.Equal(t, "Last", componentName("function First(){}\nconst Last = () => 1"))
	assert.Equal(t, "", componentName("const x = 1"))
}

func TestLimitsDefaults(t *testing.T) {
	assert.Equal(t, DefaultLimits, Limits{}.withDefaults())
	got := Limits{PerFile: 1, Total: 2, Files: 3, Timeout: time.Second, FileTimeout: time.Millisecond}.withDefaults()
	assert.Equal(t, Limits{PerFile: 1, Total: 2, Files: 3, Timeout: time.Second, FileTimeout: time.Millisecond}, got)
}

// TestBuild_StylesheetImportsAreFollowedToABound: an @import is replaced by
// the stylesheet it names, wrapped in its media list; one that cannot be
// fetched stays; one past the depth bound stays and is named.
func TestBuild_StylesheetImportsAreFollowedToABound(t *testing.T) {
	own := map[string]file{
		"/portal/vendor/a.css": {body: `@import "b.css" print; .a{}`, ct: "text/css"},
		"/portal/vendor/b.css": {body: `@import url("c.css"); .b{}`, ct: "text/css"},
		"/portal/vendor/c.css": {body: `@import 'd.css'; .c{}`, ct: "text/css"},
		"/portal/vendor/d.css": {body: `@import "e.css"; .d{}`, ct: "text/css"},
		"/portal/vendor/e.css": {body: `@import "f.css"; .e{}`, ct: "text/css"},
	}
	doc := `<html><head><style>@import "/portal/vendor/a.css"; @import "/portal/vendor/missing.css";</style></head></html>`
	res, err := exportDoc(t, doc, "text/html", fixtures{own: own})
	require.NoError(t, err)
	out := string(res.HTML)
	assert.Contains(t, out, "@media print{")
	assert.Contains(t, out, ".a{}")
	assert.Contains(t, out, ".d{}")
	assert.Contains(t, out, `@import "/portal/vendor/missing.css";`)
	assert.True(t, noticed(res, "e.css"), "the import past the bound is named: %v", res.Notices)
}

// TestBuild_FramesSVGImagesAndNamespacedLinks: a framed page is named, an SVG
// <image> by xlink:href is inlined, and a download link that cannot be fetched
// stays and is named.
func TestBuild_FramesSVGImagesAndNamespacedLinks(t *testing.T) {
	ext := map[string]file{"https://img.example.org/p.png": {body: "P", ct: "image/png"}}
	doc := `<html><body><iframe src="https://maps.example.org/embed"></iframe>
<svg xmlns:xlink="http://www.w3.org/1999/xlink"><image xlink:href="https://img.example.org/p.png"/></svg>
<a href="http://platform.example/portal/refs/a1/gone" download>gone</a>
<a href="http://platform.example/portal/refs/a1/plain">plain</a></body></html>`
	res, err := exportDoc(t, doc, "text/html", fixtures{ext: ext})
	require.NoError(t, err)
	out := string(res.HTML)
	assert.True(t, noticed(res, "https://maps.example.org/embed"))
	assert.Contains(t, out, "data:image/png;base64,"+b64("P"))
	assert.True(t, noticed(res, "a declared file on the platform"), "a reference is named without its token: %v", res.Notices)
	assert.Contains(t, out, `href="http://platform.example/portal/refs/a1/plain"`, "a plain link stays a link")
	for _, n := range res.Notices {
		assert.NotContains(t, n.URL, "/portal/refs/", "a notice never repeats a reference's capability")
	}
}

// TestBuild_ScriptLiteralsInlineStylesheetsAndNameOtherFiles: a quoted
// stylesheet URL in a script becomes a data: URI with its own fonts inlined,
// and a quoted URL to a file that is neither shown nor imported is named.
func TestBuild_ScriptLiteralsInlineStylesheetsAndNameOtherFiles(t *testing.T) {
	ext := map[string]file{
		"https://fonts.example.org/f.css":   {body: "@font-face{src:url(f.woff2)}", ct: "text/css"},
		"https://fonts.example.org/f.woff2": {body: "W", ct: "font/woff2"},
		"https://api.example.org/data.json": {body: "{}", ct: "application/json"},
	}
	doc := `<html><body><script>
const link = document.createElement("link"); link.href = "https://fonts.example.org/f.css";
fetch('https://api.example.org/data.json');
</script></body></html>`
	res, err := exportDoc(t, doc, "text/html", fixtures{ext: ext})
	require.NoError(t, err)
	out := string(res.HTML)
	assert.Contains(t, out, "data:text/css;base64,")
	assert.NotContains(t, out, "https://fonts.example.org/f.css")
	assert.Contains(t, out, "https://api.example.org/data.json")
	assert.True(t, noticed(res, "https://api.example.org/data.json"))
}

// TestBuild_JSXModuleFailuresDoNotBuild: a module by URL that cannot be
// fetched, and a served library the build answers with a page, stop the
// build naming why; a stylesheet module is loaded as CSS.
func TestBuild_JSXModuleFailuresDoNotBuild(t *testing.T) {
	_, err := exportDoc(t, `import x from "https://esm.sh/gone@1"; export default function A(){ return <p>{x}</p> }`, "text/jsx", fixtures{own: fakeVendor})
	var be *BuildError
	require.ErrorAs(t, err, &be)
	assert.Contains(t, be.Error(), "esm.sh/gone@1")

	broken := map[string]file{}
	maps.Copy(broken, fakeVendor)
	broken[jsxVendorPath+"lucide-react.mjs"] = file{body: "<!doctype html>", ct: "text/html; charset=utf-8"}
	_, err = exportDoc(t, `import { Star } from "lucide-react"; export default function A(){ return <Star /> }`, "text/jsx", fixtures{own: broken})
	require.ErrorAs(t, err, &be)
	assert.Contains(t, be.Error(), "does not serve")

	ext := map[string]file{
		"https://esm.sh/lib@1":      {body: `import "./style.css"; import "../shared.mjs"; export const v = "lib-v";`, ct: "application/javascript"},
		"https://esm.sh/style.css":  {body: ".lib{color:red}", ct: "text/css"},
		"https://esm.sh/shared.mjs": {body: "export const s = 1;", ct: "application/javascript"},
	}
	res, err := exportDoc(t, `import { v } from "https://esm.sh/lib@1"; export default function A(){ return <p>{v}</p> }`, "text/jsx", fixtures{own: fakeVendor, ext: ext})
	require.NoError(t, err)
	assert.Contains(t, string(res.HTML), "lib-v")
	assert.Contains(t, string(res.HTML), ".lib{color:red}", "a library's stylesheet is added by the script")

	_, err = exportDoc(t, `import x from "./local.js"; export default function A(){ return <p>{x}</p> }`, "text/jsx", fixtures{own: fakeVendor})
	require.ErrorAs(t, err, &be, "a relative import from the asset resolves to nothing")

	bare := map[string]file{"https://esm.sh/needs-lodash@1": {body: `import _ from "lodash"; export const v = _;`, ct: "application/javascript"}}
	_, err = exportDoc(t, `import { v } from "https://esm.sh/needs-lodash@1"; export default function A(){ return <p>{v}</p> }`, "text/jsx", fixtures{own: fakeVendor, ext: bare})
	require.ErrorAs(t, err, &be, "a bare import inside a fetched module is not fetched")
	assert.Contains(t, be.Error(), `"lodash"`)
}

func TestHelpers(t *testing.T) {
	assert.Equal(t, "data:application/octet-stream;base64,"+b64("x"), dataURI("", []byte("x")))
	assert.Contains(t, (&PlatformDataError{Reasons: []string{"a", "b"}}).Error(), "a; b")
	assert.NotContains(t, originPath(nil, "/forms/"), "/forms/x")
	assert.Equal(t, "http://h/forms/", originPath(&url.URL{Scheme: "http", Host: "h"}, "/forms/"))
	assert.Equal(t, "first", errorText(errors.New("first\nsecond")))
	long := errorText(errors.New(strings.Repeat("x", 300)))
	assert.True(t, strings.HasSuffix(long, "...") && len(long) < 200)
	assert.Equal(t, "https://cdn.example.org/a.js", displayURL(&url.URL{Scheme: "https", Host: "cdn.example.org", Path: "/a.js"}, placeExternal))
}

// TestDataURI_CarriesOnlyAWellFormedMediaType: a Content-Type a host chose
// cannot end the quoted string the data: URI is written in.
func TestDataURI_CarriesOnlyAWellFormedMediaType(t *testing.T) {
	assert.Equal(t, "data:image/svg+xml;base64,"+b64("x"), dataURI("image/svg+xml; charset=utf-8", []byte("x")))
	for _, hostile := range []string{`image/png");}body{background:red`, `image/png"`, "image/png'", "text/css)", "a b/c", `image/png\`, "text/javascript\x60"} {
		got := dataURI(hostile, []byte("x"))
		assert.Equal(t, "data:application/octet-stream;base64,"+b64("x"), got, hostile)
	}
}

// TestBuild_AHostileContentTypeCannotBreakOutOfTheFile: through the whole
// export, a host answering with a quote in its Content-Type changes neither
// the stylesheet nor the script the URI lands in.
func TestBuild_AHostileContentTypeCannotBreakOutOfTheFile(t *testing.T) {
	ext := map[string]file{"https://evil.example.org/x.png": {body: "P", ct: `image/png");}*{color:red}/*`}}
	doc := `<html><head><style>.a{background:url("https://evil.example.org/x.png")}</style></head>
<body><script>const i = "https://evil.example.org/x.png";</script></body></html>`
	res, err := exportDoc(t, doc, "text/html", fixtures{ext: ext})
	require.NoError(t, err)
	out := string(res.HTML)
	assert.NotContains(t, out, "color:red")
	assert.Contains(t, out, `url("data:application/octet-stream;base64,`+b64("P")+`")`)
}
