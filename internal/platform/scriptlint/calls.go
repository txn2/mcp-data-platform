package scriptlint

import (
	"fmt"
	"slices"

	"go.starlark.net/resolve"
	"go.starlark.net/syntax"
)

// The platform members the host-call rules are about.
const (
	memberQuery     = "query"
	memberCall      = "call"
	memberExport    = "export"
	memberSaveState = "save_state"
)

// loopedMembers are the calls that reach an upstream or write an output: one
// per element of a collection is the most common reason a migrated flow turns
// slow or rate limited.
var loopedMembers = []string{memberQuery, memberCall, memberExport}

// hostCalls applies the rules about how a script uses platform.*: SQL built
// from values, an upstream call per element of a loop, and state saved without
// the previous run's state being read.
func (l *linter) hostCalls() {
	h := &hostScan{l: l, assigned: l.assignments()}
	l.walkLoops(l.file.Stmts, false, h.visit)
	if h.readsState {
		return
	}
	for _, c := range h.saves {
		l.add(finding{
			rule: RuleStateWithoutRead, subject: "", line: line(c),
			message: "platform.save_state is called but run.state is never read",
			hint: "A saved state is what the next run starts from. Read it with run.state.get(key, default) " +
				"and continue from it, or drop the save if no run needs it.",
		})
	}
}

// hostScan is what one walk of the file learns for the host-call rules.
type hostScan struct {
	l          *linter
	assigned   map[*syntax.Ident][]syntax.Expr
	readsState bool
	saves      []*syntax.CallExpr
}

// visit is called for every node, with whether it sits in a loop over a
// collection.
func (h *hostScan) visit(n syntax.Node, inLoop bool) {
	if dot, ok := n.(*syntax.DotExpr); ok && isName(dot.X, "run") && dot.Name.Name == "state" {
		h.readsState = true
	}
	c, ok := n.(*syntax.CallExpr)
	if !ok {
		return
	}
	member, ok := platformMember(c)
	if !ok {
		return
	}
	switch member {
	case memberQuery:
		h.l.sqlFromValues(c, h.assigned)
	case memberSaveState:
		h.saves = append(h.saves, c)
	}
	if inLoop && slices.Contains(loopedMembers, member) {
		h.l.add(finding{
			rule: RuleCallInLoop, subject: "platform." + member, line: line(c),
			message: fmt.Sprintf("platform.%s is called once per element of a loop", member),
			hint: "Make one call for the whole set: bind the list in params and filter with IN :name, or let the tool page " +
				"(paginate=, api_export). A loop over range() that fetches one page per pass is not counted.",
		})
	}
}

// walkLoops visits every node, telling visit whether it sits inside a loop
// over a collection. A loop over range() is a bounded count (one page per
// pass), not one pass per element, and does not count; a comprehension over a
// collection does.
func (l *linter) walkLoops(stmts []syntax.Stmt, inLoop bool, visit func(syntax.Node, bool)) {
	for _, s := range stmts {
		l.walkNode(s, inLoop, visit)
	}
}

func (l *linter) walkNode(root syntax.Node, inLoop bool, visit func(syntax.Node, bool)) {
	syntax.Walk(root, func(n syntax.Node) bool {
		visit(n, inLoop)
		switch n := n.(type) {
		case *syntax.ForStmt:
			l.walkNode(n.X, inLoop, visit)
			l.walkLoops(n.Body, inLoop || !isRange(n.X), visit)
			return false
		case *syntax.Comprehension:
			l.comprehension(n, inLoop, visit)
			return false
		case *syntax.DefStmt:
			// A function's body is not inside the loop it is written after,
			// and a call of it from a loop is the caller's decision.
			l.walkLoops(n.Body, false, visit)
			return false
		}
		return true
	})
}

// comprehension walks a comprehension: its first iterable outside the loop,
// and everything after it inside a loop when any clause iterates a collection.
func (l *linter) comprehension(c *syntax.Comprehension, inLoop bool, visit func(syntax.Node, bool)) {
	looped := inLoop
	for i, clause := range c.Clauses {
		fc, ok := clause.(*syntax.ForClause)
		if !ok {
			l.walkNode(clause, looped, visit)
			continue
		}
		if i == 0 {
			l.walkNode(fc.X, inLoop, visit)
		} else {
			l.walkNode(fc.X, looped, visit)
		}
		looped = looped || !isRange(fc.X)
	}
	l.walkNode(c.Body, looped, visit)
}

// isRange reports whether a loop iterates range(...).
func isRange(e syntax.Expr) bool {
	c, ok := e.(*syntax.CallExpr)
	return ok && isName(c.Fn, "range")
}

// sqlFromValues reports a platform.query whose SQL is built with +, % or
// .format() from something other than literals and module constants. SQL
// assembled only from those is fixed text (a table name held in a constant),
// which params= cannot express and which is not what this rule is about.
func (l *linter) sqlFromValues(c *syntax.CallExpr, assigned map[*syntax.Ident][]syntax.Expr) {
	sql := sqlArg(c)
	if sql == nil {
		return
	}
	candidates := []syntax.Expr{sql}
	if id, ok := sql.(*syntax.Ident); ok {
		if b, ok := id.Binding.(*resolve.Binding); ok && b.First != nil {
			candidates = assigned[b.First]
		}
	}
	for _, e := range candidates {
		if builtFromValues(e) {
			if _, known := l.consts.String(e); !known {
				l.add(finding{
					rule: RuleSQLFromValues, subject: "", line: line(c),
					message: "the SQL passed to platform.query is built from values with +, % or .format()",
					hint: "Write a :name placeholder in the SQL and pass the value in params={\"name\": value}; the platform binds it by type. " +
						"A list binds as IN :name. A table a register= made binds by its record: FROM :t with params={\"t\": out[\"table\"]}.",
				})
				return
			}
		}
	}
}

// builtFromValues reports whether an expression builds a string.
func builtFromValues(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.ParenExpr:
		return builtFromValues(e.X)
	case *syntax.BinaryExpr:
		return e.Op == syntax.PLUS || e.Op == syntax.PERCENT
	case *syntax.CallExpr:
		dot, ok := e.Fn.(*syntax.DotExpr)
		return ok && dot.Name.Name == "format"
	}
	return false
}

// sqlArg is the SQL a platform.query call passes: its first positional
// argument or its sql= keyword.
func sqlArg(c *syntax.CallExpr) syntax.Expr {
	for _, a := range c.Args {
		if kw, ok := a.(*syntax.BinaryExpr); ok && kw.Op == syntax.EQ {
			if isName(kw.X, "sql") {
				return kw.Y
			}
			continue
		}
		return a
	}
	return nil
}

// assignments maps each name's first binding to every value a plain
// assignment gives it, so the SQL rule can see through `sql = "..." + x`.
func (l *linter) assignments() map[*syntax.Ident][]syntax.Expr {
	out := map[*syntax.Ident][]syntax.Expr{}
	syntax.Walk(l.file, func(n syntax.Node) bool {
		as, ok := n.(*syntax.AssignStmt)
		if !ok || as.Op != syntax.EQ {
			return true
		}
		if id, ok := as.LHS.(*syntax.Ident); ok {
			if b, ok := id.Binding.(*resolve.Binding); ok && b.First != nil {
				out[b.First] = append(out[b.First], as.RHS)
			}
		}
		return true
	})
	return out
}

// platformMember names the platform.<member> a call calls.
func platformMember(c *syntax.CallExpr) (string, bool) {
	dot, ok := c.Fn.(*syntax.DotExpr)
	if !ok || !isName(dot.X, "platform") {
		return "", false
	}
	return dot.Name.Name, true
}

// isName reports whether e is the identifier name.
func isName(e syntax.Expr, name string) bool {
	id, ok := e.(*syntax.Ident)
	return ok && id.Name == name
}
