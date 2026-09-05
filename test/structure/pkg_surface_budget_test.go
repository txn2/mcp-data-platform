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
// Scope: pkg/ only, and deliberately so (#1079). The size budget in
// package_budget_test.go covers internal/ as well, because a package that is
// too large to reason about is too large wherever it lives. This one does not,
// because it measures PUBLIC API: under pkg/ an exported name is a semver
// commitment to consumers outside the module, while under internal/ it is
// merely module-visible and costs a consumer nothing. The measurement supports
// that reading — the largest internal/ surface is 11 identifiers
// (internal/platform/toolkitcfg and internal/platform/promptlayer), against a
// ceiling of 150 here, so applying this gate to internal/ would either be
// decoration at 150 or a constant tripwire at 11. Revisit only if an internal/
// package starts exporting a public-API-sized surface, which the size budget
// would flag first.
//
// Run: go test -run TestPackageExportedSurfaceBudget .
package structure_test

import (
	"fmt"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// maxExportedSurface caps exported top-level identifiers per pkg/ package.
// Measured with this gate at the time of #1077, the largest are pkg/middleware
// (138), pkg/platform (137) and pkg/portal (129). Reproduce with:
//
//	go test -count=1 -run TestPackageExportedSurfaceBudget -v .
//
// The ceiling stays at 150 rather than ratcheting to just above 138: #1077's
// point was to buy real headroom, so the next exported identifier lands as
// design feedback in review rather than as a build failure on an unrelated
// feature. It ratchets down, never up (unexport helpers, move detail into
// internal/ or unexported types).
//
// #1076 relocated 22 implementation-detail packages out of pkg/ and moved
// these three counts by zero, which is worth recording: this gate counts
// package-scope names PER PACKAGE, non-recursively, so moving a subpackage
// under internal/ does not shrink its parent — the relocated package still
// needs whatever the parent exports to it. Shrinking a surface means
// unexporting or deleting names in the package itself.
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
	var measured []string
	for _, p := range pkgs {
		rel := relPath(p.PkgPath)
		if !strings.HasPrefix(rel, "pkg/") {
			continue
		}
		n := exportedSurface(p)
		measured = append(measured, fmt.Sprintf("%4d %s", n, rel))
		if n > maxExportedSurface {
			violations = append(violations, fmt.Sprintf(
				"%s: %d exported identifiers exceeds budget of %d (shrink the public API; do not raise the budget)",
				rel, n, maxExportedSurface))
		}
	}
	require.NotEmpty(t, measured, "should find packages under pkg/")
	logLargestSurfaces(t, measured)
	sort.Strings(violations)

	require.Empty(t, violations,
		"exported-surface budget exceeded:\n  %s", strings.Join(violations, "\n  "))
}

// logLargestSurfaces reports the packages closest to the ceiling under -v, so
// the numbers quoted in maxExportedSurface's doc comment are reproducible from
// the gate itself rather than from a one-off script. Entries are formatted with
// the count right-aligned in a fixed width, which is what makes a lexicographic
// sort order them numerically.
func logLargestSurfaces(t *testing.T, measured []string) {
	t.Helper()
	sort.Sort(sort.Reverse(sort.StringSlice(measured)))
	const show = 5
	if len(measured) > show {
		measured = measured[:show]
	}
	t.Logf("largest exported surfaces under pkg/ (ceiling %d):\n  %s",
		maxExportedSurface, strings.Join(measured, "\n  "))
}
