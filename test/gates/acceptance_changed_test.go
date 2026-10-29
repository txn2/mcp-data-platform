package gates

// A pull request's changed criteria run in CI on a fresh stack (#2341), and the
// test/e2e tests that need no service run in make verify and CI's test job
// (#2342). Each was otherwise run before merge only on the author's machine or
// not at all, and both failed the E2E Nightly after #2338 and #2340 merged.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// prTickets runs scripts/acceptance-pr-paths.py --tickets over paths, from the
// repository root, and returns what it printed.
func prTickets(t *testing.T, paths ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", filepath.Join("..", "..", "scripts", "acceptance-pr-paths.py"), "--tickets") // #nosec G204 -- a fixed script in this repository
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("acceptance-pr-paths.py --tickets: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestAPullRequestNamesTheCriteriaItChanges: the tickets are those whose
// criteria file changed and still exists, once each and in order; a deleted
// file, a fixture and production code name none.
func TestAPullRequestNamesTheCriteriaItChanges(t *testing.T) {
	got := prTickets(t,
		"test/acceptance/issue_2228_test.go",
		"pkg/portal/handler.go",
		"test/acceptance/issue_2179_test.go",
		"test/acceptance/issue_2179_test.go",
		"test/acceptance/issue_99999999_test.go",
		"test/acceptance/testdata/form.json",
		"test/acceptance/acceptance_test.go",
	)
	if got != "2179,2228" {
		t.Errorf("tickets = %q, want 2179,2228", got)
	}
	if got := prTickets(t, "pkg/portal/handler.go", "dev/start.sh"); got != "" {
		t.Errorf("no criteria changed: tickets = %q, want none", got)
	}
}

// TestTicketFlagsRunThoseTicketsOnThePlainStack: --tickets runs the named
// tickets' criteria with every other environment's left out, and refuses a
// malformed list or a named environment.
func TestTicketFlagsRunThoseTicketsOnThePlainStack(t *testing.T) {
	root := envRepo(t, "alpha  ^TestIssue9_  A=1\n")
	out, stderr, ok := envRun(t, root, "flags", "dev", "--tickets", "30,3,3")
	if want := `-run '^TestIssue(3|30)_' -skip '^TestIssue9_'`; !ok || strings.TrimSpace(out) != want {
		t.Errorf("--tickets 30,3,3 = %q (ok %v, %s); want %s", out, ok, stderr, want)
	}
	for _, args := range [][]string{
		{"flags", "dev", "--tickets", "3,"},
		{"flags", "dev", "--tickets", "three"},
		{"flags", "alpha", "--tickets", "3"},
	} {
		if out, _, ok := envRun(t, root, args...); ok {
			t.Errorf("%v = %q; want a refusal", args, out)
		}
	}
}

// TestCIRunsTheChangedCriteriaAndTheStandaloneE2E: CI runs the changed
// criteria through the shared acceptance definition on the plain stack when
// the paths job names tickets, and its test job runs make e2e-standalone, as
// make verify does before the lanes.
func TestCIRunsTheChangedCriteriaAndTheStandaloneE2E(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	require(t, err)
	ci := string(raw)
	for _, want := range []string{
		"python3 scripts/acceptance-pr-paths.py --tickets",
		"tickets: ${{ steps.paths.outputs.tickets }}",
		"if: needs.stack-changes.outputs.tickets != ''",
		"tickets: ${{ needs.stack-changes.outputs.tickets }}",
		"run: make e2e-standalone",
	} {
		if !strings.Contains(ci, want) {
			t.Errorf("ci.yml does not contain %q", want)
		}
	}
	raw, err = os.ReadFile(filepath.Join("..", "..", "Makefile"))
	require(t, err)
	verify := recipe(t, string(raw), "verify")
	// Where in the preamble it runs is its cost's to decide (#2369,
	// TestVerifyRunsTheCheapestStepFirst); it runs before the lanes.
	standalone, lanes := indexOf(verify, "e2e-standalone"), indexOf(verify, "verify-checks")
	if standalone < 0 || standalone > lanes {
		t.Errorf("verify runs e2e-standalone at %d and the lanes at %d; e2e-standalone must run before them", standalone, lanes)
	}
	if !strings.Contains(strings.Join(recipe(t, string(raw), "e2e-standalone"), "\n"), "E2E_STANDALONE=1") {
		t.Error("e2e-standalone does not set E2E_STANDALONE=1, so the tests that need the services are not skipped")
	}
}
