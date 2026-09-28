package scriptdialect

import (
	"maps"
	"slices"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// TestPrefix starts the name of a test (#1939): a top-level def whose name
// begins with it is one of the script's tests, run by the test runner and
// never by a run.
const TestPrefix = "test_"

// IsTest reports whether a top-level statement defines a test.
func IsTest(s syntax.Stmt) bool {
	d, ok := s.(*syntax.DefStmt)
	return ok && strings.HasPrefix(d.Name.Name, TestPrefix)
}

// Tests is the names of the file's tests, in source order.
func Tests(file *syntax.File) []string {
	var out []string
	for _, s := range file.Stmts {
		if d, ok := s.(*syntax.DefStmt); ok && IsTest(d) {
			out = append(out, d.Name.Name)
		}
	}
	return out
}

// coverName is the builtin every instrumented statement calls before it runs.
// A script cannot name it: it is bound only for the module Instrument rewrites,
// and the dunder spelling is one no script writes.
const coverName = "__cover__"

// CoverStepsPerStatement is the interpreter steps one inserted __cover__ call
// costs (load the builtin, load the index, call, pop the result), which a test
// run adds to its step cap for each call made so the instrumentation never
// fails a test that would fit uninstrumented (#1940).
const CoverStepsPerStatement = 4

// Coverage records which of a script's statements ran (#1940).
//
// A statement is one that compiles to code: every statement but pass and a
// docstring, in every statement list, function bodies included, outside the
// script's tests. Coverage is per statement, so the branches inside one
// expression (a conditional expression, and/or, a comprehension's if) are not
// measured. One Coverage is shared by every test of one source: the table is
// built by the first run, and the statements are numbered in walk order, which
// is the same order for the same source every time.
type Coverage struct {
	stmts []syntax.Position
	hit   []bool
	calls uint64
}

// NewCoverage returns an empty Coverage.
func NewCoverage() *Coverage { return &Coverage{} }

// Calls is how many instrumented statements ran, across every run the Coverage
// was handed to.
func (c *Coverage) Calls() uint64 { return c.calls }

// Total and Covered are the statements counted and the ones that ran.
func (c *Coverage) Total() int { return len(c.stmts) }

// Covered is the number of statements that ran.
func (c *Coverage) Covered() int {
	n := 0
	for _, h := range c.hit {
		if h {
			n++
		}
	}
	return n
}

// MissedLines is the lines holding a statement that never ran, ascending and
// each once.
func (c *Coverage) MissedLines() []int {
	lines := map[int]bool{}
	for i, h := range c.hit {
		if !h {
			lines[int(c.stmts[i].Line)] = true
		}
	}
	out := slices.Collect(maps.Keys(lines))
	slices.Sort(out)
	return out
}

// whole is a share stated in percent.
const whole = 100

// Percent is the share of statements that ran, all of them for a script with
// none.
func (c *Coverage) Percent() float64 {
	if len(c.stmts) == 0 {
		return whole
	}
	return float64(c.Covered()) * whole / float64(len(c.stmts))
}

// instrument rewrites file so that each statement it counts first calls the
// coverName builtin with that statement's number, and returns env with the
// builtin bound. The inserted call carries the statement's own position, so a
// traceback through it still names the author's line.
func (c *Coverage) instrument(file *syntax.File, env starlark.StringDict) starlark.StringDict {
	fresh := c.stmts == nil
	next := 0
	number := func(s syntax.Stmt) int {
		start, _ := s.Span()
		if fresh {
			c.stmts = append(c.stmts, start)
			c.hit = append(c.hit, false)
		}
		next++
		return next - 1
	}
	var list func([]syntax.Stmt) []syntax.Stmt
	list = func(stmts []syntax.Stmt) []syntax.Stmt {
		// An absent else stays absent: the parser leaves it nil, and a
		// statement's span reads a non-nil one as holding a statement.
		if len(stmts) == 0 {
			return stmts
		}
		out := make([]syntax.Stmt, 0, len(stmts))
		for _, s := range stmts {
			if counts(s) {
				start, _ := s.Span()
				out = append(out, coverCall(start, number(s)))
			}
			instrumentInner(s, list)
			out = append(out, s)
		}
		return out
	}
	stmts := make([]syntax.Stmt, 0, len(file.Stmts))
	for _, s := range file.Stmts {
		if IsTest(s) {
			stmts = append(stmts, s)
			continue
		}
		stmts = append(stmts, list([]syntax.Stmt{s})...)
	}
	file.Stmts = stmts
	out := make(starlark.StringDict, len(env))
	maps.Copy(out, env)
	out[coverName] = starlark.NewBuiltin(coverName, c.cover)
	return out
}

// instrumentInner rewrites the statement lists a statement holds.
func instrumentInner(s syntax.Stmt, list func([]syntax.Stmt) []syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.DefStmt:
		s.Body = list(s.Body)
	case *syntax.IfStmt:
		s.True = list(s.True)
		s.False = list(s.False)
	case *syntax.ForStmt:
		s.Body = list(s.Body)
	case *syntax.WhileStmt:
		s.Body = list(s.Body)
	}
}

// cover is the coverName builtin: it marks one statement as run.
func (c *Coverage) cover(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	c.calls++
	if len(args) == 1 {
		if n, ok := args[0].(starlark.Int); ok {
			if i, ok := n.Int64(); ok && i >= 0 && int(i) < len(c.hit) {
				c.hit[i] = true
			}
		}
	}
	return starlark.None, nil
}

// counts reports whether a statement compiles to code: pass and a string
// standing alone (a docstring) compile to nothing.
func counts(s syntax.Stmt) bool {
	switch s := s.(type) {
	case *syntax.ExprStmt:
		_, literal := s.X.(*syntax.Literal)
		return !literal
	case *syntax.BranchStmt:
		return s.Token != syntax.PASS
	}
	return true
}

// coverCall is the statement calling the coverName builtin with n, at pos.
func coverCall(pos syntax.Position, n int) syntax.Stmt {
	return &syntax.ExprStmt{X: &syntax.CallExpr{
		Fn:     &syntax.Ident{NamePos: pos, Name: coverName},
		Lparen: pos, Rparen: pos,
		Args: []syntax.Expr{&syntax.Literal{Token: syntax.INT, TokenPos: pos, Value: int64(n)}},
	}}
}
