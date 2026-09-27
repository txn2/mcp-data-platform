// Package scriptconst reads the names a managed script defines once, at module
// level, from literals, and renders an expression as the text it evaluates to
// when every part of it is known from the source.
//
// Two readers of a script's source need the same answer. scriptrun.Validate
// reports the connections, tools and destinations a script names, and a script
// that writes WAREHOUSE = "warehouse" and then connection=WAREHOUSE names the
// warehouse connection as plainly as one that writes the string at the call.
// The flow graph (internal/platform/scriptflow) shows the same value on the
// step. One resolver keeps the two from disagreeing about what a name is.
//
// A constant is decided from the resolved syntax tree, never from spelling: an
// identifier refers to a module constant only when the Starlark resolver bound
// it at module scope, so a function parameter or a local that happens to share
// the constant's name is not read as the constant.
package scriptconst

import (
	"fmt"
	"regexp"
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/syntax"
)

// Table holds a file's module constants by name.
type Table struct {
	values map[string]string
}

// Collect returns the module constants of a parsed and resolved file.
//
// A name is a constant when the module binds it exactly once, with a plain
// assignment written directly in the file's top-level statements, to an
// expression that renders fully from literals and constants bound before it.
// Every other binding of the name anywhere in the module counts against it, so
// a name reassigned, augmented, or also bound by a top-level for loop is not a
// constant. An assignment inside a top-level if or for is not a candidate
// either: whether it ran is a run-time fact.
//
// The file must have been resolved (resolve.File, or starlark.FileProgram);
// on an unresolved file no identifier carries a module binding and the table
// resolves nothing, which is the safe answer.
func Collect(file *syntax.File) Table {
	t := Table{values: map[string]string{}}
	if file == nil {
		return t
	}
	count := map[string]int{}
	countModuleBindings(file.Stmts, count)
	r := Renderer{Name: t.nameOnly}
	for _, s := range file.Stmts {
		as, ok := s.(*syntax.AssignStmt)
		if !ok || as.Op != syntax.EQ {
			continue
		}
		id, ok := as.LHS.(*syntax.Ident)
		if !ok || count[id.Name] != 1 {
			continue
		}
		if v, full := r.Render(as.RHS); full {
			t.values[id.Name] = v
		}
	}
	return t
}

// countModuleBindings counts every binding of each name in the module's own
// scope: assignments, for-loop variables and defs, in the top-level statements
// and in the bodies of top-level if and for statements, which bind in the same
// scope. A def's body is a different scope and is not entered.
func countModuleBindings(stmts []syntax.Stmt, count map[string]int) {
	for _, s := range stmts {
		switch s := s.(type) {
		case *syntax.AssignStmt:
			countTargets(s.LHS, count)
		case *syntax.ForStmt:
			countTargets(s.Vars, count)
			countModuleBindings(s.Body, count)
		case *syntax.IfStmt:
			countModuleBindings(s.True, count)
			countModuleBindings(s.False, count)
		case *syntax.DefStmt:
			count[s.Name.Name]++
		case *syntax.LoadStmt:
			for _, id := range s.To {
				count[id.Name]++
			}
		}
	}
}

// countTargets counts the names one assignment target binds.
func countTargets(lhs syntax.Expr, count map[string]int) {
	switch l := lhs.(type) {
	case *syntax.Ident:
		count[l.Name]++
	case *syntax.TupleExpr:
		for _, x := range l.List {
			countTargets(x, count)
		}
	case *syntax.ListExpr:
		for _, x := range l.List {
			countTargets(x, count)
		}
	case *syntax.ParenExpr:
		countTargets(l.X, count)
	}
}

// Value returns the constant an identifier refers to. It reports false for an
// identifier the resolver did not bind at module scope, whatever its name.
func (t Table) Value(id *syntax.Ident) (string, bool) {
	if !IsModuleName(id) {
		return "", false
	}
	v, ok := t.values[id.Name]
	return v, ok
}

// Len reports how many constants the table holds.
func (t Table) Len() int { return len(t.values) }

// nameOnly is the Namer Collect renders with: constants, and nothing else.
func (t Table) nameOnly(id *syntax.Ident) (string, bool) {
	if v, ok := t.Value(id); ok {
		return v, true
	}
	return "{" + id.Name + "}", false
}

// String returns the text an expression is known to be from the source alone:
// a string literal, a module constant, or string building over those
// ("prod-" + REGION, "{}-drop".format(ACME)). A part that is computed at run
// time, or a value that is not a string, reports false.
func (t Table) String(e syntax.Expr) (string, bool) {
	if lit, ok := e.(*syntax.Literal); ok {
		s, isString := lit.Value.(string)
		return s, isString
	}
	if !buildsString(e) {
		return "", false
	}
	r := Renderer{Name: t.nameOnly}
	return r.Render(e)
}

// buildsString reports whether an expression is one String may read: a name,
// or string building. Rendering anything else (a number, a list) would report
// text the value is not.
func buildsString(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.Ident:
		return true
	case *syntax.ParenExpr:
		return buildsString(e.X)
	case *syntax.BinaryExpr:
		return e.Op == syntax.PLUS || e.Op == syntax.PERCENT
	case *syntax.CallExpr:
		dot, ok := e.Fn.(*syntax.DotExpr)
		return ok && (dot.Name.Name == "format" || dot.Name.Name == "join")
	}
	return false
}

// IsModuleName reports whether the resolver bound an identifier at module
// scope.
func IsModuleName(id *syntax.Ident) bool {
	b, ok := id.Binding.(*resolve.Binding)
	return ok && b.Scope == resolve.Global
}

// Namer renders one identifier: its text, and whether that text is the value
// rather than a stand-in for a computed one.
type Namer func(id *syntax.Ident) (string, bool)

// Renderer renders an expression as text. Literals and the names its Namer
// knows are written as their values; string concatenation, "...".format(...),
// "..." % (...) and sep.join([...]) are rendered with each part filled in, and
// template.replace(...) as the template, computed; any
// part that cannot be read from the source is written as {its source text}.
type Renderer struct {
	// Name renders an identifier. Nil renders every identifier as {name}.
	Name Namer
	// Source returns an expression's source text, for the {…} stand-in. Nil
	// writes {…}.
	Source func(syntax.Expr) string
	depth  int
}

// maxRenderDepth bounds how far a Namer that renders a variable's own
// expression may recurse, so a chain of assignments cannot run away.
const maxRenderDepth = 6

// maxStandIn is the longest source text a {…} stand-in carries before it is
// shortened: the stand-in says where a value comes from, not what it is.
const maxStandIn = 28

// Render renders e, reporting whether every part of it was known.
func (r *Renderer) Render(e syntax.Expr) (string, bool) {
	if r.depth > maxRenderDepth {
		return "{…}", false
	}
	if text, full, ok := r.leaf(e); ok {
		return text, full
	}
	if text, full, ok := r.compound(e); ok {
		return text, full
	}
	return r.standIn(e), false
}

// leaf renders an expression with no parts: nothing, a literal, a name.
func (r *Renderer) leaf(e syntax.Expr) (text string, full, ok bool) {
	switch e := e.(type) {
	case nil:
		return "", true, true
	case *syntax.Literal:
		if s, isString := e.Value.(string); isString {
			return s, true, true
		}
		return fmt.Sprint(e.Value), true, true
	case *syntax.Ident:
		text, full = r.ident(e)
		return text, full, true
	}
	return "", false, false
}

// compound renders an expression built from parts.
func (r *Renderer) compound(e syntax.Expr) (text string, full, ok bool) {
	switch e := e.(type) {
	case *syntax.ParenExpr:
		text, full = r.Render(e.X)
		return text, full, true
	case *syntax.CondExpr:
		// Either branch may run. The first is shown so the reader sees the shape
		// of the value, and the whole is marked computed.
		text, _ = r.Render(e.True)
		return text, false, true
	case *syntax.BinaryExpr:
		return r.binary(e)
	case *syntax.CallExpr:
		return r.method(e)
	}
	return "", false, false
}

// Nested renders e one level deeper, for a Namer that renders a variable by
// rendering the expression it was assigned.
func (r *Renderer) Nested(e syntax.Expr) (string, bool) {
	r.depth++
	defer func() { r.depth-- }()
	return r.Render(e)
}

func (r *Renderer) ident(id *syntax.Ident) (string, bool) {
	if r.Name == nil {
		return "{" + id.Name + "}", false
	}
	return r.Name(id)
}

// binary renders a + b and a format % args.
func (r *Renderer) binary(e *syntax.BinaryExpr) (text string, full, ok bool) {
	switch e.Op {
	case syntax.PLUS:
		left, leftFull := r.Render(e.X)
		right, rightFull := r.Render(e.Y)
		// A number added to a string is an error at run time, so the text is
		// not a value the script can produce.
		exact := !isNumber(e.X) && !isNumber(e.Y)
		return left + right, leftFull && rightFull && exact, true
	case syntax.PERCENT:
		format, known := r.Render(e.X)
		if !known {
			return "", false, false
		}
		text, full = r.substitute(format, percentVerb, percentArgs(e.Y))
		return text, full && plainPercent(format), true
	}
	return "", false, false
}

// percentArgs lists the operands of a % format: a tuple's items, or the one
// value.
func percentArgs(y syntax.Expr) []syntax.Expr {
	if p, ok := y.(*syntax.ParenExpr); ok {
		y = p.X
	}
	if tup, ok := y.(*syntax.TupleExpr); ok {
		return tup.List
	}
	return []syntax.Expr{y}
}

// method renders the string methods a script builds text with.
func (r *Renderer) method(e *syntax.CallExpr) (text string, full, ok bool) {
	dot, isDot := e.Fn.(*syntax.DotExpr)
	if !isDot {
		return "", false, false
	}
	switch dot.Name.Name {
	case "format":
		format, known := r.Render(dot.X)
		if !known {
			return "", false, false
		}
		text, full = r.substitute(format, braceField, positionalArgs(e))
		return text, full && plainBraces(format), true
	case "join":
		return r.join(dot, e)
	case "replace":
		// A template filled by replacing its placeholders reads as the
		// template, which keeps the tables it names readable. What replaces
		// them is computed, so the whole is.
		text, _ = r.Render(dot.X)
		return text, false, true
	}
	return "", false, false
}

// join renders sep.join([...]) over a list the source writes out.
func (r *Renderer) join(dot *syntax.DotExpr, e *syntax.CallExpr) (text string, full, ok bool) {
	if len(e.Args) != 1 {
		return "", false, false
	}
	list, isList := e.Args[0].(*syntax.ListExpr)
	if !isList {
		return "", false, false
	}
	sep, full := r.Render(dot.X)
	parts := make([]string, 0, len(list.List))
	for _, x := range list.List {
		part, partFull := r.Render(x)
		parts = append(parts, part)
		full = full && partFull
	}
	return strings.Join(parts, sep), full, true
}

// isNumber reports whether an expression is a number literal.
func isNumber(e syntax.Expr) bool {
	lit, ok := e.(*syntax.Literal)
	if !ok {
		return false
	}
	_, isString := lit.Value.(string)
	return !isString
}

// plainPercent reports whether every verb in a % format is a bare %s or %d,
// the verbs whose output is the argument's own text. A width, a precision, a
// float or repr verb, a %(name)s mapping or a %% escape each produce text the
// substitution does not, so the result is only as good as computed.
func plainPercent(format string) bool {
	for _, v := range percentVerb.FindAllString(format, -1) {
		if v != "%s" && v != "%d" {
			return false
		}
	}
	return !strings.Contains(format, "%%") && !strings.Contains(format, "%(")
}

// plainBraces reports whether every field in a .format template is a bare {},
// filled in order. A numbered or named field, a format spec, or a {{ }} escape
// produces text the substitution does not.
func plainBraces(format string) bool {
	for _, f := range braceField.FindAllString(format, -1) {
		if f != "{}" {
			return false
		}
	}
	return !strings.Contains(format, "{{") && !strings.Contains(format, "}}")
}

// positionalArgs lists a call's positional arguments.
func positionalArgs(c *syntax.CallExpr) []syntax.Expr {
	out := make([]syntax.Expr, 0, len(c.Args))
	for _, a := range c.Args {
		if b, ok := a.(*syntax.BinaryExpr); ok && b.Op == syntax.EQ {
			continue
		}
		out = append(out, a)
	}
	return out
}

var (
	braceField  = regexp.MustCompile(`\{[^{}]*\}`)
	percentVerb = regexp.MustCompile(`%[-0-9.]*[sdrfx]`)
)

// substitute fills a format's placeholders with the rendered arguments, in
// order. A placeholder with no argument left is written {?} and makes the
// result incomplete.
func (r *Renderer) substitute(format string, re *regexp.Regexp, args []syntax.Expr) (string, bool) {
	full := true
	i := 0
	out := re.ReplaceAllStringFunc(format, func(string) string {
		if i >= len(args) {
			full = false
			return "{?}"
		}
		v, ok := r.Render(args[i])
		i++
		full = full && ok
		return v
	})
	return out, full
}

// standIn writes a computed value as {its source text}, shortened.
func (r *Renderer) standIn(e syntax.Expr) string {
	if r.Source == nil {
		return "{…}"
	}
	t := r.Source(e)
	if len([]rune(t)) > maxStandIn {
		t = string([]rune(t)[:maxStandIn-2]) + "…"
	}
	return "{" + t + "}"
}
