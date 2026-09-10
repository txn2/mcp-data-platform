// This file adds the gate that keeps the draft write barrier's classification
// from going stale (issue #1664).
//
// internal/toolwrite decides whether one platform tool call persists anything,
// and a managed script's draft run refuses every call it cannot say reads. The
// table is deny-by-default, so a tool nobody classified is not a security hole
// — it is a tool an author cannot use in a draft, silently, until somebody
// notices. This gate makes adding a tool and forgetting to classify it a build
// failure instead.
//
// Run: go test -run TestEveryRegisteredToolIsClassified .
package structure_test

import (
	"go/ast"
	"go/constant"
	"go/types"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/txn2/mcp-data-platform/internal/toolwrite"
)

// addToolFunc is the SDK registration this gate reads tool names out of. Both
// the generic helper and the method form land here: the platform registers
// every tool it defines through one of them.
const (
	addToolPkg     = "github.com/modelcontextprotocol/go-sdk/mcp"
	addToolGeneric = "AddTool"
	// inventoryMethod is registry.Toolkit's inventory method: the names a
	// toolkit says it registers.
	inventoryMethod = "Tools"
)

// unclassifiedByDesign are the registered tools this gate does not require a
// rule for, each with the reason it cannot have one.
//
// It is deliberately tiny. An entry here is a statement that a draft will
// refuse the tool, which is a cost to an author, so it is paid only where no
// rule could be written.
var unclassifiedByDesign = map[string]string{}

// TestEveryRegisteredToolIsClassified fails when the platform registers a tool
// internal/toolwrite has no rule for.
//
// The tools an MCP gateway connection proxies are absent by construction: their
// names come from an upstream server at run time, no source registers them
// under a literal name, and the barrier reads their own read-only annotation
// instead.
func TestEveryRegisteredToolIsClassified(t *testing.T) {
	registered := registeredToolNames(t)
	require.NotEmpty(t, registered, "should find registered tool names")

	var missing []string
	for _, name := range registered {
		if toolwrite.Classified(name) {
			continue
		}
		if _, allowed := unclassifiedByDesign[name]; allowed {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)

	require.Empty(t, missing,
		"tool(s) registered with no rule in internal/toolwrite, so a managed script's draft run "+
			"refuses them:\n  %s\nAdd each to readOnlyTools, writeTools or actionTools "+
			"(internal/toolwrite/toolwrite.go), or to unclassifiedByDesign here with the reason no "+
			"rule can be written.", strings.Join(missing, "\n  "))
}

// registeredToolNames reads every tool name the first-party tree registers with
// the MCP SDK, resolving the constants the registration sites name.
//
// It reads the SOURCE rather than an assembled server because there is no
// configuration under which one server registers all of them: the toolkits are
// conditional on connections, stores and export dependencies, so an assembled
// fixture would assert over whichever subset that fixture happened to build.
//
// cmd/ is excluded. The tools registered there belong to the development mock
// UPSTREAM (cmd/dev-mcp-mock), a server the platform connects to as a gateway
// client; its tools reach a caller proxied and namespaced, never under the
// names it registers them with.
func registeredToolNames(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, pkg := range firstPartyPackages(t) {
		if !strings.HasPrefix(relPath(pkg.PkgPath), "pkg/") &&
			!strings.HasPrefix(relPath(pkg.PkgPath), "internal/") {
			continue
		}
		for _, file := range pkg.Syntax {
			collectToolNames(pkg, file, seen)
			collectToolkitInventory(pkg, file, seen)
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// collectToolNames walks one file for MCP tool registrations and records the
// Name each one is registered under.
func collectToolNames(pkg *packages.Package, file *ast.File, into map[string]bool) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isAddToolCall(pkg, call) {
			return true
		}
		for _, arg := range call.Args {
			if name, ok := toolNameOf(pkg, arg); ok {
				into[name] = true
			}
		}
		return true
	})
}

// collectToolkitInventory walks one file for the registry contract's inventory
// method and records every tool name it returns.
//
// It is the second half of the reading because not every registration passes a
// tool literal at the call site: a toolkit that builds its *mcp.Tool in a
// helper, and one whose tools are registered by an upstream library rather than
// by this module, are both invisible to the call-site read. Every toolkit
// answers Tools() with the names it registers — that is what the registry, the
// persona filter and the instruction baseline all read it for — so the method
// body carries the names the call sites do not.
func collectToolkitInventory(pkg *packages.Package, file *ast.File, into map[string]bool) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != inventoryMethod || fn.Body == nil {
			continue
		}
		if !returnsStringSlice(pkg, fn) {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			expr, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			if name, ok := constantString(pkg, expr); ok && name != "" {
				into[name] = true
			}
			return true
		})
	}
}

// returnsStringSlice reports whether a method's single result is []string,
// which is the registry inventory contract's shape.
func returnsStringSlice(pkg *packages.Package, fn *ast.FuncDecl) bool {
	sig, ok := pkg.TypesInfo.Defs[fn.Name].(*types.Func)
	if !ok {
		return false
	}
	results := sig.Signature().Results()
	if results.Len() != 1 {
		return false
	}
	slice, ok := results.At(0).Type().(*types.Slice)
	if !ok {
		return false
	}
	basic, ok := slice.Elem().Underlying().(*types.Basic)
	return ok && basic.Kind() == types.String
}

// isAddToolCall reports whether a call is the SDK's tool registration, by the
// package the callee is declared in rather than by the text of the expression:
// a dot-import or a local alias would defeat a textual match.
func isAddToolCall(pkg *packages.Package, call *ast.CallExpr) bool {
	fun := call.Fun
	if idx, ok := fun.(*ast.IndexExpr); ok {
		fun = idx.X
	}
	if idx, ok := fun.(*ast.IndexListExpr); ok {
		fun = idx.X
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != addToolGeneric {
		return false
	}
	obj, ok := pkg.TypesInfo.Uses[sel.Sel]
	if !ok || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == addToolPkg
}

// toolNameOf reads the Name field out of an &mcp.Tool{...} argument, resolving
// a constant reference to its value. A computed name yields ok=false: nothing
// static can classify it, and the gateway's proxied tools are exactly that
// shape.
func toolNameOf(pkg *packages.Package, arg ast.Expr) (string, bool) {
	unary, ok := arg.(*ast.UnaryExpr)
	if !ok {
		return "", false
	}
	lit, ok := unary.X.(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Name" {
			continue
		}
		return constantString(pkg, kv.Value)
	}
	return "", false
}

// constantString evaluates an expression to its string value when the type
// checker folded it to one.
func constantString(pkg *packages.Package, expr ast.Expr) (string, bool) {
	tv, ok := pkg.TypesInfo.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	if basic, ok := tv.Type.Underlying().(*types.Basic); !ok || basic.Kind() != types.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}
