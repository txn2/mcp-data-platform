package scriptlint

import (
	"fmt"

	"go.starlark.net/syntax"
)

// functions applies the per-function rules to every def: the complexity and
// size limits, and the docstring every function carries.
func (l *linter) functions() {
	for _, d := range l.defs() {
		name := d.Name.Name
		if n := cyclomatic(d.Body); n > MaxCyclomatic {
			l.add(finding{
				rule: RuleCyclomatic, subject: name, line: line(d),
				message: fmt.Sprintf("`%s` has cyclomatic complexity %d (limit %d)", name, n, MaxCyclomatic),
				hint:    "Every if, elif, for, conditional expression, comprehension clause, and and/or is a path. Move a branch's work into a function with a name that says what it decides.",
			})
		}
		if n := cognitive(d.Body); n > MaxCognitive {
			l.add(finding{
				rule: RuleCognitive, subject: name, line: line(d),
				message: fmt.Sprintf("`%s` has cognitive complexity %d (limit %d)", name, n, MaxCognitive),
				hint:    "Nested branches cost more the deeper they sit. Return early instead of nesting, and move an inner loop's body into its own function.",
			})
		}
		if n := statements(d.Body); n > MaxStatements {
			l.add(finding{
				rule: RuleFunctionLength, subject: name, line: line(d),
				message: fmt.Sprintf("`%s` has %d statements (limit %d)", name, n, MaxStatements),
				hint:    "Split it into steps, one function each, and have this function call them in order.",
			})
		}
		if at := tooDeep(d.Body, 0); at != nil {
			l.add(finding{
				rule: RuleNestingDepth, subject: name, line: line(at),
				message: fmt.Sprintf("`%s` nests blocks more than %d deep", name, MaxNesting),
				hint:    "Return or continue early instead of nesting another level, or move the inner block into a function.",
			})
		}
		if !hasDocstring(d) {
			l.add(finding{
				rule: RuleMissingDocstring, subject: name, line: line(d),
				message: fmt.Sprintf("`%s` has no docstring", name),
				hint: "Open the body with a one-sentence \"\"\"docstring\"\"\" saying what the function does in plain words. " +
					"The script's flow diagram shows that sentence on the function's box.",
			})
		}
	}
}

// hasDocstring reports whether a def's body opens with a string literal.
func hasDocstring(d *syntax.DefStmt) bool {
	if len(d.Body) == 0 {
		return false
	}
	es, ok := d.Body[0].(*syntax.ExprStmt)
	if !ok {
		return false
	}
	lit, ok := es.X.(*syntax.Literal)
	return ok && lit.Token == syntax.STRING
}

// cyclomatic is one plus the decision points in a function body.
func cyclomatic(body []syntax.Stmt) int {
	n := 1
	walkBody(body, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.IfStmt, *syntax.ForStmt, *syntax.CondExpr, *syntax.ForClause, *syntax.IfClause:
			n++
		case *syntax.BinaryExpr:
			if node.Op == syntax.AND || node.Op == syntax.OR {
				n++
			}
		}
		return true
	})
	return n
}

// statements counts every statement in a function body, a nested def as one.
func statements(body []syntax.Stmt) int {
	n := 0
	for _, s := range body {
		n++
		switch s := s.(type) {
		case *syntax.IfStmt:
			n += statements(s.True) + statements(s.False)
		case *syntax.ForStmt:
			n += statements(s.Body)
		}
	}
	return n
}

// tooDeep returns the first statement that sits more than MaxNesting blocks
// deep, or nil. An elif continues its if rather than nesting under it.
func tooDeep(body []syntax.Stmt, depth int) syntax.Stmt {
	for _, s := range body {
		var inner [][]syntax.Stmt
		switch s := s.(type) {
		case *syntax.IfStmt:
			inner = ifBranches(s)
		case *syntax.ForStmt:
			inner = [][]syntax.Stmt{s.Body}
		default:
			continue
		}
		if depth+1 > MaxNesting {
			return s
		}
		for _, b := range inner {
			if at := tooDeep(b, depth+1); at != nil {
				return at
			}
		}
	}
	return nil
}

// ifBranches is every block of an if/elif/else chain.
func ifBranches(s *syntax.IfStmt) [][]syntax.Stmt {
	out := [][]syntax.Stmt{s.True}
	for {
		if elif := elifOf(s); elif != nil {
			out = append(out, elif.True)
			s = elif
			continue
		}
		if len(s.False) > 0 {
			out = append(out, s.False)
		}
		return out
	}
}

// elifOf returns the elif that continues s, or nil. The parser writes an elif
// as an if that is the whole else branch and starts where the else does.
func elifOf(s *syntax.IfStmt) *syntax.IfStmt {
	if len(s.False) != 1 {
		return nil
	}
	next, ok := s.False[0].(*syntax.IfStmt)
	if !ok || next.If != s.ElsePos {
		return nil
	}
	return next
}

// cognitive is the cognitive complexity of a function body: each break in the
// linear flow costs one, and one more for every block it sits inside
// (G. Ann Campbell, "Cognitive Complexity", the measure gocognit applies to
// this repository's Go).
func cognitive(body []syntax.Stmt) int {
	c := &cog{}
	c.stmts(body, 0)
	return c.n
}

type cog struct{ n int }

func (c *cog) stmts(list []syntax.Stmt, nest int) {
	for _, s := range list {
		c.stmt(s, nest)
	}
}

func (c *cog) stmt(s syntax.Stmt, nest int) {
	switch s := s.(type) {
	case *syntax.IfStmt:
		c.n += 1 + nest
		c.ifChain(s, nest)
	case *syntax.ForStmt:
		c.n += 1 + nest
		c.expr(s.X, nest)
		c.stmts(s.Body, nest+1)
	case *syntax.DefStmt:
		// Linted as its own function.
	default:
		syntax.Walk(s, func(n syntax.Node) bool {
			if e, ok := n.(syntax.Expr); ok {
				c.expr(e, nest)
				return false
			}
			return true
		})
	}
}

// ifChain scores an if's condition and blocks, and each elif and else after
// it, which cost one apiece without the nesting increment.
func (c *cog) ifChain(s *syntax.IfStmt, nest int) {
	for {
		c.expr(s.Cond, nest)
		c.stmts(s.True, nest+1)
		if elif := elifOf(s); elif != nil {
			c.n++
			s = elif
			continue
		}
		if len(s.False) > 0 {
			c.n++
			c.stmts(s.False, nest+1)
		}
		return
	}
}

// expr scores what an expression adds: a conditional expression and each
// comprehension clause break the flow, and each run of one boolean operator
// costs one (a and b and c is one; a and b or c is two).
func (c *cog) expr(e syntax.Expr, nest int) {
	syntax.Walk(e, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CondExpr:
			c.n += 1 + nest
		case *syntax.ForClause, *syntax.IfClause:
			c.n++
		case *syntax.LambdaExpr:
			c.expr(n.Body, nest+1)
			return false
		case *syntax.BinaryExpr:
			if n.Op == syntax.AND || n.Op == syntax.OR {
				c.n += boolRuns(n)
				return false
			}
		}
		return true
	})
}

// boolRuns counts the runs of like boolean operators in a chain, and scores
// the operands, which may hold more.
func boolRuns(b *syntax.BinaryExpr) int {
	var ops []syntax.Token
	var operands []syntax.Expr
	var flatten func(e syntax.Expr)
	flatten = func(e syntax.Expr) {
		if p, ok := e.(*syntax.ParenExpr); ok {
			e = p.X
		}
		if be, ok := e.(*syntax.BinaryExpr); ok && (be.Op == syntax.AND || be.Op == syntax.OR) {
			flatten(be.X)
			ops = append(ops, be.Op)
			flatten(be.Y)
			return
		}
		operands = append(operands, e)
	}
	flatten(b)
	runs := 0
	for i, op := range ops {
		if i == 0 || ops[i-1] != op {
			runs++
		}
	}
	inner := &cog{}
	for _, o := range operands {
		inner.expr(o, 0)
	}
	return runs + inner.n
}
