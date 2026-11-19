package stmtlookup

import (
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/scriptconst"
)

// Libraries is the source of each library version a script loads, by the
// reference its load names (#2699). A statement a script runs is often a
// constant a library defines, or the value a library's function returns; with
// the library's source a name the script loads reads as what the library
// binds it to, at the version the load names, rather than as a value only
// known when the script runs.
type Libraries map[scriptlib.Ref]string

// Named is each library's source by its reference's text.
func (l Libraries) Named() map[string]string {
	out := make(map[string]string, len(l))
	for ref, text := range l {
		out[ref.String()] = text
	}
	return out
}

// library is what one library binds at module scope.
type library struct {
	consts   scriptconst.Table
	assigned map[string]syntax.Expr
	funcs    map[string]*syntax.DefStmt
}

// Loaded is one name a script loads: the library and the name it binds
// there. The zero Loaded is a name loaded from no library read.
type Loaded struct {
	lib  *library
	name string
}

// LoadLibraries reads every name file loads from a library libs holds. A
// load of a library libs does not hold, or whose source does not parse, binds
// nothing.
func LoadLibraries(file *syntax.File, libs Libraries) map[string]Loaded {
	out := map[string]Loaded{}
	if len(libs) == 0 {
		return out
	}
	modules := map[scriptlib.Ref]*library{}
	for _, stmt := range file.Stmts {
		load, ok := stmt.(*syntax.LoadStmt)
		if !ok {
			continue
		}
		ref, err := scriptlib.ParseModule(load.ModuleName())
		if err != nil {
			continue
		}
		lib, ok := modules[ref]
		if !ok {
			lib = parseLibrary(libs[ref])
			modules[ref] = lib
		}
		if lib == nil {
			continue
		}
		for i, to := range load.To {
			out[to.Name] = Loaded{lib: lib, name: load.From[i].Name}
		}
	}
	return out
}

// parseLibrary is what a library's source binds at module scope, nil when
// there is no source or it does not parse.
func parseLibrary(source string) *library {
	if source == "" {
		return nil
	}
	file, err := scriptrun.Parse(source)
	if err != nil {
		return nil
	}
	lib := &library{consts: scriptconst.Collect(file), assigned: map[string]syntax.Expr{}, funcs: map[string]*syntax.DefStmt{}}
	for _, stmt := range file.Stmts {
		switch s := stmt.(type) {
		case *syntax.AssignStmt:
			if id, ok := s.LHS.(*syntax.Ident); ok && s.Op == syntax.EQ {
				lib.assigned[id.Name] = s.RHS
			}
		case *syntax.DefStmt:
			lib.funcs[s.Name.Name] = s
		}
	}
	return lib
}

// libScope is the scope a library's names are read in: its own module.
type libScope struct{ lib *library }

// Bound is what the library binds a module name to.
func (l libScope) Bound(id *syntax.Ident) syntax.Expr { return l.lib.assigned[id.Name] }

// Known is a library constant's text.
func (l libScope) Known(id *syntax.Ident) (string, bool) { return l.lib.consts.Named(id.Name) }

// Global is a library module name's constant text, else what the library
// binds it to.
func (l libScope) Global(id *syntax.Ident) (string, syntax.Expr, bool) {
	if v, ok := l.lib.consts.Named(id.Name); ok {
		return v, nil, true
	}
	if x, ok := l.lib.assigned[id.Name]; ok {
		return "", x, true
	}
	return "", nil, false
}

// Func is a function the library defines, read in the library's scope.
func (l libScope) Func(name string) (*syntax.DefStmt, Resolver) {
	if d := l.lib.funcs[name]; d != nil {
		return d, l
	}
	return nil, nil
}

// Constant is a loaded name's text when the library binds it to a constant.
func (n Loaded) Constant() (string, bool) {
	if n.lib == nil {
		return "", false
	}
	return n.lib.consts.Named(n.name)
}

// Expr is what the library binds a loaded name to, scope free; nil for none.
func (n Loaded) Expr() syntax.Expr {
	if n.lib == nil {
		return nil
	}
	x, ok := n.lib.assigned[n.name]
	if !ok {
		return nil
	}
	return Substitute(libScope{n.lib}, x)
}

// Def is the function a loaded name is in its library, with the scope its
// body is read in; nil for a name that is no function.
func (n Loaded) Def() (*syntax.DefStmt, Resolver) {
	if n.lib == nil {
		return nil, nil
	}
	if d := n.lib.funcs[n.name]; d != nil {
		return d, libScope{n.lib}
	}
	return nil, nil
}
