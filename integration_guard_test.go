package mcp_data_platform_test

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This guard closes the "integration test that never runs" gap (#880 finding
// 3.1). CI executes integration-tagged tests only through `make test-realdb`,
// which runs `go test -tags=integration -run 'RealDB' ./...`. A test whose
// name does not contain "RealDB" therefore runs nowhere automated and rots
// silently. TestIntegrationTestsAreExecuted fails when such an orphan exists,
// turning silent rot into a compile-adjacent failure.

// allowlistedIntegrationDirs maps a repo-relative directory prefix to the
// justification for why every integration-tagged test under it may skip the
// RealDB-executed pattern. These suites need external systems CI does not
// provide and stay manual.
var allowlistedIntegrationDirs = map[string]string{
	"test/e2e":   "manual e2e suite against a live DataHub / assembled server; a nightly is #880 finding 3.4",
	"test/smoke": "manual post-deploy smoke against a running MCP server (requires MCP_API_KEY)",
}

// allowlistedIntegrationFuncs maps "relpath::FuncName" to a justification for a
// single integration-tagged test that cannot run under `make test-realdb`.
var allowlistedIntegrationFuncs = map[string]string{
	"pkg/platform/integration_embedjobs_realollama_test.go::TestEmbedJobs_RealOllama_BatchedPathCompletes": "requires a live Ollama container + model pull; too heavy for the per-commit RealDB gate",
}

// TestIntegrationTestsAreExecuted walks the module for integration-tagged test
// files and fails unless every test function either matches the "RealDB"
// pattern that `make test-realdb` runs or carries a justified allowlist entry.
func TestIntegrationTestsAreExecuted(t *testing.T) {
	projectRoot, err := filepath.Abs(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	walkErr := filepath.Walk(projectRoot, func(path string, info os.FileInfo, fErr error) error {
		if fErr != nil {
			return fErr
		}
		if info.IsDir() {
			if path != projectRoot && skipWalkDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path) //nolint:gosec // test reads project sources
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}
		if !requiresIntegrationTag(content) {
			return nil
		}

		rel, relErr := filepath.Rel(projectRoot, path)
		require.NoError(t, relErr)
		rel = filepath.ToSlash(rel)

		file, parseErr := parser.ParseFile(fset, path, content, 0)
		require.NoError(t, parseErr, "parsing %s", rel)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !isTestFuncName(fn.Name.Name) {
				continue
			}
			require.True(t, integrationTestReachesCI(rel, fn.Name.Name),
				integrationOrphanMessage(rel, fn.Name.Name))
		}
		return nil
	})
	require.NoError(t, walkErr)
}

// skipWalkDir reports whether a directory should be pruned from the walk:
// version control, dependency trees, build output, and hidden directories hold
// no first-party Go tests.
//
// "dist" is pruned because it is deleted underneath this walk. `make verify`
// runs release-check concurrently with the Go lane, and goreleaser's --clean
// removes and recreates the project's dist/ while it works — so walking into
// it fails the guard with "lstat dist: no such file or directory" on a
// directory that never held a test. This is the node_modules race the Makefile
// documents, one directory over.
func skipWalkDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "testdata", "dist":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// requiresIntegrationTag reports whether the file's build constraints require
// the "integration" build tag: it builds when integration is set but not when
// no tags are set. Build constraints precede the package clause, so the scan
// stops there.
func requiresIntegrationTag(content []byte) bool {
	for line := range strings.SplitSeq(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return false
		}
		if !constraint.IsGoBuild(trimmed) {
			continue
		}
		expr, err := constraint.Parse(trimmed)
		if err != nil {
			return false
		}
		withTag := expr.Eval(func(tag string) bool { return tag == "integration" })
		withoutTag := expr.Eval(func(string) bool { return false })
		return withTag && !withoutTag
	}
	return false
}

// isTestFuncName reports whether name is a Go test function name: "Test"
// followed by a non-lowercase rune. TestMain is the process entry point, not a
// test the runner executes, so it is excluded.
func isTestFuncName(name string) bool {
	if name == "TestMain" {
		return false
	}
	rest, ok := strings.CutPrefix(name, "Test")
	if !ok || rest == "" {
		return false
	}
	r := rest[0]
	return r < 'a' || r > 'z'
}

// integrationTestReachesCI reports whether an integration-tagged test at rel
// with the given function name runs in a CI-executed pattern or is justified
// on an allowlist.
func integrationTestReachesCI(rel, name string) bool {
	if strings.Contains(name, "RealDB") {
		return true
	}
	for dir := range allowlistedIntegrationDirs {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	_, ok := allowlistedIntegrationFuncs[rel+"::"+name]
	return ok
}

// integrationOrphanMessage tells the author exactly which options resolve an
// orphaned integration test.
func integrationOrphanMessage(rel, name string) string {
	return fmt.Sprintf(
		"integration-tagged test %s in %s runs nowhere automated: CI executes integration "+
			"tests only via `make test-realdb` (go test -tags=integration -run 'RealDB' ./...), "+
			"and this name does not contain \"RealDB\".\nResolve it one of three ways:\n"+
			"  1. Needs only Docker/testcontainers (or nothing special): rename it to contain "+
			"\"RealDB\" (e.g. %s_RealDB) so `make test-realdb` runs it.\n"+
			"  2. Needs no infrastructure at all: remove the `//go:build integration` tag so it "+
			"runs in the plain `go test ./...` step.\n"+
			"  3. Needs an external service CI cannot provide (Ollama, a running MCP, live "+
			"DataHub): add an explicit env-var t.Skip and an allowlist entry in "+
			"integration_guard_test.go (allowlistedIntegrationDirs or allowlistedIntegrationFuncs) "+
			"with a one-line reason.",
		name, rel, name)
}

// TestSkipWalkDir pins the directories the guard's walk prunes. "dist" is the
// one that matters for reliability rather than speed: `make verify` runs
// release-check beside the Go lane, and goreleaser's --clean deletes and
// recreates dist/ while this test is walking it, so a walk that descends there
// fails on a directory that never held a first-party test.
func TestSkipWalkDir(t *testing.T) {
	for _, name := range []string{".git", "node_modules", "vendor", "testdata", "dist", ".idea"} {
		require.True(t, skipWalkDir(name), "%s should be pruned from the walk", name)
	}
	for _, name := range []string{"pkg", "internal", "cmd", "distributed"} {
		require.False(t, skipWalkDir(name), "%s holds first-party source and must be walked", name)
	}
}
