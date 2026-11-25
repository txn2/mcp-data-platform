package cachescope

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every cacheable result is stamped with its method's scope:
// private where the content depends on the caller, public where it does not,
// whether the SDK's handler or a middleware built it (#2794).
func TestStampSetsEachMethodsCacheScope(t *testing.T) {
	cases := []struct {
		method string
		result mcp.Result
		want   string
	}{
		{methodToolsList, &mcp.ListToolsResult{}, Private},
		{methodPromptsList, &mcp.ListPromptsResult{}, Private},
		{methodListResources, &mcp.ListResourcesResult{}, Private},
		{methodReadResource, &mcp.ReadResourceResult{}, Private},
		// The SDK normalized this one to public before the middleware saw it.
		{methodToolsList, func() *mcp.ListToolsResult { r := &mcp.ListToolsResult{}; r.CacheScope = Public; return r }(), Private},
		{methodResourcesTemplatesList, &mcp.ListResourceTemplatesResult{}, Public},
		{methodServerDiscover, &mcp.DiscoverResult{}, Public},
	}
	for _, tc := range cases {
		Stamp(tc.method, tc.result)
		if s := scopeOf(tc.result); s != tc.want {
			t.Errorf("%s: cacheScope %q, want %q", tc.method, s, tc.want)
		}
	}
	if Of("tools/call") != "" {
		t.Error("tools/call is not a cacheable result and has no scope")
	}
}

// scopeOf is the cache scope a result carries.
func scopeOf(r mcp.Result) string {
	switch v := r.(type) {
	case *mcp.ListToolsResult:
		return v.CacheScope
	case *mcp.ListPromptsResult:
		return v.CacheScope
	case *mcp.ListResourcesResult:
		return v.CacheScope
	case *mcp.ReadResourceResult:
		return v.CacheScope
	case *mcp.ListResourceTemplatesResult:
		return v.CacheScope
	case *mcp.DiscoverResult:
		return v.CacheScope
	}
	return ""
}

// TestEveryCacheableMethodHasAScope: every go-sdk result that embeds Cacheable
// is stamped by stampCacheScope, so an SDK upgrade that makes another result
// cacheable fails here rather than going out public; and a method whose scope
// is public is intercepted only by the middleware listed below, each with the
// reason its answer is the same for every caller. A middleware that filters a
// public method by caller fails until its method is decided private (#2794).
func TestEveryCacheableMethodHasAScope(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", "github.com/modelcontextprotocol/go-sdk").Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	sdk := filepath.Join(strings.TrimSpace(string(out)), "mcp")
	cacheable := cacheableResultTypes(t, sdk)
	if len(cacheable) == 0 {
		t.Fatal("found no go-sdk result embedding Cacheable; the scan is broken")
	}
	src, err := os.ReadFile("cachescope.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range cacheable {
		if !strings.Contains(string(src), "case *mcp."+name+":") {
			t.Errorf("go-sdk's %s carries a cache scope and Stamp does not set it", name)
		}
	}

	publicMethodNames := []string{`"resources/templates/list"`, `"server/discover"`, "methodResourcesTemplatesList", "methodServerDiscover"}
	allowed := map[string]string{
		"internal/cachescope/cachescope.go": "decides the scope",
		"pkg/middleware/mcp_visibility.go":  "declares the method name",
		"pkg/middleware/mcp_icons.go":       "adds the same icons to every caller's template list",
		"internal/mcpobs/mcpobs.go":         "observes the method; changes no result",
	}
	root := filepath.Join("..", "..")
	for _, dir := range []string{"pkg", "internal", "cmd"} {
		walkErr := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			raw, readErr := os.ReadFile(path) // #nosec G304,G122 -- the repository's own sources
			if readErr != nil {
				return fmt.Errorf("reading %s: %w", path, readErr)
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for _, name := range publicMethodNames {
				if strings.Contains(string(raw), name) && allowed[rel] == "" {
					t.Errorf("%s names %s, whose cache scope is public; decide it private in cacheScopes if the "+
						"answer depends on the caller, or list the file here with why it does not", rel, name)
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
}

// cacheableResultTypes names the structs in the SDK package that embed
// Cacheable.
func cacheableResultTypes(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return false
			}
			for _, field := range st.Fields.List {
				if id, ok := field.Type.(*ast.Ident); ok && len(field.Names) == 0 && id.Name == "Cacheable" {
					names = append(names, ts.Name.Name)
				}
			}
			return false
		})
	}
	sort.Strings(names)
	return names
}
