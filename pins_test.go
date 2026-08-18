// Pin-drift gates. A documentation claim about a build gate is a claim like any
// other: it must be verified mechanically, not by review. These tests fail when
// CONTRIBUTING.md names a tool version the Makefile does not pin, when CI pins a
// different version than the Makefile, or when the coverage floor is stated as
// two different numbers across the Makefile, codecov.yml, the CI workflow and
// CONTRIBUTING.md (issue #1083; the same drift class as #888/#889).
package mcp_data_platform_test

import (
	"os"
	"path/filepath"
	"regexp"
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
	data, err := os.ReadFile(filepath.Join(parts...)) //nolint:gosec // test reads project files
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
