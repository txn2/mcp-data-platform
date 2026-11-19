// Package stmtlookup reads which statements a script's call can run when the
// source says so (#2615, #2623): a lookup into literals (spec[3], where spec
// is one value of a dict of constant lists) is each value it can pick, and a
// statements argument built as a list, a list comprehension or a name bound
// to either is the statements it holds. The flow diagram and an application's
// diagram read what a call writes through it.
package stmtlookup

import (
	"math/big"
	"slices"
	"strconv"
	"strings"

	"go.starlark.net/syntax"
)

// decimal is the base an integer key is written in.
const decimal = 10

// maxCandidates bounds the expressions one lookup is read as, and maxLookup
// how deep a lookup is followed through names and containers.
const (
	maxCandidates = 64
	maxLookup     = 8
)

// Resolver reads the names a lookup is followed through, in the scope a call
// was written in.
type Resolver interface {
	// Bound is the expression a name was last assigned, nil for none.
	Bound(id *syntax.Ident) syntax.Expr
	// Known is a local's value when the analyzer knows it.
	Known(id *syntax.Ident) (string, bool)
}

// Candidates is every expression e can evaluate to, when the source says so
// (#2623): a lookup into a literal -- {...}[k], [...][i], or a name bound to
// one and then indexed, as spec[3] where spec = {...}[kind] -- is each value
// the lookup can return, the value a constant key names or every value when
// the key is computed. Anything that is not a lookup is itself. ok is false
// when a lookup's container is not a literal the source holds, a constant key
// names nothing in it, or there are more than maxCandidates.
func Candidates(sc Resolver, e syntax.Expr) ([]syntax.Expr, bool) {
	return lookup(sc, e, 0)
}

func lookup(sc Resolver, e syntax.Expr, depth int) ([]syntax.Expr, bool) {
	if depth > maxLookup {
		return nil, false
	}
	switch x := e.(type) {
	case *syntax.ParenExpr:
		return lookup(sc, x.X, depth+1)
	case *syntax.Ident:
		// A name bound to a lookup is that lookup; any other name is left to
		// the renderer, which reads constants and locals.
		if bound, ok := sc.Bound(x).(*syntax.IndexExpr); ok {
			return lookup(sc, bound, depth+1)
		}
		return []syntax.Expr{x}, true
	case *syntax.IndexExpr:
		return index(sc, x, depth)
	}
	return []syntax.Expr{e}, true
}

// index is what one lookup can return: the value its key picks from each
// container its operand evaluates to.
func index(sc Resolver, x *syntax.IndexExpr, depth int) ([]syntax.Expr, bool) {
	containers, ok := containersOf(sc, x.X, depth+1)
	if !ok {
		return nil, false
	}
	var out []syntax.Expr
	for _, c := range containers {
		values, ok := pick(sc, c, x.Y)
		if !ok {
			return nil, false
		}
		out = append(out, values...)
	}
	if len(out) == 0 || len(out) > maxCandidates {
		return nil, false
	}
	return out, true
}

// containersOf is the list, tuple and dict literals e evaluates to.
func containersOf(sc Resolver, e syntax.Expr, depth int) ([]syntax.Expr, bool) {
	if depth > maxLookup {
		return nil, false
	}
	switch x := e.(type) {
	case *syntax.ParenExpr:
		return containersOf(sc, x.X, depth+1)
	case *syntax.ListExpr, *syntax.TupleExpr, *syntax.DictExpr:
		return []syntax.Expr{x}, true
	case *syntax.Ident:
		bound := sc.Bound(x)
		if bound == nil {
			return nil, false
		}
		return containersOf(sc, bound, depth+1)
	case *syntax.IndexExpr:
		values, ok := lookup(sc, x, depth+1)
		if !ok {
			return nil, false
		}
		return containersOfEach(sc, values, depth)
	case *syntax.BinaryExpr:
		// A list built in steps (xs = [a]; xs.append(b)) reads as one list.
		if x.Op != syntax.PLUS {
			return nil, false
		}
		left, okL := containersOf(sc, x.X, depth+1)
		right, okR := containersOf(sc, x.Y, depth+1)
		if !okL || !okR {
			return nil, false
		}
		joined := &syntax.ListExpr{}
		for _, c := range append(left, right...) {
			l, isList := c.(*syntax.ListExpr)
			if !isList {
				return nil, false
			}
			joined.List = append(joined.List, l.List...)
		}
		return []syntax.Expr{joined}, true
	case *syntax.CallExpr:
		// A function the source defines that returns a container, such as
		// spec_of(phase) returning specs[phase] from a dict it builds (#2699).
		values, ok := returnedValues(sc, x)
		if !ok {
			return nil, false
		}
		return containersOfEach(sc, values, depth)
	}
	return nil, false
}

// containersOfEach is the containers each of values evaluates to.
func containersOfEach(sc Resolver, values []syntax.Expr, depth int) ([]syntax.Expr, bool) {
	var out []syntax.Expr
	for _, v := range values {
		cs, ok := containersOf(sc, v, depth+1)
		if !ok {
			return nil, false
		}
		out = append(out, cs...)
	}
	return out, true
}

// pick is the values container c holds at key k: the one a constant key
// names, or every value when the key is only known when the script runs.
func pick(sc Resolver, c, k syntax.Expr) ([]syntax.Expr, bool) {
	key, constant := constKey(sc, k)
	var elems []syntax.Expr
	switch c := c.(type) {
	case *syntax.ListExpr:
		elems = c.List
	case *syntax.TupleExpr:
		elems = c.List
	case *syntax.DictExpr:
		return pickDict(c, key, constant)
	}
	if !constant {
		return elems, true
	}
	i, err := strconv.Atoi(key)
	if err != nil {
		return nil, false
	}
	if i < 0 {
		i += len(elems)
	}
	if i < 0 || i >= len(elems) {
		return nil, false
	}
	return []syntax.Expr{elems[i]}, true
}

// pickDict is pick over a dict literal, whose keys are string literals.
func pickDict(d *syntax.DictExpr, key string, constant bool) ([]syntax.Expr, bool) {
	var out []syntax.Expr
	for _, entry := range d.List {
		de, ok := entry.(*syntax.DictEntry)
		if !ok {
			return nil, false
		}
		if !constant {
			out = append(out, de.Value)
			continue
		}
		if lit, ok := de.Key.(*syntax.Literal); ok {
			if s, ok := lit.Value.(string); ok && s == key {
				return []syntax.Expr{de.Value}, true
			}
		}
	}
	if constant {
		return nil, false
	}
	return out, true
}

// constKey is a lookup key the source fixes: a string or integer literal, a
// negated integer, or a local whose value the analyzer knows.
func constKey(sc Resolver, k syntax.Expr) (string, bool) {
	switch k := k.(type) {
	case *syntax.Literal:
		switch v := k.Value.(type) {
		case string:
			return v, true
		case int64:
			return strconv.FormatInt(v, decimal), true
		case *big.Int:
			return v.String(), true
		}
	case *syntax.UnaryExpr:
		if k.Op == syntax.MINUS {
			if v, ok := constKey(sc, k.X); ok {
				return "-" + v, true
			}
		}
	case *syntax.Ident:
		return sc.Known(k)
	}
	return "", false
}

// Statements is what a statements argument holds, when the source says: a
// list literal's elements, a list comprehension's element (one statement
// repeated per row, #2623), or what a name is bound to in the function the
// call is written in (bound) or else in sc. ok is false when the statements
// are assembled at run time.
func Statements(sc Resolver, e syntax.Expr, bound map[string][]syntax.Expr) ([]syntax.Expr, bool) {
	stmts, ok := statementList(sc, e, bound, 0)
	if !ok {
		return nil, false
	}
	return expandCalls(sc, stmts, 0), true
}

// maxCallExpansion bounds how many helper calls deep a statement is followed.
const maxCallExpansion = 3

// expandCalls replaces each statement that is a call of a function the source
// defines by what it returns (#2699): [{"sql": SEND}, event(id, "sent")] is
// SEND and the statement event returns. A call the source does not define
// stays, and is read as a statement built when the script runs.
func expandCalls(sc Resolver, stmts []syntax.Expr, depth int) []syntax.Expr {
	if depth >= maxCallExpansion {
		return stmts
	}
	out := make([]syntax.Expr, 0, len(stmts))
	for _, e := range stmts {
		call, ok := e.(*syntax.CallExpr)
		if !ok {
			out = append(out, e)
			continue
		}
		values, ok := returnedValues(sc, call)
		if !ok {
			out = append(out, e)
			continue
		}
		out = append(out, expandCalls(sc, values, depth+1)...)
	}
	return out
}

func statementList(sc Resolver, e syntax.Expr, bound map[string][]syntax.Expr, depth int) ([]syntax.Expr, bool) {
	if depth > maxLookup {
		return nil, false
	}
	switch a := e.(type) {
	case *syntax.ParenExpr:
		return statementList(sc, a.X, bound, depth+1)
	case *syntax.ListExpr:
		return a.List, true
	case *syntax.Comprehension:
		if !a.Curly {
			return comprehensionStatements(sc, a, depth)
		}
	case *syntax.Ident:
		if strings.HasPrefix(a.Name, opaquePrefix) {
			return nil, false
		}
		return boundStatements(sc, a, bound, depth)
	case *syntax.BinaryExpr:
		if a.Op == syntax.PLUS {
			return concatenated(sc, a, bound, depth)
		}
	case *syntax.CallExpr:
		return returned(sc, a, depth)
	case *syntax.IndexExpr:
		return lookedUpStatements(sc, a, depth)
	}
	return nil, false
}

// concatenated is the statements of a + b, both lists the source says.
func concatenated(sc Resolver, a *syntax.BinaryExpr, bound map[string][]syntax.Expr, depth int) ([]syntax.Expr, bool) {
	left, ok := statementList(sc, a.X, bound, depth+1)
	if !ok {
		return nil, false
	}
	right, ok := statementList(sc, a.Y, bound, depth+1)
	if !ok {
		return nil, false
	}
	return append(append([]syntax.Expr{}, left...), right...), true
}

// lookedUpStatements is the statements of a list picked by a lookup into
// literals (spec["write"]): every statement of every list the lookup can
// pick.
func lookedUpStatements(sc Resolver, x *syntax.IndexExpr, depth int) ([]syntax.Expr, bool) {
	values, ok := lookup(sc, x, depth+1)
	if !ok {
		return nil, false
	}
	var out []syntax.Expr
	for _, v := range values {
		if v == syntax.Expr(x) {
			return nil, false
		}
		stmts, ok := statementList(sc, v, nil, depth+1)
		if !ok {
			return nil, false
		}
		out = append(out, stmts...)
	}
	return out, true
}

// comprehensionStatements is what a list comprehension holds: its element,
// one statement repeated per row (#2623), or, when the element (or its "sql")
// is the comprehension's loop variable, each statement the loop draws it
// from (#2699): [{"sql": sql} for sql in spec["write"]] is every statement
// of spec["write"].
func comprehensionStatements(sc Resolver, c *syntax.Comprehension, depth int) ([]syntax.Expr, bool) {
	target := c.Body
	if d, ok := target.(*syntax.DictExpr); ok {
		target = sqlField(d)
	}
	id, ok := target.(*syntax.Ident)
	if !ok {
		return []syntax.Expr{c.Body}, true
	}
	for _, clause := range c.Clauses {
		fc, ok := clause.(*syntax.ForClause)
		if !ok {
			continue
		}
		if elem, ok := LoopElements(fc.Vars, fc.X)[id.Name]; ok {
			return elementStatements(sc, elem, depth)
		}
		if slices.Contains(targetNames(fc.Vars), id.Name) {
			return nil, false
		}
	}
	return []syntax.Expr{c.Body}, true
}

// elementStatements is each statement an element of a loop can be: elem is a
// lookup into what the loop iterates, read as every element.
func elementStatements(sc Resolver, elem syntax.Expr, depth int) ([]syntax.Expr, bool) {
	ix, ok := elem.(*syntax.IndexExpr)
	if !ok {
		return nil, false
	}
	if lists, ok := statementList(sc, ix.X, nil, depth+1); ok {
		return lists, true
	}
	return lookedUpStatements(sc, ix, depth)
}

// sqlField is a statement dict's "sql" value, nil for none.
func sqlField(d *syntax.DictExpr) syntax.Expr {
	for _, entry := range d.List {
		de, ok := entry.(*syntax.DictEntry)
		if !ok {
			continue
		}
		if lit, ok := de.Key.(*syntax.Literal); ok && lit.Value == "sql" {
			return de.Value
		}
	}
	return nil
}

// boundStatements is what the name id is bound to as a statement list: every
// list the function assigns it (bound), or else what sc binds it to.
func boundStatements(sc Resolver, id *syntax.Ident, bound map[string][]syntax.Expr, depth int) ([]syntax.Expr, bool) {
	exprs, ok := bound[id.Name]
	if !ok {
		if x := sc.Bound(id); x != nil {
			return statementList(sc, x, nil, depth+1)
		}
		return nil, false
	}
	var out []syntax.Expr
	for _, x := range exprs {
		stmts, ok := statementList(sc, x, nil, depth+1)
		if !ok {
			return nil, false
		}
		out = append(out, stmts...)
	}
	return out, true
}

// ListsBound is each list a function body builds under a plain name, by the
// name: every list literal, list comprehension or other value assigned to it,
// and every element appended and list extended or added to it (#2699). A name
// the body only appends to holds, first, what it was bound to before the body
// ran (a parameter), as the name itself read in the enclosing scope.
func ListsBound(body []syntax.Stmt) map[string][]syntax.Expr {
	out, _ := collectLists(body)
	return out
}

// Grown is ListsBound narrowed to the names the body grows (appends to,
// extends, or adds to with +=): the lists built in more than one step.
func Grown(body []syntax.Stmt) map[string][]syntax.Expr {
	all, grown := collectLists(body)
	out := map[string][]syntax.Expr{}
	for name := range grown {
		out[name] = all[name]
	}
	return out
}

// collectLists is ListsBound and the names the body grows.
func collectLists(body []syntax.Stmt) (out map[string][]syntax.Expr, grown map[string]bool) {
	out = map[string][]syntax.Expr{}
	assigned := map[string]bool{}
	grown = map[string]bool{}
	grow := func(name string, x syntax.Expr) {
		grown[name] = true
		if !assigned[name] && len(out[name]) == 0 {
			out[name] = append(out[name], &syntax.Ident{Name: name})
		}
		out[name] = append(out[name], x)
	}
	for _, stmt := range body {
		syntax.Walk(stmt, func(node syntax.Node) bool {
			if _, def := node.(*syntax.DefStmt); def {
				return false
			}
			if name, list, ok := listAssigned(node); ok {
				assigned[name] = true
				out[name] = append(out[name], list)
				return true
			}
			if name, x, ok := listGrown(node); ok {
				grow(name, x)
			}
			return true
		})
	}
	return out, grown
}

// listGrown is the name and the list a statement adds to it: x.append(e)
// adds [e], x.extend(l) and x += l add l.
func listGrown(node syntax.Node) (string, syntax.Expr, bool) {
	switch n := node.(type) {
	case *syntax.AssignStmt:
		if id, ok := n.LHS.(*syntax.Ident); ok && n.Op == syntax.PLUS_EQ {
			return id.Name, n.RHS, true
		}
	case *syntax.CallExpr:
		dot, ok := n.Fn.(*syntax.DotExpr)
		if !ok || len(n.Args) != 1 {
			return "", nil, false
		}
		id, ok := dot.X.(*syntax.Ident)
		if !ok {
			return "", nil, false
		}
		switch dot.Name.Name {
		case "append":
			return id.Name, &syntax.ListExpr{List: []syntax.Expr{n.Args[0]}}, true
		case "extend":
			return id.Name, n.Args[0], true
		}
	}
	return "", nil, false
}

// listAssigned is the name and the value a plain assignment binds, any value
// but a dict comprehension: what it holds as a list is read when the list is
// (statementList), and one it cannot read leaves the list assembled at run
// time rather than silently shorter.
func listAssigned(node syntax.Node) (string, syntax.Expr, bool) {
	a, ok := node.(*syntax.AssignStmt)
	if !ok || a.Op != syntax.EQ {
		return "", nil, false
	}
	id, ok := a.LHS.(*syntax.Ident)
	if !ok {
		return "", nil, false
	}
	if c, ok := a.RHS.(*syntax.Comprehension); ok && c.Curly {
		return "", nil, false
	}
	return id.Name, a.RHS, true
}
