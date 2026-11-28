// Package scriptdefs reads what a script defines and what the platform calls
// in it, from its syntax alone: every def by a key that tells two nested defs
// of one name apart, the def a name refers to where it is written (#2848), and
// the calls the platform makes after the module runs. The script flow graph
// reads a script through it.
package scriptdefs

import (
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
)

// Table is every def of a script by its key: a module-level def by its name,
// a nested one by the key of the function it is written in, a dot and its
// name. Two nested defs of one name in two functions are two functions, which
// a table keyed by the bare name made one: the last in the file (#2848).
type Table struct {
	// Funcs is every def by its key, and KeyOf each def's key.
	Funcs map[string]*syntax.DefStmt
	KeyOf map[*syntax.DefStmt]string
	// ParentOf is the key of the function a nested def is written in, by the
	// nested def's key.
	ParentOf map[string]string
}

// Collect reads every def in stmts, nested ones included, under its key.
func Collect(stmts []syntax.Stmt) *Table {
	t := &Table{Funcs: map[string]*syntax.DefStmt{}, KeyOf: map[*syntax.DefStmt]string{}, ParentOf: map[string]string{}}
	t.collect(stmts, "")
	return t
}

// collect records the defs of stmts, written in the function of key parent.
func (t *Table) collect(stmts []syntax.Stmt, parent string) {
	for _, s := range stmts {
		switch s := s.(type) {
		case *syntax.DefStmt:
			key := s.Name.Name
			if parent != "" {
				key = parent + "." + key
				t.ParentOf[key] = parent
			}
			t.Funcs[key], t.KeyOf[s] = s, key
			t.collect(s.Body, key)
		case *syntax.ForStmt:
			t.collect(s.Body, parent)
		case *syntax.IfStmt:
			t.collect(s.True, parent)
			t.collect(s.False, parent)
		}
	}
}

// Resolve is the def a name written in the function of key scope ("" at
// module level) refers to, and its key: a def nested in that function, then
// in each function enclosing it, then a module-level def, as Starlark
// resolves a name. A def nested in another function is not visible outside
// it. It is nil, with an empty key, for a name no def holds there.
func (t *Table) Resolve(scope, name string) (string, *syntax.DefStmt) {
	for s := scope; s != ""; s = t.ParentOf[s] {
		if d := t.Funcs[s+"."+name]; d != nil {
			return s + "." + name, d
		}
	}
	if d := t.Funcs[name]; d != nil {
		return name, d
	}
	return "", nil
}

// EntryCalls are the calls the platform makes after the module runs: main()
// for a run; in a script with no main(), handle(req) for an endpoint's
// request (#2172, #2315), or a form's form_spec(req) then form_submit(req,
// values), in the order a person meets them (#2176). None when it makes none.
func EntryCalls(file *syntax.File) []*syntax.CallExpr {
	if main := scriptdialect.EntryPoint(file); main != nil {
		return []*syntax.CallExpr{{Fn: main.Name, Lparen: main.Def, Rparen: main.Def}}
	}
	if handler := scriptdialect.HandlerOf(file); handler != nil {
		return []*syntax.CallExpr{call(handler, "req")}
	}
	var calls []*syntax.CallExpr
	for _, s := range file.Stmts {
		d, ok := s.(*syntax.DefStmt)
		switch {
		case !ok:
		case d.Name.Name == scriptdialect.FormSpecName && len(d.Params) == 1:
			calls = append([]*syntax.CallExpr{call(d, "req")}, calls...)
		case d.Name.Name == scriptdialect.FormSubmitName && len(d.Params) == 2:
			calls = append(calls, call(d, "req", "values"))
		}
	}
	if len(calls) != 2 {
		return nil
	}
	return calls
}

// call is the platform's call of d with arguments of the names given.
func call(d *syntax.DefStmt, args ...string) *syntax.CallExpr {
	c := &syntax.CallExpr{Fn: d.Name, Lparen: d.Def, Rparen: d.Def}
	for _, name := range args {
		c.Args = append(c.Args, &syntax.Ident{NamePos: d.Def, Name: name})
	}
	return c
}
