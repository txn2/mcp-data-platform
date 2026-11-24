// Package sitebuild compiles the TypeScript, TSX and JSX of a site's source tree
// into the JavaScript it serves (#2774). A release opts in with a _build.json at
// its root; each entry it names is bundled with the local modules it imports
// into one ES module beside it, and its imports of libraries go to the exact
// version URLs the file maps them to. The compile reads only the tree, and the
// network only when the release asks for its URL modules to be bundled.
package sitebuild

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// ControlFile is the file whose presence turns the compile on. It is source:
// read at publish and never served.
const ControlFile = "_build.json"

// The two answers to what a URL import becomes.
const (
	// RemoteExternal leaves a URL import a URL, for the browser to load.
	RemoteExternal = "external"
	// RemoteBundle fetches it at publish and bundles it into the output.
	RemoteBundle = "bundle"
)

// What one control file may declare.
const (
	maxEntries = 100
	maxImports = 500
	maxKeyLen  = 256
	maxURLLen  = 2048
)

// The extensions of the files a build reads, and the separators of a path.
const (
	extTS   = ".ts"
	extTSX  = ".tsx"
	extJSX  = ".jsx"
	extMTS  = ".mts"
	extCTS  = ".cts"
	extJS   = ".js"
	extMJS  = ".mjs"
	extJSON = ".json"
	extCSS  = ".css"
	sep     = "/"
	dot     = "."
)

// sourceExts are the files that are compiled rather than served.
var sourceExts = []string{extTS, extTSX, extJSX, extMTS, extCTS}

// IsSource reports whether a release path is compiled source, which a release
// with a control file never serves.
func IsSource(name string) bool {
	return name == ControlFile || slices.Contains(sourceExts, strings.ToLower(path.Ext(name)))
}

// OutputOf is the file an entry compiles to: the same path ending in .js.
func OutputOf(entry string) string {
	return strings.TrimSuffix(entry, path.Ext(entry)) + extJS
}

// Config is what a control file declares.
type Config struct {
	// Entries are the files compiled, each to its own output.
	Entries []string `json:"entries"`
	// Imports maps a bare specifier, or a prefix of one ending in /, to the
	// exact-version URL it is loaded from.
	Imports map[string]string `json:"imports"`
	// Remote is RemoteExternal (the default) or RemoteBundle.
	Remote string `json:"remote"`
	// Sourcemap writes <output>.map beside each output, holding the source.
	Sourcemap bool `json:"sourcemap"`
	// Minify shortens the output.
	Minify bool `json:"minify"`
}

// pinnedURL is an https URL naming an exact version: name@1.2.3, with an
// optional pre-release or build suffix, then the end of the path, a / or a
// query.
var pinnedURL = regexp.MustCompile(`^https://[^/?#@]+/.*@v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?(?:[/?#]|$)`)

// Pinned reports whether a URL may be imported: https, at an exact version, so
// that what it loads cannot change under a release.
func Pinned(u string) bool {
	return len(u) <= maxURLLen && pinnedURL.MatchString(u)
}

// Parse reads a control file, refusing anything it cannot honor.
func Parse(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	cfg := &Config{}
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("not a build file: %w", err)
	}
	if dec.More() {
		return nil, errors.New("not a build file: more than one JSON value")
	}
	if cfg.Remote == "" {
		cfg.Remote = RemoteExternal
	}
	if cfg.Remote != RemoteExternal && cfg.Remote != RemoteBundle {
		return nil, fmt.Errorf("remote is %q; it is %q or %q", cfg.Remote, RemoteExternal, RemoteBundle)
	}
	if err := checkEntries(cfg.Entries); err != nil {
		return nil, err
	}
	if err := checkImports(cfg.Imports); err != nil {
		return nil, err
	}
	return cfg, nil
}

func checkEntries(entries []string) error {
	switch {
	case len(entries) == 0:
		return errors.New(`entries is empty; name the files to compile, such as ["app.tsx"]`)
	case len(entries) > maxEntries:
		return fmt.Errorf("entries names %d files, more than the %d allowed", len(entries), maxEntries)
	}
	outputs := make(map[string]string, len(entries))
	for _, e := range entries {
		if !fs.ValidPath(e) || e == "." {
			return fmt.Errorf("entry %q is not a path in the site", e)
		}
		if !slices.Contains(sourceExts, strings.ToLower(path.Ext(e))) {
			return fmt.Errorf("entry %q is not TypeScript or JSX (%s)", e, strings.Join(sourceExts, ", "))
		}
		out := OutputOf(e)
		if other, taken := outputs[out]; taken {
			return fmt.Errorf("entries %q and %q both compile to %s", other, e, out)
		}
		outputs[out] = e
	}
	return nil
}

func checkImports(imports map[string]string) error {
	if len(imports) > maxImports {
		return fmt.Errorf("imports maps %d names, more than the %d allowed", len(imports), maxImports)
	}
	for _, key := range sortedKeys(imports) {
		u := imports[key]
		switch {
		case key == "" || len(key) > maxKeyLen || !isBare(key):
			return fmt.Errorf("imports: %q is not a package name; local files are imported by a path starting ./", key)
		case !Pinned(u):
			return fmt.Errorf("imports: %q maps to %q, which is not an https URL at an exact version, such as https://esm.sh/react@18.3.1", key, u)
		case strings.HasSuffix(key, sep) != strings.HasSuffix(u, sep):
			return fmt.Errorf("imports: %q and %q must both end in / or neither", key, u)
		}
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// isBare reports a specifier that names a package: not a path and not a URL.
func isBare(spec string) bool {
	return !strings.HasPrefix(spec, dot) && !strings.HasPrefix(spec, sep) && !strings.Contains(spec, ":")
}

// mapped is the URL a bare specifier is loaded from: its own entry, the longest
// prefix entry ending in / that holds it, or, for a path inside a package whose
// root is mapped, that URL with the path added (react/jsx-runtime from react).
func (c *Config) mapped(spec string) (string, bool) {
	if u, ok := c.Imports[spec]; ok {
		return u, true
	}
	best := ""
	for key := range c.Imports {
		if strings.HasSuffix(key, sep) && strings.HasPrefix(spec, key) && len(key) > len(best) {
			best = key
		}
	}
	if best != "" {
		return c.Imports[best] + strings.TrimPrefix(spec, best), true
	}
	pkg, sub := packageOf(spec)
	if u, ok := c.Imports[pkg]; ok && sub != "" && !strings.ContainsAny(u, "?#") {
		return strings.TrimSuffix(u, sep) + sep + sub, true
	}
	return "", false
}

// packageOf splits a bare specifier into its package and the path inside it:
// a scoped package (@scope/name) takes two segments, any other one.
func packageOf(spec string) (pkg, sub string) {
	first, rest, _ := strings.Cut(spec, sep)
	if !strings.HasPrefix(spec, "@") || rest == "" {
		return first, rest
	}
	name, sub, _ := strings.Cut(rest, sep)
	return first + sep + name, sub
}
