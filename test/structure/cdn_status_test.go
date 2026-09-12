package structure_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// cdnReplacedStatusExempt names the production files allowed to answer 502 or
// 504, each with the reason. Every other route the platform serves is reached
// by a browser through whatever sits in front of the deployment, and a CDN
// replaces the body of an origin 502 or 504 with its own: the portal received
// "error code: 502" where the platform had written the sentence saying what
// failed (#1704). A relayed upstream failure answers 503, whose body passes
// through.
//
// An entry is removed when its file stops answering either status; the gate
// fails on a stale one.
var cdnReplacedStatusExempt = map[string]string{
	"internal/httpserver/gatewayhttp/handler.go": "the REST shim for machine callers (POST /api/v1/gateway/...) " +
		"documents 502 for an unreachable upstream and 504 for a timed-out one in docs/server/api-gateway.md, " +
		"and its callers route on that status",
	"internal/platform/utilhandler/fetch.go": "POST /util/fetch is served on the built-in util connection " +
		"and reached through api_invoke_endpoint and api_export, so its status is read by the gateway " +
		"toolkit in-process and never crosses a CDN",
}

// cdnReplacedStatusSelectors are the net/http names for the two statuses.
var cdnReplacedStatusSelectors = map[string]bool{
	"StatusBadGateway":     true,
	"StatusGatewayTimeout": true,
}

// TestNoRouteAnswersAStatusACDNReplaces fails on a production file under pkg/,
// internal/ or cmd/ that answers 502 or 504, by name or as a literal, unless
// the file is exempt for a stated reason.
func TestNoRouteAnswersAStatusACDNReplaces(t *testing.T) {
	root := moduleRoot(t)
	found := map[string][]string{}
	for _, dir := range []string{"pkg", "internal", "cmd"} {
		for file, sites := range cdnReplacedStatusSites(t, filepath.Join(root, dir)) {
			rel, err := filepath.Rel(root, file)
			if err != nil {
				t.Fatalf("relative path for %s: %v", file, err)
			}
			found[filepath.ToSlash(rel)] = sites
		}
	}
	if len(found) == 0 {
		t.Fatal("no file answers 502 or 504, including the exempt ones; the scan is not reading the tree")
	}

	var offenders []string
	for file, sites := range found {
		if _, ok := cdnReplacedStatusExempt[file]; !ok {
			offenders = append(offenders, file+": "+strings.Join(sites, ", "))
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("a route answers 502 or 504, whose body a CDN in front of the deployment replaces (#1704). "+
			"Answer a relayed upstream failure with http.StatusServiceUnavailable, or, for a route no browser "+
			"calls, add the file to cdnReplacedStatusExempt with the reason:\n  %s", strings.Join(offenders, "\n  "))
	}

	var stale []string
	for file := range cdnReplacedStatusExempt {
		if _, ok := found[file]; !ok {
			stale = append(stale, file)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("exempt files that no longer answer 502 or 504; remove them from cdnReplacedStatusExempt: %v", stale)
	}
}

// TestCDNStatusGateFires is the control: a file answering 502 by name and one
// answering 504 as a literal are both found, and a test file is not read.
func TestCDNStatusGateFires(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write("named.go", "package p\n\nimport \"net/http\"\n\nfunc f(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) }\n")
	write("literal.go", "package p\n\nimport \"net/http\"\n\nfunc g(w http.ResponseWriter) { w.WriteHeader(504) }\n")
	write("clean.go", "package p\n\nimport \"net/http\"\n\nfunc h(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }\n")
	write("named_test.go", "package p\n\nimport \"net/http\"\n\nvar want = http.StatusGatewayTimeout\n")

	sites := cdnReplacedStatusSites(t, dir)
	if len(sites) != 2 {
		t.Fatalf("found %v; want named.go and literal.go only", sites)
	}
	for _, name := range []string{"named.go", "literal.go"} {
		if _, ok := sites[filepath.Join(dir, name)]; !ok {
			t.Errorf("%s answers a replaced status and was not found: %v", name, sites)
		}
	}
}

// cdnReplacedStatusSites parses every non-test Go file under dir and returns,
// per file, the positions that name StatusBadGateway or StatusGatewayTimeout
// or write the integer 502 or 504.
func cdnReplacedStatusSites(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return fmt.Errorf("parsing %s: %w", path, perr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.SelectorExpr:
				if cdnReplacedStatusSelectors[v.Sel.Name] {
					out[path] = append(out[path], fset.Position(v.Pos()).String())
				}
			case *ast.BasicLit:
				if v.Kind == token.INT && (v.Value == "502" || v.Value == "504") {
					out[path] = append(out[path], fset.Position(v.Pos()).String())
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scanning %s: %v", dir, err)
	}
	return out
}
