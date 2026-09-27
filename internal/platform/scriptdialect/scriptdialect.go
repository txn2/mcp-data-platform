// Package scriptdialect is the Starlark dialect every managed script is
// parsed, resolved and executed under, in one place, so the run, the validator
// and every reader of a script's syntax tree (the flow graph,
// internal/platform/scriptflow) read one language.
package scriptdialect

import (
	"fmt"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
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
