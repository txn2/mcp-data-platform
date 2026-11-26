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
// with a control file never serves: the TypeScript and JSX, the control file,
// and a project's package and tooling files (#2785).
func IsSource(name string) bool {
	return name == ControlFile || slices.Contains(sourceExts, strings.ToLower(path.Ext(name))) || IsProjectFile(name)
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
	// Sourcemap writes <output>.map beside each output, holding the source:
	// served to anyone (true), or only to the site's managers ("private").
	Sourcemap SourcemapMode `json:"sourcemap"`
	// Minify shortens the output.
	Minify bool `json:"minify"`
	// Splitting puts what an entry loads by import() in chunks of its own
	// under _chunks/, loaded when first used (#2778). Nil is on.
	Splitting *bool `json:"splitting"`
	// Define replaces each import.meta.env.X or process.env.X it names with
	// its JSON value (#2781).
	Define map[string]json.RawMessage `json:"define"`
	// Tailwind is the exact Tailwind CSS 4 version that compiles the
	// stylesheet holding @import "tailwindcss" (#2780), and TailwindInput
	// that stylesheet when it is not an entry's own.
	Tailwind      string `json:"tailwind"`
	TailwindInput string `json:"tailwind_input"`
	// Budgets warns when an entry's first load is larger than it should be
	// (#2783).
	Budgets map[string]Budget `json:"budgets"`
	// ImportsFrom generates the imports map from package.json and the lockfile
	// at every publish, the entries in Imports winning (#2785).
	ImportsFrom string `json:"imports_from"`
	// Singletons are packages only one copy of which may load, beside react and
	// react-dom.
	Singletons []string `json:"singletons"`
	// Exclude are globs of files kept as source and never served.
	Exclude []string `json:"exclude"`
	// Typecheck type-checks each release with TypeScript after it publishes
	// (#2793): a check, not a gate, reported on the release.
	Typecheck *Typecheck `json:"typecheck"`

	// localPrefixes are the Imports keys that map to a folder of the site.
	localPrefixes map[string]string
	excludeRes    []*regexp.Regexp
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

// Typecheck is a site's type check (#2793).
type Typecheck struct {
	// Tsconfig is the project file checked, a path in the site; empty is
	// tsconfig.json.
	Tsconfig string `json:"tsconfig"`
}

// DefaultTsconfig is the project file a type check reads when none is named.
const DefaultTsconfig = "tsconfig.json"

// TsconfigPath is the project file the check reads.
func (t *Typecheck) TsconfigPath() string {
	if t == nil || t.Tsconfig == "" {
		return DefaultTsconfig
	}
	return t.Tsconfig
}

// checkTypecheck refuses a typecheck block naming no path in the site.
func (c *Config) checkTypecheck() error {
	if c.Typecheck == nil {
		return nil
	}
	p := c.Typecheck.TsconfigPath()
	if !fs.ValidPath(p) || p == "." || !strings.HasSuffix(strings.ToLower(p), ".json") {
		return fmt.Errorf("typecheck.tsconfig %q is not a .json file in the site", p)
	}
	return nil
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
	if err := cfg.checkImports(); err != nil {
		return nil, err
	}
	if err := cfg.checkOptions(); err != nil {
		return nil, err
	}
	if err := cfg.checkTypecheck(); err != nil {
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

func (c *Config) checkImports() error {
	imports := c.Imports
	if len(imports) > maxImports {
		return fmt.Errorf("imports maps %d names, more than the %d allowed", len(imports), maxImports)
	}
	c.localPrefixes = map[string]string{}
	for _, key := range sortedKeys(imports) {
		if err := c.checkImport(key, imports[key]); err != nil {
			return err
		}
	}
	return nil
}

// checkImport refuses one imports entry it cannot honor, and records a prefix
// that maps to a folder of the site.
func (c *Config) checkImport(key, u string) error {
	switch {
	case key == "" || len(key) > maxKeyLen || !isBare(key):
		return fmt.Errorf("imports: %q is not a package name; local files are imported by a path starting ./", key)
	case strings.HasSuffix(key, sep) && !strings.HasPrefix(u, "https:"):
		if err := checkLocalPrefix(key, u); err != nil {
			return err
		}
		c.localPrefixes[key] = u
	case !Pinned(u):
		return fmt.Errorf("imports: %q maps to %q, which is not an https URL at an exact version, such as https://esm.sh/react@18.3.1", key, u)
	case strings.HasSuffix(key, sep) != strings.HasSuffix(u, sep):
		return fmt.Errorf("imports: %q and %q must both end in / or neither", key, u)
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

// mapped is where a bare specifier is loaded from: its own entry, the longest
// prefix entry ending in / that holds it, or, for a path inside a package whose
// root is mapped, that URL with the path added (react/jsx-runtime from react).
// local is true when a prefix maps it to a file of the site (#2781): the
// answer is then a path from the root.
func (c *Config) mapped(spec string) (target string, local, ok bool) {
	if u, found := c.Imports[spec]; found && c.localPrefixes[spec] == "" {
		return u, false, true
	}
	if best := c.longestPrefix(spec); best != "" {
		rest := strings.TrimPrefix(spec, best)
		if dir, isLocal := c.localPrefixes[best]; isLocal {
			return dir + rest, true, true
		}
		return c.Imports[best] + rest, false, true
	}
	pkg, sub := packageOf(spec)
	if u, found := c.Imports[pkg]; found && sub != "" && !strings.ContainsAny(u, "?#") {
		return strings.TrimSuffix(u, sep) + sep + sub, false, true
	}
	return "", false, false
}

// Unmapped is the specifiers of specs that imports does not resolve, by their
// own entry, a prefix entry or their package's entry (#2790). A specifier an
// entry written in imports resolves is not missing from a lockfile: that
// entry wins over what the lockfile would give, so an imports_from site gains
// a library with one line in the build file and no lockfile edit.
func (c *Config) Unmapped(specs []string) []string {
	out := []string{}
	for _, spec := range specs {
		if _, _, ok := c.mapped(spec); !ok {
			out = append(out, spec)
		}
	}
	return out
}

// tailwindSpecifier is the bare specifier a stylesheet compiled with Tailwind
// imports, "tailwindcss" or a path inside it. With tailwind set the
// stylesheet compiler answers it, and no import map entry is wanted (#2790).
func (c *Config) tailwindSpecifier(spec string) bool {
	return c.Tailwind != "" && (spec == "tailwindcss" || strings.HasPrefix(spec, "tailwindcss/"))
}

// longestPrefix is the longest imports key ending in / that holds spec.
func (c *Config) longestPrefix(spec string) string {
	best := ""
	for key := range c.Imports {
		if strings.HasSuffix(key, sep) && strings.HasPrefix(spec, key) && len(key) > len(best) {
			best = key
		}
	}
	return best
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
