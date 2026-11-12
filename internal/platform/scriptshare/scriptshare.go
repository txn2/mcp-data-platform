// Package scriptshare finds the functions a script being saved shares with
// the deployment's libraries and other scripts (#2273).
//
// Libraries exist and work (#1941), and an agent writing its tenth automation
// still writes its own copy of the pagination helper the other nine carry,
// because nothing tells it the function already exists. The save gate reads
// every saved source anyway, so it can see a duplicate at the moment one is
// written and say so in the response the agent reads: the library and version
// that already holds the function and the load line that replaces the copy,
// or the scripts that also define it. A match is reported and never refused.
//
// Two functions match when their bodies are the same after normalization: the
// docstring, comments, formatting, the function's own name and the names of
// its parameters and local variables do not count. Anything else -- a
// constant, an operator, a call, the shape of a statement -- does, so a
// function that differs in one expression is a different function.
package scriptshare

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
)

// minNodes is the smallest function body worth naming: below it a function is
// a line any two scripts can share by accident (return x + 1), and moving it
// into a library would cost more than the copy.
const minNodes = 12

// entryPoints are the functions the platform calls in a script. A library
// holds none of them, so a shared one is nothing a library can take.
var entryPoints = []string{"main", "handle", "form_spec", "form_submit"}

// testPrefix starts a test function, which is never reported (#1939).
const testPrefix = "test_"

// Source is one saved script's current source, as a comparison reads it.
type Source struct {
	ID      string
	Name    string
	Library bool
	Version int
	Code    string
}

// Sources reads every live script's current source: the libraries and the
// scripts a saved source is compared with.
type Sources interface {
	CodeSources(ctx context.Context) ([]Source, error)
}

// Match is one function of the saved source that already exists elsewhere.
// It names functions and scripts, never source.
type Match struct {
	// Function is the function's name in the saved source.
	Function string `json:"function"`
	// Library, Version and LibraryFunction name the library holding the same
	// function and its name there, and Load is the line that replaces the copy.
	Library         string `json:"library,omitempty"`
	Version         int    `json:"version,omitempty"`
	LibraryFunction string `json:"library_function,omitempty"`
	Load            string `json:"load,omitempty"`
	// Scripts are the other scripts that define it, when no library does.
	Scripts []string `json:"scripts,omitempty"`
	// Next is what to do about it.
	Next string `json:"next"`
}

// Find is every function of code (saved as the script self, a library when
// library) that a library or another script in others also defines, in the
// order code defines them. A source that does not parse shares nothing.
func Find(code, self string, library bool, others []Source) []Match {
	mine := functions(code)
	out := []Match{}
	if len(mine) == 0 {
		return out
	}
	index := map[string][]found{}
	for _, o := range others {
		if o.ID == self {
			continue
		}
		for _, fn := range functions(o.Code) {
			index[fn.print] = append(index[fn.print], found{source: o, name: fn.name})
		}
	}
	for _, fn := range mine {
		if m, ok := match(fn, index[fn.print], library); ok {
			out = append(out, m)
		}
	}
	return out
}

// found is one function another source defines.
type found struct {
	source Source
	name   string
}

// match is what the saved function fn shares with the functions in hits.
func match(fn function, hits []found, library bool) (Match, bool) {
	if len(hits) == 0 {
		return Match{}, false
	}
	slices.SortFunc(hits, func(a, b found) int { return strings.Compare(a.source.Name, b.source.Name) })
	for _, h := range hits {
		if h.source.Library {
			load := fmt.Sprintf("load(%q, %q)", fmt.Sprintf("lib:%s@%d", h.source.Name, h.source.Version), h.name)
			return Match{
				Function: fn.name, Library: h.source.Name, Version: h.source.Version, LibraryFunction: h.name, Load: load,
				Next: fmt.Sprintf("Remove %s and add %s; the library holds the same function.", fn.name, load),
			}, true
		}
	}
	var scripts []string
	for _, h := range hits {
		if !slices.Contains(scripts, h.source.Name) {
			scripts = append(scripts, h.source.Name)
		}
	}
	next := fmt.Sprintf("%s also defines %s. It can move into a library each script loads at a pinned version "+
		"(load(\"lib:<name>@<version>\", \"%s\")); save the library first, and change the other scripts only with "+
		"their owners' agreement.", strings.Join(scripts, ", "), fn.name, fn.name)
	if library {
		next = fmt.Sprintf("%s also defines %s. Each can load it from this library at a pinned version, changed only "+
			"with its owner's agreement.", strings.Join(scripts, ", "), fn.name)
	}
	return Match{Function: fn.name, Scripts: scripts, Next: next}, true
}

// function is one top-level function and its normalized print.
type function struct {
	name  string
	print string
}

// functions are the top-level functions of code worth comparing: not a test,
// not an entry point, and not too small to matter.
func functions(code string) []function {
	file, err := scriptdialect.Options.Parse("script", code, 0)
	if err != nil {
		return nil
	}
	var out []function
	for _, stmt := range file.Stmts {
		def, ok := stmt.(*syntax.DefStmt)
		if !ok || strings.HasPrefix(def.Name.Name, testPrefix) || slices.Contains(entryPoints, def.Name.Name) {
			continue
		}
		if p, nodes := normalize(def); nodes >= minNodes {
			out = append(out, function{name: def.Name.Name, print: p})
		}
	}
	return out
}

// ListInput is manage_script list's library filter (#2273): true lists the
// libraries alone, false the scripts that run, and leaving it out both.
type ListInput struct {
	Library *bool `json:"library,omitempty"`
}

// ListProperties is the schema fragment for ListInput.
func ListProperties() map[string]any {
	return map[string]any{"library": map[string]any{
		"type": "boolean",
		"description": "For list: true lists only libraries (sources with no main() and no handle() that other " +
			"scripts load with load(\"lib:<name>@<version>\", \"fn\")); false lists only the scripts that run; " +
			"leave it out for both. Look for a library before writing a helper a script needs.",
	}}
}
