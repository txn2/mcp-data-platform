// This file adds the gate that keeps MCP tool annotations from drifting
// (issue #1692).
//
// A client is told by the MCP specification to assume a tool is NOT read-only
// when readOnlyHint is absent, so a tool registered with no Annotations field
// is advertised as a write. Twenty-one of the platform's tools were in that
// state, platform_info, search and fetch among them, which is the sequence the
// platform's own instructions require first: a user on a strict client
// approved three "writes" before any data came back.
//
// Two invariants are enforced here, on the mcp.Tool literals themselves rather
// than on an assembled server. There is no configuration under which one
// server registers every tool -- the toolkits are conditional on connections,
// stores and export dependencies -- so an assembled fixture would assert over
// whichever subset it happened to build. What a running server actually puts
// on the wire is asserted instead by test/acceptance/issue_1692_test.go.
//
//  1. Every tool literal sets Title and Annotations.
//  2. Where the literal's name is a resolvable constant and its annotation is
//     one of the two shared constructors, the hint agrees with
//     internal/toolwrite, which is the platform's other per-tool statement
//     about whether a tool writes. Two places saying opposite things about
//     manage_asset is the bug this half exists to prevent.
//
// Run: go test -run 'TestEveryToolLiteralIsAnnotated|TestAnnotationsAgreeWithToolwrite' .
package structure_test

import (
	"go/ast"
	"go/constant"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/txn2/mcp-data-platform/internal/toolwrite"
)

// annotationCtors are the two shared constructors a registration states its
// classification with. Reading the call rather than the struct is what lets
// this gate answer "read-only or not" for a literal it never evaluates.
const (
	annotationsPkg         = "github.com/txn2/mcp-data-platform/pkg/toolkit"
	ctorReadOnly           = "ReadOnlyAnnotations"
	ctorWrite              = "WriteAnnotations"
	mcpProtocolPkg         = "github.com/modelcontextprotocol/go-sdk/mcp"
	mcpToolTypeName        = "Tool"
	mcpAnnotationsTypeName = "ToolAnnotations"

	// minAnnotationComparisons is how many tools the agreement gate reached
	// when it was written (#1692). Raise it when the tree grows; never lower
	// it to make a run pass.
	minAnnotationComparisons = 25
)

// toolLiteral is one mcp.Tool composite literal found in the tree, with what
// the gate needs to judge it.
type toolLiteral struct {
	// where is the file and line, for a failure a reader can open.
	where string
	// name is the tool's registered name when the literal names it with a
	// resolvable constant, and "" when it is computed (the gateway builds a
	// proxied tool's name from a connection name at run time).
	name string
	// hasTitle and hasAnnotations report which fields the literal sets.
	hasTitle, hasAnnotations bool
	// ann is what the Annotations expression stated, and annRead says whether
	// the expression was a shape this gate could read.
	ann     annotationRead
	annRead bool
}

// toolLiterals returns every mcp.Tool composite literal in the first-party
// tree under pkg/ and internal/.
//
// cmd/ is excluded for the reason the sibling gate excludes it: the tools
// registered there belong to the development mock upstream
// (cmd/dev-mcp-mock), a server the platform connects to as a gateway client.
// Its annotations are the mock's business.
func toolLiterals(t *testing.T) []toolLiteral {
	t.Helper()
	var found []toolLiteral
	for _, pkg := range firstPartyPackages(t) {
		rel := relPath(pkg.PkgPath)
		if !strings.HasPrefix(rel, "pkg/") && !strings.HasPrefix(rel, "internal/") {
			continue
		}
		for _, file := range pkg.Syntax {
			found = append(found, toolLiteralsIn(pkg, file)...)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].where < found[j].where })
	return found
}

// toolLiteralsIn walks one file for mcp.Tool composite literals.
func toolLiteralsIn(pkg *packages.Package, file *ast.File) []toolLiteral {
	var found []toolLiteral
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isMCPToolLiteral(pkg, lit) {
			return true
		}
		found = append(found, readToolLiteral(pkg, lit))
		return true
	})
	return found
}

// isMCPToolLiteral reports whether a composite literal builds an mcp.Tool. It
// asks the type checker rather than matching the expression text, so an
// aliased import cannot slip a registration past the gate.
func isMCPToolLiteral(pkg *packages.Package, lit *ast.CompositeLit) bool {
	tv, ok := pkg.TypesInfo.Types[lit]
	return ok && tv.Type != nil && isMCPType(tv.Type, mcpToolTypeName)
}

// isMCPType reports whether a type is the named type from the MCP SDK.
func isMCPType(t types.Type, name string) bool {
	named, ok := t.(interface {
		Obj() *types.TypeName
	})
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Pkg() != nil &&
		obj.Pkg().Path() == mcpProtocolPkg && obj.Name() == name
}

// readToolLiteral reads the fields the gate judges out of one literal.
func readToolLiteral(pkg *packages.Package, lit *ast.CompositeLit) toolLiteral {
	out := toolLiteral{where: relPath(pkg.PkgPath) + ": " + positionOf(pkg, lit)}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name":
			if name, ok := constantString(pkg, kv.Value); ok {
				out.name = name
			}
		case "Title":
			out.hasTitle = true
		case "Annotations":
			out.hasAnnotations = true
			out.ann, out.annRead = annotationSays(pkg, kv.Value)
		}
	}
	return out
}

// annotationSays reports what an Annotations expression says about a tool, and
// whether it was a shape this gate can read at all.
//
// Two shapes are read. Most registrations call one of the shared constructors,
// which names the classification outright. The platform's own three tools
// (platform_info, list_connections, platform_find_tools) write the
// mcp.ToolAnnotations literal inline instead, because pkg/platform sits within
// three lines of its package-size budget and the helper's import would push it
// over; those are read field by field. That is the only reason for the
// inconsistency, and it goes away when pkg/platform is decomposed.
// Anything else -- a variable, a value from a helper in another package -- is
// unreadable here and judged only by the acceptance suite, against a running
// server.
func annotationSays(pkg *packages.Package, expr ast.Expr) (a annotationRead, ok bool) {
	switch e := expr.(type) {
	case *ast.CallExpr:
		return annotationCtorSays(pkg, e)
	case *ast.UnaryExpr:
		lit, isLit := e.X.(*ast.CompositeLit)
		if !isLit {
			return annotationRead{}, false
		}
		return annotationLiteralSays(pkg, lit)
	default:
		return annotationRead{}, false
	}
}

// annotationRead is what one Annotations expression stated.
type annotationRead struct {
	// readOnly is the readOnlyHint the expression sets.
	readOnly bool
	// statesDestructive reports whether destructiveHint is set rather than
	// left to the specification's default, which is true. A write that omits
	// it therefore claims it may destroy even when it only adds.
	statesDestructive bool
}

// annotationCtorSays reads a call to one of the shared constructors.
func annotationCtorSays(pkg *packages.Package, call *ast.CallExpr) (a annotationRead, ok bool) {
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel {
		return annotationRead{}, false
	}
	obj, used := pkg.TypesInfo.Uses[sel.Sel]
	if !used || obj.Pkg() == nil || obj.Pkg().Path() != annotationsPkg {
		return annotationRead{}, false
	}
	switch obj.Name() {
	case ctorReadOnly:
		return annotationRead{readOnly: true}, true
	case ctorWrite:
		// The constructor always sets destructiveHint; that is its point.
		return annotationRead{statesDestructive: true}, true
	default:
		return annotationRead{}, false
	}
}

// annotationLiteralSays reads an mcp.ToolAnnotations composite literal.
func annotationLiteralSays(pkg *packages.Package, lit *ast.CompositeLit) (a annotationRead, ok bool) {
	tv, typed := pkg.TypesInfo.Types[lit]
	if !typed || tv.Type == nil || !isMCPType(tv.Type, mcpAnnotationsTypeName) {
		return annotationRead{}, false
	}
	for _, elt := range lit.Elts {
		kv, isKV := elt.(*ast.KeyValueExpr)
		if !isKV {
			continue
		}
		key, isIdent := kv.Key.(*ast.Ident)
		if !isIdent {
			continue
		}
		switch key.Name {
		case "ReadOnlyHint":
			if v, known := constantBool(pkg, kv.Value); known {
				a.readOnly = v
			}
		case "DestructiveHint":
			a.statesDestructive = true
		}
	}
	return a, true
}

// constantBool evaluates an expression the type checker folded to a bool.
func constantBool(pkg *packages.Package, expr ast.Expr) (value, known bool) {
	tv, ok := pkg.TypesInfo.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.Bool {
		return false, false
	}
	return constant.BoolVal(tv.Value), true
}

// positionOf renders a node's file and line, module-relative.
func positionOf(pkg *packages.Package, n ast.Node) string {
	pos := pkg.Fset.Position(n.Pos())
	return filepath.Base(pos.Filename) + ":" + strconv.Itoa(pos.Line)
}

// TestEveryToolLiteralIsAnnotated fails when a tool is registered without
// saying whether it can modify state, which is the state #1692 was filed on,
// or without a title, which is how trino_export reached the admin listing
// nameless (#1691).
func TestEveryToolLiteralIsAnnotated(t *testing.T) {
	lits := toolLiterals(t)
	require.NotEmpty(t, lits, "should find mcp.Tool literals in the tree")

	unannotated, untitled := annotationGaps(lits)

	require.Empty(t, unannotated,
		"tool(s) registered with no Annotations field. A client is told to assume a tool is "+
			"NOT read-only when readOnlyHint is absent, so each of these is advertised to every "+
			"conforming client as a write:\n  %s\nSet Annotations with toolkit.ReadOnlyAnnotations() "+
			"or toolkit.WriteAnnotations(destructive).", strings.Join(unannotated, "\n  "))

	require.Empty(t, untitled,
		"tool(s) registered with no Title field, which the admin listing and every client's tool "+
			"picker show:\n  %s", strings.Join(untitled, "\n  "))
}

// TestAnnotationsAgreeWithToolwrite fails when the hint a tool advertises
// contradicts internal/toolwrite, which is what a managed script's draft run
// consults to decide whether a call persists anything. A tool advertised
// read-only and refused inside a draft, or the reverse, is one of the two
// being wrong, and neither is discoverable by reading one of them.
func TestAnnotationsAgreeWithToolwrite(t *testing.T) {
	disagree, checked := toolwriteDisagreements(toolLiterals(t))
	// A floor rather than a presence check. Both halves of the comparison are
	// resolved from source, so a change to how a registration is written --
	// a name built at run time, an annotation assembled somewhere else -- would
	// quietly shrink what this gate judges while it kept reporting PASS.
	require.GreaterOrEqual(t, checked, minAnnotationComparisons,
		"the gate compared %d tools against internal/toolwrite, fewer than the %d it reached before. "+
			"A registration written so its name or its annotation cannot be read from source is "+
			"outside this comparison; write it with a constant name and a shared constructor, or "+
			"lower the floor deliberately.", checked, minAnnotationComparisons)
	require.Empty(t, disagree,
		"the advertised annotation and the draft write barrier disagree:\n  %s",
		strings.Join(disagree, "\n  "))
}

// boolWord renders a hint for a failure message.
func boolWord(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// annotationGaps returns the literals that set no Annotations and the ones
// that set no Title, each labeled by tool name where it has a static one.
func annotationGaps(lits []toolLiteral) (unannotated, untitled []string) {
	for _, lit := range lits {
		label := lit.where
		if lit.name != "" {
			label = lit.name + " (" + lit.where + ")"
		}
		if !lit.hasAnnotations {
			unannotated = append(unannotated, label)
		}
		if !lit.hasTitle {
			untitled = append(untitled, label)
		}
	}
	return unannotated, untitled
}

// toolwriteDisagreements returns the literals whose advertised hint
// contradicts internal/toolwrite, and how many were in a position to be
// compared at all. A literal with a computed name, or one that built its
// annotation some other way, is not judged here.
func toolwriteDisagreements(lits []toolLiteral) (disagree []string, checked int) {
	for _, lit := range lits {
		if lit.name == "" || !lit.annRead {
			continue
		}
		checked++
		want := toolwrite.ReadOnly(lit.name)
		if want != lit.ann.readOnly {
			disagree = append(disagree, lit.name+" ("+lit.where+") advertises readOnlyHint="+
				boolWord(lit.ann.readOnly)+", internal/toolwrite says "+boolWord(want))
			continue
		}
		if !want && !lit.ann.statesDestructive {
			disagree = append(disagree, lit.name+" ("+lit.where+") writes and states no destructiveHint, "+
				"so a client reads the specification default, which is true")
		}
	}
	return disagree, checked
}

// TestToolAnnotationGatesFire is the negative control. Both gates above pass
// on a clean tree, which is also what a gate that judges nothing does, so each
// is shown here refusing the shape it exists to refuse.
func TestToolAnnotationGatesFire(t *testing.T) {
	unannotated, untitled := annotationGaps([]toolLiteral{
		{where: "a.go:1", name: "annotated", hasTitle: true, hasAnnotations: true},
		{where: "b.go:2", name: "bare"},
		{where: "c.go:3", hasAnnotations: true},
	})
	// c.go:3 annotates and does not title, so it is caught by one list only.
	require.Equal(t, []string{"bare (b.go:2)"}, unannotated)
	require.Equal(t, []string{"bare (b.go:2)", "c.go:3"}, untitled)

	// search is read-only in internal/toolwrite; save_asset is not.
	readOnly := annotationRead{readOnly: true}
	write := annotationRead{statesDestructive: true}
	disagree, checked := toolwriteDisagreements([]toolLiteral{
		{where: "a.go:1", name: "search", annRead: true, ann: readOnly},
		{where: "b.go:2", name: "search", annRead: true, ann: write},
		{where: "c.go:3", name: "save_asset", annRead: true, ann: readOnly},
		// A write that leaves destructiveHint to the specification default.
		{where: "d.go:4", name: "save_asset", annRead: true, ann: annotationRead{}},
		// Neither of these can be judged: no static name, and no readable
		// annotation expression.
		{where: "e.go:5", annRead: true, ann: readOnly},
		{where: "f.go:6", name: "search"},
	})
	require.Equal(t, 4, checked)
	require.Len(t, disagree, 3)
	require.Contains(t, disagree[0], "search (b.go:2) advertises readOnlyHint=false")
	require.Contains(t, disagree[1], "save_asset (c.go:3) advertises readOnlyHint=true")
	require.Contains(t, disagree[2], "save_asset (d.go:4) writes and states no destructiveHint")
}
