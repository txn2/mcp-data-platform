// Package scriptdialect is the Starlark dialect every managed script is
// parsed, resolved and executed under, in one place, so the run, the validator
// and every reader of a script's syntax tree (the flow graph,
// internal/platform/scriptflow) read one language.
package scriptdialect

import (
	"fmt"
	"maps"
	"slices"

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
}

// Exec loads a script's module and then calls its main() when the platform
// owns that call (EntryPoint, #1944), or the function hooks.Entry names. What
// the function returns is not the run's result; platform.result is the one way
// a script reports one. The module's globals are returned whatever happened,
// since a caller that measures the run walks them.
func Exec(thread *starlark.Thread, name, source string, env starlark.StringDict, hooks Hooks) (starlark.StringDict, error) {
	file, err := Options.Parse(name, source, 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // the parser's own message is what the author reads
	}
	file, env = Formatting(file, env)
	// Instrumented first, so the statement withMainEnd appends to main is not
	// counted as the author's.
	if hooks.Cover != nil {
		env = hooks.Cover.instrument(file, env)
	}
	if hooks.AtMainEnd != nil {
		env = withMainEnd(file, env, hooks.AtMainEnd)
	}
	prog, err := starlark.FileProgram(file, env.Has)
	if err != nil {
		return nil, err //nolint:wrapcheck // the resolver's own message is what the author reads
	}
	globals, err := prog.Init(thread, env)
	if err != nil {
		return globals, err //nolint:wrapcheck // the interpreter's failure, whose backtrace is the message
	}
	entry := hooks.Entry
	if entry == "" {
		if EntryPoint(file) == nil {
			return globals, nil
		}
		entry = EntryPointName
	}
	fn, ok := globals[entry].(starlark.Callable)
	if !ok {
		if hooks.Entry != "" {
			return globals, fmt.Errorf("the script defines no function %s", entry)
		}
		return globals, nil
	}
	_, err = starlark.Call(thread, fn, nil, nil)
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

// mainEndName is the builtin withMainEnd routes main's ending through. A
// script cannot name it: it is bound only for the module it rewrites, and the
// dunder spelling is one no script writes.
const mainEndName = "__main_ends__"

// withMainEnd rewrites the top-level main() taking no parameters so that it
// ends through the mainEndName builtin, which calls hook and hands back the
// value main was returning, and returns env with that builtin bound. A file
// with no such main is left as it is.
func withMainEnd(file *syntax.File, env starlark.StringDict, hook func(*starlark.Thread) error) starlark.StringDict {
	var main *syntax.DefStmt
	for _, s := range file.Stmts {
		if d, ok := s.(*syntax.DefStmt); ok && d.Name.Name == EntryPointName && len(d.Params) == 0 {
			main = d
		}
	}
	if main == nil {
		return env
	}
	for _, r := range returnsOf(main.Body) {
		r.Result = endCall(r.Return, r.Result)
	}
	_, end := main.Span()
	main.Body = append(main.Body, &syntax.ExprStmt{X: endCall(end, nil)})
	out := make(starlark.StringDict, len(env))
	maps.Copy(out, env)
	out[mainEndName] = starlark.NewBuiltin(mainEndName, func(th *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		if err := hook(th); err != nil {
			return nil, err
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
