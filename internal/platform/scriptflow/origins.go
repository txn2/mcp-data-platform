package scriptflow

import (
	"go.starlark.net/syntax"
)

// origins is where a value came from: each origin (a step "op:<n>",
// "param:<name>", or "state") mapped to the pure user functions the value
// passed through on its way from there.
type origins map[string]map[string]bool

// origin is a value that came from one place, unchanged.
func origin(id string) origins { return origins{id: map[string]bool{}} }

// add merges o into t.
func (t origins) add(o origins) {
	for k, fns := range o {
		cur, ok := t[k]
		if !ok {
			cur = map[string]bool{}
			t[k] = cur
		}
		for fn := range fns {
			cur[fn] = true
		}
	}
}

func (t origins) clone() origins {
	c := origins{}
	c.add(t)
	return c
}

// via is t after passing through the pure function fn.
func (t origins) via(fn string) origins {
	c := origins{}
	for k, fns := range t {
		n := map[string]bool{fn: true}
		for f := range fns {
			n[f] = true
		}
		c[k] = n
	}
	return c
}

// env maps a variable to the origins of its value.
type env map[string]origins

func (e env) clone() env {
	c := env{}
	for k, v := range e {
		c[k] = v.clone()
	}
	return c
}

// merge adds o's origins into e, for the join after a branch.
func (e env) merge(o env) {
	for k, v := range o {
		if cur, ok := e[k]; ok {
			cur.add(v)
		} else {
			e[k] = v.clone()
		}
	}
}

// frame is one expansion of a function body, or the module itself.
type frame struct {
	fn string
	// group is the box the frame's steps are drawn in; chain identifies the
	// expansion, so a helper called from two places makes two steps.
	group string
	chain string
	env   env
	// known holds the variables whose value the source fully determines at
	// this point, exprs the expression each variable was last assigned, and
	// shown the rendered text of an argument that was computed in the caller.
	// dicts holds a variable assigned a dict literal, so a call passing it is
	// read as that dict.
	known map[string]string
	exprs map[string]syntax.Expr
	shown map[string]string
	dicts map[string]*syntax.DictExpr
	loops []string
	// lexical is the frame of the enclosing function, for a nested def.
	lexical *frame
	ret     origins
	// wrapper and site are set while expanding a one-call wrapper, which is
	// folded into its step rather than drawn as a box.
	wrapper string
	site    int
	// sites is the position of every call on the stack that led here,
	// outermost first: what a run's thread reports at a call made in this
	// frame (internal/scriptcallsite).
	sites []string
}

func newFrame(fn string) *frame {
	return &frame{
		fn: fn, env: env{}, known: map[string]string{}, exprs: map[string]syntax.Expr{},
		shown: map[string]string{}, dicts: map[string]*syntax.DictExpr{}, ret: origins{},
	}
}

// lookup returns the origins of a variable visible from f.
func (a *analyzer) lookup(f *frame, name string) origins {
	for fr := f; fr != nil; fr = fr.lexical {
		if t, ok := fr.env[name]; ok {
			return t
		}
	}
	if t, ok := a.top.env[name]; ok {
		return t
	}
	return origins{}
}

// assign binds an assignment target to t. weak adds to the target's origins
// rather than replacing them: an augmented assignment, or a loop variable on
// the second pass.
func (a *analyzer) assign(f *frame, lhs syntax.Expr, t origins, weak bool) {
	switch l := lhs.(type) {
	case *syntax.Ident:
		if cur, ok := f.env[l.Name]; ok && weak {
			cur.add(t)
			return
		}
		f.env[l.Name] = t.clone()
	case *syntax.TupleExpr, *syntax.ListExpr:
		for _, x := range elements(l) {
			a.assign(f, x, t, weak)
		}
	case *syntax.ParenExpr:
		a.assign(f, l.X, t, weak)
	case *syntax.IndexExpr:
		a.addToBase(f, l.X, t)
	case *syntax.DotExpr:
		a.addToBase(f, l.X, t)
	}
}

// elements is the items of a tuple or list target.
func elements(e syntax.Expr) []syntax.Expr {
	switch e := e.(type) {
	case *syntax.TupleExpr:
		return e.List
	case *syntax.ListExpr:
		return e.List
	}
	return nil
}

// addToBase adds t to the variable an element or field store writes into,
// wherever that variable lives: rows[i] = x and totals.update(x) change rows
// and totals.
func (a *analyzer) addToBase(f *frame, x syntax.Expr, t origins) {
	id := baseIdent(x)
	if id == nil {
		return
	}
	for fr := f; fr != nil; fr = fr.lexical {
		if cur, ok := fr.env[id.Name]; ok {
			cur.add(t)
			return
		}
	}
	if cur, ok := a.top.env[id.Name]; ok {
		cur.add(t)
		return
	}
	f.env[id.Name] = t.clone()
}

// baseIdent is the variable at the root of an index, field or parenthesized
// expression.
func baseIdent(x syntax.Expr) *syntax.Ident {
	for {
		switch e := x.(type) {
		case *syntax.IndexExpr:
			x = e.X
		case *syntax.DotExpr:
			x = e.X
		case *syntax.ParenExpr:
			x = e.X
		case *syntax.Ident:
			return e
		default:
			return nil
		}
	}
}
