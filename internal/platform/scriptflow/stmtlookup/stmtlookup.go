// Package stmtlookup reads which statements a script's call can run when the
// source says so (#2615, #2623): a lookup into literals (spec[3], where spec
// is one value of a dict of constant lists) is each value it can pick, and a
// statements argument built as a list, a list comprehension or a name bound
// to either is the statements it holds. The flow diagram and an application's
// diagram read what a call writes through it.
package stmtlookup

import (
	"math/big"
	"strconv"

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
	return nil, false
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
	return statementList(sc, e, bound, 0)
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
			return []syntax.Expr{a.Body}, true
		}
	case *syntax.Ident:
		return boundStatements(sc, a, bound, depth)
	}
	return nil, false
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

// ListsBound is each list literal and list comprehension a function body
// assigns to a plain name, by the name.
func ListsBound(body []syntax.Stmt) map[string][]syntax.Expr {
	out := map[string][]syntax.Expr{}
	for _, stmt := range body {
		syntax.Walk(stmt, func(node syntax.Node) bool {
			if name, list, ok := listAssigned(node); ok {
				out[name] = append(out[name], list)
			}
			return true
		})
	}
	return out
}

// listAssigned is the name and the list a plain assignment binds, when its
// right side is a list literal or a list comprehension.
func listAssigned(node syntax.Node) (string, syntax.Expr, bool) {
	a, ok := node.(*syntax.AssignStmt)
	if !ok || a.Op != syntax.EQ {
		return "", nil, false
	}
	id, ok := a.LHS.(*syntax.Ident)
	if !ok {
		return "", nil, false
	}
	switch rhs := a.RHS.(type) {
	case *syntax.ListExpr:
		return id.Name, rhs, true
	case *syntax.Comprehension:
		return id.Name, rhs, !rhs.Curly
	}
	return "", nil, false
}
