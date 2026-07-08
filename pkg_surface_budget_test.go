// Package verify enforces project-level structural invariants.
//
// This file adds the exported-surface budget (issue #825, a follow-up to the
// relationship gates in #738). It is the public-API counterpart to the
// LOC/file size budget in package_budget_test.go: where TestPackageSizeBudget
// bounds how much a package weighs, this bounds how much of a package is
// EXPORTED. A small public surface is the idiomatic Go goal — minimal API,
// internals unexported — and unlike an LOC cap it cannot be satisfied by
// reshuffling whitespace or splitting files; the only way under the budget is
// to unexport helpers or move them into internal/.
//
// It counts top-level exported identifiers per pkg/ package (exported
// package-scope funcs, types, vars and consts — one unit per exported name, so
// each name in a grouped var/const block counts, independent of a type's fields
// or methods). Like the LOC budget it is seeded ABOVE today's largest surface so
// it lands green, and is a ceiling to ratchet DOWN, not a number to raise when a
// package bumps against it.
//
// Run: go test -run TestPackageExportedSurfaceBudget .
package mcp_data_platform_test

import (
	"fmt"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// maxExportedSurface caps exported top-level identifiers per pkg/ package. The
// largest today are pkg/middleware and pkg/portal (142) and pkg/platform (140);
// this ceiling sits just above them so the gate is green, and ratchets down as
// those packages shrink their public API (unexport helpers, move detail into
// internal/ or unexported types).
const maxExportedSurface = 150

// exportedSurface counts the exported identifiers in p's package scope: the
// top-level funcs, types, vars and consts a consumer can name. Methods and
// struct fields live in a type's scope, not the package scope, so they are not
// counted. The metric is one unit per exported name — each name in a grouped
// var/const block is a separately-referenceable API element and counts.
func exportedSurface(p *packages.Package) int {
	scope := p.Types.Scope()
	n := 0
	for _, name := range scope.Names() {
		if token.IsExported(name) {
			n++
		}
	}
	return n
}

// TestPackageExportedSurfaceBudget fails when any package under pkg/ exports
// more than maxExportedSurface top-level identifiers.
//
// This is the public-API counterpart to TestPackageSizeBudget: that bounds a
// package's size, this bounds its exported surface. If it fails, shrink the
// package's public API — unexport helpers only used within the module, move
// implementation detail behind interfaces or into internal/ — rather than
// raising the budget (that defeats the gate). See CONTRIBUTING.md, "Structural
// maintainability gates".
func TestPackageExportedSurfaceBudget(t *testing.T) {
	pkgs := firstPartyPackages(t)

	var violations []string
	counted := 0
	for _, p := range pkgs {
		rel := relPath(p.PkgPath)
		if !strings.HasPrefix(rel, "pkg/") {
			continue
		}
		counted++
		if n := exportedSurface(p); n > maxExportedSurface {
			violations = append(violations, fmt.Sprintf(
				"%s: %d exported identifiers exceeds budget of %d (shrink the public API; do not raise the budget)",
				rel, n, maxExportedSurface))
		}
	}
	require.NotZero(t, counted, "should find packages under pkg/")
	sort.Strings(violations)

	require.Empty(t, violations,
		"exported-surface budget exceeded:\n  %s", strings.Join(violations, "\n  "))
}
