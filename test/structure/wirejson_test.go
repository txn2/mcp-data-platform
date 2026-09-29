package structure_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The platform answers in JSON through one encoder, internal/wirejson, which
// writes a nil slice as [] where encoding/json writes null (#1832). These
// tests hold every response to it.
//
// A response is written by a function that takes an http.ResponseWriter,
// returns an MCP tool result (*mcp.CallToolResult), or builds a tool result's
// text block (mcp.TextContent). Such a function may not call encoding/json's
// Marshal, MarshalIndent or NewEncoder: whatever it encodes is on its way to a
// client. And a tool is registered through toolkit.AddTool, which encodes a
// typed tool's output with the same encoder, never mcp.AddTool, which encodes
// it with encoding/json.

// wirejsonRoots are the trees whose code answers clients.
var wirejsonRoots = []string{"cmd", "internal", "pkg"}

// wirejsonExempt are the paths whose encoding is not a platform response: the
// encoder itself, the wrapper that registers tools through it, and the dev
// stack's stand-in upstream MCP server.
var wirejsonExempt = []string{
	"internal/wirejson/",
	"pkg/toolkit/addtool.go",
	"cmd/dev-mcp-mock/",
}

func TestResponsesAreEncodedByWirejson(t *testing.T) {
	root := moduleRoot(t)
	found := make([]string, 0, len(wirejsonRoots))
	for _, dir := range wirejsonRoots {
		found = append(found, scanWirejson(t, root, dir)...)
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Errorf("%d response(s) encoded outside internal/wirejson, which writes an empty list as [] rather than null (#1832); "+
			"use wirejson.Marshal, wirejson.MarshalIndent or wirejson.Encode, and toolkit.AddTool for a tool:\n  %s",
			len(found), strings.Join(found, "\n  "))
	}
}

// The gate fails on a handler that encodes its response directly, whichever of
// the three shapes it has, and passes the same handler written through the
// encoder.
func TestResponsesAreEncodedByWirejson_RefusesADirectEncoding(t *testing.T) {
	const bad = `package x

import (
	stdjson "encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func handler(w http.ResponseWriter, r *http.Request) {
	_ = stdjson.NewEncoder(w).Encode(map[string]any{})
}

func tool() (*mcp.CallToolResult, error) {
	b, _ := stdjson.Marshal([]string(nil))
	return &mcp.CallToolResult{}, nil
}

func text(v any) mcp.Content {
	b, _ := stdjson.MarshalIndent(v, "", "  ")
	return &mcp.TextContent{Text: string(b)}
}

func register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "x"}, nil)
	route := func(w http.ResponseWriter, _ *http.Request) {
		b, _ := stdjson.Marshal(1)
		_, _ = w.Write(b)
	}
	_ = route
}

func unrelated(v any) []byte {
	b, _ := stdjson.Marshal(v)
	return b
}
`
	got := wirejsonViolations(t, "x.go", bad)
	want := []string{
		"x.go:11: encoding/json NewEncoder in handler, which writes a response",
		"x.go:15: encoding/json Marshal in tool, which writes a response",
		"x.go:20: encoding/json MarshalIndent in text, which writes a response",
		"x.go:25: mcp.AddTool in register; register the tool with toolkit.AddTool",
		"x.go:27: encoding/json Marshal in func literal, which writes a response",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	good := strings.NewReplacer(`stdjson "encoding/json"`, `"github.com/txn2/mcp-data-platform/internal/wirejson"`,
		"stdjson.NewEncoder(w).Encode(map[string]any{})", "wirejson.Encode(w, map[string]any{})",
		"stdjson.", "wirejson.", "mcp.AddTool(", "toolkit.AddTool(").Replace(bad)
	if got := wirejsonViolations(t, "x.go", good); len(got) != 0 {
		t.Fatalf("the encoder's own calls must pass: %v", got)
	}
}

func scanWirejson(t *testing.T, root, dir string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || wirejsonExempted(rel) {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // a path under the module root
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}
		found = append(found, wirejsonViolations(t, rel, string(src))...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return found
}

func wirejsonExempted(rel string) bool {
	for _, p := range wirejsonExempt {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

// wirejsonViolations reports each direct encoding of a response in one file.
func wirejsonViolations(t *testing.T, name, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	imports := importNames(file)
	jsonName, httpName, mcpName := imports["encoding/json"], imports["net/http"], imports["github.com/modelcontextprotocol/go-sdk/mcp"]
	var out []string
	report := func(pos token.Pos, msg string) {
		out = append(out, fmt.Sprintf("%s:%d: %s", name, fset.Position(pos).Line, msg))
	}
	var visit func(fn string, typ *ast.FuncType, body *ast.BlockStmt)
	visit = func(fn string, typ *ast.FuncType, body *ast.BlockStmt) {
		if body == nil {
			return
		}
		responds := writesResponse(typ, body, httpName, mcpName)
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				visit("func literal", n.Type, n.Body)
				return false
			case *ast.CallExpr:
				if pkg, sel := selector(n.Fun); pkg == mcpName && mcpName != "" && sel == "AddTool" {
					report(n.Pos(), "mcp.AddTool in "+fn+"; register the tool with toolkit.AddTool")
				} else if responds && pkg == jsonName && jsonName != "" && (sel == "Marshal" || sel == "MarshalIndent" || sel == "NewEncoder") {
					report(n.Pos(), "encoding/json "+sel+" in "+fn+", which writes a response")
				}
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			visit(fd.Name.Name, fd.Type, fd.Body)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return lineOf(out[i]) < lineOf(out[j]) })
	return out
}

// writesResponse reports whether a function writes a response: it takes an
// http.ResponseWriter, returns a *mcp.CallToolResult, or builds an
// mcp.TextContent outside the functions nested in it.
func writesResponse(typ *ast.FuncType, body *ast.BlockStmt, httpName, mcpName string) bool {
	if typ.Params != nil {
		for _, f := range typ.Params.List {
			if pkg, sel := selector(f.Type); httpName != "" && pkg == httpName && sel == "ResponseWriter" {
				return true
			}
		}
	}
	if typ.Results != nil {
		for _, f := range typ.Results.List {
			if star, ok := f.Type.(*ast.StarExpr); ok {
				if pkg, sel := selector(star.X); mcpName != "" && pkg == mcpName && sel == "CallToolResult" {
					return true
				}
			}
		}
	}
	builds := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CompositeLit:
			if pkg, sel := selector(n.Type); mcpName != "" && pkg == mcpName && sel == "TextContent" {
				builds = true
			}
		}
		return !builds
	})
	return builds
}

// importNames maps each import path to the name the file refers to it by.
func importNames(file *ast.File) map[string]string {
	names := map[string]string{}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[path] = name
	}
	return names
}

// selector splits pkg.Name into its two identifiers, or returns "" for
// anything else.
func selector(e ast.Expr) (pkg, sel string) {
	s, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	id, ok := s.X.(*ast.Ident)
	if !ok {
		return "", ""
	}
	return id.Name, s.Sel.Name
}

func lineOf(finding string) int {
	parts := strings.SplitN(finding, ":", 3)
	n, _ := strconv.Atoi(parts[1])
	return n
}
