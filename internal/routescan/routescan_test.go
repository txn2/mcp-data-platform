package routescan

import (
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeTree lays a fixture module out under a temp dir: path -> source.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(src), 0o600))
	}
	return root
}

// TestScan_FoldsEveryShapeTheTreeUses covers the registration shapes the
// platform writes: a literal method pattern, a pattern assembled from a
// package constant declared in a sibling file, a registrar whose mount
// prefix is a parameter filled at two call sites, a subtree mount built
// from a parameter, a pattern forwarded through a local helper, a
// cross-package constant used as a mount, and a test file, which is skipped.
func TestScan_FoldsEveryShapeTheTreeUses(t *testing.T) {
	root := writeTree(t, map[string]string{
		"pkg/admin/consts.go": `package admin

const base = "/api/v1/admin"
const docs = base + "/docs"
`,
		"pkg/admin/handler.go": `package admin

import "net/http"

func register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/users/{id}", nil)
	mux.Handle(docs+"/doc.json", nil)
	mux.Handle(base+"/audit/", nil)
	mux.HandleFunc("/healthz", nil)
	route("DELETE /api/v1/admin/users/{id}", mux)
	mux.Handle(other.PathPrefix, nil)
	if strings.HasPrefix(path, "/api/v1/admin/public/") {
		return
	}
}

func route(pattern string, mux *http.ServeMux) {
	mux.HandleFunc(pattern, nil)
}
`,
		"pkg/admin/handler_test.go": `package admin

import "net/http"

func registerForTest(mux *http.ServeMux) {
	mux.HandleFunc("GET /never/served", nil)
}
`,
		"internal/attach/attach.go": `package attach

import "net/http"

func Register(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("GET "+prefix+"/attachments", nil)
	mux.Handle(prefix+"/", nil)
}
`,
		"internal/httpserver/mounts.go": `package httpserver

import "net/http"

func mount(mux *http.ServeMux) {
	attach.Register(mux, "/api/v1/admin/prompts/{id}")
	attach.Register(mux, "/api/v1/portal/prompts/{id}")
	mux.HandleFunc("GET "+notAConst, nil)
}
`,
	})
	res, err := Scan(filepath.Join(root, "pkg"), filepath.Join(root, "internal"))
	require.NoError(t, err)

	want := []string{
		"/api/v1/admin/audit/",
		"/api/v1/admin/docs/doc.json",
		"/api/v1/admin/prompts/{id}/",
		"/api/v1/portal/prompts/{id}/",
		"/healthz",
		"DELETE /api/v1/admin/users/{id}",
		"GET /api/v1/admin/prompts/{id}/attachments",
		"GET /api/v1/admin/users/{id}",
		"GET /api/v1/portal/prompts/{id}/attachments",
	}
	require.Equal(t, want, res.Patterns())
	for _, rt := range res.Routes {
		require.Contains(t, rt.Pos, ".go:", "a route names where it is registered")
	}
	require.Len(t, res.Unresolved, 1, "the pattern built from an unknown value is reported, not skipped")
	require.Contains(t, res.Unresolved[0], "mounts.go")
}

func TestScan_ARegistrarWithNoCallSiteIsReadAtTheAdminDefault(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/scripts/scripts.go": `package scripts

import "net/http"

func Register(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("GET "+prefix+"/scripts", nil)
}
`,
	})
	res, err := Scan(filepath.Join(root, "internal"))
	require.NoError(t, err)
	require.Equal(t, []string{"GET " + AdminPathPrefixDefault + "/scripts"}, res.Patterns())
	require.Empty(t, res.Unresolved)
}

func TestScan_ParseErrorIsReturned(t *testing.T) {
	root := writeTree(t, map[string]string{"pkg/bad/bad.go": "package bad\n\nfunc {"})
	_, err := Scan(filepath.Join(root, "pkg"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing")
}

func TestScan_MissingRootIsReturned(t *testing.T) {
	_, err := Scan(filepath.Join(t.TempDir(), "nowhere"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "walking")
}

func TestIsRoutePattern(t *testing.T) {
	require.True(t, isRoutePattern("/x"))
	require.True(t, isRoutePattern("GET /x"))
	require.False(t, isRoutePattern("SELECT 1"))
	require.False(t, isRoutePattern("not a route"))
	require.True(t, isMethodPattern("DELETE /x/{id}"))
	require.False(t, isMethodPattern("/x"))
	require.False(t, isMethodPattern("BREW /x"), "an invented method is a string, not a route")
}

// TestBindings_IsTheCrossProductOfParameterValues: a registrar mounted under
// two prefixes with a second two-valued parameter yields four bindings, each
// carrying the package constants.
func TestBindings_IsTheCrossProductOfParameterValues(t *testing.T) {
	consts := map[string]string{"c": "v"}
	out := bindings(consts, map[string][]string{"a": {"1", "2"}, "b": {"x", "y"}})
	require.Len(t, out, 4)
	seen := map[string]bool{}
	for _, m := range out {
		require.Equal(t, "v", m["c"])
		seen[m["a"]+m["b"]] = true
	}
	require.Len(t, seen, 4)
	require.Equal(t, []map[string]string{consts}, bindings(consts, nil), "no parameters: the constants alone")
}

func TestStaticString_FoldsOnlyWhatIsFixed(t *testing.T) {
	root := writeTree(t, map[string]string{"pkg/s/s.go": `package s

const a = "/a"
const b = a + "/b"
const c = (b) + "/c"
var d = 7
var e = a + f()
`})
	fset := token.NewFileSet()
	byDir, err := parseTree(fset, []string{filepath.Join(root, "pkg")})
	require.NoError(t, err)
	require.Len(t, byDir, 1)
	for _, files := range byDir {
		consts := collectStringConsts(files)
		require.Equal(t, map[string]string{"a": "/a", "b": "/a/b", "c": "/a/b/c"}, consts,
			"a number and a call do not fold; parentheses and chained constants do")
	}
}
