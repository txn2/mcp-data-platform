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

// TestPackageImportRatchet fails when a first-party import edge appears that is
// not in the seeded allowlist. It is the un-gameable backstop to the layering
// rules: it catches ALL new coupling, including same-direction edges the deny
// rules permit, so that adding a dependency between two internal packages is a
// deliberate, reviewed act.
//
// A genuinely intended new edge is admitted by regenerating the golden:
//
//	go test -run TestPackageImportRatchet . -args -update-imports
//
// Do that only with a one-line justification in the PR; the diff of this file
// is the review surface for new internal coupling.
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

	added := newEdges(current, parseEdgeSet(string(raw)))
	require.Empty(t, added,
		"new first-party import edge(s) not in the allowlist (%d). If intentional, regenerate with "+
			"`go test -run TestPackageImportRatchet . -args -update-imports` and justify the new coupling in the PR:\n  %s",
		len(added), strings.Join(added, "\n  "))
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

// cohesionAllowlist is the set of packages (module-relative) currently exempt
// from the single-cluster rule. Every entry has two or more significant clusters
// today and is seeded so the gate is green; each is a candidate for
// decomposition into cohesive sub-packages (issue #738 follow-ups). The set is
// meant to SHRINK as those packages are split, never to grow — TestPackageCohesion
// fails on a stale entry that no longer has multiple clusters.
func cohesionAllowlist() map[string]bool {
	return map[string]bool{
		"pkg/audit":                       true,
		"pkg/memory":                      true,
		"pkg/toolkits/knowledge":          true,
		"pkg/portal/knowledgepage":        true,
		"pkg/toolkits/apigateway/catalog": true,
		"pkg/session/postgres":            true,
		"pkg/tuning":                      true,
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
