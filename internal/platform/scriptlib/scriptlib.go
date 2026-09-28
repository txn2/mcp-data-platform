// Package scriptlib is managed-script libraries (#1941): a library is a script
// with no main(), pure code another script loads by name and pinned version,
// load("lib:<name>@<version>", "fn").
//
// A library is shared. Its code reaches nothing (no platform, no run), so
// loading it grants nothing, and a library's name is unique across the
// deployment, whoever owns it. A load names a version, and a saved version
// never changes, so a new version of a library changes no script that did not
// ask for it.
//
// Every execution of a script resolves its loads through Loader over one
// Source: a run, a draft, a test and a behavior replay read the same pinned
// source, so a test and a run cannot disagree about the library's code.
package scriptlib

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
)

// Prefix is the scheme a library load names.
const Prefix = "lib:"

// MaxDepth is how many libraries deep a chain of loads may go. A chain longer
// than this is refused on save and at run time alike.
const MaxDepth = 8

// ErrNotFound is what a Source answers for a library or version that does not
// exist.
var ErrNotFound = errors.New("no such library version")

// Source reads the source of one saved version of a library.
type Source interface {
	LibrarySource(ctx context.Context, name string, version int) (string, error)
}

// Ref names one version of a library.
type Ref struct {
	Name    string `json:"name" example:"date-windows"`
	Version int    `json:"version" example:"2"`
}

// String is the module a load names the version by.
func (r Ref) String() string { return Prefix + r.Name + "@" + strconv.Itoa(r.Version) }

// moduleRe is lib:<name>@<version>, the name spelled as a script's name is.
var moduleRe = regexp.MustCompile(`^lib:([a-z0-9][a-z0-9_-]*)@([1-9]\d{0,8})$`)

// ParseModule reads the module string of a load.
func ParseModule(module string) (Ref, error) {
	m := moduleRe.FindStringSubmatch(module)
	if m == nil {
		return Ref{}, fmt.Errorf("load(%q) names no library: a library is loaded as \"lib:<name>@<version>\", "+
			"the version a whole number, as load(\"lib:date-windows@2\", \"last_week\")", module)
	}
	v, _ := strconv.Atoi(m[2]) // the pattern admits only a number that fits
	return Ref{Name: m[1], Version: v}, nil
}

// Load is one load statement: the library version it names, or the error
// its module string is, and its line.
type Load struct {
	Ref  Ref
	Err  error
	Line int
}

// Loads is every load statement of file, in source order.
func Loads(file *syntax.File) []Load {
	var out []Load
	for _, s := range file.Stmts {
		ld, ok := s.(*syntax.LoadStmt)
		if !ok {
			continue
		}
		module, _ := ld.Module.Value.(string)
		ref, err := ParseModule(module)
		start, _ := ld.Span()
		out = append(out, Load{Ref: ref, Err: err, Line: int(start.Line)})
	}
	return out
}

// Refs is the library versions source loads, in source order, each once. A
// source that does not parse, and a load naming no library, contribute none.
func Refs(source string) []Ref {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	if err != nil {
		return []Ref{}
	}
	out := []Ref{}
	seen := map[Ref]bool{}
	for _, l := range Loads(file) {
		if l.Err == nil && !seen[l.Ref] {
			seen[l.Ref] = true
			out = append(out, l.Ref)
		}
	}
	return out
}

// IsLibrary reports whether file is a library: it defines no main() at its
// top level.
func IsLibrary(file *syntax.File) bool {
	for _, s := range file.Stmts {
		if d, ok := s.(*syntax.DefStmt); ok && d.Name.Name == scriptdialect.EntryPointName {
			return false
		}
	}
	return true
}

// SourceIsLibrary is IsLibrary over source; a source that does not parse is
// not a library.
func SourceIsLibrary(source string) bool {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	return err == nil && IsLibrary(file)
}

// Loader is the thread's Load for an execution: each library version is read
// from src once, executed in env (the predeclared names a library sees), and
// its frozen globals handed to every load of it. A load while the same version
// is still loading is a cycle; a chain deeper than MaxDepth is refused.
func Loader(ctx context.Context, src Source, env starlark.StringDict) func(*starlark.Thread, string) (starlark.StringDict, error) {
	l := &loader{ctx: ctx, src: src, env: env, done: map[Ref]starlark.StringDict{}, loading: map[Ref]bool{}}
	return l.load
}

type loader struct {
	ctx     context.Context
	src     Source
	env     starlark.StringDict
	done    map[Ref]starlark.StringDict
	loading map[Ref]bool
	depth   int
}

func (l *loader) load(thread *starlark.Thread, module string) (starlark.StringDict, error) {
	ref, err := ParseModule(module)
	if err != nil {
		return nil, err
	}
	if globals, ok := l.done[ref]; ok {
		return globals, nil
	}
	if l.loading[ref] {
		return nil, fmt.Errorf("library %s loads itself through the libraries it loads; a load cycle is not allowed", ref)
	}
	if l.depth >= MaxDepth {
		return nil, fmt.Errorf("loading %s goes more than %d libraries deep", ref, MaxDepth)
	}
	if l.src == nil {
		return nil, fmt.Errorf("library %s cannot be loaded here: no library store is available", ref)
	}
	source, err := l.src.LibrarySource(l.ctx, ref.Name, ref.Version)
	if err != nil {
		return nil, describe(ref, err)
	}
	l.loading[ref] = true
	l.depth++
	defer func() { delete(l.loading, ref); l.depth-- }()
	file, err := scriptdialect.Options.Parse(ref.String(), source, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ref, err)
	}
	prog, err := starlark.FileProgram(file, l.env.Has)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", ref, err)
	}
	globals, err := prog.Init(thread, l.env)
	if err != nil {
		return nil, err //nolint:wrapcheck // the interpreter's failure, whose backtrace names the library
	}
	globals.Freeze()
	l.done[ref] = globals
	return globals, nil
}

// describe is what a load that could not read the library says.
func describe(ref Ref, err error) error {
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("library %s does not exist: no library %q has a version %d", ref, ref.Name, ref.Version)
	}
	return fmt.Errorf("reading %s: %w", ref, err)
}

// Check resolves every load of source, and every load of the libraries it
// loads, against src: a save is refused for a load of a library or version
// that does not exist, a load cycle, or a chain deeper than MaxDepth. self is
// the library being saved, when it is one ("" otherwise), so a library that
// names itself is reported as the cycle it is. The findings are returned as
// one sentence each with its line in source.
func Check(ctx context.Context, src Source, source, self string) []Problem {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	if err != nil {
		return nil
	}
	c := &checker{ctx: ctx, src: src, self: self, checked: map[Ref]bool{}}
	var out []Problem
	for _, ld := range Loads(file) {
		if msg := c.check(ld, nil); msg != "" {
			out = append(out, Problem{Line: ld.Line, Message: msg})
		}
	}
	return out
}

// Problem is one load a save is refused for.
type Problem struct {
	Line    int
	Message string
}

type checker struct {
	ctx     context.Context
	src     Source
	self    string
	checked map[Ref]bool
}

// check follows one load, chain being the versions that led to it.
func (c *checker) check(ld Load, chain []Ref) string {
	if ld.Err != nil {
		return ld.Err.Error()
	}
	ref := ld.Ref
	if msg := c.refuse(ref, chain); msg != "" || c.checked[ref] {
		return msg
	}
	return c.follow(ref, chain)
}

// refuse is why a load of ref after chain is refused before it is read: the
// library being saved, a version already in the chain, or a chain too deep.
func (c *checker) refuse(ref Ref, chain []Ref) string {
	switch {
	case c.self != "" && ref.Name == c.self:
		return fmt.Sprintf("%s is the library being saved; a library cannot load itself", ref)
	case slices.Contains(chain, ref):
		return "load cycle: " + path(append(chain, ref))
	case len(chain) >= MaxDepth:
		return fmt.Sprintf("%s goes more than %d libraries deep", path(append(chain, ref)), MaxDepth)
	}
	return ""
}

// follow reads ref and checks every load it makes.
func (c *checker) follow(ref Ref, chain []Ref) string {
	if c.src == nil {
		return fmt.Sprintf("%s cannot be checked: no library store is available", ref)
	}
	source, err := c.src.LibrarySource(c.ctx, ref.Name, ref.Version)
	if err != nil {
		return describe(ref, err).Error()
	}
	file, err := scriptdialect.Options.Parse(ref.String(), source, 0)
	if err != nil {
		return fmt.Sprintf("%s does not parse: %v", ref, err)
	}
	next := append(append([]Ref{}, chain...), ref)
	for _, inner := range Loads(file) {
		if msg := c.check(inner, next); msg != "" {
			return msg
		}
	}
	c.checked[ref] = true
	return ""
}

func path(chain []Ref) string {
	parts := make([]string, 0, len(chain))
	for _, r := range chain {
		parts = append(parts, r.String())
	}
	return strings.Join(parts, " loads ")
}
