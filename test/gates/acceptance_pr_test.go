package gates

// A pull request that changes how the dev stack starts or how the acceptance
// suite is launched runs one acceptance group in CI (#2274), with the steps the
// E2E Nightly runs, from one definition. These hold the path rule, the job's
// environment, and the shared definition to what #2274 asks.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// prPaths runs scripts/acceptance-pr-paths.py over paths and returns what it
// printed.
func prPaths(t *testing.T, paths ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", filepath.Join("..", "..", "scripts", "acceptance-pr-paths.py")) // #nosec G204 -- a fixed script in this repository
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("acceptance-pr-paths.py: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestAPullRequestThatChangesTheStackRunsAnAcceptanceGroup: the stack, the
// Makefile, the acceptance scripts, the suite's harness and the two workflows
// run the group; one ticket's criteria, a fixture, and everything else do not.
func TestAPullRequestThatChangesTheStackRunsAnAcceptanceGroup(t *testing.T) {
	for _, path := range []string{
		"dev/start.sh",
		"dev/seaweedfs/tls.key",
		"Makefile",
		"scripts/acceptance-env.py",
		"scripts/acceptance-services.sh",
		"scripts/release-acceptance.sh",
		"test/acceptance/acceptance_test.go",
		"test/acceptance/devsmoke_test.go",
		"test/acceptance/environments.txt",
		".github/workflows/e2e-nightly.yml",
		".github/workflows/acceptance-run.yml",
	} {
		if got := prPaths(t, "pkg/portal/handler.go", path); got != "true" {
			t.Errorf("%s: %s, want true", path, got)
		}
	}
	for _, path := range []string{
		"test/acceptance/issue_2299_test.go",
		"test/acceptance/testdata/form.json",
		"pkg/portal/handler.go",
		"scripts/schedule-lane.py",
		"docs/dev/Makefile.md",
		".github/workflows/ci.yml",
		"ui/dev/thing.ts",
	} {
		if got := prPaths(t, path); got != "false" {
			t.Errorf("%s: %s, want false", path, got)
		}
	}
	if got := prPaths(t); got != "false" {
		t.Errorf("no paths: %s, want false", got)
	}
}

// TestThePullRequestGroupAndTheNightlyShareOneDefinition: CI's group and every
// nightly acceptance job call acceptance-run.yml rather than restating its
// steps, CI's group runs only when the paths job says so, and the environment
// it names is one the nightly runs.
func TestThePullRequestGroupAndTheNightlyShareOneDefinition(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	require(t, err)
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", name)) // #nosec G304 -- the repository's own workflows
		require(t, err)
		return string(raw)
	}
	const uses = "uses: ./.github/workflows/acceptance-run.yml"
	ci, nightly := read("ci.yml"), read("e2e-nightly.yml")
	for name, body := range map[string]string{"ci.yml": ci, "e2e-nightly.yml": nightly} {
		if !strings.Contains(body, uses) {
			t.Errorf("%s does not call acceptance-run.yml", name)
		}
		if strings.Contains(body, "scripts/release-acceptance.sh") {
			t.Errorf("%s runs release-acceptance.sh itself; the steps are acceptance-run.yml's", name)
		}
	}
	if !strings.Contains(ci, "if: needs.stack-changes.outputs.changed == 'true'") {
		t.Error("CI's acceptance group does not wait on the stack-changes job's answer")
	}
	m := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(uses) + `\s+with:\s+environment: ([\w-]+)`).FindStringSubmatch(ci)
	if m == nil {
		t.Fatal("CI's acceptance group names no environment")
	}
	out, stderr, ok := envRun(t, repo, "nightly")
	if !ok {
		t.Fatalf("acceptance-env.py nightly: %s", stderr)
	}
	if !slices.Contains(strings.Fields(out), m[1]) {
		t.Errorf("CI's acceptance group runs %q, which the nightly does not run (%s)", m[1], strings.TrimSpace(out))
	}
}
