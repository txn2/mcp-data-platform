// Package scriptdialect is the Starlark dialect every managed script is
// parsed, resolved and executed under, in one place, so the run, the validator
// and every reader of a script's syntax tree (the flow graph,
// internal/platform/scriptflow) read one language.
package scriptdialect

import (
	"fmt"
	"maps"
	"slices"
	"sync"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/scriptprintf"
)

// Options is the dialect every managed script is parsed and resolved under.
//
// while and recursion are OFF. Both are unbounded control flow whose cost
// cannot be read off the source, and a script that needs either is doing
// computation that belongs in SQL. This is the deliberate restrictiveness of
// the feature, not an oversight, and it is the only pair of switches here that
// is about safety.
//
// TopLevelControl and GlobalReassign are ON, and both defaults are inverted on
// purpose. Starlark's defaults come from Bazel, where a .bzl file is a
// DECLARATION loaded by other files: top-level control flow and rebinding a
// top-level name would make what a file declares depend on evaluation order. A
// managed script is the opposite — a procedure executed once, top to bottom, by
// one runner, loaded by nobody. Under the Bazel defaults an author could not
// write `total = 0` and then accumulate into it inside a loop without wrapping
// the whole script in a function, which is friction that buys no safety and no
// determinism: neither switch has anything to do with either. `load` stays
// file-local (and there is nothing to load).
//
// A script created since #1944 is held to more than the dialect: its work is in
// main(), which the platform calls (EntryPoint), and its top level declares.
// That is a rule of the authoring gates, checked on save, not a switch here, so
// a script saved before it keeps parsing and running as it did.
var Options = &syntax.FileOptions{
	Set:               true,
	While:             false,
	TopLevelControl:   true,
	GlobalReassign:    true,
	LoadBindsGlobally: false,
	Recursion:         false,
}

// Parse parses and resolves source under Options, keeping its comments, for a
// reader that walks the syntax tree and must see the tree a run sees, with
// every identifier's binding set by the resolver. predeclared reports whether
// a name is part of the script environment. The error is the parser's or the
// resolver's.
func Parse(source string, predeclared func(string) bool) (*syntax.File, error) {
	file, err := Options.Parse("script", source, syntax.RetainComments)
	if err != nil {
		return nil, fmt.Errorf("parsing script: %w", err)
	}
	if _, err := starlark.FileProgram(file, predeclared); err != nil {
		return nil, fmt.Errorf("resolving script: %w", err)
	}
	return file, nil
}

// EntryPointName is the function the platform calls after a script's module is
// loaded (#1944).
const EntryPointName = "main"

// EntryPoint returns the main() the platform calls for file, or nil when it
// calls none: main is not defined at the top level, takes parameters, or is
// already called by the top level itself. The last case is a script written in
// the Python habit (def main(), then main() at the bottom); calling it again
// would run its work twice, so the script's own call is the one that runs.
//
// The run, the flow graph and the lint all ask this, so a script's main() is
// either called by all three or by none.
func EntryPoint(file *syntax.File) *syntax.DefStmt {
	if file == nil {
		return nil
	}
	var main *syntax.DefStmt
	for _, s := range file.Stmts {
		if d, ok := s.(*syntax.DefStmt); ok && d.Name.Name == EntryPointName {
			main = d
		}
	}
	if main == nil || len(main.Params) > 0 || topLevelCalls(file.Stmts, EntryPointName) {
		return nil
	}
	return main
}

// HandlerName is the function an endpoint's request calls, with the request
// as its one argument (#2172).
const HandlerName = "handle"

// HandlerOf returns file's top-level handle(req) taking exactly one
// parameter, or nil when it defines none that a request can call.
func HandlerOf(file *syntax.File) *syntax.DefStmt {
	if file == nil {
		return nil
	}
	var handler *syntax.DefStmt
	for _, s := range file.Stmts {
		if d, ok := s.(*syntax.DefStmt); ok && d.Name.Name == HandlerName {
			handler = d
		}
	}
	if handler == nil || len(handler.Params) != 1 {
		return nil
	}
	return handler
}

// topLevelCalls reports whether a top-level statement calls the named
// function. A def's body is not the top level, so a call inside one does not
// count; a lambda's body runs only when it is called, so neither does a call
// inside one.
func topLevelCalls(stmts []syntax.Stmt, name string) bool {
	return slices.ContainsFunc(stmts, func(s syntax.Stmt) bool {
		if _, ok := s.(*syntax.DefStmt); ok {
			return false
		}
		found := false
		syntax.Walk(s, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.LambdaExpr:
				return false
			case *syntax.CallExpr:
				if id, ok := n.Fn.(*syntax.Ident); ok && id.Name == name {
					found = true
				}
			}
			return !found
		})
		return found
	})
}

// Hooks is what a caller asks Exec to do beyond loading a script's module and
// calling its main(). The zero value asks for nothing more.
type Hooks struct {
	// AtMainEnd, when not nil, is called on the thread as main() finishes, by
	// whichever caller called it, while main's frame is still live: at each
	// return and at the end of its body. It is how a caller measures what main
	// holds when it ends, which is gone by the time Exec returns; an error from
	// it fails the run at that point.
	AtMainEnd func(*starlark.Thread) error
	// Entry, when not empty, is the top-level function called after the module
	// loads in place of main(): a test (#1939). It takes no arguments.
	Entry string
	// Cover, when not nil, is told each statement of the script as it is about
	// to run (#1940); see Instrument.
	Cover *Coverage
	// Handler, when not nil, is the top-level function called in place of
	// main() with arguments: an endpoint's handle(req) (#2172).
	Handler *Handler
}

// Handler is a top-level function an execution calls with arguments in place
// of main(), and what it returned (#2172). A request to an endpoint is one:
// handle(req) is called with the request and answers with what it returns.
type Handler struct {
	// Name is the function called.
	Name string
	// Args are the positional arguments it is called with.
	Args starlark.Tuple
	// Returned, when not nil, is handed what the function returned while the
	// execution is still live; an error from it fails the execution.
	Returned func(starlark.Value) error
	// Program, when not nil, is the module compiled once and reused by every
	// execution that names it, so a request does not parse the script again.
	Program *Program
}

// Program is a script's module compiled once and initialized per execution
// (#2172). The zero value is ready to use. One Program holds one source: the
// first execution that names it compiles it, and every later one reuses that
// compilation whatever source it passes, so a caller keeps one Program per
// script version. A compiled starlark program is immutable, which is what
// lets concurrent executions initialize it at once.
type Program struct {
	once sync.Once
	prog *starlark.Program
	err  error
}

// compile parses and resolves source the first time it is called, the way
// Exec does, against the names env predeclares.
func (p *Program) compile(name, source string, env starlark.StringDict) (*starlark.Program, error) {
	p.once.Do(func() {
		file, err := Options.Parse(name, source, 0)
		if err != nil {
			p.err = err
			return
		}
		scriptprintf.Rewrite(file)
		rewriteMainEnd(file)
		p.prog, p.err = starlark.FileProgram(file, env.Has)
	})
	return p.prog, p.err
}

// Exec loads a script's module and then calls its main() when the platform
// owns that call (EntryPoint, #1944), the function hooks.Entry names, or the
// handler hooks.Handler names with its arguments. What main() or a test
// returns is not the run's result; platform.result is the one way a script
// reports one, and a handler's return is handed to Handler.Returned. The
// module's globals are returned whatever happened, since a caller that
// measures the run walks them.
func Exec(thread *starlark.Thread, name, source string, env starlark.StringDict, hooks Hooks) (starlark.StringDict, error) {
	if h := hooks.Handler; h != nil && h.Program != nil && hooks.Cover == nil {
		env = bindMainEnd(scriptprintf.Bind(env), hooks.AtMainEnd)
		prog, err := h.Program.compile(name, source, env)
		if err != nil {
			return nil, err //nolint:wrapcheck // the parser's or resolver's own message is what the author reads
		}
		return run(thread, prog, env, hooks, "")
	}
	file, err := Options.Parse(name, source, 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // the parser's own message is what the author reads
	}
	file, env = Formatting(file, env)
	// Instrumented first, so the statement rewriteMainEnd appends to main is
	// not counted as the author's.
	if hooks.Cover != nil {
		env = hooks.Cover.instrument(file, env)
	}
	if rewriteMainEnd(file) {
		env = bindMainEnd(env, hooks.AtMainEnd)
	}
	prog, err := starlark.FileProgram(file, env.Has)
	if err != nil {
		return nil, err //nolint:wrapcheck // the resolver's own message is what the author reads
	}
	entry := ""
	if EntryPoint(file) != nil {
		entry = EntryPointName
	}
	return run(thread, prog, env, hooks, entry)
}

// run initializes prog's module and calls the function the execution names:
// the handler or test hooks name, else main when the platform owns that call
// (main is "" when it does not).
func run(thread *starlark.Thread, prog *starlark.Program, env starlark.StringDict, hooks Hooks, main string) (starlark.StringDict, error) {
	globals, err := prog.Init(thread, env)
	if err != nil {
		return globals, err //nolint:wrapcheck // the interpreter's failure, whose backtrace is the message
	}
	entry, args := main, starlark.Tuple(nil)
	switch {
	case hooks.Handler != nil:
		entry, args = hooks.Handler.Name, hooks.Handler.Args
	case hooks.Entry != "":
		entry = hooks.Entry
	}
	if entry == "" {
		return globals, nil
	}
	fn, ok := globals[entry].(starlark.Callable)
	if !ok {
		if entry != main {
			return globals, fmt.Errorf("the script defines no function %s", entry)
		}
		return globals, nil
	}
	out, err := starlark.Call(thread, fn, args, nil)
	if err == nil && hooks.Handler != nil && hooks.Handler.Returned != nil {
		err = hooks.Handler.Returned(out)
	}
	return globals, err //nolint:wrapcheck // the interpreter's failure, whose backtrace is the message
}

// Formatting routes the file's `%` and str.format through the platform's
// implementation, which takes the flags, widths and precisions Starlark's own
// refuses (#2048), and returns env with it bound. Every module a run compiles
// goes through it: the script's own, and each library it loads.
func Formatting(file *syntax.File, env starlark.StringDict) (*syntax.File, starlark.StringDict) {
	scriptprintf.Rewrite(file)
	return file, scriptprintf.Bind(env)
}

// mainEndName is the builtin rewriteMainEnd routes main's ending through. A
// script cannot name it: it is bound only for the module it rewrites, and the
// dunder spelling is one no script writes.
const mainEndName = "__main_ends__"

// rewriteMainEnd rewrites the top-level main() taking no parameters so that
// it ends through the mainEndName builtin, and reports whether it found one to
// rewrite. bindMainEnd binds that builtin; a file with no such main is left as
// it is.
func rewriteMainEnd(file *syntax.File) bool {
	var main *syntax.DefStmt
	for _, s := range file.Stmts {
		if d, ok := s.(*syntax.DefStmt); ok && d.Name.Name == EntryPointName && len(d.Params) == 0 {
			main = d
		}
	}
	if main == nil {
		return false
	}
	for _, r := range returnsOf(main.Body) {
		r.Result = endCall(r.Return, r.Result)
	}
	_, end := main.Span()
	main.Body = append(main.Body, &syntax.ExprStmt{X: endCall(end, nil)})
	return true
}

// bindMainEnd returns env with the mainEndName builtin bound: it calls hook,
// when there is one, and hands back the value main was returning.
func bindMainEnd(env starlark.StringDict, hook func(*starlark.Thread) error) starlark.StringDict {
	out := make(starlark.StringDict, len(env))
	maps.Copy(out, env)
	out[mainEndName] = starlark.NewBuiltin(mainEndName, func(th *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		if hook != nil {
			if err := hook(th); err != nil {
				return nil, err
			}
		}
		if len(args) == 1 {
			return args[0], nil
		}
		return starlark.None, nil
	})
	return out
}

// endCall is a call of the mainEndName builtin at pos, passing value when
// there is one.
func endCall(pos syntax.Position, value syntax.Expr) *syntax.CallExpr {
	call := &syntax.CallExpr{Fn: &syntax.Ident{NamePos: pos, Name: mainEndName}, Lparen: pos, Rparen: pos}
	if value != nil {
		call.Args = []syntax.Expr{value}
	}
	return call
}

// returnsOf is every return statement of a function body, not those of a def
// or lambda nested in it.
func returnsOf(body []syntax.Stmt) []*syntax.ReturnStmt {
	var out []*syntax.ReturnStmt
	for _, s := range body {
		syntax.Walk(s, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.DefStmt, *syntax.LambdaExpr:
				return false
			case *syntax.ReturnStmt:
				out = append(out, n)
			}
			return true
		})
	}
	return out
}
