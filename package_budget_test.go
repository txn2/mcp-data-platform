// Package verify enforces project-level structural invariants.
//
// This file adds the package-size budget gate (issue #594): a structural
// backstop that the per-function complexity linters cannot provide. Every
// gocyclo/gocognit/revive rule evaluates code INSIDE a single function, so a
// god-package assembled from a hundred small, low-complexity functions passes
// all of them. This test caps the size of a package as a whole, forcing
// decomposition before a package grows too large to reason about in isolation.
//
// The budgets are deliberately set ABOVE today's largest package so the gate
// is green on the current tree and purely additive. They are ceilings to
// ratchet DOWN over time (in separate follow-up PRs), not numbers to raise
// when a package bumps against them: hitting the budget is the signal to
// decompose the package, not to relax the gate.
//
// Generated files (those carrying a "Code generated ... DO NOT EDIT." marker)
// are excluded from the count, so an embedded spec like internal/apidocs does
// not masquerade as hand-written code (#594, item 4).
//
// Run: go test -run TestPackageSizeBudget .
package mcp_data_platform_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// maxPackageLOC caps non-generated, non-test lines per package under
	// pkg/. After the pkcestore extraction (#636) the largest packages are
	// pkg/admin (~11.7k LOC) and pkg/platform (~11.5k); this ceiling sits
	// just above them so the gate stays green, and ratchets down further as
	// those packages are decomposed.
	maxPackageLOC = 11800

	// maxPackageFiles caps non-generated, non-test .go files per package
	// under pkg/. The largest today (pkg/middleware) holds 27 files; this
	// ceiling leaves headroom and pressures decomposition.
	maxPackageFiles = 35
)

// generatedMarkerRe matches the canonical "generated code" line that Go
// tooling (swag, mockgen, stringer, protoc-gen-go, etc.) emits. A file
// carrying this marker is excluded from the budget.
var generatedMarkerRe = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// packageSize accumulates the non-generated, non-test footprint of one package.
type packageSize struct {
	loc   int
	files int
}

// TestPackageSizeBudget fails when any package under pkg/ exceeds the LOC or
// file-count budget, counting only hand-written, non-test source.
//
// This is the structural counterpart to the per-function complexity gates:
// those bound the inside of a function, this bounds the size of a package.
// If it fails, decompose the offending package into cohesive sub-packages —
// do not raise the budget (that defeats the gate). See CONTRIBUTING.md,
// "Structural maintainability gates".
func TestPackageSizeBudget(t *testing.T) {
	projectRoot, err := filepath.Abs(".")
	require.NoError(t, err)

	pkgDir := filepath.Join(projectRoot, "pkg")
	sizes := map[string]*packageSize{}

	walkErr := filepath.Walk(pkgDir, func(path string, info os.FileInfo, fErr error) error {
		if fErr != nil {
			return fErr
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		generated, loc, countErr := countGoFile(path)
		if countErr != nil {
			return countErr
		}
		if generated {
			return nil
		}
		dir := filepath.Dir(path)
		rel, relErr := filepath.Rel(projectRoot, dir)
		if relErr != nil {
			return fmt.Errorf("computing relative path for %s: %w", dir, relErr)
		}
		ps, ok := sizes[rel]
		if !ok {
			ps = &packageSize{}
			sizes[rel] = ps
		}
		ps.loc += loc
		ps.files++
		return nil
	})
	require.NoError(t, walkErr)
	require.NotEmpty(t, sizes, "should find packages under pkg/")

	var violations []string
	for pkg, ps := range sizes {
		if ps.loc > maxPackageLOC {
			violations = append(violations, fmt.Sprintf(
				"%s: %d LOC exceeds budget of %d (decompose the package; do not raise the budget)",
				pkg, ps.loc, maxPackageLOC))
		}
		if ps.files > maxPackageFiles {
			violations = append(violations, fmt.Sprintf(
				"%s: %d files exceeds budget of %d (decompose the package; do not raise the budget)",
				pkg, ps.files, maxPackageFiles))
		}
	}
	sort.Strings(violations)

	require.Empty(t, violations,
		"package size budget exceeded:\n  %s", strings.Join(violations, "\n  "))
}

// TestCountGoFile exercises generated-marker detection and line counting
// directly. No package under pkg/ currently carries a generated marker, so
// this is the unit that proves generated files are excluded from the budget
// (#594, item 4) and that line counting is correct.
func TestCountGoFile(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		wantGenerated bool
		wantLOC       int
	}{
		{
			name:          "hand-written file is counted",
			content:       "package x\n\nfunc f() {}\n",
			wantGenerated: false,
			wantLOC:       3,
		},
		{
			name:          "swag-style generated marker is detected",
			content:       "// Code generated by swaggo/swag. DO NOT EDIT.\npackage docs\n",
			wantGenerated: true,
			wantLOC:       2,
		},
		{
			name:          "marker after a build constraint is still detected",
			content:       "//go:build ignore\n\n// Code generated by mockgen. DO NOT EDIT.\npackage m\n",
			wantGenerated: true,
			wantLOC:       4,
		},
		{
			name:          "a comment that merely mentions generated code is not a marker",
			content:       "package x\n// this is not Code generated by anything\n",
			wantGenerated: false,
			wantLOC:       2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.go")
			require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o600))

			generated, loc, err := countGoFile(path)
			require.NoError(t, err)
			require.Equal(t, tt.wantGenerated, generated)
			require.Equal(t, tt.wantLOC, loc)
		})
	}
}

// TestCountGoFile_MissingFile covers the open-error path.
func TestCountGoFile_MissingFile(t *testing.T) {
	_, _, err := countGoFile(filepath.Join(t.TempDir(), "does-not-exist.go"))
	require.Error(t, err)
}

// countGoFile reports whether path is a generated file and, if not, how many
// lines it contains. Generated files are detected by the canonical
// "Code generated ... DO NOT EDIT." marker, which by convention appears before
// the package clause; scanning the whole file is cheap and avoids missing a
// marker placed after a build-constraint or license header.
func countGoFile(path string) (generated bool, loc int, err error) {
	f, err := os.Open(path) //nolint:gosec // test reads project source files
	if err != nil {
		return false, 0, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if generatedMarkerRe.MatchString(strings.TrimSpace(line)) {
			generated = true
		}
		loc++
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return false, 0, fmt.Errorf("scanning %s: %w", path, scanErr)
	}
	return generated, loc, nil
}
