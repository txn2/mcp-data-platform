// Package verify enforces project-level structural invariants.
//
// This file adds the relationship-based structural gates (issue #738) that
// complement the volume-based TestPackageSizeBudget (#594). Size measures how
// much text a package holds; these gates measure the STRUCTURE of the
// relationships between and within packages, which is what actually determines
// whether a decomposition is good design or mechanical shattering.
//
//	TestPackageImportRatchet — dependency direction (coverage half). Freezes the
//	  entire first-party import graph in a seeded golden file and fails on ANY
//	  new edge, so new coupling between two internal packages is a deliberate,
//	  reviewed act rather than an accident. The intent half — the hard
//	  architectural invariants (nothing imports cmd, pkg/toolkit is a dependency
//	  root, providers do not depend up, admin/toolkits layering) — is enforced
//	  declaratively by depguard in .golangci.yml, the mechanism this project
//	  already uses for import boundaries.
//
//	TestPackageCohesion — internal cohesion. A package whose declarations split
//	  into two or more independently-cohesive islands is two packages wearing one
//	  import path; the size budget cannot see this and is in fact gamed by it
//	  (shatter a package to pass the LOC cap). This gate builds each package's
//	  declaration reference graph and fails when it holds more than one
//	  SIGNIFICANT cluster.
//
// Both are seeded to be green on the current tree and are meant to be ratcheted
// DOWN in follow-ups (shrink the ratchet golden as coupling is removed; empty
// the cohesion allowlist as packages are decomposed), never loosened to
// accommodate a regression. See CONTRIBUTING.md, "Structural maintainability
// gates".
//
// Run: go test -run 'TestPackageImportRatchet|TestPackageCohesion' .
package mcp_data_platform_test

import (
	"flag"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// modulePath is this module's import-path root. Package paths are reported
// module-relative (this prefix stripped) so rules and messages read as
// "pkg/toolkits/search", not the full URL.
const modulePath = "github.com/txn2/mcp-data-platform"

// updateImports rewrites the import-ratchet golden file to the current graph
// instead of asserting against it (go test -run TestPackageImportRatchet -args -update-imports).
var updateImports = flag.Bool("update-imports", false,
	"rewrite testdata/allowed_internal_imports.txt from the current import graph")

// ---------------------------------------------------------------------------
// Shared package loader
// ---------------------------------------------------------------------------

var (
	firstPartyOnce    sync.Once
	firstPartyPkgs    []*packages.Package
	errFirstPartyLoad error
)

// firstPartyPackages loads every first-party package (pkg/, cmd/, internal/)
// once per test binary with full syntax and type information. The load is
// memoised because both gates need it and a typed load of the tree is the most
// expensive step here.
func firstPartyPackages(t *testing.T) []*packages.Package {
	t.Helper()
	firstPartyOnce.Do(func() {
		// No NeedDeps: neither gate reads a dependency's syntax or type info
		// (the ratchet reads only import-path keys; cohesion reads only each
		// first-party package's own TypesInfo), and NeedDeps would type-check
		// the whole transitive tree on every run.
		cfg := &packages.Config{
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
				packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
			Tests: false,
			Dir:   ".",
		}
		pkgs, err := packages.Load(cfg, "./...")
		if err != nil {
			errFirstPartyLoad = err
			return
		}
		var firstParty []*packages.Package
		for _, p := range pkgs {
			if isFirstParty(p.PkgPath) && len(p.Syntax) > 0 {
				firstParty = append(firstParty, p)
			}
		}
		errFirstPartyLoad = firstPartyLoadErrors(firstParty)
		firstPartyPkgs = firstParty
	})
	require.NoError(t, errFirstPartyLoad, "loading first-party packages")
	require.NotEmpty(t, firstPartyPkgs, "should load first-party packages")
	return firstPartyPkgs
}

// firstPartyLoadErrors returns a combined error if any first-party package
// failed to load or type-check. packages.Load reports per-package errors in
// p.Errors rather than the top-level error, so without this check a package
// with a transient type error would be analyzed with partial TypesInfo — the
// cohesion gate would then drop the identifier edges it never resolved and
// report a spurious violation that masks the real compile error.
func firstPartyLoadErrors(pkgs []*packages.Package) error {
	var msgs []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			msgs = append(msgs, p.PkgPath+": "+e.Error())
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	sort.Strings(msgs)
	return fmt.Errorf("first-party packages failed to type-check (fix these before the structural gates can run):\n  %s",
		strings.Join(msgs, "\n  "))
}

// isFirstParty reports whether importPath is a package within this module.
// The "/" boundary matters: a bare HasPrefix(modulePath) would also match a
// sibling module such as "<modulePath>-extras".
func isFirstParty(importPath string) bool {
	return strings.HasPrefix(importPath, modulePath+"/")
}

// relPath strips the module prefix, yielding e.g. "pkg/toolkits/search".
func relPath(importPath string) string {
	return strings.TrimPrefix(importPath, modulePath+"/")
}

// firstPartyEdges returns the sorted, de-duplicated set of internal import
// edges as "from -> to" strings (module-relative), across all loaded packages.
func firstPartyEdges(pkgs []*packages.Package) []string {
	seen := map[string]bool{}
	for _, p := range pkgs {
		from := relPath(p.PkgPath)
		for impPath := range p.Imports {
			if !isFirstParty(impPath) {
				continue
			}
			seen[from+" -> "+relPath(impPath)] = true
		}
	}
	edges := make([]string, 0, len(seen))
	for e := range seen {
		edges = append(edges, e)
	}
	sort.Strings(edges)
	return edges
}

// ---------------------------------------------------------------------------
// Import ratchet (any new first-party edge must be acknowledged)
// ---------------------------------------------------------------------------
//
// The DIRECTION invariants — the hard architectural rules (nothing imports cmd,
// pkg/toolkit is a root, providers do not depend up, admin/toolkits layering) —
// are enforced declaratively by depguard in .golangci.yml, the mechanism this
// project already uses for import boundaries. The ratchet below is the
// complementary coverage depguard cannot express: it freezes the ENTIRE
// first-party edge set, so every new dependency between two internal packages —
// even a same-direction one the layer rules permit — is a deliberate, reviewed
// act rather than an accident.

// allowedImportsPath is the golden set of first-party import edges. It is
// seeded from the current graph and ratcheted DOWN as coupling is removed.
var allowedImportsPath = filepath.Join("testdata", "allowed_internal_imports.txt")

// parseEdgeSet parses the golden file's edges (one "from -> to" per line,
// blanks ignored) into a set.
func parseEdgeSet(raw string) map[string]bool {
	set := map[string]bool{}
	for line := range strings.SplitSeq(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	return set
}

// newEdges returns, in order, the edges in current that are not in allowed.
func newEdges(current []string, allowed map[string]bool) []string {
	var added []string
	for _, edge := range current {
		if !allowed[edge] {
			added = append(added, edge)
		}
	}
	return added
}

// staleEdges returns, sorted, the edges in allowed that no longer exist in
// current. These are entries the golden file still authorizes for couplings
// that have since been removed: a later change could reintroduce one without
// the ratchet noticing, because the stale entry pre-approves it.
func staleEdges(current []string, allowed map[string]bool) []string {
	present := make(map[string]bool, len(current))
	for _, edge := range current {
		present[edge] = true
	}
	var removed []string
	for edge := range allowed {
		if !present[edge] {
			removed = append(removed, edge)
		}
	}
	sort.Strings(removed)
	return removed
}

// TestPackageImportRatchet fails when the golden file and the first-party
// import graph disagree in EITHER direction. It is the un-gameable backstop to
// the layering rules: it catches ALL new coupling, including same-direction
// edges the deny rules permit, so that adding a dependency between two internal
// packages is a deliberate, reviewed act.
//
// The removal half matters as much as the addition half (#1081). The golden is
// an allowlist, so an entry for an edge that no longer exists silently
// pre-approves reintroducing a coupling that was deliberately removed. Asserting
// equality in both directions makes the golden an exact mirror of the graph by
// construction rather than by discipline — which is what the decomposition work
// in #1076 through #1080 needs, since removing coupling is precisely what it does.
//
// A genuinely intended change on either side is admitted by regenerating the
// golden, which rewrites it wholesale from the current graph:
//
//	go test -run TestPackageImportRatchet . -args -update-imports
//
// Do that only with a one-line justification in the PR; the diff of this file
// is the review surface for changes to internal coupling.
func TestPackageImportRatchet(t *testing.T) {
	pkgs := firstPartyPackages(t)
	current := firstPartyEdges(pkgs)
	// Guard against a silent-empty graph (a broken isFirstParty/firstPartyEdges
	// would make the ratchet pass vacuously). See TestFirstPartyEdgesAreFound.
	require.NotEmpty(t, current, "first-party import graph is empty — the ratchet cannot bite")

	if *updateImports {
		require.NoError(t, os.MkdirAll(filepath.Dir(allowedImportsPath), 0o750))
		require.NoError(t, os.WriteFile(allowedImportsPath,
			[]byte(strings.Join(current, "\n")+"\n"), 0o600))
		t.Logf("wrote %d edges to %s", len(current), allowedImportsPath)
		return
	}

	raw, err := os.ReadFile(allowedImportsPath) //nolint:gosec // test reads project testdata
	require.NoError(t, err, "read import allowlist (regenerate with -args -update-imports)")

	allowed := parseEdgeSet(string(raw))

	added := newEdges(current, allowed)
	require.Empty(t, added,
		"new first-party import edge(s) not in the allowlist (%d). If intentional, regenerate with "+
			"`go test -run TestPackageImportRatchet . -args -update-imports` and justify the new coupling in the PR:\n  %s",
		len(added), strings.Join(added, "\n  "))

	stale := staleEdges(current, allowed)
	require.Empty(t, stale,
		"allowlisted import edge(s) no longer exist in the graph (%d). A stale entry pre-approves "+
			"reintroducing coupling that was removed; regenerate with "+
			"`go test -run TestPackageImportRatchet . -args -update-imports`:\n  %s",
		len(stale), strings.Join(stale, "\n  "))
}

// ---------------------------------------------------------------------------
// Cohesion as declaration-graph connectivity
// ---------------------------------------------------------------------------

// minSignificantCluster is the smallest connected component of package-level
// declarations treated as an independent "island". A package may hold at most
// one such island; lone leaves and tiny helper groups below this size are
// tolerated appendages, not a second responsibility. Two islands of five or
// more mutually-referencing declarations that touch nothing in the other island
// is two packages in one import path.
const minSignificantCluster = 5

// cohesionExemption records why one package is exempt from the single-cluster
// rule and what would let the exemption go. An allowlist entry that carries
// neither is indistinguishable from a permanent exemption (#1081): the reader
// cannot tell a package awaiting decomposition from one nobody ever intended to
// split. Both fields are required, and TestCohesionExemptionsAreJustified
// enforces that.
// It must open with the cluster count in the form "N clusters:", which
// TestCohesionExemptionsAreJustified checks against the count the gate actually
// measures — so a justification cannot quietly go stale as the package changes.
type cohesionExemption struct {
	// why opens with "N clusters:" and then names them.
	why string
	// exit states the decomposition that removes the entry.
	exit string
}

// exemptionClusterCountRe extracts the cluster count an exemption claims.
var exemptionClusterCountRe = regexp.MustCompile(`^(\d+) clusters:`)

// cohesionAllowlist is the set of packages (module-relative) currently exempt
// from the single-cluster rule. Every entry has two or more significant clusters
// today and is seeded so the gate is green; each is a candidate for
// decomposition into cohesive sub-packages (issue #738 follow-ups). The set is
// meant to SHRINK as those packages are split, never to grow — TestPackageCohesion
// fails on a stale entry that no longer has multiple clusters.
//
// The cluster sizes below were measured with the gate itself at the time of
// #1081; run TestPackageCohesion with the package removed from this map to see
// the current split.
func cohesionAllowlist() map[string]cohesionExemption {
	return map[string]cohesionExemption{
		"pkg/audit": {
			why:  "3 clusters: the event/writer core (59 decls), the timeseries query surface (11), and the breakdown query surface (8). The two analytics clusters share nothing with the write path.",
			exit: "extract the timeseries and breakdown query types into pkg/audit/analytics, leaving the event model and writers behind.",
		},
		"pkg/memory": {
			why:  "5 clusters: the record store (135 decls) plus four self-contained vocabularies — dimension (19), category (10), source (8) and confidence (6) — each a set of constants with its own Normalize/Validate pair.",
			exit: "move the four vocabularies into pkg/memory/vocab; the store keeps the record model.",
		},
		"pkg/toolkits/knowledge": {
			why:  "4 clusters: the toolkit itself (506 decls) plus unexported copies of the same three vocabularies pkg/memory carries — category (9), confidence (7) and source (7).",
			exit: "share one vocabulary package with pkg/memory rather than duplicating it here; the toolkit cluster is separately oversized and is covered by the LOC budget.",
		},
		"pkg/portal/knowledgepage": {
			why:  "3 clusters: the page store with its dedup and search surface (126 decls); the text-shape functions that compose, split and measure a page's indexed text (15) — IndexText, IndexChunks and the oversize signal, which the store calls into only through exported entry points its consumers use; and PageGuardsConfig with its oversize/dedup threshold resolution (8), which the store never references.",
			exit: "extract pkg/portal/knowledgepage/pagetext for the text-shape cluster, and fold the guard thresholds into the platform config seam that supplies them (or extract .../guards).",
		},
		"pkg/toolkits/apigateway/catalog": {
			why:  "3 clusters: the catalog store (71 decls), remote spec fetching with its SSRF guards (25), and spec parsing/validation (19). Fetch and parse are a pipeline the store only consumes the output of.",
			exit: "extract catalog/specfetch and catalog/specparse; the store keeps the persistence surface.",
		},
		"pkg/session/postgres": {
			why:  "2 clusters: the LISTEN/NOTIFY broadcaster (18 decls) and the session Store (15). They share the same database handle but no declarations.",
			exit: "extract the broadcaster into its own package; the Store depends on the notify channel name only.",
		},
		"pkg/tuning": {
			why:  "2 clusters: PromptManager with its file loading (9 decls) and RuleEngine with its operational-rule defaults (8). The package comment already names them as two features.",
			exit: "split into pkg/tuning/prompts and pkg/tuning/rules; this is the smallest entry here and the cheapest to retire.",
		},
	}
}

// TestPackageCohesion fails when a package's declaration reference graph holds
// more than one significant cluster (see minSignificantCluster), unless the
// package is on the seeded allowlist. The failure names the clusters so the
// remediation ("cut here") is mechanical.
//
// Edges include both direct references (A calls/uses B) and shared references
// (A and B both use package-level identifier C), the latter via C being a node
// every user attaches to. This is why independent handlers over one shared
// Store read as cohesive rather than as false-positive islands.
//
// Known blind spot: the shared-identifier edge also produces a false NEGATIVE —
// two unrelated islands that both touch a single incidental symbol (a shared
// logger var, a sentinel error) are joined through it and pass. The gate
// reliably catches fragmentation where islands share nothing; it is a heuristic
// backstop, not a proof (the exact direction gates are the un-gameable half).
// See CONTRIBUTING.md, gate 5 "Known blind spot".
//
// The blind spot was measured and left in place (#1080). The candidate
// refinement — discount a connection that survives only through one shared
// declaration, i.e. flag a package whose graph splits into two significant
// clusters when a single cut vertex is removed — flags 50 of the 158 first-party
// packages that are green today, among them pkg/platform, pkg/auth,
// pkg/middleware and most toolkits. In nearly every case the cut vertex is the
// package's central type (Handler, Toolkit, Store, Config) and the "split" is
// its own methods separating from the free functions around it, which is the
// cohesive shape the shared-identifier edge was added to admit. A rule with that
// false-positive rate would be turned off rather than obeyed, so the gate keeps
// the heuristic and this comment records why. Weighting edges by referenced-symbol
// kind (a shared named type counting for more than an incidental value) remains
// the open avenue; it was not attempted here.
func TestPackageCohesion(t *testing.T) {
	pkgs := firstPartyPackages(t)
	allow := cohesionAllowlist()

	var violations []string
	staleAllow := map[string]bool{}
	for k := range allow {
		staleAllow[k] = true
	}

	for _, p := range pkgs {
		rel := relPath(p.PkgPath)
		clusters := significantClusters(p)
		if len(clusters) > 1 {
			if _, exempt := allow[rel]; exempt {
				delete(staleAllow, rel)
				continue
			}
			violations = append(violations, describeClusters(rel, clusters))
		}
	}
	sort.Strings(violations)

	require.Empty(t, violations,
		"package cohesion violated — package(s) with %d+ independent declaration clusters (%d):\n%s",
		2, len(violations), strings.Join(violations, "\n"))

	// Keep the allowlist honest: a package that has been decomposed must be
	// removed from the seed so the gate re-arms for it.
	stale := make([]string, 0, len(staleAllow))
	for k := range staleAllow {
		stale = append(stale, k)
	}
	sort.Strings(stale)
	require.Empty(t, stale,
		"cohesion allowlist entries are no longer multi-cluster and must be removed: %v", stale)
}

// TestCohesionExemptionsAreJustified requires every allowlist entry to carry a
// description of its clusters and the decomposition that retires it, and holds
// the description to the measurement: the cluster count it opens with must equal
// the count the gate reports for that package today.
//
// The allowlist is meant to shrink, and an entry with no stated exit condition
// has nothing to shrink towards — that is how a seeded exemption becomes
// permanent (#1081). Checking the count as well as its presence is what stops
// the justification from decaying into decoration: split one of these packages
// partway, and the entry that still claims the old shape fails here.
func TestCohesionExemptionsAreJustified(t *testing.T) {
	allow := cohesionAllowlist()

	measured := map[string]int{}
	for _, p := range firstPartyPackages(t) {
		if rel := relPath(p.PkgPath); allow[rel].why != "" {
			measured[rel] = len(significantClusters(p))
		}
	}

	for pkg, ex := range allow {
		require.NotEmpty(t, ex.exit, "%s: cohesion exemption needs an exit condition", pkg)

		m := exemptionClusterCountRe.FindStringSubmatch(ex.why)
		require.Len(t, m, 2,
			"%s: cohesion exemption must open with the cluster count, e.g. `3 clusters: ...`; got %q", pkg, ex.why)
		claimed, err := strconv.Atoi(m[1])
		require.NoError(t, err)

		got, loaded := measured[pkg]
		require.True(t, loaded, "%s: allowlisted package was not loaded — is the path still correct?", pkg)
		require.Equal(t, claimed, got,
			"%s: exemption claims %d clusters but the gate measures %d; update the justification "+
				"(or retire the entry if the package is now cohesive)", pkg, claimed, got)
	}
}

// significantClusters returns the connected components of p's declaration graph
// whose size is at least minSignificantCluster, largest first.
func significantClusters(p *packages.Package) [][]string {
	nodes, edges := buildDeclGraph(p)
	uf := newUnionFind()
	for _, n := range nodes {
		uf.add(n)
	}
	for _, e := range edges {
		uf.union(e[0], e[1])
	}
	members := map[string][]string{}
	for _, n := range nodes {
		root := uf.find(n)
		members[root] = append(members[root], n)
	}
	var clusters [][]string
	for _, m := range members {
		if len(m) >= minSignificantCluster {
			sort.Strings(m)
			clusters = append(clusters, m)
		}
	}
	sort.Slice(clusters, func(i, j int) bool { return len(clusters[i]) > len(clusters[j]) })
	return clusters
}

// describeClusters renders the offending package and a preview of each cluster.
func describeClusters(pkg string, clusters [][]string) string {
	var b strings.Builder
	b.WriteString("  " + pkg + ": " + strconv.Itoa(len(clusters)) + " significant clusters (cut between them):")
	for i, c := range clusters {
		b.WriteString("\n    cluster " + strconv.Itoa(i+1) + " (" + strconv.Itoa(len(c)) + "): " + preview(c))
	}
	return b.String()
}

// preview joins up to eight member names, marking the rest with an ellipsis.
func preview(members []string) string {
	const maxNames = 8
	if len(members) <= maxNames {
		return strings.Join(members, ", ")
	}
	return strings.Join(members[:maxNames], ", ") + ", … (+" + strconv.Itoa(len(members)-maxNames) + ")"
}

// ---------------------------------------------------------------------------
// Declaration reference graph
// ---------------------------------------------------------------------------

// buildDeclGraph returns the package-level declaration nodes of p and the
// undirected edges between them. A node is a package-scope func, method, type,
// var or const (methods are keyed "method:Recv.Name"). An edge connects a
// declaration to every package-level identifier it references; two declarations
// that both reference identifier C are therefore connected through C.
func buildDeclGraph(p *packages.Package) (nodes []string, edges [][2]string) {
	pkgObjs := packageScopeObjects(p)
	g := &declGraph{pkg: p, pkgObjs: pkgObjs, nodeSet: map[string]bool{}}
	for _, file := range p.Syntax {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				g.addFunc(d)
			case *ast.GenDecl:
				g.addGen(d)
			}
		}
	}
	return g.nodes, g.edges
}

// declGraph accumulates nodes and edges while walking a package's declarations.
type declGraph struct {
	pkg     *packages.Package
	pkgObjs map[types.Object]bool
	nodes   []string
	nodeSet map[string]bool
	edges   [][2]string
}

// packageScopeObjects returns the set of objects declared in p's package scope.
func packageScopeObjects(p *packages.Package) map[types.Object]bool {
	scope := p.Types.Scope()
	objs := make(map[types.Object]bool, len(scope.Names()))
	for _, name := range scope.Names() {
		objs[scope.Lookup(name)] = true
	}
	return objs
}

func (g *declGraph) addNode(name string) {
	if !g.nodeSet[name] {
		g.nodeSet[name] = true
		g.nodes = append(g.nodes, name)
	}
}

// objID names an object as a graph node, disambiguating methods by receiver.
func objID(obj types.Object) string {
	if fn, ok := obj.(*types.Func); ok {
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			return "method:" + namedTypeName(sig.Recv().Type()) + "." + fn.Name()
		}
	}
	return obj.Name()
}

func (g *declGraph) addFunc(d *ast.FuncDecl) {
	obj := g.pkg.TypesInfo.Defs[d.Name]
	if obj == nil {
		return
	}
	self := objID(obj)
	g.addNode(self)
	if d.Recv != nil && len(d.Recv.List) > 0 {
		if recv := recvTypeName(d.Recv.List[0].Type); recv != "" {
			g.addNode(recv)
			g.edges = append(g.edges, [2]string{self, recv})
		}
	}
	g.collectRefs(d, self)
}

func (g *declGraph) addGen(d *ast.GenDecl) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			g.addNamedSpec(s.Name, s)
		case *ast.ValueSpec:
			for _, name := range s.Names {
				g.addNamedSpec(name, s)
			}
		}
	}
}

// addNamedSpec adds the node defined by name and connects it to the package-
// level identifiers referenced anywhere in spec.
func (g *declGraph) addNamedSpec(name *ast.Ident, spec ast.Node) {
	if name.Name == "_" {
		return
	}
	obj := g.pkg.TypesInfo.Defs[name]
	if obj == nil {
		return
	}
	self := obj.Name()
	g.addNode(self)
	g.collectRefs(spec, self)
}

// collectRefs connects self to every package-level object referenced in node.
func (g *declGraph) collectRefs(node ast.Node, self string) {
	ast.Inspect(node, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj := g.pkg.TypesInfo.Uses[ident]
		if obj == nil || !g.pkgObjs[obj] {
			return true
		}
		if target := objID(obj); target != self {
			g.addNode(target)
			g.edges = append(g.edges, [2]string{self, target})
		}
		return true
	})
}

// namedTypeName returns the name of a (possibly pointer/generic) named type.
func namedTypeName(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}

// recvTypeName extracts the receiver type name from a receiver type expression.
func recvTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvTypeName(t.X)
	case *ast.IndexListExpr:
		return recvTypeName(t.X)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Union-find
// ---------------------------------------------------------------------------

type unionFind struct{ parent map[string]string }

func newUnionFind() *unionFind { return &unionFind{parent: map[string]string{}} }

func (u *unionFind) add(x string) {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
	}
}

func (u *unionFind) find(x string) string {
	u.add(x)
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}
