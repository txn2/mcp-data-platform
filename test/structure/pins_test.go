// Pin-drift gates. A documentation claim about a build gate is a claim like any
// other: it must be verified mechanically, not by review. These tests fail when
// CONTRIBUTING.md names a tool version the Makefile does not pin, when CI pins a
// different version than the Makefile, or when the coverage floor is stated as
// two different numbers across the Makefile, codecov.yml, the CI workflow and
// CONTRIBUTING.md (issue #1083; the same drift class as #888/#889).
package structure_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// makefileVar extracts a `NAME := value` assignment from the Makefile.
func makefileVar(t *testing.T, makefile, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*:=\s*(\S+)\s*$`)
	m := re.FindStringSubmatch(makefile)
	if m == nil {
		t.Fatalf("Makefile has no %s assignment", name)
	}
	return m[1]
}

// firstSubmatch returns the first capture of pattern in text, failing the test
// when the pattern no longer matches — a rewrite that removes the statement is
// itself drift, not a reason to skip the check.
func firstSubmatch(t *testing.T, text, pattern, what string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("could not find %s (pattern %s)", what, pattern)
	}
	return m[1]
}

// allSubmatches returns every first-capture of pattern in text.
func allSubmatches(text, pattern string) []string {
	matches := regexp.MustCompile(pattern).FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(rootPath(t, parts...)) //nolint:gosec // test reads project files
	if err != nil {
		t.Fatalf("reading %s: %v", filepath.Join(parts...), err)
	}
	return string(data)
}

// TestToolPinsAgree asserts that every place naming a pinned tool version names
// the Makefile's version. Following CONTRIBUTING.md verbatim must produce a
// toolchain `make tools-check` accepts, and CI must run what the Makefile pins:
// a local gosec that silently drops a rule CI still enforces is how a real SSRF
// bug reached PR #377.
func TestToolPinsAgree(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")
	contributing := readRepoFile(t, "CONTRIBUTING.md")
	ci := readRepoFile(t, ".github", "workflows", "ci.yml")
	mutation := readRepoFile(t, ".github", "workflows", "mutation.yml")

	golangciPin := makefileVar(t, makefile, "GOLANGCI_LINT_VERSION")
	gosecPin := makefileVar(t, makefile, "GOSEC_VERSION")
	gremlinsPin := makefileVar(t, makefile, "GREMLINS_VERSION")
	govulncheckPin := makefileVar(t, makefile, "GOVULNCHECK_VERSION")

	golangciInstall := `golangci-lint/v2/cmd/golangci-lint@(v[0-9.]+)`
	gosecInstall := `gosec/v2/cmd/gosec@(v[0-9.]+)`
	gremlinsInstall := `gremlins/cmd/gremlins@(v[0-9.]+)`
	govulncheckInstall := `golang.org/x/vuln/cmd/govulncheck@(v[0-9.]+)`

	cases := []struct {
		what    string
		text    string
		pattern string
		want    string
	}{
		{"CONTRIBUTING.md golangci-lint install", contributing, golangciInstall, golangciPin},
		{"CONTRIBUTING.md gosec install", contributing, gosecInstall, gosecPin},
		{"ci.yml gosec install", ci, gosecInstall, gosecPin},
		{
			"ci.yml golangci-lint-action version", ci,
			`(?s)golangci-lint-action@.{0,200}?version:\s*(v[0-9.]+)`, golangciPin,
		},
		{"mutation.yml gremlins install", mutation, gremlinsInstall, gremlinsPin},
		{"CONTRIBUTING.md govulncheck install", contributing, govulncheckInstall, govulncheckPin},
		{"ci.yml govulncheck install", ci, govulncheckInstall, govulncheckPin},
	}
	for _, c := range cases {
		for _, got := range allSubmatches(c.text, c.pattern) {
			if got != c.want {
				t.Errorf("%s pins %s, Makefile pins %s", c.what, got, c.want)
			}
		}
		if len(allSubmatches(c.text, c.pattern)) == 0 {
			t.Errorf("%s: no version found (pattern %s)", c.what, c.pattern)
		}
	}

	// The prose parenthetical above the install block names both versions; a
	// contributor reads it before the commands.
	parenthetical := firstSubmatch(t, contributing,
		`currently (v[0-9.]+ / v[0-9.]+)`, "CONTRIBUTING.md tool-version parenthetical")
	if want := golangciPin + " / " + gosecPin; parenthetical != want {
		t.Errorf("CONTRIBUTING.md says the pins are %q, Makefile pins %q", parenthetical, want)
	}
}

// TestGateFiguresAgree asserts the coverage floors are each one number. A change
// landing between two different floors passes locally and fails in CI, which is
// the parity gap the tools-check discipline exists to eliminate.
func TestGateFiguresAgree(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")
	contributing := readRepoFile(t, "CONTRIBUTING.md")
	ci := readRepoFile(t, ".github", "workflows", "ci.yml")
	codecov := readRepoFile(t, "codecov.yml")

	total := makefileVar(t, makefile, "COVERAGE_MIN")
	patch := makefileVar(t, makefile, "PATCH_COVERAGE_MIN")

	checks := []struct {
		what    string
		text    string
		pattern string
		want    string
	}{
		{"ci.yml coverage threshold", ci, `COVERAGE < ([0-9]+)`, total},
		{
			"codecov.yml project target", codecov,
			`(?s)project:.*?target:\s*([0-9]+)%`, total,
		},
		{
			"codecov.yml patch target", codecov,
			`(?s)patch:.*?target:\s*([0-9]+)%`, patch,
		},
		{
			"CONTRIBUTING.md total-coverage floor", contributing,
			`Total coverage must be at least ([0-9]+)%`, total,
		},
		{
			"CONTRIBUTING.md patch-coverage floor", contributing,
			`lines your change touches must be at least ([0-9]+)%`, patch,
		},
	}
	for _, c := range checks {
		if got := firstSubmatch(t, c.text, c.pattern, c.what); got != c.want {
			t.Errorf("%s is %s%%, Makefile says %s%%", c.what, got, c.want)
		}
	}
}

// ── Coverage exclusion parity ───────────────────────────────────────────────

// The floors above are pinned across every file that states them. The
// exclusion lists beside those floors were not, and #1747 is what that cost:
// scripts/patch-coverage.sh was widened from `cmd/dev-mcp-mock/` to every
// `cmd/dev-*-mock/` when a second dev fixture arrived, codecov.yml was left
// naming the first one, and the same diff read 88.8% of changed lines covered
// here and 64.20% in CI -- 271 of the 383 lines codecov counted as missing
// being the fixture the local gate had been told to ignore (#1748).
//
// The two gates express the same policy in different languages: an awk regex
// in the shell script, a glob in the YAML. Comparing the patterns as strings
// would compare their spelling rather than their meaning, so what is compared
// below is the SET each one resolves to over the repository's actual Go files.

// excludedByPatchCoverage reports whether scripts/patch-coverage.sh drops path
// from the changed-line set. The script's exclusions live in its awk program as
// `if (f ~ /re/) f = ""` and `if (f == "literal") f = ""`, which is what these
// two patterns read; a rewrite that states an exclusion some third way fails
// the test rather than silently narrowing it, because the count below is
// asserted.
var (
	patchCovRegexExclusion   = regexp.MustCompile(`if \(f ~ /((?:[^/\\]|\\.)+)/\)\s*f = ""`)
	patchCovLiteralExclusion = regexp.MustCompile(`if \(f == "([^"]+)"\)\s*f = ""`)
)

// awkToGoRegexp converts an awk ERE as written in the script to a Go regexp.
// The two agree on everything the script uses; the only rewrite needed is the
// escaped slash an awk /.../ literal requires.
func awkToGoRegexp(t *testing.T, ere string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(strings.ReplaceAll(ere, `\/`, `/`))
	if err != nil {
		t.Fatalf("patch-coverage.sh exclusion %q is not a regexp Go can read: %v", ere, err)
	}
	return re
}

// globMatches reports whether a codecov ignore glob matches path. `**` matches
// any number of path segments including none; every other segment is matched
// by filepath.Match, so `*` stops at a separator the way codecov's does.
func globMatches(t *testing.T, pattern, path string) bool {
	t.Helper()
	return segmentsMatch(t, strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func segmentsMatch(t *testing.T, pat, seg []string) bool {
	t.Helper()
	switch {
	case len(pat) == 0:
		return len(seg) == 0
	case pat[0] == "**":
		// Zero segments consumed, or one and try again.
		for i := 0; i <= len(seg); i++ {
			if segmentsMatch(t, pat[1:], seg[i:]) {
				return true
			}
		}
		return false
	case len(seg) == 0:
		return false
	}
	ok, err := filepath.Match(pat[0], seg[0])
	if err != nil {
		t.Fatalf("codecov.yml ignore pattern segment %q is not a glob: %v", pat[0], err)
	}
	return ok && segmentsMatch(t, pat[1:], seg[1:])
}

// codecovIgnores reads the `ignore:` list out of codecov.yml. The file is read
// as lines rather than through a YAML library because the repository pins no
// YAML dependency for tests and the block is a flat list of quoted strings.
func codecovIgnores(t *testing.T, yaml string) []string {
	t.Helper()
	var out []string
	inBlock := false
	item := regexp.MustCompile(`^\s+-\s+"([^"]+)"\s*$`)
	for line := range strings.SplitSeq(yaml, "\n") {
		switch {
		case strings.HasPrefix(line, "ignore:"):
			inBlock = true
		case !inBlock:
			continue
		case strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#"):
			continue
		case item.MatchString(line):
			out = append(out, item.FindStringSubmatch(line)[1])
		default:
			// A line at column 0 ends the block.
			if !strings.HasPrefix(line, " ") {
				inBlock = false
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("codecov.yml has no ignore: list; the gate below would compare nothing")
	}
	return out
}

// repoGoFiles is every tracked Go file that is not a test, which is the
// universe both gates draw their exclusions from: a _test.go file is dropped by
// name in each, before any exclusion runs.
func repoGoFiles(t *testing.T) []string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "ls-files", "*.go")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("listing tracked Go files: %v", err)
	}
	var files []string
	for f := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if f != "" && !strings.HasSuffix(f, "_test.go") {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		t.Fatal("no tracked non-test Go files found")
	}
	return files
}

// TestCoverageExclusionsAgree asserts that the local patch-coverage gate and
// codecov exclude the same files. Every path one of them drops, the other
// drops: a file inside the gate here and outside it in CI (or the reverse) is
// the drift that made two readings of one diff disagree by 24 points.
func TestCoverageExclusionsAgree(t *testing.T) {
	script := readRepoFile(t, "scripts", "patch-coverage.sh")
	codecov := readRepoFile(t, "codecov.yml")

	var localRegexps []*regexp.Regexp
	for _, m := range patchCovRegexExclusion.FindAllStringSubmatch(script, -1) {
		// The script's first exclusion is the "not a Go file, or a test"
		// filter, which is the universe rather than a policy choice: codecov
		// states the same thing as **/*_test.go and both are applied to
		// repoGoFiles before this test runs.
		if m[1] == `_test\.go$` {
			continue
		}
		localRegexps = append(localRegexps, awkToGoRegexp(t, m[1]))
	}
	local := func(path string) bool {
		for _, re := range localRegexps {
			if re.MatchString(path) {
				return true
			}
		}
		for _, m := range patchCovLiteralExclusion.FindAllStringSubmatch(script, -1) {
			if m[1] == path {
				return true
			}
		}
		return false
	}

	ignores := codecovIgnores(t, codecov)
	remote := func(path string) bool {
		for _, g := range ignores {
			if globMatches(t, g, path) {
				return true
			}
		}
		return false
	}

	var localOnly, remoteOnly []string
	for _, f := range repoGoFiles(t) {
		switch l, r := local(f), remote(f); {
		case l && !r:
			localOnly = append(localOnly, f)
		case r && !l:
			remoteOnly = append(remoteOnly, f)
		}
	}
	for _, f := range localOnly {
		t.Errorf("scripts/patch-coverage.sh excludes %s and codecov.yml does not: "+
			"the local gate reads a higher patch percentage than CI on any diff touching it", f)
	}
	for _, f := range remoteOnly {
		t.Errorf("codecov.yml excludes %s and scripts/patch-coverage.sh does not: "+
			"the local gate counts lines CI ignores and fails a diff CI would accept", f)
	}
}
