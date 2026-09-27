package scriptflow

import (
	"slices"
	"strings"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/scriptconst"
)

// Limits on how far one script is expanded. A function is not expanded inside
// its own expansion, so the expansion is finite, but a helper called three
// times from a function called three times is nine expansions, and a diagram
// of thousands of steps is not a diagram. Past either bound the walk stops
// expanding and the graph says so.
const (
	maxSteps      = 400
	maxExpansions = 20000
	// maxDepth bounds the call stack of the walk; a function is never
	// expanded inside itself, so it is only reached by a long chain of
	// distinct functions.
	maxDepth = 64
)

// analyzer is one walk of a script.
type analyzer struct {
	lines []string
	file  *syntax.File
	// module is what runs at the module level: the file's statements, then
	// the call of main() the platform makes when it makes one (#1944).
	module []syntax.Stmt
	// entryCall is that call, which has no position of its own in the source
	// and no frame at run time, so it adds no call site.
	entryCall *syntax.CallExpr
	consts    scriptconst.Table
	funcs     map[string]*syntax.DefStmt
	// parentOf names the function a nested def is written in.
	parentOf map[string]string
	// hasSteps is every function that makes a platform call, directly or
	// through a function it calls; wrapper is each of those whose only effect
	// is one platform call.
	hasSteps map[string]bool
	wrapper  map[string]bool
	// whole is a module-level function called once that holds every step: the
	// script's main(), which is not drawn as a box around everything.
	whole string

	steps     map[string]*step
	order     []*step
	params    map[string]int
	decides   map[string]bool
	stateLine int
	readState bool

	stack      []*frame
	top        *frame
	groups     map[string]*groupInfo
	expansions int
	truncated  bool
}

// groupInfo is one function box as the walk finds it.
type groupInfo struct {
	fn         string
	calledFrom map[int]bool
}

func newAnalyzer(source string, file *syntax.File) *analyzer {
	a := &analyzer{
		lines: strings.Split(source, "\n"), file: file, consts: scriptconst.Collect(file),
		funcs: map[string]*syntax.DefStmt{}, parentOf: map[string]string{},
		hasSteps: map[string]bool{}, wrapper: map[string]bool{},
		steps: map[string]*step{}, params: map[string]int{}, decides: map[string]bool{},
		groups: map[string]*groupInfo{},
	}
	a.module = file.Stmts
	if main := scriptdialect.EntryPoint(file); main != nil {
		a.entryCall = &syntax.CallExpr{Fn: main.Name, Lparen: main.Def, Rparen: main.Def}
		a.module = append(slices.Clone(file.Stmts), &syntax.ExprStmt{X: a.entryCall})
	}
	a.collectDefs(file.Stmts, "")
	a.survey()
	return a
}

// collectDefs records every def, and the function each nested one is written
// in.
func (a *analyzer) collectDefs(stmts []syntax.Stmt, parent string) {
	for _, s := range stmts {
		switch s := s.(type) {
		case *syntax.DefStmt:
			a.funcs[s.Name.Name] = s
			if parent != "" {
				a.parentOf[s.Name.Name] = parent
			}
			a.collectDefs(s.Body, s.Name.Name)
		case *syntax.ForStmt:
			a.collectDefs(s.Body, parent)
		case *syntax.IfStmt:
			a.collectDefs(s.True, parent)
			a.collectDefs(s.False, parent)
		}
	}
}

// bodyCalls is what one function body calls, nested defs excluded: how many
// platform calls it makes and which user functions it calls, in order.
type bodyCalls struct {
	platform int
	callees  []string
}

// scanBody reads the calls a function makes itself.
func (a *analyzer) scanBody(d *syntax.DefStmt) bodyCalls {
	var out bodyCalls
	for _, st := range d.Body {
		walkCalls(st, func(c *syntax.CallExpr) {
			if member, ok := platformMember(c); ok && member != memberProgress {
				out.platform++
			}
			if name, ok := a.userCall(c); ok {
				out.callees = append(out.callees, name)
			}
		})
	}
	return out
}

// walkCalls visits every call in a statement, not entering nested defs.
func walkCalls(st syntax.Node, visit func(*syntax.CallExpr)) {
	syntax.Walk(st, func(n syntax.Node) bool {
		if _, ok := n.(*syntax.DefStmt); ok {
			return false
		}
		if c, ok := n.(*syntax.CallExpr); ok {
			visit(c)
		}
		return true
	})
}

// userCall names the user function a call calls, if it calls one.
func (a *analyzer) userCall(c *syntax.CallExpr) (string, bool) {
	id, ok := c.Fn.(*syntax.Ident)
	if !ok || a.funcs[id.Name] == nil {
		return "", false
	}
	return id.Name, true
}

// survey decides which functions hold steps and which are wrappers.
func (a *analyzer) survey() {
	calls := make(map[string]bodyCalls, len(a.funcs))
	for name, d := range a.funcs {
		calls[name] = a.scanBody(d)
		if calls[name].platform > 0 {
			a.hasSteps[name] = true
		}
	}
	settle(calls, a.hasSteps, func(_ string, c bodyCalls) bool { return a.anyHasSteps(c.callees) })
	settle(calls, a.wrapper, func(name string, c bodyCalls) bool { return a.hasSteps[name] && a.isWrapper(c) })
	a.whole = a.wholeScript()
}

// settle marks every function the rule holds for, repeating until a pass marks
// nothing new: a function's mark may depend on the marks of those it calls.
func settle(calls map[string]bodyCalls, marked map[string]bool, rule func(string, bodyCalls) bool) {
	for changed := true; changed; {
		changed = false
		for name, c := range calls {
			if !marked[name] && rule(name, c) {
				marked[name] = true
				changed = true
			}
		}
	}
}

func (a *analyzer) anyHasSteps(names []string) bool {
	for _, n := range names {
		if a.hasSteps[n] {
			return true
		}
	}
	return false
}

// isWrapper reports whether a function's only effect is one platform call:
// it makes exactly one and calls nothing that makes any, or it makes none and
// calls exactly one step-bearing function, itself a wrapper.
func (a *analyzer) isWrapper(c bodyCalls) bool {
	var stepCallees []string
	for _, n := range c.callees {
		if a.hasSteps[n] {
			stepCallees = append(stepCallees, n)
		}
	}
	if c.platform == 1 {
		return len(stepCallees) == 0
	}
	return c.platform == 0 && len(stepCallees) == 1 && a.wrapper[stepCallees[0]]
}

// wholeScript names the function a script's top level hands everything to,
// when it calls exactly one step-bearing function and makes no platform call
// of its own.
func (a *analyzer) wholeScript() string {
	var found []string
	topPlatform := false
	for _, s := range a.module {
		walkCalls(s, func(c *syntax.CallExpr) {
			if _, ok := platformMember(c); ok {
				topPlatform = true
			}
			if name, ok := a.userCall(c); ok && a.hasSteps[name] {
				found = append(found, name)
			}
		})
	}
	if len(found) == 1 && !topPlatform {
		return found[0]
	}
	return ""
}

// memberProgress is the one platform member that is not a step: it reports
// how far a run has got, and reaches nothing.
const memberProgress = "progress"

// platformMember recognizes a call on the platform module and names the
// member.
func platformMember(c *syntax.CallExpr) (string, bool) {
	dot, ok := c.Fn.(*syntax.DotExpr)
	if !ok {
		return "", false
	}
	id, ok := dot.X.(*syntax.Ident)
	if !ok || id.Name != "platform" {
		return "", false
	}
	return dot.Name.Name, true
}
