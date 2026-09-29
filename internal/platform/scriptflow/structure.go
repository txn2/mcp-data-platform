package scriptflow

import (
	"fmt"
	"slices"
	"strings"

	"go.starlark.net/syntax"
)

// Structure is the script drawn in the order it runs (#1972): one Start, the
// statements that matter in sequence, a decision for every if that holds one,
// a box for every loop and helper function that holds one, and an exit for
// the normal end, every fail() and every early return from main(). It is the
// Flow tab's default view; the value graph (Graph.Nodes and Graph.Edges) is
// the Calls view.
//
// The graph is acyclic by construction: a loop is a box around its body, and
// run.state is read at the top and saved at the bottom rather than drawn as an
// edge back. A platform call is a node whose Step names the value graph's card
// for the same call, so a run's calls, which are attributed to cards by call
// site, land on the same node in both views.
type Structure struct {
	Nodes []StructNode `json:"nodes"`
	Edges []StructEdge `json:"edges"`
	Boxes []StructBox  `json:"boxes"`
	// Functions is every def with its span, so a call site's frames can be
	// named by the function each line is in.
	Functions []FuncSpan `json:"functions"`
	// Truncated is true when the script expands into more nodes than one
	// diagram draws.
	Truncated bool `json:"truncated"`
}

// Kinds of structure node.
const (
	StructStart  = "start"
	StructEnd    = "end"
	StructStop   = "stop"
	StructReturn = "return"
	StructIf     = "if"
	StructStep   = "step"
)

// Kinds of structure box.
const (
	BoxLoop     = "loop"
	BoxFunction = "function"
)

// Arm labels on the edges out of an if.
const (
	ArmYes = "yes"
	ArmNo  = "no"
)

// StructNode is one node of the structure.
type StructNode struct {
	ID   string `json:"id" example:"s:3"`
	Kind string `json:"kind" example:"if"`
	// Label is the if's condition and the fail()'s message, as the source
	// writes them.
	Label string `json:"label,omitempty" example:"len(rows) < 1000"`
	// Step is the value graph's node for a platform call.
	Step string `json:"step,omitempty" example:"op:3"`
	// Box is the loop or function box the node is drawn in.
	Box  string `json:"box,omitempty"`
	Line int    `json:"line" example:"42"`
	// CallSite is a platform call's or a fail()'s position on the stack, as
	// a run records it (#1907).
	CallSite []string `json:"call_site,omitempty"`
}

// StructEdge is "runs next". Label is yes or no on an if's arms.
type StructEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty" example:"yes"`
}

// StructBox is a loop or a helper function around the nodes it runs.
type StructBox struct {
	ID   string `json:"id" example:"b:2"`
	Kind string `json:"kind" example:"loop"`
	// Label is a loop's header or a function's signature; Caption the
	// function's first comment sentence.
	Label   string `json:"label"`
	Caption string `json:"caption,omitempty"`
	Parent  string `json:"parent,omitempty"`
	Line    int    `json:"line" example:"12"`
	// CallSite is where this expansion of a function was called from, the
	// prefix every call made inside it carries.
	CallSite []string `json:"call_site,omitempty"`
}

// FuncSpan is one def and the lines it covers.
type FuncSpan struct {
	Name    string `json:"name"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line"`
}

// maxStructNodes bounds one structure diagram.
const maxStructNodes = 600

// pending is an edge waiting for the next node: the node it leaves and the
// arm it is on.
type pending struct {
	from, label string
}

// sctx is where the structure walk is: the box it draws into, the call sites
// of the expansions it is inside, and where a return goes.
type sctx struct {
	box   string
	sites []string
	fn    string
	// main is true in main()'s body, where a return ends the run.
	main bool
	// last is the statement that ends main()'s body, whose return is the
	// normal end rather than an early one.
	last syntax.Stmt
	// returns collects a helper's returns, which continue after its call.
	returns *[]pending
	// exits collects a loop's break and continue, which leave the iteration.
	exits *[]pending
}

// structWalk builds a Structure.
type structWalk struct {
	a     *analyzer
	out   *Structure
	steps map[string]string
	stack []string
	// drawable is every function whose run can reach a node: a platform
	// call, a fail(), or a function that can.
	drawable map[string]bool
	// expansions counts helper expansions, bounded by maxExpansions like the
	// value walk: a helper that calls the next twice, twenty deep, is a
	// million expansions however few nodes they draw.
	expansions int
}

// buildStructure walks the module in execution order after the value walk has
// numbered the steps.
func (a *analyzer) buildStructure(g *Graph) Structure {
	out := Structure{Nodes: []StructNode{}, Edges: []StructEdge{}, Boxes: []StructBox{}, Functions: a.funcSpans()}
	w := &structWalk{a: a, out: &out, steps: map[string]string{}, drawable: a.drawableFuncs()}
	for _, n := range g.Nodes {
		if n.Kind == KindTable || len(n.CallSite) == 0 {
			continue
		}
		if _, ok := w.steps[siteKey(n.CallSite)]; !ok {
			w.steps[siteKey(n.CallSite)] = n.ID
		}
	}
	start := w.node(StructNode{Kind: StructStart}, sctx{}, nil)
	front := w.stmts(a.module, sctx{}, []pending{{from: start}})
	w.node(StructNode{Kind: StructEnd}, sctx{}, front)
	w.foldWrappers()
	return out
}

func siteKey(site []string) string { return strings.Join(site, ">") }

// funcSpans is every def with its lines, in source order.
func (a *analyzer) funcSpans() []FuncSpan {
	out := make([]FuncSpan, 0, len(a.funcs))
	for name, d := range a.funcs {
		start, end := d.Span()
		out = append(out, FuncSpan{Name: name, Line: int(start.Line), EndLine: int(end.Line)})
	}
	slices.SortFunc(out, func(x, y FuncSpan) int { return x.Line - y.Line })
	return out
}

// drawableFuncs is every function whose body, or a function it calls, makes a
// platform call or calls fail().
func (a *analyzer) drawableFuncs() map[string]bool {
	out := map[string]bool{}
	callees := map[string][]string{}
	for name, d := range a.funcs {
		out[name], callees[name] = a.drawsDirectly(d)
	}
	settleCallers(out, callees)
	return out
}

// drawsDirectly reports whether a function body makes a platform call or
// calls fail() itself, and names the user functions it calls.
func (a *analyzer) drawsDirectly(d *syntax.DefStmt) (direct bool, callees []string) {
	for _, st := range d.Body {
		walkCalls(st, func(c *syntax.CallExpr) {
			if m, ok := platformMember(c); (ok && m != memberProgress) || isFail(c) {
				direct = true
			}
			if callee, ok := a.userCall(c); ok {
				callees = append(callees, callee)
			}
		})
	}
	return direct, callees
}

// settleCallers marks every function that calls a marked one, repeating until
// a pass marks nothing new.
func settleCallers(marked map[string]bool, callees map[string][]string) {
	for changed := true; changed; {
		changed = false
		for name, cs := range callees {
			if !marked[name] && slices.ContainsFunc(cs, func(c string) bool { return marked[c] }) {
				marked[name], changed = true, true
			}
		}
	}
}

func isFail(c *syntax.CallExpr) bool {
	id, ok := c.Fn.(*syntax.Ident)
	return ok && id.Name == "fail"
}

// node adds a node, wires the pending edges to it, and returns its id.
func (w *structWalk) node(n StructNode, cx sctx, in []pending) string {
	if len(w.out.Nodes) >= maxStructNodes {
		w.out.Truncated = true
		return ""
	}
	n.ID = fmt.Sprintf("s:%d", len(w.out.Nodes)+1)
	n.Box = cx.box
	w.out.Nodes = append(w.out.Nodes, n)
	for _, p := range in {
		if p.from != "" {
			w.out.Edges = append(w.out.Edges, StructEdge{From: p.from, To: n.ID, Label: p.label})
		}
	}
	return n.ID
}

// stmts walks a statement list and returns the edges leaving its end.
func (w *structWalk) stmts(list []syntax.Stmt, cx sctx, in []pending) []pending {
	for _, s := range list {
		in = w.stmt(s, cx, in)
	}
	return in
}

func (w *structWalk) stmt(s syntax.Stmt, cx sctx, in []pending) []pending {
	switch s := s.(type) {
	case *syntax.ExprStmt:
		return w.expr(s.X, cx, in)
	case *syntax.AssignStmt:
		return w.expr(s.RHS, cx, in)
	case *syntax.ReturnStmt:
		return w.ret(s, cx, in)
	case *syntax.IfStmt:
		return w.ifStmt(s, cx, in)
	case *syntax.ForStmt:
		return w.forStmt(s, cx, in)
	case *syntax.BranchStmt:
		if cx.exits != nil && s.Token != syntax.PASS {
			*cx.exits = append(*cx.exits, in...)
			return nil
		}
	}
	return in
}

// ret is a return: in main() anywhere but its last statement it ends the run
// early; in a helper it continues after the call.
func (w *structWalk) ret(s *syntax.ReturnStmt, cx sctx, in []pending) []pending {
	if s.Result != nil {
		in = w.expr(s.Result, cx, in)
	}
	switch {
	case cx.main && syntax.Stmt(s) == cx.last:
		return in
	case cx.main:
		line, _ := s.Span()
		w.node(StructNode{Kind: StructReturn, Line: int(line.Line)}, cx, in)
		return nil
	case cx.returns != nil:
		*cx.returns = append(*cx.returns, in...)
		return nil
	}
	return in
}

// ifStmt draws a decision when either arm holds something to draw; its arms
// join again at whatever comes next.
func (w *structWalk) ifStmt(s *syntax.IfStmt, cx sctx, in []pending) []pending {
	in = w.expr(s.Cond, cx, in)
	if !w.holds(s.True, cx) && !w.holds(s.False, cx) {
		return in
	}
	id := w.node(StructNode{Kind: StructIf, Label: w.condText(s), Line: int(s.If.Line)}, cx, in)
	yes := w.stmts(s.True, cx, []pending{{from: id, label: ArmYes}})
	no := w.stmts(s.False, cx, []pending{{from: id, label: ArmNo}})
	return append(yes, no...)
}

// forStmt draws a loop box around its body when the body holds something to
// draw. The box is the repetition, so no edge goes back.
func (w *structWalk) forStmt(s *syntax.ForStmt, cx sctx, in []pending) []pending {
	in = w.expr(s.X, cx, in)
	if !w.holds(s.Body, cx) {
		return in
	}
	label := clip("for "+w.a.text(s.Vars)+" in "+w.a.srcOf(s.X), maxLoopLabel)
	box := w.box(StructBox{Kind: BoxLoop, Label: label, Line: int(s.For.Line)}, cx)
	inner := cx
	inner.box = box
	exits := []pending{}
	inner.exits = &exits
	out := w.stmts(s.Body, inner, in)
	return append(out, exits...)
}

// box adds a box inside the walk's current one.
func (w *structWalk) box(b StructBox, cx sctx) string {
	b.ID = fmt.Sprintf("b:%d", len(w.out.Boxes)+1)
	b.Parent = cx.box
	w.out.Boxes = append(w.out.Boxes, b)
	return b.ID
}

// condText is an if's condition as the source writes it on its line.
func (w *structWalk) condText(s *syntax.IfStmt) string {
	line := int(s.If.Line)
	if line < 1 || line > len(w.a.lines) {
		return ""
	}
	t := strings.TrimSpace(w.a.lines[line-1])
	t = strings.TrimPrefix(strings.TrimPrefix(t, "elif "), "if ")
	if i := strings.LastIndex(t, ":"); i > 0 && strings.HasSuffix(strings.TrimSpace(t), ":") {
		t = t[:i]
	}
	return clip(strings.TrimSpace(t), maxCondLabel)
}

// maxCondLabel is the longest condition a decision shows.
const maxCondLabel = 80

// holds reports whether a statement list draws anything: a platform call, a
// fail(), a drawable helper, a return or a break that changes where the run
// goes.
func (w *structWalk) holds(list []syntax.Stmt, cx sctx) bool {
	found := false
	for _, s := range list {
		syntax.Walk(s, func(n syntax.Node) bool {
			if found {
				return false
			}
			switch n := n.(type) {
			case *syntax.DefStmt, *syntax.LambdaExpr:
				return false
			case *syntax.ReturnStmt:
				found = cx.main || cx.returns != nil
			case *syntax.BranchStmt:
				found = cx.exits != nil && n.Token != syntax.PASS
			case *syntax.CallExpr:
				found = w.drawsCall(n)
			}
			return !found
		})
	}
	return found
}

// drawsCall reports whether a call draws a node or a box.
func (w *structWalk) drawsCall(c *syntax.CallExpr) bool {
	if m, ok := platformMember(c); ok {
		return m != memberProgress
	}
	if isFail(c) {
		return true
	}
	name, ok := w.a.userCall(c)
	return ok && w.drawable[name] && !slices.Contains(w.stack, name)
}

// expr draws the calls an expression makes, in the order they run: a call's
// arguments before the call.
func (w *structWalk) expr(e syntax.Expr, cx sctx, in []pending) []pending {
	if e == nil {
		return in
	}
	syntax.Walk(e, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.LambdaExpr, *syntax.DefStmt:
			return false
		case *syntax.Comprehension:
			in = w.comprehension(n, cx, in)
			return false
		case *syntax.CallExpr:
			in = w.expr(n.Fn, cx, in)
			for _, arg := range n.Args {
				in = w.expr(arg, cx, in)
			}
			in = w.call(n, cx, in)
			return false
		}
		return true
	})
	return in
}

// comprehension draws a loop box around the calls its body makes.
func (w *structWalk) comprehension(c *syntax.Comprehension, cx sctx, in []pending) []pending {
	first, ok := c.Clauses[0].(*syntax.ForClause)
	if !ok {
		return in
	}
	in = w.expr(first.X, cx, in)
	body := make([]syntax.Stmt, 0, len(c.Clauses))
	for _, cl := range c.Clauses[1:] {
		switch cl := cl.(type) {
		case *syntax.ForClause:
			body = append(body, &syntax.ExprStmt{X: cl.X})
		case *syntax.IfClause:
			body = append(body, &syntax.ExprStmt{X: cl.Cond})
		}
	}
	body = append(body, &syntax.ExprStmt{X: c.Body})
	if !w.holds(body, cx) {
		return in
	}
	label := clip("for "+w.a.text(first.Vars)+" in "+w.a.srcOf(first.X), maxLoopLabel)
	inner := cx
	inner.box = w.box(StructBox{Kind: BoxLoop, Label: label, Line: int(first.For.Line)}, cx)
	return w.stmts(body, inner, in)
}

// call draws one call: a platform call's step, a fail()'s stop, or a helper
// expanded in a box.
func (w *structWalk) call(c *syntax.CallExpr, cx sctx, in []pending) []pending {
	if m, ok := platformMember(c); ok {
		if m == memberProgress {
			return in
		}
		site := append(slices.Clone(cx.sites), position(c.Lparen))
		// Label names the call when the value graph was cut short before it.
		id := w.node(StructNode{
			Kind: StructStep, Step: w.steps[siteKey(site)], Label: "platform." + m,
			Line: int(c.Lparen.Line), CallSite: site,
		}, cx, in)
		return []pending{{from: id}}
	}
	if isFail(c) {
		site := append(slices.Clone(cx.sites), position(c.Lparen))
		w.node(StructNode{Kind: StructStop, Label: w.failText(c), Line: int(c.Lparen.Line), CallSite: site}, cx, in)
		return nil
	}
	name, ok := w.a.userCall(c)
	if !ok || !w.drawable[name] || slices.Contains(w.stack, name) {
		return in
	}
	w.expansions++
	if len(w.stack) >= maxDepth || w.expansions > maxExpansions {
		w.out.Truncated = true
		return in
	}
	return w.invoke(name, c, cx, in)
}

// invoke expands one call of a helper in a box; main() is the whole script
// and is drawn without one.
func (w *structWalk) invoke(name string, c *syntax.CallExpr, cx sctx, in []pending) []pending {
	d := w.a.funcs[name]
	inner := sctx{box: cx.box, sites: slices.Clone(cx.sites), fn: name}
	if c == w.a.entryCall {
		inner.main = true
		if len(d.Body) > 0 {
			inner.last = d.Body[len(d.Body)-1]
		}
		return w.expand(name, d, inner, in)
	}
	inner.sites = append(inner.sites, position(c.Lparen))
	inner.box = w.box(StructBox{
		Kind: BoxFunction, Label: w.a.signature(d), Caption: caption(d),
		Line: int(c.Lparen.Line), CallSite: slices.Clone(inner.sites),
	}, cx)
	returns := []pending{}
	inner.returns = &returns
	return append(w.expand(name, d, inner, in), returns...)
}

// expand walks a function's body with the function on the stack, so a call of
// it inside its own expansion is not expanded again.
func (w *structWalk) expand(name string, d *syntax.DefStmt, inner sctx, in []pending) []pending {
	w.stack = append(w.stack, name)
	out := w.stmts(d.Body, inner, in)
	w.stack = w.stack[:len(w.stack)-1]
	return out
}

// failText is a fail()'s message: the literal, or the expression that builds
// it.
func (w *structWalk) failText(c *syntax.CallExpr) string {
	if len(c.Args) == 0 {
		return ""
	}
	if lit, ok := c.Args[0].(*syntax.Literal); ok {
		if s, ok := lit.Value.(string); ok {
			return clip(s, maxCondLabel)
		}
	}
	return clip(w.a.srcOf(c.Args[0]), maxCondLabel)
}

// foldWrappers removes the box of a helper that draws exactly one platform
// call and nothing else: the box would only say again what the card says.
func (w *structWalk) foldWrappers() {
	children, only := w.boxContents()
	keep := make([]StructBox, 0, len(w.out.Boxes))
	parent := map[string]string{}
	for _, b := range w.out.Boxes {
		i, ok := only[b.ID]
		if b.Kind == BoxFunction && ok && children[b.ID] == 1 && w.out.Nodes[i].Kind == StructStep {
			parent[b.ID] = b.Parent
			continue
		}
		keep = append(keep, b)
	}
	for i := range w.out.Nodes {
		if p, folded := parent[w.out.Nodes[i].Box]; folded {
			w.out.Nodes[i].Box = p
		}
	}
	w.out.Boxes = keep
}

// boxContents counts what each box directly holds, nodes and boxes alike, and
// the index of a node each holds.
func (w *structWalk) boxContents() (children, node map[string]int) {
	children, node = map[string]int{}, map[string]int{}
	for _, b := range w.out.Boxes {
		if b.Parent != "" {
			children[b.Parent]++
		}
	}
	for i, n := range w.out.Nodes {
		if n.Box != "" {
			children[n.Box]++
			node[n.Box] = i
		}
	}
	return children, node
}
