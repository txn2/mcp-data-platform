package scriptflow

import (
	"fmt"

	"go.starlark.net/syntax"
)

// run walks the module twice. The second pass lets a value assigned late in a
// top-level loop reach a step written earlier in it, which is what the loop
// does at run time; steps are keyed by where they are, so the second pass adds
// inputs to the steps the first one found rather than finding new ones.
func (a *analyzer) run() {
	for range 2 {
		a.top = newFrame("")
		a.stack = []*frame{a.top}
		a.stmts(a.top, a.file.Stmts)
	}
}

func (a *analyzer) stmts(f *frame, list []syntax.Stmt) {
	for _, s := range list {
		a.stmt(f, s)
	}
}

func (a *analyzer) stmt(f *frame, s syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.ExprStmt:
		a.expr(f, s.X)
	case *syntax.AssignStmt:
		a.assignStmt(f, s)
	case *syntax.ReturnStmt:
		if s.Result != nil {
			f.ret.add(a.expr(f, s.Result))
		}
	case *syntax.IfStmt:
		a.ifStmt(f, s)
	case *syntax.ForStmt:
		a.forStmt(f, s)
	}
}

// assignStmt binds the value and records what the source says about it: the
// expression, a dict literal, and the text when the source fully determines
// it.
func (a *analyzer) assignStmt(f *frame, s *syntax.AssignStmt) {
	t := a.expr(f, s.RHS)
	if id, ok := s.LHS.(*syntax.Ident); ok {
		a.record(f, id.Name, s)
	}
	a.assign(f, s.LHS, t, s.Op != syntax.EQ)
}

// record replaces what the frame knows about a variable with what one
// assignment to it says. An augmented assignment leaves it unknown.
func (a *analyzer) record(f *frame, name string, s *syntax.AssignStmt) {
	delete(f.known, name)
	delete(f.shown, name)
	delete(f.dicts, name)
	if s.Op != syntax.EQ {
		return
	}
	f.exprs[name] = s.RHS
	if d, ok := s.RHS.(*syntax.DictExpr); ok {
		f.dicts[name] = d
	}
	if v, full := a.renderIn(f, s.RHS); full {
		f.known[name] = v
	}
}

// ifStmt walks both branches and joins them: a variable's origins after the if
// are the union of both, and its text is known only where both branches agree.
func (a *analyzer) ifStmt(f *frame, s *syntax.IfStmt) {
	a.noteDecides(a.expr(f, s.Cond))
	envBefore, knownBefore := f.env.clone(), cloneStrings(f.known)
	a.stmts(f, s.True)
	envTrue, knownTrue := f.env, f.known
	f.env, f.known = envBefore, knownBefore
	a.stmts(f, s.False)
	f.env.merge(envTrue)
	for k, v := range f.known {
		if knownTrue[k] != v {
			delete(f.known, k)
		}
	}
}

// forStmt walks a loop body twice, so a value one iteration computes reaches
// the steps the next iteration runs.
func (a *analyzer) forStmt(f *frame, s *syntax.ForStmt) {
	it := a.expr(f, s.X)
	f.loops = append(f.loops, clip("for "+a.text(s.Vars)+" in "+a.text(s.X), maxLoopLabel))
	changed := assignedNames(s.Body)
	for _, id := range targetNames(s.Vars) {
		changed[id] = true
	}
	for pass := range 2 {
		for name := range changed {
			delete(f.known, name)
		}
		before := f.env.clone()
		a.assign(f, s.Vars, it, pass > 0)
		a.stmts(f, s.Body)
		f.env.merge(before)
	}
	f.loops = f.loops[:len(f.loops)-1]
}

// maxLoopLabel is the longest loop chip a step carries.
const maxLoopLabel = 64

// noteDecides records the parameters a condition reads.
func (a *analyzer) noteDecides(t origins) {
	for k := range t {
		if name, ok := paramOrigin(k); ok {
			a.decides[name] = true
		}
	}
}

func (a *analyzer) exprList(f *frame, list []syntax.Expr) origins {
	t := origins{}
	for _, x := range list {
		t.add(a.expr(f, x))
	}
	return t
}

// expr returns the origins of an expression's value, recording every step the
// expression calls on the way.
func (a *analyzer) expr(f *frame, e syntax.Expr) origins {
	switch e := e.(type) {
	case *syntax.Ident:
		return a.lookup(f, e.Name).clone()
	case *syntax.DotExpr:
		return a.dotExpr(f, e)
	case *syntax.IndexExpr:
		return a.indexExpr(f, e)
	case *syntax.CondExpr:
		a.noteDecides(a.expr(f, e.Cond))
		return a.exprList(f, []syntax.Expr{e.True, e.False})
	case *syntax.Comprehension:
		return a.comprehension(f, e)
	case *syntax.CallExpr:
		return a.call(f, e)
	}
	return a.exprList(f, parts(e))
}

// parts is the subexpressions whose origins an expression's value carries,
// for every expression that is no more than the sum of its parts.
func parts(e syntax.Expr) []syntax.Expr {
	switch e := e.(type) {
	case *syntax.SliceExpr:
		return []syntax.Expr{e.X, e.Lo, e.Hi, e.Step}
	case *syntax.ParenExpr:
		return []syntax.Expr{e.X}
	case *syntax.UnaryExpr:
		return []syntax.Expr{e.X}
	case *syntax.BinaryExpr:
		return []syntax.Expr{e.X, e.Y}
	case *syntax.ListExpr:
		return e.List
	case *syntax.TupleExpr:
		return e.List
	case *syntax.DictExpr:
		return e.List
	case *syntax.DictEntry:
		return []syntax.Expr{e.Key, e.Value}
	case *syntax.LambdaExpr:
		return []syntax.Expr{e.Body}
	}
	return nil
}

// dotExpr reads run.state, the one input a run carries from the last one.
func (a *analyzer) dotExpr(f *frame, e *syntax.DotExpr) origins {
	if id, ok := e.X.(*syntax.Ident); ok && id.Name == "run" {
		if e.Name.Name != "state" {
			return origins{}
		}
		a.readState = true
		if a.stateLine == 0 {
			a.stateLine = int(e.Dot.Line)
		}
		return origin(StateNodeID)
	}
	return a.expr(f, e.X)
}

// indexExpr reads run.params["name"], a run parameter.
func (a *analyzer) indexExpr(f *frame, e *syntax.IndexExpr) origins {
	if name, ok := paramKey(e.X, e.Y); ok {
		a.noteParam(name, int(e.Lbrack.Line))
		return origin(paramPrefix + name)
	}
	return a.exprList(f, []syntax.Expr{e.X, e.Y})
}

// comprehension binds its loop variables in a scope that ends with it.
func (a *analyzer) comprehension(f *frame, e *syntax.Comprehension) origins {
	saved := f.env.clone()
	t := origins{}
	for _, c := range e.Clauses {
		switch c := c.(type) {
		case *syntax.ForClause:
			it := a.expr(f, c.X)
			t.add(it)
			a.assign(f, c.Vars, it, false)
		case *syntax.IfClause:
			a.noteDecides(a.expr(f, c.Cond))
		}
	}
	t.add(a.expr(f, e.Body))
	f.env = saved
	return t
}

// paramPrefix marks a parameter origin.
const paramPrefix = "param:"

func paramOrigin(k string) (string, bool) {
	if len(k) > len(paramPrefix) && k[:len(paramPrefix)] == paramPrefix {
		return k[len(paramPrefix):], true
	}
	return "", false
}

func (a *analyzer) noteParam(name string, line int) {
	if _, seen := a.params[name]; !seen {
		a.params[name] = line
	}
}

// paramKey recognizes run.params[<literal>].
func paramKey(x, key syntax.Expr) (string, bool) {
	if !isRunParams(x) {
		return "", false
	}
	if lit, ok := key.(*syntax.Literal); ok {
		if s, ok := lit.Value.(string); ok {
			return s, true
		}
	}
	return "", false
}

func isRunParams(x syntax.Expr) bool {
	dot, ok := x.(*syntax.DotExpr)
	if !ok || dot.Name.Name != "params" {
		return false
	}
	id, ok := dot.X.(*syntax.Ident)
	return ok && id.Name == "run"
}

// mutators are the methods that change the value they are called on, so the
// argument's origins become the receiver's.
var mutators = map[string]bool{"append": true, "extend": true, "update": true, "insert": true, "setdefault": true}

func (a *analyzer) call(f *frame, c *syntax.CallExpr) origins {
	switch fn := c.Fn.(type) {
	case *syntax.DotExpr:
		if member, ok := platformMember(c); ok {
			return a.platformCall(f, c, member)
		}
		return a.methodCall(f, c, fn)
	case *syntax.Ident:
		if d, ok := a.funcs[fn.Name]; ok {
			return a.invoke(f, d, c)
		}
		args := a.argOrigins(f, c)
		if fn.Name == "print" || fn.Name == "fail" {
			return origins{}
		}
		return args
	}
	t := a.expr(f, c.Fn)
	t.add(a.argOrigins(f, c))
	return t
}

// methodCall is a call on a value: run.params.get("name") is a parameter; a
// mutating method adds its argument's origins to the receiver.
func (a *analyzer) methodCall(f *frame, c *syntax.CallExpr, fn *syntax.DotExpr) origins {
	if fn.Name.Name == "get" && len(c.Args) > 0 {
		if name, ok := paramKey(fn.X, c.Args[0]); ok {
			a.noteParam(name, int(fn.Dot.Line))
			return origin(paramPrefix + name)
		}
	}
	args := a.argOrigins(f, c)
	if mutators[fn.Name.Name] {
		a.addToBase(f, fn.X, args)
	}
	recv := a.expr(f, fn.X)
	recv.add(args)
	return recv
}

// argOrigins is the union of a call's argument origins.
func (a *analyzer) argOrigins(f *frame, c *syntax.CallExpr) origins {
	t := origins{}
	for _, arg := range c.Args {
		if b, ok := arg.(*syntax.BinaryExpr); ok && b.Op == syntax.EQ {
			t.add(a.expr(f, b.Y))
			continue
		}
		t.add(a.expr(f, arg))
	}
	return t
}

// platformCall records one step and returns its result as a new origin.
func (a *analyzer) platformCall(f *frame, c *syntax.CallExpr, member string) origins {
	args := a.argOrigins(f, c)
	if member == memberProgress {
		return origins{}
	}
	key := fmt.Sprintf("%s|%d:%d", f.chain, c.Lparen.Line, c.Lparen.Col)
	s, ok := a.steps[key]
	if !ok {
		if len(a.order) >= maxSteps {
			a.truncated = true
			return origins{}
		}
		s = a.newStep(f, c, member)
		a.steps[key] = s
		a.order = append(a.order, s)
	}
	s.inputs.add(args)
	return origin(s.id())
}
