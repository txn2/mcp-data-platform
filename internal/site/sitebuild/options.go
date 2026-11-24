package sitebuild

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ChunkDir is the folder the code an entry loads by import() is written to
// (#2778). Every name in it carries a hash of its content, so it is served to
// be cached for good, and a release cannot hold a file of its own there.
const ChunkDir = "_chunks"

// GuideFile is a site's guide for whoever works on it next (#2782): never
// served, versioned with the release, and handed to the agent working on it.
const GuideFile = "AGENTS.md"

// SourcemapMode is what a release does with source maps.
type SourcemapMode string

// The answers to sourcemap.
const (
	// SourcemapOff writes no maps.
	SourcemapOff SourcemapMode = ""
	// SourcemapPublic writes maps and serves them to anyone.
	SourcemapPublic SourcemapMode = "public"
	// SourcemapPrivate writes maps and serves them only to the site's
	// managers (#2784).
	SourcemapPrivate SourcemapMode = "private"
)

// UnmarshalJSON reads true, false or "private".
func (m *SourcemapMode) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true":
		*m = SourcemapPublic
	case "false", "null":
		*m = SourcemapOff
	case `"private"`:
		*m = SourcemapPrivate
	default:
		return fmt.Errorf(`sourcemap is %s; it is true, false or "private"`, data)
	}
	return nil
}

// MarshalJSON writes the form it was read from.
func (m SourcemapMode) MarshalJSON() ([]byte, error) {
	switch m {
	case SourcemapPublic:
		return []byte("true"), nil
	case SourcemapPrivate:
		return []byte(`"private"`), nil
	}
	return []byte("false"), nil
}

// Budget is how large an entry's first load may be before a publish warns.
// Each is a size such as "300KB" (1024 bytes to the KB) or a number of bytes.
type Budget struct {
	JS      Size `json:"js"`
	JSGzip  Size `json:"js_gzip"`
	CSS     Size `json:"css"`
	CSSGzip Size `json:"css_gzip"`
}

// Size is a number of bytes written as a number or as "300KB".
type Size int64

// sizeText is a size as a person writes it.
var sizeText = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*([KkMmGg]?[Bb]?)\s*$`)

// UnmarshalJSON reads a number of bytes or a size with a unit.
func (s *Size) UnmarshalJSON(data []byte) error {
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*s = Size(n)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("a size is a number of bytes or a string such as \"300KB\", not %s", data)
	}
	m := sizeText.FindStringSubmatch(text)
	if m == nil {
		return fmt.Errorf("a size is a number of bytes or a string such as \"300KB\", not %q", text)
	}
	v, err := strconv.ParseFloat(m[1], bitsOf64)
	if err != nil {
		return fmt.Errorf("reading the size %q: %w", text, err)
	}
	*s = Size(int64(v * float64(unitBytes[strings.TrimSuffix(strings.ToUpper(m[2]), "B")])))
	return nil
}

// bitsOf64 is the precision a size is parsed at.
const bitsOf64 = 64

// unitBytes is what each unit of a size is, at 1024 to the step.
var unitBytes = map[string]int64{"": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30}

// Bounds on what one control file may declare beside its entries and imports.
const (
	maxDefines    = 200
	maxGlobs      = 100
	maxSingletons = 50
	importsFromPM = "package.json"
)

// defineKey is a name a define replaces.
var defineKey = regexp.MustCompile(`^(?:import\.meta\.env|process\.env)\.[A-Za-z_$][A-Za-z0-9_$]*$`)

// tailwindVersion is an exact Tailwind CSS 4 version.
var tailwindVersion = regexp.MustCompile(`^4\.\d+\.\d+$`)

// packageName is a package as the registry names it.
var packageName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)

func (c *Config) checkOptions() error {
	for _, check := range []func() error{c.checkDefine, c.checkTailwind, c.checkBudgets, c.checkSingletons, c.checkExclude} {
		if err := check(); err != nil {
			return err
		}
	}
	if c.ImportsFrom != "" && c.ImportsFrom != importsFromPM {
		return fmt.Errorf("imports_from is %q; the one source it reads is %q", c.ImportsFrom, importsFromPM)
	}
	return nil
}

func (c *Config) checkDefine() error {
	if len(c.Define) > maxDefines {
		return fmt.Errorf("define names %d values, more than the %d allowed", len(c.Define), maxDefines)
	}
	for key, raw := range c.Define {
		if !defineKey.MatchString(key) {
			return fmt.Errorf("define: %q is not a name under import.meta.env. or process.env., such as import.meta.env.DEV", key)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("define: %q is not JSON: %w", key, err)
		}
		switch v.(type) {
		case string, float64, bool, nil:
		default:
			return fmt.Errorf("define: %q is %s; a value is a string, number, true, false or null", key, raw)
		}
	}
	return nil
}

func (c *Config) checkTailwind() error {
	if c.Tailwind == "" {
		if c.TailwindInput != "" {
			return errors.New("tailwind_input names a stylesheet, but tailwind names no version")
		}
		return nil
	}
	if !tailwindVersion.MatchString(c.Tailwind) {
		return fmt.Errorf("tailwind is %q; it is an exact Tailwind CSS 4 version, such as \"4.1.13\"", c.Tailwind)
	}
	if c.TailwindInput != "" && (!fs.ValidPath(c.TailwindInput) || !strings.EqualFold(path.Ext(c.TailwindInput), extCSS)) {
		return fmt.Errorf("tailwind_input %q is not a stylesheet of the site, such as \"src/app.css\"", c.TailwindInput)
	}
	return nil
}

func (c *Config) checkBudgets() error {
	for entry, b := range c.Budgets {
		if !slices.Contains(c.Entries, entry) {
			return fmt.Errorf("budgets names %q, which entries does not", entry)
		}
		if b.JS < 0 || b.JSGzip < 0 || b.CSS < 0 || b.CSSGzip < 0 {
			return fmt.Errorf("budgets: %q holds a negative size", entry)
		}
	}
	return nil
}

func (c *Config) checkSingletons() error {
	if len(c.Singletons) > maxSingletons {
		return fmt.Errorf("singletons names %d packages, more than the %d allowed", len(c.Singletons), maxSingletons)
	}
	for _, s := range c.Singletons {
		if !packageName.MatchString(s) {
			return fmt.Errorf("singletons: %q is not a package name", s)
		}
	}
	return nil
}

func (c *Config) checkExclude() error {
	if len(c.Exclude) > maxGlobs {
		return fmt.Errorf("exclude holds %d globs, more than the %d allowed", len(c.Exclude), maxGlobs)
	}
	c.excludeRes = make([]*regexp.Regexp, 0, len(c.Exclude))
	for _, g := range c.Exclude {
		if g == "" || len(g) > maxKeyLen || strings.HasPrefix(g, sep) || strings.Contains(g, "..") {
			return fmt.Errorf("exclude: %q is not a glob of paths in the site, such as \"e2e/**\"", g)
		}
		c.excludeRes = append(c.excludeRes, globRegexp(g))
	}
	return nil
}

// globRegexp is a glob as a whole-path pattern: ** crosses folders, * and ?
// do not.
func globRegexp(g string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^") //nolint:revive // a strings.Builder write never fails
	for i := 0; i < len(g); i++ {
		piece, skip := globPiece(g[i:])
		b.WriteString(piece) //nolint:revive // a strings.Builder write never fails
		i += skip
	}
	b.WriteString("$") //nolint:revive // a strings.Builder write never fails
	return regexp.MustCompile(b.String())
}

// globPiece is the pattern for the start of a glob, and how many bytes past
// the first it consumed.
func globPiece(rest string) (piece string, skip int) {
	switch {
	case strings.HasPrefix(rest, "**/"):
		return "(?:.*/)?", 2
	case strings.HasPrefix(rest, "**"):
		return ".*", 1
	case rest[0] == '*':
		return "[^/]*", 0
	case rest[0] == '?':
		return "[^/]", 0
	}
	return regexp.QuoteMeta(rest[:1]), 0
}

// Excluded reports a file the control file keeps as source and never serves.
func (c *Config) Excluded(name string) bool {
	for _, re := range c.excludeRes {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// Splits reports whether the code an entry loads by import() goes to chunks.
func (c *Config) Splits() bool { return c.Splitting == nil || *c.Splitting }

// checkLocalPrefix refuses a prefix that maps outside the site, or to a path
// that is not one from its root.
func checkLocalPrefix(key, dir string) error {
	inner := strings.Trim(dir, sep)
	switch {
	case !strings.HasPrefix(dir, sep) || !strings.HasSuffix(dir, sep):
		return fmt.Errorf("imports: %q maps to %q; a folder of the site is written from its root, starting and ending in /, such as \"/src/\"", key, dir)
	case inner != "" && (!fs.ValidPath(inner) || strings.Contains(dir, "//")):
		return fmt.Errorf("imports: %q maps to %q, which is outside the site", key, dir)
	}
	return nil
}

// projectFiles are a JavaScript project's package and tooling files, which a
// release with a control file keeps as source and never serves (#2785).
var projectFiles = []string{"package.json", "package-lock.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "yarn.lock", "bun.lockb"}

// configExts are the extensions of a tool's configuration file at the root.
var configExts = []string{".js", ".ts", ".mjs", ".cjs", ".mts", ".cts"}

// IsProjectFile reports a package or tooling file at the root of the site:
// package.json and the lockfiles, tsconfig*.json, *.config.{js,ts,...}, and the
// tooling dotfiles ToolingDotfile names.
func IsProjectFile(name string) bool {
	if strings.Contains(name, sep) {
		return false
	}
	switch {
	case slices.Contains(projectFiles, name), ToolingDotfile(name):
		return true
	case strings.HasPrefix(name, "tsconfig") && strings.HasSuffix(name, extJSON):
		return true
	}
	ext := path.Ext(name)
	return slices.Contains(configExts, ext) && strings.HasSuffix(strings.TrimSuffix(name, ext), ".config")
}

// toolingDotfiles are the dotfiles a project keeps at its root that hold no
// credential. .npmrc is not one: it holds a registry token as often as not.
var toolingDotfiles = []string{".nvmrc", ".node-version", ".eslintrc", ".prettierrc", ".editorconfig", ".browserslistrc"}

// ToolingDotfile reports a tooling dotfile at the root a release may hold as
// source: .nvmrc, .eslintrc*, .prettierrc* and the like. They are never served.
func ToolingDotfile(name string) bool {
	if strings.Contains(name, sep) {
		return false
	}
	for _, d := range toolingDotfiles {
		if name == d || strings.HasPrefix(name, d+dot) {
			return true
		}
	}
	return false
}

// Merge adds generated imports the control file does not map itself: its own
// entries win (#2785).
func (c *Config) Merge(generated map[string]string) error {
	merged := make(map[string]string, len(generated))
	maps.Copy(merged, generated)
	maps.Copy(merged, c.Imports)
	c.Imports = merged
	return c.checkImports()
}
