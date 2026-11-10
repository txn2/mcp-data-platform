// Package siterender renders a site's source tree: a pure function from the
// files of the tree to the files it serves. It runs templates that the people
// who manage a site, and their agents, write, so everything a template can spend
// is metered; see engine and meter.
package siterender

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/osteele/liquid/render"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/renderer/html"
)

// Sink receives a render's output one file at a time, so a site larger than
// memory renders in the memory of its largest page.
type Sink interface {
	// Put is a rendered file, whose body the sink then owns.
	Put(file string, body []byte) error
	// Copy is a source file served as it is, and is never opened to say so.
	Copy(file string) error
	// Keep is an output of the previous render that is still current.
	Keep(file string) error
}

// Options are what a render may vary by besides its tree.
type Options struct {
	Limits Limits
	// Previous and Changed make the render incremental: Previous is what the
	// last render read, and Changed the source paths that differ from its tree.
	// With no Previous, everything is rendered.
	Previous *Index
	Changed  []string
}

// Failure is one place a render could not get past.
type Failure struct {
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

// RenderError is every failure of a render, so that they can be fixed together.
type RenderError struct {
	Failures []Failure
	// Truncated is true when there were more than the limit reports.
	Truncated bool
}

func (e *RenderError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "the site did not render:")
	for _, f := range e.Failures {
		fmt.Fprintf(&b, "\n%s:%d: %s", f.File, f.Line, f.Message)
	}
	if e.Truncated {
		fmt.Fprintf(&b, "\nand more; fix these and publish again")
	}
	return b.String()
}

// What templates call things, and how an address is written.
const (
	fieldTags       = "tags"
	fieldCategories = "categories"
	fieldPages      = "pages"
	pathSep         = "/"
	indexFile       = "index.html"
)

var markdown = goldmark.New(goldmark.WithRendererOptions(html.WithUnsafe()))

func toHTML(body []byte) (string, error) {
	var out bytes.Buffer
	if err := markdown.Convert(body, &out); err != nil {
		return "", fmt.Errorf("markdown: %w", err)
	}
	return out.String(), nil
}

// page is one source file with front matter.
type page struct {
	src  string
	meta sortedMap
}

func (p *page) str(key string) string {
	s, _ := p.meta[key].(string)
	return s
}

// instance is one output of a page: a generated page has one per record.
type instance struct {
	page      *page
	item      any
	paginator sortedMap
	// inputs are what this output depends on by being generated from it.
	inputs []string
}

// run is one render.
type run struct {
	ctx      context.Context
	src      fs.FS
	limits   Limits
	meter    *meter
	engine   *engine
	sink     Sink
	pages    []*page
	layouts  map[string]bool
	data     map[string]*collection
	outputs  map[string]string
	failures []Failure
	cut      bool
	deps     map[string]bool
	sinkErr  error
	dataErr  error
	pageList []any
	previous map[string]Entry
	changes  changes
	held     int64

	parsedLayouts map[string]*layoutFile
}

// ReadTemplate gives the engine a template file of the tree.
func (r *run) ReadTemplate(name string) ([]byte, error) { return r.readFile(name) }

func (r *run) dep(input string) {
	if r.deps != nil {
		r.deps[input] = true
	}
}

func (r *run) fail(err error) { r.dataErr = err }

// Render turns a source tree into output, handing each file to sink.
func Render(ctx context.Context, src fs.FS, opts Options, sink Sink) (*Index, error) {
	limits := opts.Limits.withDefaults()
	r := &run{
		ctx: ctx, src: src, limits: limits, meter: newMeter(ctx, limits), sink: sink,
		layouts: map[string]bool{}, data: map[string]*collection{}, outputs: map[string]string{},
		previous: opts.Previous.byOutput(), changes: newChanges(opts.Changed), parsedLayouts: map[string]*layoutFile{},
	}
	engine, err := newEngine(r.meter, r)
	if err != nil {
		return nil, err
	}
	r.engine, engine.onRead = engine, r.dep
	if err := r.scan(); err != nil {
		return nil, err
	}
	return r.renderAll()
}

// scan sorts the tree into layouts, records, pages and files to copy.
func (r *run) scan() error {
	err := fs.WalkDir(r.src, ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			r.noteCollection(name)
			return nil
		}
		return r.scanFile(name)
	})
	if err != nil {
		return fmt.Errorf("reading the source tree: %w", err)
	}
	return r.failed()
}

func (r *run) noteCollection(dir string) {
	if parent, name := path.Split(dir); path.Clean(parent) == dataDir {
		r.data[name] = &collection{run: r, name: name}
	}
}

func (r *run) scanFile(name string) error {
	first, _, _ := strings.Cut(name, "/")
	switch first {
	case layoutsDir:
		r.layouts[name] = true
		return nil
	case includesDir, dataDir:
		return nil
	}
	if !isTemplated(name) {
		return r.sink.Copy(name) //nolint:wrapcheck // the sink's own sentence
	}
	return r.scanTemplated(name)
}

func (r *run) scanTemplated(name string) error {
	doc, err := fs.ReadFile(r.src, name)
	if err != nil {
		return fmt.Errorf("reading %s: %w", name, err)
	}
	parsed, err := splitFrontMatter(doc)
	if err != nil {
		r.report(Failure{File: name, Message: err.Error()})
		return nil //nolint:nilerr // reported, with the others, when the scan ends
	}
	if parsed == nil {
		return r.sink.Copy(name) //nolint:wrapcheck // the sink's own sentence
	}
	if draft, _ := parsed.meta["draft"].(bool); draft {
		return nil
	}
	if err := r.hold(len(doc) - len(parsed.body)); err != nil {
		return err
	}
	r.pages = append(r.pages, &page{src: name, meta: parsed.meta})
	return nil
}

func (r *run) report(f Failure) {
	if len(r.failures) >= r.limits.Failures {
		r.cut = true
		return
	}
	r.failures = append(r.failures, f)
}

func (r *run) failed() error {
	if len(r.failures) == 0 {
		return nil
	}
	return &RenderError{Failures: r.failures, Truncated: r.cut}
}

// renderAll renders every instance of every page, reporting what fails and
// stopping only when the render as a whole has run out.
func (r *run) renderAll() (*Index, error) {
	index := &Index{Version: indexVersion, Entries: []Entry{}}
	for _, p := range r.pages {
		instances, err := r.expand(p)
		if err != nil {
			r.reportErr(p.src, err)
			continue
		}
		for _, in := range instances {
			if err := r.renderInstance(in, index); err != nil {
				return nil, err
			}
			if r.exhausted() {
				return nil, r.exhaustion()
			}
		}
	}
	if err := r.failed(); err != nil {
		return nil, err
	}
	return index, nil
}

// exhausted reports a limit that no later page could stay under.
func (r *run) exhausted() bool {
	return r.ctx.Err() != nil || r.meter.steps > r.limits.Steps || r.meter.total > r.limits.TotalBytes || len(r.outputs) > r.limits.Pages || r.sinkErr != nil || r.cut
}

func (r *run) exhaustion() error {
	switch {
	case r.sinkErr != nil:
		return r.sinkErr
	case r.ctx.Err() != nil:
		return fmt.Errorf("rendering: %w", r.ctx.Err())
	case len(r.failures) > 0:
		return r.failed()
	case r.meter.steps > r.limits.Steps:
		return &BudgetError{Resource: "steps", Limit: r.limits.Steps}
	case r.meter.total > r.limits.TotalBytes:
		return &BudgetError{Resource: "bytes of output", Limit: r.limits.TotalBytes}
	}
	return &BudgetError{Resource: "pages", Limit: int64(r.limits.Pages)}
}

// expand is the outputs of a page: one, one per value of for_each, or one per
// group of per_page.
func (r *run) expand(p *page) ([]instance, error) {
	source, _ := p.meta["for_each"].(string)
	if source == "" {
		return []instance{{page: p}}, nil
	}
	values, err := r.values(source)
	if err != nil {
		return nil, err
	}
	whole := []string{sourceInput(source)}
	if size := intOf(p.meta["per_page"]); size > 0 {
		return paginate(p, values, size, whole), nil
	}
	out := make([]instance, 0, len(values))
	for _, v := range values {
		out = append(out, instance{page: p, item: v, inputs: itemInputs(v, whole)})
	}
	return out, nil
}

func isTaxonomy(source string) bool { return source == fieldTags || source == fieldCategories }

func sourceInput(source string) string {
	if isTaxonomy(source) {
		return inputPages
	}
	return inputData + source
}

// itemInputs is what one generated page depends on: its own record, or all of
// what it was generated from when the value has no file of its own.
func itemInputs(v any, whole []string) []string {
	if m, ok := v.(sortedMap); ok {
		if file, ok := m[recordFile].(string); ok {
			return []string{file}
		}
	}
	return whole
}

func intOf(v any) int {
	n, _ := v.(int)
	return n
}

func paginate(p *page, values []any, size int, inputs []string) []instance {
	total := max((len(values)+size-1)/size, 1)
	out := make([]instance, 0, total)
	for n := 1; n <= total; n++ {
		low, high := (n-1)*size, min(n*size, len(values))
		out = append(out, instance{page: p, inputs: inputs, paginator: sortedMap{
			"items": values[low:high], "page": n, "per_page": size, "total_pages": total, "total_items": len(values),
			"previous_page": previous(n), "next_page": next(n, total),
		}})
	}
	return out
}

func previous(n int) any {
	if n > 1 {
		return n - 1
	}
	return nil
}

func next(n, total int) any {
	if n < total {
		return n + 1
	}
	return nil
}

// values is what for_each walks: a taxonomy or a collection.
func (r *run) values(source string) ([]any, error) {
	if isTaxonomy(source) {
		return r.taxonomy(source), nil
	}
	c, ok := r.data[source]
	if !ok {
		return nil, fmt.Errorf("for_each: %q is neither tags, categories nor a folder of %s", source, dataDir)
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c.records, nil
}

// taxonomy is every value of a page field, in order, with the pages holding it.
func (r *run) taxonomy(field string) []any {
	r.dep(inputPages)
	groups := map[string][]any{}
	for _, p := range r.pages {
		values, _ := p.meta[field].([]any)
		for _, v := range values {
			name := fmt.Sprint(v)
			groups[name] = append(groups[name], pageMap(p))
		}
	}
	names := slices.Sorted(maps.Keys(groups))
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, sortedMap{"name": name, "slug": slugify(name), fieldPages: groups[name]})
	}
	return out
}

func slugify(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), "-")
}

// pageMap is a page as a list of pages shows it: its front matter and address.
func pageMap(p *page) sortedMap {
	m := maps.Clone(p.meta)
	if m == nil {
		m = sortedMap{}
	}
	if output, err := staticOutput(p); err == nil {
		m["url"] = urlOf(output)
	}
	return m
}

// staticOutput is a page's output path when its permalink has no template in it.
func staticOutput(p *page) (string, error) {
	if link := p.str("permalink"); link != "" && !strings.Contains(link, "{") {
		return outputOf(link)
	}
	return defaultOutput(p.src), nil
}

func defaultOutput(src string) string {
	if !isMarkdown(src) {
		return src
	}
	stem := strings.TrimSuffix(src, path.Ext(src))
	if path.Base(stem) == "index" {
		return stem + ".html"
	}
	return stem + pathSep + indexFile
}

// outputOf is the file a permalink is served from.
func outputOf(link string) (string, error) {
	name := strings.TrimPrefix(link, pathSep)
	if name == "" || strings.HasSuffix(name, pathSep) {
		name += indexFile
	} else if path.Ext(name) == "" {
		name += pathSep + indexFile
	}
	if !fs.ValidPath(name) {
		return "", fmt.Errorf("permalink %q is not a path in the site", link)
	}
	return name, nil
}

func urlOf(output string) string {
	return pathSep + strings.TrimSuffix(strings.TrimSuffix(output, indexFile), pathSep) + suffix(output)
}

func suffix(output string) string {
	if strings.HasSuffix(output, indexFile) && output != indexFile {
		return pathSep
	}
	return ""
}

// site is what templates see as site.
func (r *run) site() sortedMap {
	data := make(sortedMap, len(r.data))
	for name, c := range r.data {
		data[name] = c
	}
	return sortedMap{
		"data": data, fieldPages: lazy(r.allPages),
		fieldTags: lazy(func() any { return r.taxonomy(fieldTags) }), fieldCategories: lazy(func() any { return r.taxonomy(fieldCategories) }),
	}
}

// lazy is a value computed when a template first touches it.
type lazy func() any

// ToLiquid computes the value.
func (l lazy) ToLiquid() any { return l() }

func (r *run) allPages() any {
	r.dep(inputPages)
	if r.pageList == nil {
		r.pageList = make([]any, 0, len(r.pages))
		for _, p := range r.pages {
			r.pageList = append(r.pageList, pageMap(p))
		}
	}
	return r.pageList
}

func (r *run) renderInstance(in instance, index *Index) error {
	r.meter.startPage()
	r.deps, r.dataErr = map[string]bool{}, nil
	for _, input := range in.inputs {
		r.dep(input)
	}
	vars := r.vars(in)
	output, err := r.outputFor(in, vars)
	if err != nil {
		r.reportErr(in.page.src, r.settled(err))
		return nil
	}
	if entry, ok := r.current(output, in.page.src); ok {
		return r.keep(entry, index)
	}
	body, err := r.guardedBody(in, output, vars)
	if err = r.settled(err); err != nil {
		r.reportErr(in.page.src, err)
		return nil
	}
	return r.emit(in, output, body, index)
}

// guardedBody turns a panic in the template engine into this page's failure: a
// third-party engine running written-by-anyone templates must not be able to
// take a replica down over one page.
func (r *run) guardedBody(in instance, output string, vars map[string]any) (body []byte, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("the template engine failed on this page: %v", p)
		}
	}()
	return r.renderBody(in, output, vars)
}

// settled is the error a page ended with: a limit it crossed says more than
// whatever the engine made of it, and a record that would not load more than
// the empty list the template then saw.
func (r *run) settled(err error) error {
	switch {
	case r.meter.exceeded != nil:
		return r.meter.exceeded
	case err != nil:
		return err
	}
	return r.dataErr
}

// current is the previous render's entry for an output, when nothing it read
// has changed since.
func (r *run) current(output, source string) (Entry, bool) {
	entry, ok := r.previous[output]
	if !ok || entry.Source != source || r.changes.touches(source) {
		return Entry{}, false
	}
	if slices.ContainsFunc(entry.Inputs, r.changes.touches) {
		return Entry{}, false
	}
	return entry, true
}

func (r *run) keep(entry Entry, index *Index) error {
	r.outputs[entry.Output] = entry.Source
	if err := r.sink.Keep(entry.Output); err != nil {
		r.sinkErr = err
		return fmt.Errorf("keeping %s: %w", entry.Output, err)
	}
	index.Entries = append(index.Entries, entry)
	return nil
}

func (r *run) emit(in instance, output string, body []byte, index *Index) error {
	if other, taken := r.outputs[output]; taken {
		r.report(Failure{File: in.page.src, Message: fmt.Sprintf("%s also produces %s", other, output)})
		return nil
	}
	r.outputs[output] = in.page.src
	r.meter.total += int64(len(body))
	if err := r.sink.Put(output, body); err != nil {
		r.sinkErr = err
		return fmt.Errorf("storing %s: %w", output, err)
	}
	index.Entries = append(index.Entries, Entry{Output: output, Source: in.page.src, Inputs: slices.Sorted(maps.Keys(r.deps))})
	return nil
}

func (r *run) reportErr(file string, err error) {
	f := Failure{File: file, Message: err.Error()}
	var located interface {
		Path() string
		LineNumber() int
	}
	if errors.As(err, &located) {
		f.Line = located.LineNumber()
		if p := located.Path(); p != "" {
			f.File = p
		}
	}
	r.report(f)
}

// renderBody renders one output's content and wraps it in its layouts.
func (r *run) renderBody(in instance, output string, vars map[string]any) ([]byte, error) {
	vars["page"] = withURL(in.page.meta, output)
	content, err := r.body(in, vars)
	if err != nil {
		return nil, err
	}
	return r.applyLayouts(in.page.str("layout"), content, vars)
}

func (r *run) vars(in instance) map[string]any {
	vars := map[string]any{"site": r.site(), "page": in.page.meta}
	if in.item != nil {
		vars["item"] = in.item
	}
	if in.paginator != nil {
		vars["paginator"] = in.paginator
	}
	return vars
}

func withURL(meta sortedMap, output string) sortedMap {
	page := maps.Clone(meta)
	if page == nil {
		page = sortedMap{}
	}
	page["url"] = urlOf(output)
	return page
}

func (r *run) outputFor(in instance, vars map[string]any) (string, error) {
	link := in.page.str("permalink")
	if link == "" {
		if in.item != nil || in.paginator != nil {
			return "", errors.New("a page with for_each needs a permalink that differs per page")
		}
		return defaultOutput(in.page.src), nil
	}
	rendered, err := r.inline(in.page.src+"#permalink", link, vars)
	if err != nil {
		return "", err
	}
	return outputOf(rendered)
}

func (r *run) inline(name, source string, vars map[string]any) (string, error) {
	root, err := r.engine.parse(name, source)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if rerr := render.Render(root, meterWriter{w: &out, m: r.meter}, vars, r.engine.cfg); rerr != nil {
		return "", rerr
	}
	return out.String(), nil
}

// body is a page's own content: its Liquid, then its Markdown. An HTML body is
// otherwise left exactly as written, and liquid: false leaves it alone entirely.
func (r *run) body(in instance, vars map[string]any) (string, error) {
	content, err := r.pageBody(in.page.src)
	if err != nil {
		return "", err
	}
	if off, set := in.page.meta["liquid"].(bool); !set || off {
		rendered, err := r.inline(in.page.src, content, vars)
		if err != nil {
			return "", err
		}
		content = rendered
	}
	if isMarkdown(in.page.src) {
		return toHTML([]byte(content))
	}
	return content, nil
}

// pageBody reads a page again for rendering: bodies are not held between the
// scan and the render, so a site larger than memory renders.
func (r *run) pageBody(src string) (string, error) {
	doc, err := fs.ReadFile(r.src, src)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", src, err)
	}
	parsed, err := splitFrontMatter(doc)
	if err != nil {
		return "", err
	}
	return string(parsed.body), nil
}

// applyLayouts wraps content in its layout, then that layout's layout.
func (r *run) applyLayouts(name, content string, vars map[string]any) ([]byte, error) {
	for depth := 0; name != ""; depth++ {
		if depth >= r.limits.Depth {
			return nil, fmt.Errorf("layouts nest more than %d deep", r.limits.Depth)
		}
		file := path.Join(layoutsDir, name+".html")
		if !r.layouts[file] {
			return nil, fmt.Errorf("layout %q: there is no %s", name, file)
		}
		wrapped, parent, err := r.applyLayout(file, content, vars)
		if err != nil {
			return nil, err
		}
		content, name = wrapped, parent
	}
	return []byte(content), nil
}

func (r *run) applyLayout(file, content string, vars map[string]any) (out, parent string, err error) {
	r.dep(file)
	l, err := r.layout(file)
	if err != nil {
		return "", "", err
	}
	scoped := maps.Clone(vars)
	scoped["content"] = content
	scoped["layout"] = l.meta
	var buf bytes.Buffer
	if rerr := render.Render(l.root, meterWriter{w: &buf, m: r.meter}, scoped, r.engine.cfg); rerr != nil {
		return "", "", rerr
	}
	return buf.String(), l.parent, nil
}

// layoutFile is a layout, read and parsed once per render.
type layoutFile struct {
	root   render.Node
	meta   sortedMap
	parent string
}

func (r *run) layout(file string) (*layoutFile, error) {
	if l, ok := r.parsedLayouts[file]; ok {
		return l, nil
	}
	doc, err := r.readFile(file)
	if err != nil {
		return nil, err
	}
	parsed, err := splitFrontMatter(doc)
	if err != nil {
		return nil, fmt.Errorf("layout %s: %w", file, err)
	}
	if parsed == nil {
		parsed = &document{body: doc}
	}
	root, err := r.engine.parse(file, string(parsed.body))
	if err != nil {
		return nil, err
	}
	l := &layoutFile{root: root, meta: parsed.meta}
	l.parent, _ = parsed.meta["layout"].(string)
	r.parsedLayouts[file] = l
	return l, nil
}
