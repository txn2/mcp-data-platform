// This file adds the gate that keeps the reference scheme in one package
// (issue #2776).
//
// A deployment names the scheme its references are written under
// (server.reference_scheme), so the mcp: prefix may be written in one place:
// internal/refscheme, which reads a reference under either scheme and writes
// it under the configured one. A second copy of the prefix in a parser is how
// a renamed scheme stops being recognized somewhere (the gap
// internal/portal/contentrefs documented for resource URIs before #2776).
//
// Run: go test -run TestReferencePrefixHasOneOwner .
package structure_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// prefixLiteral is a string literal that is a reference prefix and nothing
// else: "mcp:", "mcp:asset:", "mcp://". An example reference in a description
// or a struct tag carries more, and is written out under the deployment's
// scheme at the boundary.
var prefixLiteral = regexp.MustCompile(`^mcp:(?:[a-z_]+:|//)?$`)

// TestReferencePrefixHasOneOwner fails when a non-test Go file outside
// internal/refscheme holds a reference prefix as a literal. The remedy is
// refscheme's constant for the kind (refscheme.Asset, refscheme.Prefix, ...).
func TestReferencePrefixHasOneOwner(t *testing.T) {
	root := moduleRoot(t)
	owner := filepath.Join("internal", "refscheme")
	var offenders []string
	for _, tree := range []string{"pkg", "internal", "cmd"} {
		err := filepath.Walk(filepath.Join(root, tree), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return fmt.Errorf("resolving %s: %w", path, relErr)
			}
			if strings.HasPrefix(rel, owner) {
				return nil
			}
			fset := token.NewFileSet()
			f, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return fmt.Errorf("parsing %s: %w", rel, parseErr)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if v, err := strconv.Unquote(lit.Value); err == nil && prefixLiteral.MatchString(v) {
					offenders = append(offenders, fmt.Sprintf("%s:%d %s", rel, fset.Position(lit.Pos()).Line, lit.Value))
				}
				return true
			})
			return nil
		})
		require.NoError(t, err)
	}
	require.Empty(t, offenders, "a reference prefix is written only in internal/refscheme (#2776); use its constant:\n  %s", strings.Join(offenders, "\n  "))
}
