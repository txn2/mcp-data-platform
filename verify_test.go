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
	"gopkg.in/yaml.v3"
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

// ---------------------------------------------------------------------------
// Gate: CLAUDE.md project structure map stays current
// ---------------------------------------------------------------------------

// TestDatasetURNGrammarCentralized verifies that the DataHub dataset URN
// grammar (urn:li:dataset:(urn:li:dataPlatform:...)) is built and parsed only
// in pkg/urnbuild (#760). Hand-rolled copies drift independently: an edge case
// (commas in dataset names, non-PROD environments) fixed in one copy and not
// the others produces URNs one subsystem emits and another fails to resolve.
//
// The pattern matches grammar *implementations*: a parse-prefix string
// constant ("...dataPlatform:"), a format-string build ("...dataPlatform:%s"),
// or a concatenation build ("...dataPlatform:<platform>," +). Complete
// example URNs in doc comments and struct tags are left alone.
func TestDatasetURNGrammarCentralized(t *testing.T) {
	projectRoot, err := filepath.Abs(".")
	require.NoError(t, err)

	grammarRe := regexp.MustCompile(`urn:li:dataset:\(urn:li:dataPlatform:([a-zA-Z0-9_-]*,)?("|%s)`)

	for _, root := range []string{"pkg", "internal", "cmd"} {
		err := filepath.Walk(filepath.Join(projectRoot, root), func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(projectRoot, path)
			require.NoError(t, relErr)
			if strings.HasPrefix(rel, filepath.Join("pkg", "urnbuild")) {
				return nil
			}
			content, readErr := os.ReadFile(path) //nolint:gosec // test reads project sources
			require.NoError(t, readErr)
			assert.False(t, grammarRe.Match(content),
				"%s builds or parses the dataset URN grammar by hand. "+
					"Use pkg/urnbuild (DatasetURN, DatasetURNFromName, ParseDatasetURN) instead.", rel)
			return nil
		})
		require.NoError(t, err)
	}
}

// TestClaudeMdCoversPkgDirectories verifies that every top-level pkg/
// directory is referenced by name in CLAUDE.md's Project Structure section.
//
// CLAUDE.md is the first map new contributors and coding agents load. A map
// that silently drops packages as the tree grows sends every newcomer's
// first exploration to the wrong places (see issue #773).
func TestClaudeMdCoversPkgDirectories(t *testing.T) {
	projectRoot, err := filepath.Abs(".")
	require.NoError(t, err)

	entries, err := os.ReadDir(filepath.Join(projectRoot, "pkg"))
	require.NoError(t, err)

	claudeMd, err := os.ReadFile(filepath.Join(projectRoot, "CLAUDE.md")) //nolint:gosec // test reads project doc
	require.NoError(t, err)
	content := string(claudeMd)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// \b prevents a substring collision from masking a missing entry,
		// e.g. "auth/" matching inside "oauth/" or "session/" inside
		// "browsersession/".
		pkgNameRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(e.Name()) + `/`)
		assert.True(t, pkgNameRe.MatchString(content),
			"pkg/%s is not listed in CLAUDE.md's Project Structure section. "+
				"Add it with a one-line purpose note, or delete the package if it's no longer used.", e.Name())
	}
}

// ---------------------------------------------------------------------------
// Gate: No orphaned documentation pages
// ---------------------------------------------------------------------------

// mappingValue returns the value node for key in a YAML mapping (or the
// document's root mapping), or nil if absent.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// collectNavPages recursively gathers every page path (scalar ending in
// ".md") from the mkdocs nav tree. Mapping keys are section/page titles, not
// paths, so only mapping values are visited.
func collectNavPages(node *yaml.Node, pages map[string]bool) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if strings.HasSuffix(node.Value, ".md") {
			pages[node.Value] = true
		}
	case yaml.MappingNode:
		for i := 1; i < len(node.Content); i += 2 {
			collectNavPages(node.Content[i], pages)
		}
	default:
		for _, child := range node.Content {
			collectNavPages(child, pages)
		}
	}
}

// parseExcludePatterns splits an mkdocs exclude_docs/not_in_nav literal block
// into its gitignore-style patterns, dropping blanks and comment lines.
func parseExcludePatterns(block string) []string {
	var patterns []string
	for line := range strings.SplitSeq(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// validateExcludePattern rejects gitignore syntax the gate's matcher does not
// model. MkDocs accepts full gitignore syntax (pathspec); rather than
// approximating it and silently diverging, the gate restricts mkdocs.yml to a
// subset it matches exactly and fails loudly on anything else.
func validateExcludePattern(pattern string) error {
	if pattern == ".*" {
		return nil
	}
	if strings.HasPrefix(pattern, "!") {
		return fmt.Errorf("negation pattern %q is not modeled by the docs orphan gate; restructure the patterns without negation or extend matchesExcludePattern", pattern)
	}
	if strings.ContainsAny(pattern, "*?[") {
		return fmt.Errorf("glob pattern %q is not modeled by the docs orphan gate; use an anchored /dir/ or exact path, or extend matchesExcludePattern", pattern)
	}
	if !strings.HasPrefix(pattern, "/") && strings.Contains(strings.TrimSuffix(pattern, "/"), "/") {
		return fmt.Errorf("unanchored multi-segment pattern %q is ambiguous (gitignore anchors it to the root anyway); add a leading /", pattern)
	}
	return nil
}

// hasDotSegment reports whether any path segment starts with ".", mirroring
// how gitignore's ".*" pattern hides dotfiles and dot-directories at any level.
func hasDotSegment(rel string) bool {
	for segment := range strings.SplitSeq(rel, "/") {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}
	return false
}

// matchesAnchored matches a root-anchored pattern (leading "/" removed): a
// trailing "/" names a directory subtree; otherwise the pattern names an
// exact file or directory at the docs root.
func matchesAnchored(rel, pattern string) bool {
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(rel, pattern)
	}
	return rel == pattern || strings.HasPrefix(rel, pattern+"/")
}

// matchesSegmentName matches an unanchored single-name pattern against any
// path segment, per gitignore: a bare name matches a file or directory at any
// level; a trailing "/" restricts it to directories.
func matchesSegmentName(rel, pattern string) bool {
	name := strings.TrimSuffix(pattern, "/")
	dirOnly := strings.HasSuffix(pattern, "/")
	segments := strings.Split(rel, "/")
	for i, segment := range segments {
		if segment == name && (!dirOnly || i < len(segments)-1) {
			return true
		}
	}
	return false
}

// matchesExcludePattern reports whether the docs-relative path (forward
// slashes) matches one pattern from the subset accepted by
// validateExcludePattern. Within that subset the semantics are exactly
// gitignore's, so the gate cannot drift from what MkDocs actually excludes.
func matchesExcludePattern(rel, pattern string) bool {
	if pattern == ".*" {
		return hasDotSegment(rel)
	}
	if anchored, ok := strings.CutPrefix(pattern, "/"); ok {
		return matchesAnchored(rel, anchored)
	}
	return matchesSegmentName(rel, pattern)
}

// TestMatchesExcludePattern pins the matcher's semantics per branch so a
// divergence from gitignore (what MkDocs' pathspec implements) cannot ship
// silently.
func TestMatchesExcludePattern(t *testing.T) {
	cases := []struct {
		rel, pattern string
		want         bool
	}{
		{"research/notes.md", ".*", false},
		{".hidden.md", ".*", true},
		{"a/.playwright/x.md", ".*", true},
		{"research/notes.md", "/research/", true},
		{"research/sub/notes.md", "/research/", true},
		{"archive/research/notes.md", "/research/", false},
		{"research/notes.md", "/templates/", false},
		{"a/b.md", "/a/b.md", true},
		{"a/b.md/c.md", "/a/b.md", true},
		{"x/a/b.md", "/a/b.md", false},
		{"any/depth/drafts/x.md", "drafts", true},
		{"any/depth/drafts/x.md", "drafts/", true},
		{"a/b/drafts", "drafts/", false},
		{"a/b/drafts.md", "drafts", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, matchesExcludePattern(tc.rel, tc.pattern),
			"matchesExcludePattern(%q, %q)", tc.rel, tc.pattern)
	}

	for _, bad := range []string{"!/research/x.md", "/res*/", "/research/**", "/[internal/", "guides/internal/"} {
		assert.Error(t, validateExcludePattern(bad), "pattern %q should be rejected", bad)
	}
	for _, good := range []string{".*", "/templates/", "/research/", "/a/b.md", "drafts", "drafts/"} {
		assert.NoError(t, validateExcludePattern(good), "pattern %q should be accepted", good)
	}
}

// mkdocsBuiltinExcludes are always applied by MkDocs in addition to any
// configured exclude_docs (mkdocs 1.6 files.set_exclusions prepends
// _default_exclude = ['.*', '/templates/'] unconditionally).
var mkdocsBuiltinExcludes = []string{".*", "/templates/"}

// TestDocsPagesInNavOrExcluded verifies that every Markdown page under docs/
// is reachable from the mkdocs.yml nav, intentionally unlinked (not_in_nav),
// or excluded from the build (exclude_docs plus MkDocs' built-in defaults).
//
// A page in none of those sets is built and published at its URL but
// unreachable by browsing: invisible to humans evaluating the docs site
// while remaining live content that silently goes stale (see issue #772).
func TestDocsPagesInNavOrExcluded(t *testing.T) {
	projectRoot, err := filepath.Abs(".")
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(projectRoot, "mkdocs.yml")) //nolint:gosec // test reads project config
	require.NoError(t, err)
	var root yaml.Node
	require.NoError(t, yaml.Unmarshal(raw, &root))

	navPages := map[string]bool{}
	collectNavPages(mappingValue(&root, "nav"), navPages)
	require.NotEmpty(t, navPages, "mkdocs.yml nav should reference .md pages")

	patterns := append([]string{}, mkdocsBuiltinExcludes...)
	for _, key := range []string{"exclude_docs", "not_in_nav"} {
		if node := mappingValue(&root, key); node != nil {
			patterns = append(patterns, parseExcludePatterns(node.Value)...)
		}
	}
	for _, pattern := range patterns {
		require.NoError(t, validateExcludePattern(pattern),
			"mkdocs.yml exclude_docs/not_in_nav pattern %q", pattern)
	}

	docsDir := filepath.Join(projectRoot, "docs")
	walkErr := filepath.Walk(docsDir, func(p string, info os.FileInfo, fErr error) error {
		if fErr != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return fErr
		}
		rel, relErr := filepath.Rel(docsDir, p)
		require.NoError(t, relErr)
		rel = filepath.ToSlash(rel)
		excluded := false
		for _, pattern := range patterns {
			if matchesExcludePattern(rel, pattern) {
				excluded = true
				break
			}
		}
		assert.True(t, navPages[rel] || excluded,
			"docs/%s is built and published but unreachable: it is not in the mkdocs.yml nav "+
				"and no not_in_nav/exclude_docs pattern matches it. Add it to nav, list it under "+
				"not_in_nav (published, intentionally unlinked), or under exclude_docs (not built).", rel)
		return nil
	})
	require.NoError(t, walkErr)
}
