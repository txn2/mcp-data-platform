//go:build integration

// Package sqlgate collects the SQL this repository hands to PostgreSQL and
// makes each statement reachable from a test, so a real server can parse and
// plan it before it ships (#1512).
//
// The gap it closes: nothing required a statement to be executed by a database
// before release. sqlmock matches a query as a string and returns rows the test
// supplies, so a statement PostgreSQL rejects passes its store test and counts
// as covered. #1506 shipped a SELECT whose unqualified column list was
// ambiguous across two joined tables; every gate was green.
//
// Preparing is the check. It runs the server's own parser and planner and
// executes nothing, so it is cheap and has no side effects, and it fails on
// exactly the class that reached production: an ambiguous column reference, a
// column or table that does not exist, a placeholder the planner cannot type.
//
// The package is integration-tagged, like internal/testdb: it is built only
// under `make test-realdb`, so it is invisible to the default build and to dead
// code analysis.
package sqlgate

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Kind is whether a statement can be prepared as written.
type Kind int

const (
	// Constant is a statement whose text is fully known at compile time,
	// including one concatenated from constants. It is prepared as written.
	Constant Kind = iota
	// Templated is a statement assembled at run time from a format string --
	// a scope predicate, an ORDER BY, a SET list. Its text is not knowable
	// from the source, so the package that builds it must register a rendered
	// sample; one that supplies none fails the gate rather than being skipped.
	Templated
)

// Statement is one SQL statement found in the source.
type Statement struct {
	// Package is the import path the statement was found in.
	Package string
	// Pos is "file:line", as a test failure should name it.
	Pos string
	// SQL is the statement text. For a Templated statement it still holds
	// the format verbs, so it is a locator rather than something to prepare.
	SQL  string
	Kind Kind
}

// String renders the statement for a failure message: where it is, then the
// statement itself with its indentation collapsed.
func (s Statement) String() string {
	return fmt.Sprintf("%s: %s", s.Pos, flatten(s.SQL))
}

// These recognize a Go string that is a SQL statement. Each requires the shape
// of the statement and not merely its first word, so an error message such as
// "delete prompt collection: %w" is not mistaken for a DELETE. A SELECT must
// begin the string: a parenthesized one is a subquery or a projection such as
// callrecord's `(SELECT COUNT(*) ...) AS reuse_count`, which belongs to a
// statement rather than being one.
var sqlShapes = []*regexp.Regexp{
	regexp.MustCompile(`(?is)^\s*SELECT\b.*\bFROM\b`),
	regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\b`),
	regexp.MustCompile(`(?is)^\s*UPDATE\b.+\bSET\b`),
	regexp.MustCompile(`(?is)^\s*DELETE\s+FROM\b`),
	regexp.MustCompile(`(?is)^\s*WITH\b.+\bAS\s*\(`),
}

// formatVerb finds a printf verb, which is what makes a statement Templated.
// %% is an escaped percent and does not make the text a format string.
var formatVerb = regexp.MustCompile(`%[-+# 0-9.\[\]*]*[a-zA-Z]`)

// IsSQL reports whether s is a SQL statement rather than prose that happens to
// begin with a SQL keyword.
func IsSQL(s string) bool {
	for _, re := range sqlShapes {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// classify reports how a recognized statement must be checked.
func classify(s string) Kind {
	if formatVerb.MatchString(strings.ReplaceAll(s, "%%", "")) {
		return Templated
	}
	return Constant
}

// Fact records how one package produces its SQL. It exists because the
// statements alone cannot answer the question the gate has to ask -- does this
// package build SQL no test can reach? -- for a package that never writes a
// statement down.
type Fact struct {
	// Templated counts the statements the package assembles from a format
	// string or around a run-time value.
	Templated int
	// UsesBuilder is true when the package imports a SQL builder library.
	// pkg/audit/postgres composes its metrics SELECTs entirely through
	// squirrel, so no string in its source is a statement and Templated is
	// zero, yet every one of those statements is assembled at run time.
	UsesBuilder bool
	// AppendsString is true when the package grows a string with +=, the other
	// way a statement is assembled without ever being written as one --
	// pkg/toolkits/gateway/enrichment builds its filtered SELECT that way.
	AppendsString bool
}

// AssembledAtRunTime reports whether the package produces SQL that no statement
// in its source captures, and therefore owes the gate a rendering.
func (f Fact) AssembledAtRunTime() bool {
	return f.Templated > 0 || f.UsesBuilder || f.AppendsString
}

// builderPackages are the SQL builder libraries whose use means a package
// composes statements at run time.
var builderPackages = map[string]bool{
	"github.com/Masterminds/squirrel": true,
}

// Extract returns every SQL statement in the named packages in a stable order,
// with a Fact for each package that holds SQL at all.
//
// A statement is any constant-foldable string expression that has the shape of
// one, which covers a plain literal and a concatenation of constants alike --
// the `"SELECT " + selectColumns + " FROM ..."` form #1506's bug was written
// in. A statement built with a format string, or around a run-time value, is
// reported as Templated: its text is a locator rather than something to
// prepare. A package that composes SQL without ever writing a statement down
// produces no Statement at all, which is what the Fact is for.
func Extract(patterns ...string) ([]Statement, map[string]Fact, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, nil, fmt.Errorf("loading packages: %w", err)
	}
	var out []Statement
	facts := map[string]Fact{}
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			return nil, nil, fmt.Errorf("loading %s: %v", p.PkgPath, p.Errors[0])
		}
		found := statementsIn(p)
		out = append(out, found...)
		if len(found) == 0 {
			// A package with no SQL at all is not a store, whatever it imports
			// or appends to. Only packages that hold SQL are asked how they
			// build the rest of it.
			continue
		}
		f := Fact{UsesBuilder: importsBuilder(p), AppendsString: appendsToString(p)}
		for _, s := range found {
			if s.Kind == Templated {
				f.Templated++
			}
		}
		facts[p.PkgPath] = f
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pos != out[j].Pos {
			return out[i].Pos < out[j].Pos
		}
		return out[i].SQL < out[j].SQL
	})
	return out, facts, nil
}

// importsBuilder reports whether the package imports a SQL builder library.
func importsBuilder(p *packages.Package) bool {
	for path := range p.Imports {
		if builderPackages[path] {
			return true
		}
	}
	return false
}

// appendsToString reports whether the package grows a string with +=, which in
// a package that holds SQL is how a statement is assembled a clause at a time.
func appendsToString(p *packages.Package) bool {
	found := false
	for _, f := range p.Syntax {
		ast.Inspect(f, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok || assign.Tok != token.ADD_ASSIGN || len(assign.Lhs) != 1 {
				return true
			}
			if tv, ok := p.TypesInfo.Types[assign.Lhs[0]]; ok && isString(tv) {
				found = true
			}
			return true
		})
	}
	return found
}

// statementsIn walks one package's syntax for string expressions that are SQL.
//
// A concatenation is examined whole before its operands, and the walk stops
// there, which is what separates a statement from a fragment. `"SELECT ... " +
// selectColumns` folds to one Constant statement rather than being reported per
// operand; `"SELECT COUNT(*) FROM t WHERE " + where` does not fold at all, and
// is reported once as Templated -- preparing its constant half alone would fail
// on a statement that is fine.
func statementsIn(p *packages.Package) []Statement {
	glued, fragmentDecls := concatenatedConstants(p)
	var out []Statement
	for _, f := range p.Syntax {
		ast.Inspect(f, func(node ast.Node) bool {
			expr, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			if fragmentDecls[expr] {
				return false
			}
			stmt, found := statementAt(p, expr, glued)
			if !found {
				return true
			}
			if stmt != nil {
				out = append(out, *stmt)
			}
			// Whether or not the expression was SQL, its operands are parts of
			// it and are not statements of their own.
			return false
		})
	}
	return out
}

// concatenatedConstants returns the named constants the package glues onto
// another string somewhere.
//
// Such a constant is a fragment even when it reads like a statement: prompt's
// collectionSelect is a SELECT with a LEFT JOIN and a COUNT, and every caller
// appends the GROUP BY that makes it legal. Preparing the constant alone would
// report a statement the database runs happily as broken.
func concatenatedConstants(p *packages.Package) (glued map[types.Object]bool, decls map[ast.Expr]bool) {
	glued = map[types.Object]bool{}
	for _, f := range p.Syntax {
		ast.Inspect(f, func(node ast.Node) bool {
			bin, ok := node.(*ast.BinaryExpr)
			if !ok || bin.Op != token.ADD {
				return true
			}
			for _, side := range []ast.Expr{bin.X, bin.Y} {
				if id, ok := side.(*ast.Ident); ok {
					if obj := p.TypesInfo.Uses[id]; obj != nil {
						glued[obj] = true
					}
				}
			}
			return true
		})
	}

	// The declaration's right-hand side is the same text under a different
	// node, so it is skipped alongside every use of the name.
	decls = map[ast.Expr]bool{}
	for _, f := range p.Syntax {
		ast.Inspect(f, func(node ast.Node) bool {
			spec, ok := node.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range spec.Names {
				if i < len(spec.Values) && glued[p.TypesInfo.Defs[name]] {
					decls[spec.Values[i]] = true
				}
			}
			return true
		})
	}
	return glued, decls
}

// statementAt reports whether expr is a whole string expression the walk should
// stop at, and the statement it holds if that string is SQL.
//
// found is true for any string-typed literal, named constant or concatenation.
// The returned statement is nil when such an expression is not SQL, so the
// caller still stops rather than descending into halves of a non-SQL string.
func statementAt(p *packages.Package, expr ast.Expr, glued map[types.Object]bool) (stmt *Statement, found bool) {
	switch e := expr.(type) {
	case *ast.BasicLit, *ast.BinaryExpr:
	case *ast.Ident:
		if glued[p.TypesInfo.Uses[e]] {
			return nil, true
		}
	default:
		return nil, false
	}
	tv, ok := p.TypesInfo.Types[expr]
	if !ok || !isString(tv) {
		return nil, false
	}

	sql, kind := text(p, expr)
	if !IsSQL(sql) {
		return nil, true
	}
	pos := p.Fset.Position(expr.Pos())
	return &Statement{
		Package: p.PkgPath,
		Pos:     fmt.Sprintf("%s:%d", trimPath(pos.Filename), pos.Line),
		SQL:     sql,
		Kind:    kind,
	}, true
}

// isString reports whether the expression's type is a string, constant or not.
func isString(tv types.TypeAndValue) bool {
	if tv.Value != nil {
		return tv.Value.Kind() == constant.String
	}
	basic, ok := tv.Type.Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsString != 0
}

// text renders the statement an expression holds, and says how it must be
// checked.
//
// A constant expression yields its exact text and is Constant. A concatenation
// with a run-time operand yields the constant parts with the operand shown as a
// hole, which is a locator rather than something to prepare, so it is
// Templated -- as is any constant text carrying a format verb.
func text(p *packages.Package, expr ast.Expr) (string, Kind) {
	if tv, ok := p.TypesInfo.Types[expr]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
		s := constant.StringVal(tv.Value)
		return s, classify(s)
	}
	if bin, ok := expr.(*ast.BinaryExpr); ok && bin.Op == token.ADD {
		left, _ := text(p, bin.X)
		right, _ := text(p, bin.Y)
		return left + right, Templated
	}
	return runtimeHole, Templated
}

// runtimeHole marks the place a run-time value takes in a rendered statement.
const runtimeHole = " <runtime> "

// trimPath shortens an absolute path to the part below the module root, so a
// failure names a path a reader can open.
func trimPath(path string) string {
	const module = "mcp-data-platform/"
	if i := strings.LastIndex(path, module); i >= 0 {
		return path[i+len(module):]
	}
	return path
}

// flatten collapses a statement's indentation onto one line.
func flatten(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}

// TemplatedPackages groups the statements a package assembles at run time, by
// import path. It is the set of packages that owe the gate a rendering, since
// nothing in their source is a statement it can prepare.
func TemplatedPackages(stmts []Statement) map[string][]Statement {
	out := map[string][]Statement{}
	for _, s := range stmts {
		if s.Kind == Templated {
			out[s.Package] = append(out[s.Package], s)
		}
	}
	return out
}

// Describe renders statements for a failure message, one indented line each.
func Describe(stmts []Statement) string {
	var b strings.Builder
	for _, s := range stmts {
		fmt.Fprintf(&b, "\n    %s", s)
	}
	return b.String()
}
