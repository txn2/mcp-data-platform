package observability

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// approvedLabelKeys is every metric label key an instrument in this package
// may record, with what bounds its values. A key outside this set is a new
// dimension on a series, and the test below refuses it until it is added
// here with its bound written down (#1892). The values behind a key must be
// drawn from a set the operator controls or a closed set in the code; a user
// id, a request id, a URL, a raw error or a caller-chosen name is none of
// those and belongs on a span or an audit row.
//
//nolint:gochecknoglobals // test fixture: the approved set the gate reads.
var approvedLabelKeys = map[string]string{
	"tool":              "the registered tool set, plus one value for a name nothing registers",
	"toolkit_kind":      "the toolkit kinds the platform ships",
	"persona":           "the operator's persona definitions, plus unknown",
	"status_category":   "the Status* constants in status.go",
	"source":            "how a call arrived (mcp, admin, rest, script), or a webhook source the operator configured",
	"connection":        "the operator's configured connections, plus unknown",
	"http_status_class": "2xx, 3xx, 4xx, 5xx, other",
	"operation_id":      "the OpenAPI catalog's operation ids, plus unknown",
	"method":            "the supported HTTP methods, plus unknown",
	"status_class":      "2xx, 3xx, 4xx, 5xx, other",
	"identity":          "the operator's API key names, oidc, unknown",
	"script":            "the deployment's managed scripts",
	"reason":            "the admission refusal reasons (ceiling, memory, cpu)",
	"trigger":           "manual, schedule, or an index-job trigger kind",
	"status":            "ok and the error statuses in status.go",
	"query_kind":        "the SQL verbs",
	"operation":         "the provider or toolkit operations, a closed set per adapter",
	"grant_type":        "the OAuth grant types the server implements",
	"pool":              "the registered database pools",
	"outcome":           "a closed set per instrument (reexecuted/failed, ok/error/timeout, accepted/rejected)",
	"kind":              "the registered index-job consumers",
	"result":            "a closed set per instrument (embedded/reused, created/exists, ...)",
	"state":             "the index-job queue states",
	"version":           "the builds a deployment runs (mcp_platform_build_info)",
	"commit":            "the builds a deployment runs (mcp_platform_build_info)",
	"go_version":        "the Go releases a deployment's builds use (mcp_platform_build_info)",
}

// attributeConstructors are the attribute package functions whose first
// argument is a label key.
//
//nolint:gochecknoglobals // test fixture.
var attributeConstructors = map[string]bool{
	"String": true, "Int": true, "Int64": true, "Bool": true, "Float64": true,
	"StringSlice": true, "Key": true,
}

// TestLabelKeysAreApproved reads every non-test file in this package and
// resolves the key of every attribute.* call to its string value, through the
// named constant it is written as. Each must be in approvedLabelKeys. A key
// written as a literal rather than a named constant is refused too, since the
// constants are what keep a typo from minting a label at run time.
func TestLabelKeysAreApproved(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	consts := map[string]string{}
	var calls []*ast.CallExpr
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		require.NoError(t, err)
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.ValueSpec:
				for i, id := range v.Names {
					if i < len(v.Values) {
						if lit, ok := v.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							consts[id.Name], _ = strconv.Unquote(lit.Value)
						}
					}
				}
			case *ast.CallExpr:
				if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "attribute" && attributeConstructors[sel.Sel.Name] {
						calls = append(calls, v)
					}
				}
			}
			return true
		})
	}
	require.NotEmpty(t, calls, "the package records attributes; the walk found none")

	seen := map[string]bool{}
	for _, c := range calls {
		pos := fset.Position(c.Pos())
		require.NotEmpty(t, c.Args, "%s: attribute call without a key", pos)
		id, ok := c.Args[0].(*ast.Ident)
		require.True(t, ok, "%s: the label key must be a named constant, not %T", pos, c.Args[0])
		key, ok := consts[id.Name]
		require.True(t, ok, "%s: %s is not a string constant in this package", pos, id.Name)
		if resourceAttributeKeys[key] {
			// A resource attribute names the process, not a series (resource.go).
			continue
		}
		_, approved := approvedLabelKeys[key]
		require.True(t, approved, "%s: label key %q (%s) is not in approvedLabelKeys; add it with the set that bounds its values, or carry the value on a span or an audit row instead", pos, key, id.Name)
		seen[key] = true
	}
	for key := range approvedLabelKeys {
		require.True(t, seen[key], "approved label key %q is recorded by no instrument; remove it from the approved set", key)
	}
}
