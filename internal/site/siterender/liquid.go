package siterender

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/osteele/liquid/expressions"
	"github.com/osteele/liquid/filters"
	"github.com/osteele/liquid/parser"
	"github.com/osteele/liquid/render"
	"github.com/osteele/liquid/tags"
	"github.com/osteele/liquid/values"
	"github.com/osteele/tuesday"
)

// meteredBlock wraps every loop and capture body once a template is parsed, so
// the engine's own tags run unchanged and are charged all the same.
const meteredBlock = "__metered"

// reviewedEngine is the version of the template engine these limits were built
// against. Every tag and filter it has was read for what it can spend, so a
// different version has to be read again before this number moves.
const reviewedEngine = "v1.9.2"

// includesDir is where include finds its files.
const includesDir = "_includes"

// The tags whose bodies repeat or accumulate.
var meteredBodies = map[string]bool{"for": true, "tablerow": true, "capture": true}

var errNoClock = errors.New(`date: "now" and "today" would make a render differ each time it runs; use a date from the page or a record`)

// engine is one render's template engine. Filters are told nothing about the
// render they run in, so each render builds its own, closed over its meter.
type engine struct {
	cfg   render.Config
	meter *meter
	// templates are parsed once per render.
	templates map[string]render.Node
	// onRead is told each template file a page uses.
	onRead func(file string)
}

func newEngine(m *meter, store render.TemplateStore) (*engine, error) {
	e := &engine{cfg: render.NewConfig(), meter: m, templates: map[string]render.Node{}}
	e.cfg.TemplateStore = store
	filters.AddStandardFilters(&e.cfg)
	tags.AddStandardTags(&e.cfg)
	e.cfg.AddBlock(meteredBlock).Renderer(e.renderMetered)
	e.addFilters()
	if err := e.wrapTags(); err != nil {
		return nil, err
	}
	return e, nil
}

// addFilters replaces the filters whose result can be many times the size of
// their input, the one that reads the clock, and the arithmetic whose integer
// results nothing can read back as an integer.
func (e *engine) addFilters() {
	replace := func(s, old, n string) (string, error) {
		if err := e.meter.produced(int64(strings.Count(s, old)+1)*int64(max(len(n), 1)) + int64(len(s))); err != nil {
			return "", err
		}
		return strings.ReplaceAll(s, old, n), nil
	}
	e.cfg.AddFilter("replace", replace)
	e.cfg.AddFilter("date", dateFilter)
	join := filtersJoin(e.meter)
	e.cfg.AddFilter("join", join)
	e.addArithmetic()
}

func filtersJoin(m *meter) func([]any, func(string) string) (string, error) {
	return func(list []any, sep func(string) string) (string, error) {
		s := sep(" ")
		parts := make([]string, 0, len(list))
		total := int64(0)
		for _, v := range list {
			if v == nil {
				continue
			}
			p := fmt.Sprint(v)
			parts = append(parts, p)
			total += int64(len(p) + len(s))
		}
		if err := m.produced(total); err != nil {
			return "", err
		}
		return strings.Join(parts, s), nil
	}
}

// dateFilter is the standard filter, refusing the two values that are not a
// function of the source tree.
func dateFilter(v any, format func(string) string) (string, error) {
	if s, ok := v.(string); ok && (s == "now" || s == "today") {
		return "", errNoClock
	}
	converted, err := values.Convert(v, reflect.TypeFor[time.Time]())
	if err != nil {
		return "", fmt.Errorf("date: %w", err)
	}
	t, _ := converted.(time.Time)
	out, err := tuesday.Strftime(format("%a, %b %d, %y"), t)
	if err != nil {
		return "", fmt.Errorf("date: %w", err)
	}
	return out, nil
}

// wrapTags replaces the include tags, which would otherwise parse their files
// past the meter, and records what each assignment leaves in its variable.
func (e *engine) wrapTags() error {
	e.cfg.AddTag("include", e.includeTag(false))
	e.cfg.AddTag("render", e.includeTag(true))
	stock, ok := e.cfg.FindTagDefinition("assign")
	if !ok {
		return errors.New("the engine has no assign tag")
	}
	e.cfg.AddTag("assign", func(args string) (func(io.Writer, render.Context) error, error) {
		run, err := stock(args)
		if err != nil {
			return nil, err
		}
		name, _, _ := strings.Cut(args, "=")
		name = strings.TrimSpace(name)
		return func(w io.Writer, ctx render.Context) error {
			if err := run(w, ctx); err != nil {
				return err
			}
			return e.meter.assign(name, sizeOf(ctx.Get(name)))
		}, nil
	})
	return nil
}

// includeTag is {% include "x", a: 1 %}. Files come only from _includes, are
// parsed once per render with the same checks as any template, and isolated
// sees only its arguments, as the render tag does.
func (e *engine) includeTag(isolated bool) render.TagCompiler {
	return func(args string) (func(io.Writer, render.Context) error, error) {
		target, params, err := parseInclude(args)
		if err != nil {
			return nil, err
		}
		return func(w io.Writer, ctx render.Context) error {
			if err := e.meter.enter(); err != nil {
				return err
			}
			defer e.meter.leave()
			root, err := e.loadInclude(ctx, target)
			if err != nil {
				return err
			}
			vars, err := includeVars(ctx, params, isolated)
			if err != nil {
				return err
			}
			return e.renderInclude(root, w, vars)
		}, nil
	}
}

func (e *engine) renderInclude(root render.Node, w io.Writer, vars map[string]any) error {
	return render.Render(root, w, vars, e.cfg) //nolint:wrapcheck // the engine's error carries the file and line
}

func (e *engine) loadInclude(ctx render.Context, target expressions.Expression) (render.Node, error) {
	value, err := ctx.Evaluate(target)
	if err != nil {
		return nil, fmt.Errorf("include: %w", err)
	}
	name, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("include needs a file name, got %v", value)
	}
	file := path.Join(includesDir, name)
	if !strings.HasPrefix(file, includesDir+"/") {
		return nil, fmt.Errorf("include %q is outside %s", name, includesDir)
	}
	return e.compiled(file)
}

// compiled is a template file, parsed the first time it is asked for.
func (e *engine) compiled(file string) (render.Node, error) {
	if root, ok := e.templates[file]; ok {
		e.touched(file)
		return root, nil
	}
	body, err := e.cfg.TemplateStore.ReadTemplate(file)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	root, err := e.parse(file, string(body))
	if err != nil {
		return nil, err
	}
	e.templates[file] = root
	e.touched(file)
	return root, nil
}

func (e *engine) touched(file string) {
	if e.onRead != nil {
		e.onRead(file)
	}
}

func includeVars(ctx render.Context, params []includeParam, isolated bool) (map[string]any, error) {
	vars := map[string]any{}
	if !isolated {
		maps.Copy(vars, ctx.Bindings())
	}
	for _, p := range params {
		v, err := ctx.Evaluate(p.value)
		if err != nil {
			return nil, fmt.Errorf("include argument %s: %w", p.name, err)
		}
		vars[p.name] = v
	}
	return vars, nil
}

type includeParam struct {
	name  string
	value expressions.Expression
}

// parseInclude reads: target[, name: expression]...
func parseInclude(args string) (expressions.Expression, []includeParam, error) {
	parts := splitArgs(args)
	target, err := expressions.Parse(parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("include: %w", err)
	}
	params := make([]includeParam, 0, len(parts))
	for _, part := range parts[1:] {
		name, source, ok := strings.Cut(part, ":")
		if !ok {
			return nil, nil, fmt.Errorf("include: %q is not name: value", strings.TrimSpace(part))
		}
		value, err := expressions.Parse(source)
		if err != nil {
			return nil, nil, fmt.Errorf("include argument %s: %w", strings.TrimSpace(name), err)
		}
		params = append(params, includeParam{strings.TrimSpace(name), value})
	}
	return target, params, nil
}

// splitArgs cuts at the commas outside strings.
func splitArgs(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\'':
			i = skipString(s, i)
		case ',':
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// skipString is the index of the quote closing the string opening at i, reading
// strings as the engine's lexer does: backslash escapes in double quotes only.
func skipString(s string, i int) int {
	quote := s[i]
	for i++; i < len(s) && s[i] != quote; i++ {
		if quote == '"' && s[i] == '\\' {
			i++
		}
	}
	return i
}

// renderMetered charges one pass through a body and everything it writes.
func (e *engine) renderMetered(w io.Writer, ctx render.Context) error {
	if err := e.meter.step(); err != nil {
		return err
	}
	return e.renderChildren(w, ctx)
}

func (e *engine) renderChildren(w io.Writer, ctx render.Context) error {
	return ctx.RenderChildren(meterWriter{w: w, m: e.meter}) //nolint:wrapcheck // break and continue are read from this error
}

type meterWriter struct {
	w io.Writer
	m *meter
}

func (m meterWriter) Write(p []byte) (int, error) {
	if err := m.m.write(len(p)); err != nil {
		return 0, err
	}
	return m.w.Write(p) //nolint:wrapcheck // passed through to the engine
}

// parse compiles a template and fits it with the meter: the static half of the
// checks, which is what cannot wait until a template runs.
func (e *engine) parse(file, source string) (render.Node, error) {
	root, perr := e.cfg.Compile(source, parser.SourceLoc{Pathname: file, LineNo: 1})
	if perr != nil {
		return nil, perr
	}
	if err := e.instrument(root); err != nil {
		return nil, err
	}
	return root, nil
}

type walkItem struct {
	node  render.Node
	depth int
}

// instrument walks a parsed template with an explicit stack, since a template's
// nesting is what is being checked: it refuses nesting too deep to render and a
// range outside a for head, and wraps each loop and capture body.
func (e *engine) instrument(root render.Node) error {
	stack := []walkItem{{root, 0}}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		children, err := e.visit(top.node, top.depth)
		if err != nil {
			return err
		}
		for _, c := range children {
			stack = append(stack, walkItem{c, top.depth + 1})
		}
	}
	return nil
}

func (e *engine) visit(n render.Node, depth int) ([]render.Node, error) {
	if depth > e.meter.limits.Nesting {
		return nil, fmt.Errorf("blocks nest more than %d deep", e.meter.limits.Nesting)
	}
	switch node := n.(type) {
	case *render.SeqNode:
		return node.Children, nil
	case *render.ObjectNode:
		return nil, checkArgs(node.Token)
	case *render.TagNode:
		return nil, checkArgs(node.Token)
	case *render.BlockNode:
		return e.visitBlock(node)
	}
	return nil, nil
}

func (e *engine) visitBlock(b *render.BlockNode) ([]render.Node, error) {
	if b.Name == meteredBlock {
		return b.Body, nil
	}
	if !isLoop(b.Name) {
		if err := checkArgs(b.Token); err != nil {
			return nil, err
		}
	}
	children := make([]render.Node, 0, len(b.Body))
	children = append(children, b.Body...)
	for _, c := range b.Clauses {
		children = append(children, c)
	}
	if meteredBodies[b.Name] {
		if err := e.wrapBody(b); err != nil {
			return nil, err
		}
	}
	return children, nil
}

func isLoop(name string) bool { return name == "for" || name == "tablerow" }

// wrapBody moves a body inside a metered block, keeping the original nodes.
func (e *engine) wrapBody(b *render.BlockNode) error {
	wrapper, perr := e.cfg.Compile("{%"+meteredBlock+"%}{%end"+meteredBlock+"%}", b.SourceLoc)
	if perr != nil {
		return perr
	}
	block := firstBlock(wrapper)
	if block == nil {
		return errors.New("the metered block did not compile")
	}
	block.Body = b.Body
	b.Body = []render.Node{block}
	return nil
}

func firstBlock(n render.Node) *render.BlockNode {
	switch node := n.(type) {
	case *render.BlockNode:
		return node
	case *render.SeqNode:
		for _, c := range node.Children {
			if b := firstBlock(c); b != nil {
				return b
			}
		}
	}
	return nil
}

// checkArgs refuses a range outside a for head: inside one a range is walked
// lazily, and anywhere else it is expanded into an array of its full length.
func checkArgs(t parser.Token) error {
	if hasRange(t.Args) {
		return fmt.Errorf("line %d: a range such as (1..n) is only allowed in a for tag", t.SourceLoc.LineNo)
	}
	return nil
}

// hasRange reports ".." outside a string.
func hasRange(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\'':
			i = skipString(s, i)
		case '.':
			if i+1 < len(s) && s[i+1] == '.' {
				return true
			}
		}
	}
	return false
}
