package standalone

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// jsxVendorPath is where the UI build serves the libraries a JSX asset
// imports (ui/vite.config.ts jsxVendorFiles).
const jsxVendorPath = vendorPrefix + "jsx/"

// vendorModules maps each bare specifier a JSX asset may import, as the
// viewer's import map names them (JsxRenderer.tsx BARE_IMPORT_MAP), to the
// file serving it. platform-react-all is the one React instance the four React
// entry points are built over.
var vendorModules = map[string]string{
	"react":              "react.mjs",
	"react-dom":          "react-dom.mjs",
	"react-dom/client":   "react-dom-client.mjs",
	"react/jsx-runtime":  "react-jsx-runtime.mjs",
	"recharts":           "recharts.mjs",
	"lucide-react":       "lucide-react.mjs",
	"platform-react-all": "react-all.mjs",
}

const (
	vendorNamespace = "platform-vendor"
	// urlNamespace holds a module imported by URL, as a JSX asset imports any
	// library beyond the four from esm.sh, and the modules it imports in turn.
	urlNamespace = "platform-url"
)

// Patterns read off a JSX asset, the same ones the viewer reads
// (JsxRenderer.tsx findComponentName and hasMountCode), so the file mounts
// what the viewer mounts.
var (
	namedDefault = regexp.MustCompile(`export\s+default\s+(?:function|class)\s+([A-Z][A-Za-z0-9]*)`)
	reExport     = regexp.MustCompile(`export\s+default\s+([A-Z][A-Za-z0-9]*)\s*;`)
	declaration  = regexp.MustCompile(`(?:function|const|let|class)\s+([A-Z][A-Za-z0-9]*)`)
	mountCall    = regexp.MustCompile(`\bcreateRoot\s*\(|\bReactDOM\s*\.\s*render\s*\(`)
)

// componentName is the component the viewer would mount, or "".
func componentName(code string) string {
	if m := namedDefault.FindStringSubmatch(code); m != nil {
		return m[1]
	}
	if m := reExport.FindStringSubmatch(code); m != nil {
		return m[1]
	}
	all := declaration.FindAllStringSubmatch(code, -1)
	if len(all) > 0 {
		return all[len(all)-1][1]
	}
	return ""
}

// errorStyle and showErrorFn are the viewer's own error display.
const errorStyle = "color:#ef4444;background:#1e1e1e;padding:16px;font-size:13px;white-space:pre-wrap;overflow:auto;height:100%;font-family:monospace"

const showErrorFn = `function showError(text, tag, style) {
  var el = document.createElement(tag || 'pre');
  el.setAttribute('style', style || '` + errorStyle + `');
  el.textContent = text;
  var root = document.getElementById('root');
  root.textContent = '';
  root.appendChild(el);
}
window.onerror = function(msg, src, line, col, err) { showError(err && err.stack ? err.stack : msg); };`

// jsx compiles a JSX asset with the libraries it imports into one file.
func (s *session) jsx(ctx context.Context, source, title string) ([]byte, error) {
	source = s.literals(ctx, source)
	entry := source
	if !mountCall.MatchString(source) {
		mount := "showError('No component found. Use export default function MyComponent() {...}', 'p', 'color:#f59e0b;padding:16px;font-family:system-ui');"
		if name := componentName(source); name != "" {
			mount = fmt.Sprintf("__artifactCreateRoot(document.getElementById('root')).render(__artifactReact.createElement(%s));", name)
		}
		entry = "import * as __artifactReact from 'react';\n" +
			"import { createRoot as __artifactCreateRoot } from 'react-dom/client';\n" +
			source + "\ntry {\n  " + mount + "\n} catch (e) {\n  showError(e.stack || e.message);\n}\n"
	}
	result := api.Build(api.BuildOptions{
		Stdin:             &api.StdinOptions{Contents: entry, Loader: api.LoaderJSX, Sourcefile: "asset.jsx"},
		Bundle:            true,
		Write:             false,
		Format:            api.FormatIIFE,
		Platform:          api.PlatformBrowser,
		Target:            api.ES2020,
		JSX:               api.JSXAutomatic,
		MinifyWhitespace:  true,
		MinifySyntax:      true,
		MinifyIdentifiers: true,
		LegalComments:     api.LegalCommentsNone,
		Define:            map[string]string{"process.env.NODE_ENV": `"production"`},
		LogLevel:          api.LogLevelSilent,
		Plugins:           []api.Plugin{s.vendorPlugin(ctx)},
	})
	if len(result.Errors) > 0 {
		return nil, &BuildError{Messages: buildMessages(result.Errors)}
	}
	if len(result.OutputFiles) == 0 {
		return nil, &BuildError{Messages: []string{"the build produced no output"}}
	}
	bundle := scriptClose.ReplaceAllString(string(result.OutputFiles[0].Contents), `<\/script`)
	return []byte(strings.Join([]string{
		"<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"UTF-8\">\n",
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n",
		"<title>", html.EscapeString(title), "</title>\n",
		"<style>\n* { margin: 0; padding: 0; box-sizing: border-box; }\nbody { font-family: system-ui, sans-serif; padding: 16px; }\n</style>\n",
		"</head>\n<body>\n<div id=\"root\"></div>\n<script>\n", showErrorFn, "\n</script>\n",
		"<script>\n", bundle, "\n</script>\n</body>\n</html>\n",
	}, "")), nil
}

// moduleURL is the URL an import names when it is fetched rather than served:
// an https URL the asset or a fetched module names, or a path a fetched module
// names relative to its own URL (esm.sh's modules import one another by
// root-relative path).
func moduleURL(args api.OnResolveArgs) (string, bool) {
	if strings.HasPrefix(args.Path, "https://") {
		return args.Path, true
	}
	if args.Namespace != urlNamespace {
		return "", false
	}
	if !strings.HasPrefix(args.Path, "/") && !strings.HasPrefix(args.Path, "./") && !strings.HasPrefix(args.Path, "../") {
		return "", false
	}
	base, err := url.Parse(args.Importer)
	if err != nil {
		return "", false
	}
	ref, err := url.Parse(args.Path)
	if err != nil {
		return "", false
	}
	return base.ResolveReference(ref).String(), true
}

// BuildError is a JSX asset that does not compile, or imports a module the
// viewer does not provide.
type BuildError struct {
	Messages []string
}

func (e *BuildError) Error() string {
	return "the JSX asset does not build: " + strings.Join(e.Messages, "; ")
}

func buildMessages(msgs []api.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		text := m.Text
		if m.Location != nil {
			text = fmt.Sprintf("line %d: %s", m.Location.Line, text)
		}
		out = append(out, text)
	}
	return out
}

// vendorPlugin resolves the libraries a JSX asset imports: the four the viewer
// provides to the files the platform serves, read through the session's own
// fetcher, and a module imported by URL (and every module it imports) through
// the egress guard, within the export's budget. Anything else is refused: the
// viewer resolves nothing else, so an asset importing it does not render there
// either.
func (s *session) vendorPlugin(ctx context.Context) api.Plugin {
	return api.Plugin{
		Name: "platform-vendor",
		Setup: func(b api.PluginBuild) {
			b.OnResolve(api.OnResolveOptions{Filter: `.*`}, resolveModule)
			b.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: urlNamespace}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				return s.loadURLModule(ctx, args)
			})
			b.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: vendorNamespace}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				return s.loadVendorModule(ctx, args)
			})
		},
	}
}

// resolveModule places each import: a library the viewer provides, a module
// by URL, or a refusal.
func resolveModule(args api.OnResolveArgs) (api.OnResolveResult, error) {
	if _, ok := vendorModules[args.Path]; ok {
		return api.OnResolveResult{Path: args.Path, Namespace: vendorNamespace}, nil
	}
	if args.Kind == api.ResolveEntryPoint {
		return api.OnResolveResult{}, nil
	}
	if u, ok := moduleURL(args); ok {
		return api.OnResolveResult{Path: u, Namespace: urlNamespace}, nil
	}
	return api.OnResolveResult{}, fmt.Errorf("imports %q, which the JSX viewer does not provide "+
		"(react, react-dom, recharts, lucide-react, or a module by its https URL)", args.Path)
}

// loadURLModule fetches a module imported by URL through the egress guard.
func (s *session) loadURLModule(ctx context.Context, args api.OnLoadArgs) (api.OnLoadResult, error) {
	u, err := url.Parse(args.Path)
	if err != nil {
		return api.OnLoadResult{}, fmt.Errorf("reading %s: %w", args.Path, err)
	}
	got := s.fetch(ctx, u, placeExternal)
	if got.err != nil {
		return api.OnLoadResult{}, fmt.Errorf("fetching %s: %w", args.Path, got.err)
	}
	contents := string(got.body)
	if isCSS(got.contentType) {
		// A library's own stylesheet is added to the document as it loads:
		// the file is one script, with no stylesheet beside it to emit.
		css, err := json.Marshal(s.css(ctx, u, contents, 0))
		if err != nil {
			return api.OnLoadResult{}, fmt.Errorf("reading %s: %w", args.Path, err)
		}
		contents = "const s = document.createElement('style'); s.textContent = " + string(css) + "; document.head.appendChild(s);"
	}
	return api.OnLoadResult{Contents: &contents, Loader: api.LoaderJS}, nil
}

// loadVendorModule reads a library the platform serves.
func (s *session) loadVendorModule(ctx context.Context, args api.OnLoadArgs) (api.OnLoadResult, error) {
	file := vendorModules[args.Path]
	got := s.fetch(ctx, &url.URL{Path: jsxVendorPath + file}, placeOwn)
	if got.err != nil {
		return api.OnLoadResult{}, fmt.Errorf("reading the platform's %s: %w", args.Path, got.err)
	}
	// A build without the UI answers an unknown /portal/ path with the
	// portal's own page; that is not the library.
	if baseType(got.contentType) == TypeHTML {
		return api.OnLoadResult{}, fmt.Errorf("this build of the platform does not serve %s", jsxVendorPath+file)
	}
	contents := string(got.body)
	return api.OnLoadResult{Contents: &contents, Loader: api.LoaderJS}, nil
}
