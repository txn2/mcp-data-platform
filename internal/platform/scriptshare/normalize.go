package scriptshare

import (
	"fmt"
	"strconv"
	"strings"

	"go.starlark.net/syntax"
)

// normalize prints def's parameters and body as one line in which nothing
// that does not change what the function does appears: no docstring, no
// comment, no layout, and every local name (a parameter, an assigned name, a
// loop or comprehension variable) replaced by its order of first appearance.
// It also counts the syntax nodes the print covers.
func normalize(def *syntax.DefStmt) (printed string, nodes int) {
	n := &normalizer{self: def.Name.Name, locals: locals(def), renamed: map[string]string{}, skip: map[*syntax.Ident]string{}}
	body := def.Body
	if len(body) > 0 {
		if e, ok := body[0].(*syntax.ExprStmt); ok {
			if lit, ok := e.X.(*syntax.Literal); ok && lit.Token == syntax.STRING {
				body = body[1:]
			}
		}
	}
	n.emit(fmt.Sprintf("params%d", len(def.Params)))
	for _, p := range def.Params {
		n.walk(p)
	}
	n.emit(fmt.Sprintf("body%d", len(body)))
	for _, s := range body {
		n.walk(s)
	}
	return n.out.String(), n.nodes
}

// normalizer accumulates one function's print.
type normalizer struct {
	self    string
	locals  map[string]bool
	renamed map[string]string
	// skip holds the identifiers that are not names a function binds or
	// reads: an attribute after a dot and a keyword argument's name. They are
	// printed as written.
	skip  map[*syntax.Ident]string
	out   strings.Builder
	nodes int
}

func (n *normalizer) emit(token string) {
	_, _ = n.out.WriteString(token)
	_ = n.out.WriteByte(' ')
}

// walk prints node and its children, in order.
func (n *normalizer) walk(node syntax.Node) {
	syntax.Walk(node, func(x syntax.Node) bool {
		n.nodes++
		n.emit(n.token(x))
		return true
	})
}

// token is how one node prints: its kind, and what in it can change what the
// function does.
func (n *normalizer) token(x syntax.Node) string {
	switch t := x.(type) {
	case *syntax.Ident:
		return n.ident(t)
	case *syntax.Literal:
		// The value, not the raw text: 'a' and "a", 0x10 and 16 are one value.
		return fmt.Sprintf("lit:%T:%v", t.Value, t.Value)
	case *syntax.DotExpr:
		n.skip[t.Name] = "attr:"
		return "dot"
	case *syntax.CallExpr:
		n.keywords(t)
		return "call" + strconv.Itoa(len(t.Args))
	}
	if s, ok := stmtToken(x); ok {
		return s
	}
	if s, ok := exprToken(x); ok {
		return s
	}
	return fmt.Sprintf("%T", x)
}

// keywords marks the keyword names of a call's arguments, which print as
// written: a keyword is the callee's name, never a local.
func (n *normalizer) keywords(call *syntax.CallExpr) {
	for _, a := range call.Args {
		b, ok := a.(*syntax.BinaryExpr)
		if !ok || b.Op != syntax.EQ {
			continue
		}
		if id, ok := b.X.(*syntax.Ident); ok {
			n.skip[id] = "kw:"
		}
	}
}

// stmtToken is how a statement prints: its kind and its shape.
func stmtToken(x syntax.Node) (string, bool) {
	switch t := x.(type) {
	case *syntax.AssignStmt:
		return "assign:" + t.Op.String(), true
	case *syntax.BranchStmt:
		return "branch:" + t.Token.String(), true
	case *syntax.IfStmt:
		return fmt.Sprintf("if%d/%d", len(t.True), len(t.False)), true
	case *syntax.ForStmt:
		return "for" + strconv.Itoa(len(t.Body)), true
	case *syntax.WhileStmt:
		return "while" + strconv.Itoa(len(t.Body)), true
	case *syntax.DefStmt:
		return fmt.Sprintf("def%d/%d", len(t.Params), len(t.Body)), true
	case *syntax.ReturnStmt:
		return fmt.Sprintf("return%t", t.Result != nil), true
	}
	return "", false
}

// exprToken is how an expression prints: its operator or its shape.
func exprToken(x syntax.Node) (string, bool) {
	switch t := x.(type) {
	case *syntax.BinaryExpr:
		return "bin:" + t.Op.String(), true
	case *syntax.UnaryExpr:
		return "un:" + t.Op.String(), true
	case *syntax.ListExpr:
		return "list" + strconv.Itoa(len(t.List)), true
	case *syntax.TupleExpr:
		return "tuple" + strconv.Itoa(len(t.List)), true
	case *syntax.DictExpr:
		return "dict" + strconv.Itoa(len(t.List)), true
	case *syntax.LambdaExpr:
		return "lambda" + strconv.Itoa(len(t.Params)), true
	case *syntax.Comprehension:
		return fmt.Sprintf("comp%t/%d", t.Curly, len(t.Clauses)), true
	case *syntax.SliceExpr:
		return fmt.Sprintf("slice%t%t%t", t.Lo != nil, t.Hi != nil, t.Step != nil), true
	}
	return "", false
}

// ident prints a name: a local by its order of first appearance, the
// function's own name as itself, anything else (a builtin, a module name, a
// loaded symbol) as written.
func (n *normalizer) ident(id *syntax.Ident) string {
	if prefix, ok := n.skip[id]; ok {
		return prefix + id.Name
	}
	switch {
	case id.Name == n.self:
		return "self"
	case n.locals[id.Name]:
		v, ok := n.renamed[id.Name]
		if !ok {
			v = "v" + strconv.Itoa(len(n.renamed))
			n.renamed[id.Name] = v
		}
		return v
	}
	return "name:" + id.Name
}

// locals are the names def binds: its parameters, and every name its body
// assigns, loops over, defines or takes as a lambda's or comprehension's
// variable.
func locals(def *syntax.DefStmt) map[string]bool {
	out := map[string]bool{}
	for _, p := range def.Params {
		bindParam(out, p)
	}
	for _, s := range def.Body {
		syntax.Walk(s, func(x syntax.Node) bool {
			switch t := x.(type) {
			case *syntax.AssignStmt:
				bindTarget(out, t.LHS)
			case *syntax.ForStmt:
				bindTarget(out, t.Vars)
			case *syntax.ForClause:
				bindTarget(out, t.Vars)
			case *syntax.DefStmt:
				out[t.Name.Name] = true
				for _, p := range t.Params {
					bindParam(out, p)
				}
			case *syntax.LambdaExpr:
				for _, p := range t.Params {
					bindParam(out, p)
				}
			}
			return true
		})
	}
	return out
}

// bindParam records the name a parameter binds: x, x = default, *x, **x.
func bindParam(out map[string]bool, p syntax.Expr) {
	switch t := p.(type) {
	case *syntax.Ident:
		out[t.Name] = true
	case *syntax.BinaryExpr:
		bindParam(out, t.X)
	case *syntax.UnaryExpr:
		if t.X != nil {
			bindParam(out, t.X)
		}
	}
}

// bindTarget records the names an assignment target binds.
func bindTarget(out map[string]bool, e syntax.Expr) {
	switch t := e.(type) {
	case *syntax.Ident:
		out[t.Name] = true
	case *syntax.TupleExpr:
		for _, x := range t.List {
			bindTarget(out, x)
		}
	case *syntax.ListExpr:
		for _, x := range t.List {
			bindTarget(out, x)
		}
	case *syntax.ParenExpr:
		bindTarget(out, t.X)
	}
}
