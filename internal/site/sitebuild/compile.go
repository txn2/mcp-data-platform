package sitebuild

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/evanw/esbuild/pkg/api"
)

// Version names what an output is a function of beyond its inputs: the
// compiler and these options. An output compiled under another version is
// compiled again.
const Version = reviewedCompiler + "/1"

// reviewedCompiler is the esbuild these options were written against; a test
// holds it to go.mod.
const reviewedCompiler = "v0.28.2"

// Limits bound one entry's compile.
type Limits struct {
	// Modules is how many modules, local and fetched, one entry may read.
	Modules int
	// FileBytes is the largest one module may be.
	FileBytes int64
	// InputBytes is what all of one entry's modules may hold together.
	InputBytes int64
	// OutputBytes is what one entry's outputs may hold together.
	OutputBytes int64
}

// The bounds a compile takes when its Limits leave them unset.
const (
	defaultModules     = 2000
	defaultFileBytes   = int64(4 << 20)
	defaultInputBytes  = int64(64 << 20)
	defaultOutputBytes = int64(32 << 20)
)

func (l Limits) withDefaults() Limits {
	if l.Modules <= 0 {
		l.Modules = defaultModules
	}
	if l.FileBytes <= 0 {
		l.FileBytes = defaultFileBytes
	}
	if l.InputBytes <= 0 {
		l.InputBytes = defaultInputBytes
	}
	if l.OutputBytes <= 0 {
		l.OutputBytes = defaultOutputBytes
	}
	return l
}

// Fetcher reads a module by URL, for a release that bundles its URL modules.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (body []byte, contentType string, err error)
}

// Output is one file a compile produced.
type Output struct {
	Path string
	Body []byte
}

// Result is one entry compiled: its outputs, and every path of the tree it
// read or looked for, so it is compiled again when any of them changes.
type Result struct {
	Outputs []Output
	Inputs  []string
}

// Namespaces a module is placed in: a file of the tree, or a module by URL.
const (
	nsSite = "site"
	nsURL  = "url"
	outDir = "/out/"
)

// Options are what a compile is given besides its tree and entry.
type Options struct {
	Config *Config
	Limits Limits
	// Fetch reads URL modules in bundle mode; nil refuses them.
	Fetch Fetcher
}

// Compile builds one entry of the tree. Every module it reads comes from src
// or, in bundle mode, from Fetch: nothing resolves against the disk of the
// process running it.
func Compile(ctx context.Context, src fs.FS, entry string, opts Options) (res *Result, err error) {
	c := &compile{
		ctx: ctx, src: src, cfg: opts.Config, entry: entry, limits: opts.Limits.withDefaults(), fetch: opts.Fetch,
		inputs: map[string]bool{ControlFile: true},
	}
	defer func() {
		if p := recover(); p != nil {
			res, err = nil, fmt.Errorf("the compiler failed on %s: %v", entry, p)
		}
	}()
	result := api.Build(c.options())
	if c.stop != nil {
		return nil, c.stop
	}
	if len(result.Errors) > 0 {
		return nil, &Error{Messages: messages(result.Errors)}
	}
	return c.result(result.OutputFiles)
}

// compile is one entry's build: what it has read, and the limit it crossed.
type compile struct {
	ctx    context.Context
	src    fs.FS
	cfg    *Config
	entry  string
	limits Limits
	fetch  Fetcher

	mu      sync.Mutex
	inputs  map[string]bool
	modules int
	read    int64
	stop    error
}

func (c *compile) options() api.BuildOptions {
	opts := api.BuildOptions{
		EntryPoints:   []string{c.entry},
		Bundle:        true,
		Write:         false,
		Format:        api.FormatESModule,
		Platform:      api.PlatformBrowser,
		Target:        api.ES2022,
		JSX:           api.JSXAutomatic,
		Outfile:       outDir + OutputOf(c.entry),
		AbsWorkingDir: "/",
		LegalComments: api.LegalCommentsEndOfFile,
		Define:        map[string]string{"process.env.NODE_ENV": `"production"`},
		LogLevel:      api.LogLevelSilent,
		Plugins:       []api.Plugin{{Name: "site", Setup: c.setup}},
	}
	if c.cfg.Sourcemap {
		opts.Sourcemap = api.SourceMapLinked
	}
	if c.cfg.Minify {
		opts.MinifyWhitespace, opts.MinifySyntax, opts.MinifyIdentifiers = true, true, true
	}
	return opts
}

func (c *compile) setup(b api.PluginBuild) {
	b.OnResolve(api.OnResolveOptions{Filter: `.*`}, c.resolve)
	b.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: nsSite}, c.loadFile)
	b.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: nsURL}, c.loadURL)
}

// halt records the first limit crossed, which says more than the compile
// error esbuild makes of it.
func (c *compile) halt(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stop == nil {
		c.stop = err
	}
	return err
}

// charge counts one module of n bytes against the entry's limits.
func (c *compile) charge(n int64) error {
	if err := c.ctx.Err(); err != nil {
		return c.halt(fmt.Errorf("compiling %s: %w", c.entry, err))
	}
	c.mu.Lock()
	c.modules++
	c.read += n
	modules, read := c.modules, c.read
	c.mu.Unlock()
	switch {
	case n > c.limits.FileBytes:
		return c.halt(&LimitError{Entry: c.entry, Resource: "bytes in one module", Limit: c.limits.FileBytes})
	case modules > c.limits.Modules:
		return c.halt(&LimitError{Entry: c.entry, Resource: "modules", Limit: int64(c.limits.Modules)})
	case read > c.limits.InputBytes:
		return c.halt(&LimitError{Entry: c.entry, Resource: "bytes of modules", Limit: c.limits.InputBytes})
	}
	return nil
}

func (c *compile) noteInput(name string) {
	c.mu.Lock()
	c.inputs[name] = true
	c.mu.Unlock()
}

// loaders are how each kind of file in the tree is read. A .js file may hold
// JSX, as React code written without TypeScript often does.
var loaders = map[string]api.Loader{
	extTSX: api.LoaderTSX, extTS: api.LoaderTS, extMTS: api.LoaderTS, extCTS: api.LoaderTS,
	extJSX: api.LoaderJSX, extJS: api.LoaderJSX, extMJS: api.LoaderJSX,
	extJSON: api.LoaderJSON, extCSS: api.LoaderCSS,
}

func (c *compile) loadFile(args api.OnLoadArgs) (api.OnLoadResult, error) {
	name := strings.TrimPrefix(args.Path, sep)
	info, err := fs.Stat(c.src, name)
	if err != nil {
		return api.OnLoadResult{}, fmt.Errorf("reading %s: %w", name, err)
	}
	if err := c.charge(info.Size()); err != nil {
		return api.OnLoadResult{}, err
	}
	body, err := fs.ReadFile(c.src, name)
	if err != nil {
		return api.OnLoadResult{}, fmt.Errorf("reading %s: %w", name, err)
	}
	loader, ok := loaders[strings.ToLower(path.Ext(name))]
	if !ok {
		return api.OnLoadResult{}, fmt.Errorf("the file %s cannot be imported: a build reads %s", name, strings.Join(loadable(), ", "))
	}
	contents := string(body)
	return api.OnLoadResult{Contents: &contents, Loader: loader}, nil
}

func loadable() []string {
	exts := make([]string, 0, len(loaders))
	for ext := range loaders {
		exts = append(exts, ext)
	}
	slices.Sort(exts)
	return exts
}

func (c *compile) loadURL(args api.OnLoadArgs) (api.OnLoadResult, error) {
	if c.fetch == nil {
		return api.OnLoadResult{}, errors.New("this deployment cannot fetch modules at publish: set remote to external for this release")
	}
	if err := c.charge(0); err != nil {
		return api.OnLoadResult{}, err
	}
	body, contentType, err := c.fetch.Fetch(c.ctx, args.Path)
	if err != nil {
		return api.OnLoadResult{}, fmt.Errorf("fetching %s: %w", args.Path, err)
	}
	if err := c.charge(int64(len(body))); err != nil {
		return api.OnLoadResult{}, err
	}
	loader := api.LoaderJS
	if strings.HasPrefix(strings.ToLower(contentType), "text/css") {
		loader = api.LoaderCSS
	}
	contents := string(body)
	return api.OnLoadResult{Contents: &contents, Loader: loader}, nil
}

// result is the outputs at their paths in the release, within the entry's
// output limit.
func (c *compile) result(files []api.OutputFile) (*Result, error) {
	res := &Result{Outputs: make([]Output, 0, len(files))}
	var total int64
	for _, f := range files {
		name, ok := strings.CutPrefix(f.Path, outDir)
		if !ok || !fs.ValidPath(name) {
			return nil, fmt.Errorf("the compiler wrote %q, outside the site", f.Path)
		}
		total += int64(len(f.Contents))
		if total > c.limits.OutputBytes {
			return nil, &LimitError{Entry: c.entry, Resource: "bytes of output", Limit: c.limits.OutputBytes}
		}
		res.Outputs = append(res.Outputs, Output{Path: name, Body: f.Contents})
	}
	slices.SortFunc(res.Outputs, func(a, b Output) int { return strings.Compare(a.Path, b.Path) })
	for in := range c.inputs {
		res.Inputs = append(res.Inputs, in)
	}
	slices.Sort(res.Inputs)
	return res, nil
}

func messages(msgs []api.Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		msg := Message{File: "", Text: m.Text}
		if m.Location != nil {
			msg.File = strings.TrimPrefix(strings.TrimPrefix(m.Location.File, nsSite+":"), sep)
			msg.Line, msg.Column = m.Location.Line, m.Location.Column+1
		}
		out = append(out, msg)
	}
	return out
}
