// Proof-of-firing tests for the relationship gates in pkg_relationship_test.go.
//
// A structural gate that cannot fail is worse than no gate: it reports green
// forever while enforcing nothing. These tests exercise the detection logic
// against known-bad inputs so a future refactor that neuters a gate (a rule
// that stops matching, a graph that stops splitting) breaks the build.
package mcp_data_platform_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// TestFirstPartyLoadErrorsAggregates proves a type-check failure in any loaded
// package is surfaced (named) rather than swallowed — the guard that stops a
// broken tree from producing a spurious cohesion violation.
func TestFirstPartyLoadErrorsAggregates(t *testing.T) {
	assert.NoError(t, firstPartyLoadErrors(nil))

	broken := &packages.Package{
		PkgPath: "github.com/txn2/mcp-data-platform/pkg/broken",
		Errors:  []packages.Error{{Msg: "undefined: Foo"}},
	}
	err := firstPartyLoadErrors([]*packages.Package{broken})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pkg/broken")
	assert.Contains(t, err.Error(), "undefined: Foo")
}

// TestNewEdgesFires proves the ratchet's diff logic reports an edge that is
// absent from the allowlist and stays silent when every edge is allowed.
// Without this, a regression that empties the edge set would make
// TestPackageImportRatchet pass vacuously.
func TestNewEdgesFires(t *testing.T) {
	allowed := parseEdgeSet("pkg/a -> pkg/b\npkg/c -> pkg/d\n")
	assert.Empty(t, newEdges([]string{"pkg/a -> pkg/b"}, allowed))
	assert.Equal(t,
		[]string{"pkg/x -> pkg/y"},
		newEdges([]string{"pkg/a -> pkg/b", "pkg/x -> pkg/y"}, allowed),
		"an edge missing from the allowlist must be reported")
}

// TestStaleEdgesFires proves the ratchet's removal half reports an allowlisted
// edge that no longer exists in the graph, and stays silent when the golden and
// the graph agree. Without this, decomposition work would leave dead entries in
// the golden that silently pre-approve reintroducing the coupling it removed.
func TestStaleEdgesFires(t *testing.T) {
	allowed := parseEdgeSet("pkg/a -> pkg/b\npkg/c -> pkg/d\n")
	assert.Empty(t, staleEdges([]string{"pkg/a -> pkg/b", "pkg/c -> pkg/d"}, allowed))
	assert.Equal(t,
		[]string{"pkg/c -> pkg/d"},
		staleEdges([]string{"pkg/a -> pkg/b"}, allowed),
		"an allowlisted edge absent from the graph must be reported")
	// An edge the graph has but the golden does not is the ADDED direction and
	// must not be reported here, or the two halves would double-report.
	assert.Empty(t, staleEdges([]string{"pkg/a -> pkg/b", "pkg/c -> pkg/d", "pkg/x -> pkg/y"}, allowed))
}

// TestFirstPartyEdgesAreFound proves firstPartyEdges actually extracts the
// internal graph from the real tree: a non-trivial edge count including a known
// edge. If isFirstParty or firstPartyEdges silently returned nothing, the
// ratchet would pass on an empty diff — this is the guard against that.
func TestFirstPartyEdgesAreFound(t *testing.T) {
	edges := firstPartyEdges(firstPartyPackages(t))
	require.Greater(t, len(edges), 50, "expected the internal import graph to be substantial")
	assert.Contains(t, edges, "cmd/mcp-data-platform -> pkg/platform",
		"the composition root must import the platform package")
}

// loadFixturePackage loads a single package by directory, used for the
// testdata/ fixtures that ./... deliberately excludes (cohesion and
// exported-surface proof-of-firing).
func loadFixturePackage(t *testing.T, dir string) *packages.Package {
	t.Helper()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir: dir,
	}
	pkgs, err := packages.Load(cfg, ".")
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	require.Empty(t, pkgs[0].Errors, "fixture package should type-check")
	return pkgs[0]
}

// TestCohesionDetectsFragmentation proves the gate flags a package built from
// two independent declaration islands.
func TestCohesionDetectsFragmentation(t *testing.T) {
	p := loadFixturePackage(t, "testdata/cohesionfixture/fragmented")
	clusters := significantClusters(p)
	require.Len(t, clusters, 2, "fragmented fixture should surface two significant clusters")
	// Each island has exactly five mutually-referencing declarations.
	assert.Len(t, clusters[0], 5)
	assert.Len(t, clusters[1], 5)
}

// TestCohesionAcceptsSharedType proves the shared-identifier edge keeps
// independent handlers over one Store a single cluster — the false-positive
// case the naive call-graph definition would wrongly flag.
func TestCohesionAcceptsSharedType(t *testing.T) {
	p := loadFixturePackage(t, "testdata/cohesionfixture/cohesive")
	clusters := significantClusters(p)
	assert.Len(t, clusters, 1, "handlers sharing one Store should be a single cluster")
}

// TestExportedSurfaceCounts proves exportedSurface counts exported package-scope
// identifiers only: each exported NAME (including both names in a grouped const
// block) counts, while methods and unexported decls do not. The fixture has six
// exported package-scope names (Widget, New, Default, Version, Alpha, Beta), one
// exported method (Do), and two unexported decls.
func TestExportedSurfaceCounts(t *testing.T) {
	p := loadFixturePackage(t, "testdata/surfacefixture")
	assert.Equal(t, 6, exportedSurface(p),
		"each exported package-scope name counts (grouped consts included); methods and unexported decls do not")
}

// TestPreviewTruncates pins the member-list preview at eight names.
func TestPreviewTruncates(t *testing.T) {
	short := []string{"A", "B", "C"}
	assert.Equal(t, "A, B, C", preview(short))

	long := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	got := preview(long)
	assert.Contains(t, got, "a, b, c, d, e, f, g, h")
	assert.Contains(t, got, "(+2)")
}
