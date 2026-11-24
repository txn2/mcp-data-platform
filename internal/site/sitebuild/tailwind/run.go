package tailwind

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
)

// Limits bound one compile.
type Limits struct {
	Time        time.Duration
	Candidates  int
	OutputBytes int
	// ScanBytes is how much of the release's files is read for class names.
	ScanBytes int64
}

// The bounds a compile takes when its Limits leave them unset.
const (
	defaultTime        = 30 * time.Second
	defaultCandidates  = 500_000
	defaultOutputBytes = 8 << 20
	defaultScanBytes   = int64(64 << 20)
)

func (l Limits) withDefaults() Limits {
	if l.Time <= 0 {
		l.Time = defaultTime
	}
	if l.Candidates <= 0 {
		l.Candidates = defaultCandidates
	}
	if l.OutputBytes <= 0 {
		l.OutputBytes = defaultOutputBytes
	}
	if l.ScanBytes <= 0 {
		l.ScanBytes = defaultScanBytes
	}
	return l
}

// LimitError is a compile stopped for taking more than it may.
type LimitError struct {
	File  string
	Limit string
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%s: Tailwind CSS exceeded the limit on %s", e.File, e.Limit)
}

// root is the start of a path from the release's root.
const root = "/"

// usesTailwind is a stylesheet Tailwind compiles: it imports tailwindcss.
var usesTailwind = regexp.MustCompile(`@import\s+(?:url\()?["']tailwindcss(?:/[a-z.]+)?["']`)

// refused are the directives that would load code, which a site's compile
// never runs.
var refused = regexp.MustCompile(`(?m)@(plugin|config)\b[^;]*`)

// sourceRule is an @source directive: a glob of files scanned for class names,
// or with not one excluded.
var sourceRule = regexp.MustCompile(`@source\s+(not\s+)?["']([^"']+)["']\s*;`)

// scanned are the files read for class names.
var scanned = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts", ".html", ".htm", ".md", ".markdown", ".vue", ".svelte", ".liquid"}

// Compiler compiles one release's Tailwind stylesheets: a StylesheetCompiler.
type Compiler struct {
	pkg    *Package
	src    fs.FS
	input  string
	limits Limits

	mu   sync.Mutex
	done map[[sha256.Size]byte][]byte
}

// Factory is what the publish path calls to build the compiler a release's
// build file asks for.
func (l *Loader) Factory(limits Limits) func(ctx context.Context, cfg *sitebuild.Config, src fs.FS) (compiler sitebuild.StylesheetCompiler, err error) {
	return func(ctx context.Context, cfg *sitebuild.Config, src fs.FS) (sitebuild.StylesheetCompiler, error) {
		pkg, err := l.Load(ctx, cfg.Tailwind)
		if err != nil {
			return nil, err
		}
		return &Compiler{pkg: pkg, src: src, input: cfg.TailwindInput, limits: limits.withDefaults(), done: map[[sha256.Size]byte][]byte{}}, nil
	}
}

// NewCompiler is a compiler over a prepared package.
func NewCompiler(pkg *Package, src fs.FS, input string, limits Limits) *Compiler {
	return &Compiler{pkg: pkg, src: src, input: input, limits: limits.withDefaults(), done: map[[sha256.Size]byte][]byte{}}
}

// CompileStylesheet compiles a stylesheet that imports tailwindcss, and leaves
// any other as written.
func (c *Compiler) CompileStylesheet(ctx context.Context, name string, body []byte) (out []byte, changed bool, err error) {
	if (c.input != "" && name != c.input) || !usesTailwind.Match(body) {
		return body, false, nil
	}
	if m := refused.FindSubmatch(body); m != nil {
		return nil, false, fmt.Errorf("stylesheet %s: %s is not supported: a site's compile runs no code but Tailwind's own, so plugins and a JavaScript config cannot load; configure in CSS with @theme, @utility and @custom-variant",
			name, strings.TrimSpace(string(m[0])))
	}
	key := sha256.Sum256(append([]byte(name+"\x00"), body...))
	c.mu.Lock()
	held, ok := c.done[key]
	c.mu.Unlock()
	if ok {
		return held, true, nil
	}
	candidates, err := c.candidates(name, string(body))
	if err != nil {
		return nil, false, err
	}
	out, err = c.run(ctx, name, string(body), candidates)
	if err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	c.done[key] = out
	c.mu.Unlock()
	return out, true, nil
}

// candidates are the tokens of the release's files that may be class names:
// every source and page split on the characters a class name cannot hold.
// Collecting too many is harmless; build ignores what is not a class.
func (c *Compiler) candidates(name, css string) ([]string, error) {
	base := path.Dir(name)
	var include, exclude []*regexp.Regexp
	for _, m := range sourceRule.FindAllStringSubmatch(css, -1) {
		re := globRegexp(path.Join(base, m[2]))
		if m[1] != "" {
			exclude = append(exclude, re)
		} else {
			include = append(include, re)
		}
	}
	w := &candidateWalk{c: c, name: name, include: include, exclude: exclude, seen: map[string]bool{}}
	if err := fs.WalkDir(c.src, ".", w.visit); err != nil {
		return nil, err //nolint:wrapcheck // each its own sentence
	}
	seen := w.seen
	out := make([]string, 0, len(seen))
	for tok := range seen {
		out = append(out, tok)
	}
	slices.Sort(out)
	return out, nil
}

// candidateWalk splits the release's files into candidates, within the
// limits.
type candidateWalk struct {
	c                *Compiler
	name             string
	include, exclude []*regexp.Regexp
	seen             map[string]bool
	read             int64
}

func (w *candidateWalk) visit(p string, d fs.DirEntry, err error) error {
	if err != nil || d.IsDir() || !scans(p, w.include, w.exclude) {
		return err
	}
	data, err := fs.ReadFile(w.c.src, p)
	if err != nil {
		return fmt.Errorf("reading %s: %w", p, err)
	}
	if w.read += int64(len(data)); w.read > w.c.limits.ScanBytes {
		return &LimitError{File: w.name, Limit: fmt.Sprintf("%d bytes of files scanned for class names", w.c.limits.ScanBytes)}
	}
	for _, tok := range strings.FieldsFunc(string(data), separates) {
		w.seen[tok] = true
	}
	if len(w.seen) > w.c.limits.Candidates {
		return &LimitError{File: w.name, Limit: fmt.Sprintf("%d candidate class names", w.c.limits.Candidates)}
	}
	return nil
}

func scans(p string, include, exclude []*regexp.Regexp) bool {
	for _, re := range exclude {
		if re.MatchString(p) {
			return false
		}
	}
	for _, re := range include {
		if re.MatchString(p) {
			return true
		}
	}
	if strings.HasPrefix(p, sitebuild.ChunkDir+root) || strings.HasPrefix(p, "node_modules/") {
		return false
	}
	return slices.Contains(scanned, strings.ToLower(path.Ext(p)))
}

func separates(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '"', '\'', '`', '{', '}', '(', ')', ',', ';':
		return true
	}
	return false
}

// globRegexp is a glob as a whole-path pattern: ** crosses folders, {a,b}
// is either, and a folder matches what it holds.
func globRegexp(g string) *regexp.Regexp {
	g = strings.TrimPrefix(path.Clean(g), root)
	parts := make([]string, 0, len(g))
	parts = append(parts, "^")
	for i := 0; i < len(g); i++ {
		piece, skip := globPiece(g[i:])
		parts = append(parts, piece)
		i += skip
	}
	parts = append(parts, "(?:/.*)?$")
	return regexp.MustCompile(strings.Join(parts, ""))
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
	case rest[0] == '{':
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return `\{`, 0
		}
		alts := strings.Split(rest[1:end], ",")
		for j := range alts {
			alts[j] = regexp.QuoteMeta(alts[j])
		}
		return "(?:" + strings.Join(alts, "|") + ")", end
	}
	return regexp.QuoteMeta(rest[:1]), 0
}

// driver is the script that calls Tailwind's compile and build and hands the
// result to Go, since compile answers with a promise.
const driver = `(function (css, base, candidates, load, done, fail) {
  __tailwind.compile(css, { base: base, loadStylesheet: function (id, from) { return Promise.resolve(load(id, from)); } })
    .then(function (compiler) { done(compiler.build(candidates)); })
    .catch(function (e) { fail(String(e && e.message ? e.message : e)); });
})`

// run compiles one stylesheet in a fresh runtime, under the time limit.
func (c *Compiler) run(ctx context.Context, name, css string, candidates []string) ([]byte, error) {
	vm := goja.New()
	timer := time.AfterFunc(c.limits.Time, func() { vm.Interrupt("time") })
	defer timer.Stop()
	stop := context.AfterFunc(ctx, func() { vm.Interrupt("canceled") })
	defer stop()
	if _, err := vm.RunString(c.pkg.script); err != nil {
		return nil, c.failed(name, err)
	}
	fn, err := vm.RunString(driver)
	if err != nil {
		return nil, c.failed(name, err)
	}
	call, ok := goja.AssertFunction(fn)
	if !ok {
		return nil, errors.New("tailwind: the driver is not a function")
	}
	var out string
	var compileErr error
	load := func(id, from string) (map[string]any, error) { return c.load(name, id, from) }
	done := func(s string) { out = s }
	fail := func(msg string) { compileErr = fmt.Errorf("stylesheet %s: Tailwind CSS: %s", name, msg) }
	if _, err := call(goja.Undefined(), vm.ToValue(css), vm.ToValue(root+path.Dir(name)), vm.ToValue(candidates),
		vm.ToValue(load), vm.ToValue(done), vm.ToValue(fail)); err != nil {
		return nil, c.failed(name, err)
	}
	switch {
	case compileErr != nil:
		return nil, compileErr
	case len(out) > c.limits.OutputBytes:
		return nil, &LimitError{File: name, Limit: fmt.Sprintf("%d bytes of output", c.limits.OutputBytes)}
	}
	return []byte(out), nil
}

func (c *Compiler) failed(name string, err error) error {
	if interrupted, ok := errors.AsType[*goja.InterruptedError](err); ok {
		if interrupted.Value() == "time" {
			return &LimitError{File: name, Limit: fmt.Sprintf("time (%s)", c.limits.Time)}
		}
		return fmt.Errorf("stylesheet %s: the compile was stopped", name)
	}
	return fmt.Errorf("stylesheet %s: Tailwind CSS: %w", name, err)
}

// load answers a stylesheet Tailwind imports: one of its own package, or one
// of the release by a path from the importing stylesheet or from the root.
func (c *Compiler) load(name, id, from string) (map[string]any, error) {
	if sheet, ok := c.pkg.sheets[id]; ok {
		return map[string]any{"path": id, "base": root, "content": sheet}, nil
	}
	if !strings.HasPrefix(id, "./") && !strings.HasPrefix(id, "../") && !strings.HasPrefix(id, root) {
		return nil, fmt.Errorf("stylesheet %s imports %q: a stylesheet is imported by a path in the site, or as one of tailwindcss's own", name, id)
	}
	p := strings.TrimPrefix(id, root)
	if !strings.HasPrefix(id, root) {
		p = path.Join(strings.TrimPrefix(from, root), id)
	}
	if p == ".." || strings.HasPrefix(p, "../") || !fs.ValidPath(p) {
		return nil, fmt.Errorf("stylesheet %s imports %q, which is outside the site", name, id)
	}
	data, err := fs.ReadFile(c.src, p)
	if err != nil {
		return nil, fmt.Errorf("stylesheet %s imports %q: the site has no file %s", name, id, p)
	}
	return map[string]any{"path": root + p, "base": root + path.Dir(p), "content": string(data)}, nil
}
