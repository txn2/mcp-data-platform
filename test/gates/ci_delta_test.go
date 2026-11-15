package gates

import (
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const ciDelta = "scripts/ci-delta.py"

// deltaRepo is a module of three packages, where app imports lib and
// test/structure reads the repository: the shape the affected-package rule is
// written for.
func deltaRepo(t *testing.T) string {
	t.Helper()
	return newGitRepo(t, ciDelta, map[string]string{
		"go.mod":                      "module github.com/txn2/mcp-data-platform\n\ngo 1.24\n",
		"doc.go":                      "// Package platform is the root package.\npackage platform\n",
		"lib/lib.go":                  "package lib\n\nfunc One() int { return 1 }\n",
		"app/app.go":                  "package app\n\nimport \"github.com/txn2/mcp-data-platform/lib\"\n\nfunc Two() int { return lib.One() + 1 }\n",
		"other/other.go":              "package other\n\nfunc Three() int { return 3 }\n",
		"test/structure/repo_test.go": "package structure\n\nimport \"testing\"\n\nfunc TestRepo(t *testing.T) {}\n",
		"docs/index.md":               "# docs\n",
	})
}

func deltaGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // fixed git invocations in a temp repo
	cmd.Dir = dir
	out, err := cmd.Output()
	require(t, err)
	return strings.TrimSpace(string(out))
}

func commitAll(t *testing.T, root, msg string) {
	t.Helper()
	runIn(t, root, "git", "add", "-A")
	runIn(t, root, "git", "-c", "user.name=gate", "-c", "user.email=gate@example.com", "commit", "-q", "-m", msg)
}

// TestCIDeltaNarrowsToTheChangedPackageAndItsImporters pins #2535: a change
// to one package runs that package and every package importing it, and
// nothing else.
func TestCIDeltaNarrowsToTheChangedPackageAndItsImporters(t *testing.T) {
	root := deltaRepo(t)
	base := deltaGit(t, root, "rev-parse", "HEAD")
	writeFile(t, root, "lib/lib.go", "package lib\n\nfunc One() int { return 2 - 1 }\n")

	out, ok := runScript(t, root, []string{ciDelta, "packages"}, []string{"DELTA_BASE=" + base})
	if !ok {
		t.Fatalf("packages failed:\n%s", out)
	}
	got := strings.Fields(out)
	want := []string{"github.com/txn2/mcp-data-platform/app", "github.com/txn2/mcp-data-platform/lib"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("packages = %v, want %v", got, want)
	}

	out, _ = runScript(t, root, []string{ciDelta, "packages"}, nil)
	if strings.TrimSpace(out) != "./..." {
		t.Errorf("with no DELTA_BASE, packages = %q, want ./...", out)
	}
}

// TestCIDeltaReadsAFileOutsideEveryPackageWithTheRepositoryTests: docs and
// other files no package owns are read by the tests under test/, so a change
// to one runs them, and not the root package the walk passes through.
func TestCIDeltaReadsAFileOutsideEveryPackageWithTheRepositoryTests(t *testing.T) {
	root := deltaRepo(t)
	base := deltaGit(t, root, "rev-parse", "HEAD")
	writeFile(t, root, "docs/index.md", "# docs, changed\n")

	out, ok := runScript(t, root, []string{ciDelta, "packages"}, []string{"DELTA_BASE=" + base})
	if !ok {
		t.Fatalf("packages failed:\n%s", out)
	}
	if got := strings.TrimSpace(out); got != "github.com/txn2/mcp-data-platform/test/structure" {
		t.Errorf("packages = %q, want only test/structure", got)
	}
	if _, ok := runScript(t, root, []string{ciDelta, "ui"}, []string{"DELTA_BASE=" + base}); ok {
		t.Error("ui answered yes for a change outside ui/")
	}
	writeFile(t, root, "ui/src/x.ts", "export const x = 1;\n")
	if _, ok := runScript(t, root, []string{ciDelta, "ui"}, []string{"DELTA_BASE=" + base}); !ok {
		t.Error("ui answered no for a change under ui/")
	}
}

// TestCIDeltaLocalRecordRoundTrips: a green verify records its tree, the next
// verify narrows to the change since it, and a change to a file that decides
// how the gates run, or VERIFY_FULL, runs everything.
func TestCIDeltaLocalRecordRoundTrips(t *testing.T) {
	root := deltaRepo(t)
	if out, _ := runScript(t, root, []string{ciDelta, "local-base"}, nil); strings.TrimSpace(lastLine(out)) != "" && !strings.Contains(out, "verify:") {
		t.Fatalf("with no record, local-base printed a base: %q", out)
	}
	writeFile(t, root, "other/other.go", "package other\n\nfunc Three() int { return 4 - 1 }\n")
	if out, ok := runScript(t, root, []string{ciDelta, "record-local"}, nil); !ok {
		t.Fatalf("record-local failed:\n%s", out)
	}
	writeFile(t, root, "lib/lib.go", "package lib\n\nfunc One() int { return 3 - 2 }\n")

	out, ok := runScript(t, root, []string{ciDelta, "local-base"}, nil)
	base := lastLine(out)
	if !ok || len(base) != 40 {
		t.Fatalf("local-base after a record = %q", out)
	}
	pkgs, _ := runScript(t, root, []string{ciDelta, "packages"}, []string{"DELTA_BASE=" + base})
	if strings.Contains(pkgs, "/other") || !strings.Contains(pkgs, "/lib") {
		t.Errorf("the change since the green tree is lib alone, other was proved: %q", pkgs)
	}

	if out, _ := runScript(t, root, []string{ciDelta, "local-base"}, []string{"VERIFY_FULL=1"}); len(lastLine(out)) == 40 {
		t.Errorf("VERIFY_FULL=1 still printed a base: %q", out)
	}
	writeFile(t, root, "go.mod", "module github.com/txn2/mcp-data-platform\n\ngo 1.25\n")
	out, _ = runScript(t, root, []string{ciDelta, "local-base"}, nil)
	if len(lastLine(out)) == 40 || !strings.Contains(out, "go.mod") {
		t.Errorf("a go.mod change is not a delta: %q", out)
	}
}

// TestCIDeltaRebuildIsTheSameCommitEverywhere: the tested merge rebuilt from
// its two parents is one sha whoever rebuilds it, so every CI job names the
// same base.
func TestCIDeltaRebuildIsTheSameCommitEverywhere(t *testing.T) {
	root := deltaRepo(t)
	mainTip := deltaGit(t, root, "rev-parse", "main")
	writeFile(t, root, "lib/lib.go", "package lib\n\nfunc One() int { return 5 - 4 }\n")
	commitAll(t, root, "change")
	head := deltaGit(t, root, "rev-parse", "HEAD")

	first, ok := runScript(t, root, []string{ciDelta, "rebuild", head, mainTip}, nil)
	if !ok {
		t.Fatalf("rebuild failed:\n%s", first)
	}
	second, _ := runScript(t, root, []string{ciDelta, "rebuild", head, mainTip}, nil)
	if strings.TrimSpace(first) != strings.TrimSpace(second) || len(strings.TrimSpace(first)) != 40 {
		t.Errorf("rebuild gave %q then %q", first, second)
	}
	if tree := deltaGit(t, root, "rev-parse", strings.TrimSpace(first)+"^{tree}"); tree != deltaGit(t, root, "rev-parse", "HEAD^{tree}") {
		t.Errorf("the rebuilt merge of a branch and its own base is not the branch's tree")
	}
}

// TestCIDeltaCIBaseRunsAFirstRunInFull: only a synchronize push is a delta.
func TestCIDeltaCIBaseRunsAFirstRunInFull(t *testing.T) {
	root := deltaRepo(t)
	for _, env := range [][]string{
		{"GITHUB_EVENT_NAME=push"},
		{"GITHUB_EVENT_NAME=pull_request", "EVENT_ACTION=opened"},
	} {
		out, ok := runScript(t, root, []string{ciDelta, "ci-base"}, append(env, "GITHUB_OUTPUT="))
		if !ok || !strings.Contains(out, "mode=full") || !strings.Contains(out, "reason=") {
			t.Errorf("%v: %q", env, out)
		}
	}
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// previousRunJobs is the jobs list of a pull request run in which only Test
// failed, the shape #2642 was seen in on #2641.
const previousRunJobs = `[
 {"name": "Delta", "conclusion": "success"},
 {"name": "Lint", "conclusion": "success"},
 {"name": "Test", "conclusion": "failure"},
 {"name": "Build", "conclusion": "success"},
 {"name": "Frontend Build", "conclusion": "success"},
 {"name": "Frontend E2E", "conclusion": "success"},
 {"name": "Security Scan", "conclusion": "success"},
 {"name": "Stack changes", "conclusion": "success"},
 {"name": "Acceptance (worker-off)", "conclusion": "skipped"},
 {"name": "Acceptance (changed criteria) (2436,2634) / run", "conclusion": "success"},
 {"name": "Acceptance (changed criteria) (1896) / run", "conclusion": "success"}
]`

// carried runs `ci-delta.py carry` over a change written into a fresh repo and
// returns the groups it carries.
func carried(t *testing.T, jobs string, change map[string]string) map[string]string {
	t.Helper()
	root := deltaRepo(t)
	writeFile(t, root, "ui/src/app.ts", "export const a = 1\n")
	writeFile(t, root, "lib/lib_test.go", "package lib\n")
	commitAll(t, root, "files the change edits")
	base := deltaGit(t, root, "rev-parse", "HEAD")
	for rel, body := range change {
		writeFile(t, root, rel, body)
	}
	writeFile(t, root, ".jobs.json", jobs)
	out, ok := runScript(t, root, []string{ciDelta, "carry", "run 7 at abc1234", ".jobs.json"}, []string{"DELTA_BASE=" + base})
	if !ok {
		t.Fatalf("carry failed:\n%s", out)
	}
	got := map[string]string{}
	if err := json.Unmarshal([]byte(lastLine(out)), &got); err != nil {
		t.Fatalf("carry printed %q: %v", out, err)
	}
	return got
}

func groupsOf(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestCIDeltaCarriesWhatAFailedRunPassedAndTheChangeDoesNotReach pins
// #2642's first criterion: after a run that failed only Test, a change to a
// _test.go file skips the frontend and security jobs and narrows acceptance,
// each saying which run it relies on.
func TestCIDeltaCarriesWhatAFailedRunPassedAndTheChangeDoesNotReach(t *testing.T) {
	got := carried(t, previousRunJobs, map[string]string{"lib/lib_test.go": "package lib\n\n// changed\n"})
	want := []string{"acceptance", "frontend", "frontend_e2e", "security"}
	if g := groupsOf(got); strings.Join(g, " ") != strings.Join(want, " ") {
		t.Fatalf("carried %v, want %v", g, want)
	}
	for group, why := range got {
		if !strings.Contains(why, "run 7 at abc1234") {
			t.Errorf("%s says %q, which names no run it relies on", group, why)
		}
	}
}

// TestCIDeltaRunsAJobWhoseInputsTheChangeReaches pins #2642's second
// criterion: a change to a file a carried job reads runs that job.
func TestCIDeltaRunsAJobWhoseInputsTheChangeReaches(t *testing.T) {
	cases := []struct {
		name    string
		change  map[string]string
		running []string
	}{
		{"ui", map[string]string{"ui/src/app.ts": "export const a = 2\n"}, []string{"frontend", "frontend_e2e"}},
		{"api description", map[string]string{"internal/apidocs/swagger.json": "{}\n"}, []string{"frontend", "frontend_e2e"}},
		{"production go", map[string]string{"lib/lib.go": "package lib\n\nfunc One() int { return 2 - 1 }\n"}, []string{"security"}},
		{"semgrep rule", map[string]string{".semgrep/rule.yml": "rules: []\n"}, []string{"security"}},
		{"accepted advisory", map[string]string{".govulncheck-allow.txt": "GO-1\n"}, []string{"security"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := carried(t, previousRunJobs, tc.change)
			for _, group := range tc.running {
				if _, ok := got[group]; ok {
					t.Errorf("the change reaches %s, which was carried: %v", group, groupsOf(got))
				}
			}
		})
	}
}

// TestCIDeltaCarriesNothingAFailedOrUnrunJobDidNotProve: a group with a failed
// job, and every group of a run whose Delta job failed (every other job was
// skipped without running), is not carried.
func TestCIDeltaCarriesNothingAFailedOrUnrunJobDidNotProve(t *testing.T) {
	failedCriterion := strings.Replace(previousRunJobs,
		`"Acceptance (changed criteria) (1896) / run", "conclusion": "success"`,
		`"Acceptance (changed criteria) (1896) / run", "conclusion": "failure"`, 1)
	got := carried(t, failedCriterion, map[string]string{"lib/lib_test.go": "package lib\n\n// changed\n"})
	if _, ok := got["acceptance"]; ok {
		t.Errorf("acceptance carried from a run with a failed criteria job: %v", groupsOf(got))
	}
	if _, ok := got["frontend"]; !ok {
		t.Errorf("a failed acceptance job stopped the frontend being carried: %v", groupsOf(got))
	}

	noDelta := strings.Replace(previousRunJobs, `"Delta", "conclusion": "success"`, `"Delta", "conclusion": "failure"`, 1)
	if got := carried(t, noDelta, map[string]string{"lib/lib_test.go": "package lib\n\n// changed\n"}); len(got) != 0 {
		t.Errorf("carried %v from a run whose Delta job failed", groupsOf(got))
	}
}

// TestCIDeltaEveryCarriedGroupIsWiredIntoTheWorkflow: each skip output the
// delta job writes is read by the job it names, so a group added to
// CARRY_GROUPS without its job's condition fails here.
func TestCIDeltaEveryCarriedGroupIsWiredIntoTheWorkflow(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	for _, want := range []string{
		"needs.delta.outputs.skip_frontend != 'true'",
		"needs.delta.outputs.skip_frontend_e2e != 'true'",
		"needs.delta.outputs.skip_security != 'true'",
		"needs.delta.outputs.carry_head",
		"CARRIED: ${{ steps.base.outputs.carried }}",
	} {
		if !strings.Contains(ci, want) {
			t.Errorf("ci.yml does not read %q", want)
		}
	}
}
