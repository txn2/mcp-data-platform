// Package verify enforces project-level structural invariants.
//
// These tests prevent categories of bugs that unit tests cannot catch:
//   - Dead packages that compile and pass tests but are never called
//   - Noop-only interfaces that satisfy all gates while doing nothing
//
// Migration-specific checks (TestMigrationTablesHaveConsumers) remain in
// pkg/database/migrate/ because they depend on the embedded migration FS.
//
// Run: go test -run 'TestNoDeadPackages|TestNoopOnlyInterfaces' .
package mcp_data_platform_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Shared helpers for filesystem scanning
// ---------------------------------------------------------------------------

// discoverPackages walks pkgDir and returns a map of import paths for all
// packages that contain non-test Go source files.
func discoverPackages(pkgDir, projectRoot, modulePath string) (map[string]bool, error) {
	allPackages := map[string]bool{}
	err := filepath.Walk(pkgDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() {
			return nil
		}
		hasGo, dirErr := dirHasGoSource(path)
		if dirErr != nil {
			return fmt.Errorf("checking directory %s: %w", path, dirErr)
		}
		if hasGo {
			rel, relErr := filepath.Rel(projectRoot, path)
			if relErr != nil {
				return fmt.Errorf("computing relative path for %s: %w", path, relErr)
			}
			importPath := modulePath + "/" + filepath.ToSlash(rel)
			allPackages[importPath] = false
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking package directory: %w", err)
	}
	return allPackages, nil
}

// dirHasGoSource reports whether dir contains at least one non-test Go file.
func dirHasGoSource(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("reading directory %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			return true, nil
		}
	}
	return false, nil
}

// scanImports walks the given directories and marks imported packages as true.
func scanImports(scanDirs []string, importRe *regexp.Regexp, allPackages map[string]bool) error {
	for _, dir := range scanDirs {
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			continue
		}
		walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, fErr error) error {
			if fErr != nil {
				return fErr
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
				return nil
			}
			content, readErr := os.ReadFile(path) //nolint:gosec // test reads source files
			if readErr != nil {
				return fmt.Errorf("reading file %s: %w", path, readErr)
			}
			for _, match := range importRe.FindAllStringSubmatch(string(content), -1) {
				if _, exists := allPackages[match[1]]; exists {
					allPackages[match[1]] = true
				}
			}
			return nil
		})
		if walkErr != nil {
			return fmt.Errorf("scanning imports in %s: %w", dir, walkErr)
		}
	}
	return nil
}

// isNoopType reports whether a type name indicates a no-op implementation.
func isNoopType(name string) bool {
	return strings.Contains(strings.ToLower(name), "noop")
}

// ---------------------------------------------------------------------------
// Gate: No dead packages
// ---------------------------------------------------------------------------

// TestNoDeadPackages verifies that every Go package under pkg/ is imported by
// at least one non-test file in the project (pkg/, cmd/, or internal/).
//
// A package that exists but is never imported is dead code — it compiles,
// passes its own unit tests, but is never executed in the running application.
func TestNoDeadPackages(t *testing.T) {
	projectRoot, err := filepath.Abs(".")
	require.NoError(t, err)

	modulePath := "github.com/txn2/mcp-data-platform"

	pkgDir := filepath.Join(projectRoot, "pkg")
	allPackages, err := discoverPackages(pkgDir, projectRoot, modulePath)
	require.NoError(t, err)
	require.NotEmpty(t, allPackages)

	importRe := regexp.MustCompile(`"(` + regexp.QuoteMeta(modulePath) + `/[^"]+)"`)
	scanDirs := []string{
		filepath.Join(projectRoot, "pkg"),
		filepath.Join(projectRoot, "cmd"),
		filepath.Join(projectRoot, "internal"),
	}

	err = scanImports(scanDirs, importRe, allPackages)
	require.NoError(t, err)

	for pkg, imported := range allPackages {
		assert.True(t, imported,
			"package %q contains Go source files but is never imported by any non-test code. "+
				"Either wire it into the platform or delete it.", pkg)
	}
}

// ---------------------------------------------------------------------------
// Gate: No noop-only interfaces
// ---------------------------------------------------------------------------

// interfaceImpl records a concrete type that asserts interface compliance
// via `var _ InterfaceName = (*TypeName)(nil)`.
type interfaceImpl struct {
	iface    string
	typeName string
}

// TestNoopOnlyInterfaces verifies that every interface which has a noop
// implementation also has at least one real (non-noop) implementation in
// non-test Go source code.
//
// This prevents the "noop loophole" where an entire feature is built around
// a no-op implementation — everything compiles, tests pass, the package is
// imported, but the core behavior (e.g. writing to an external system) never
// actually executes. A noop bypasses every other verification level.
//
// If this test fails:
//  1. A real implementation needs to be written — the noop is a placeholder
//     for functionality that was never delivered.
//  2. The interface is intentionally noop-only — add it to the allowlist
//     with a justification comment.
func TestNoopOnlyInterfaces(t *testing.T) {
	projectRoot, err := filepath.Abs(".")
	require.NoError(t, err)

	pkgDir := filepath.Join(projectRoot, "pkg")

	implRe := regexp.MustCompile(`var\s+_\s+(\S+)\s*=\s*\(\*(\w+)\)\(nil\)`)

	var impls []interfaceImpl
	walkErr := filepath.Walk(pkgDir, func(path string, info os.FileInfo, fErr error) error {
		if fErr != nil {
			return fErr
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path) //nolint:gosec // test reads source files
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}
		for _, match := range implRe.FindAllStringSubmatch(string(content), -1) {
			impls = append(impls, interfaceImpl{
				iface:    match[1],
				typeName: match[2],
			})
		}
		return nil
	})
	require.NoError(t, walkErr)
	require.NotEmpty(t, impls, "should find interface compliance assertions in pkg/")

	byInterface := make(map[string][]interfaceImpl)
	for _, impl := range impls {
		byInterface[impl.iface] = append(byInterface[impl.iface], impl)
	}

	for iface, implList := range byInterface {
		hasNoop := false
		hasReal := false
		for _, impl := range implList {
			if isNoopType(impl.typeName) {
				hasNoop = true
			} else {
				hasReal = true
			}
		}
		if !hasNoop {
			continue
		}
		typeNames := make([]string, 0, len(implList))
		for _, impl := range implList {
			typeNames = append(typeNames, impl.typeName)
		}
		assert.True(t, hasReal,
			"interface %q has only noop implementation(s) %v — a real implementation is required. "+
				"A noop satisfies compile checks, tests, and import gates while doing nothing. "+
				"Either implement the real behavior or remove the feature that depends on it.",
			iface, typeNames)
	}
}
