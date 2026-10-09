package gates

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/release-tag.py holds a release to the release-candidate rules
// (#2012): a final tag sits on the commit its last candidate was built from,
// and a release measures from the last FINAL release, never a candidate.

const (
	releaseTagScript = "scripts/release-tag.py"
	nightlyScript    = "scripts/e2e_nightly.py"
)

// releaseRepo lays out a repository with the real scripts and this history:
// v1.138.2 on the first commit, v1.139.0-rc1 on the second, v1.139.0-rc2 on
// the third, and a fourth commit nothing is tagged on.
func releaseRepo(t *testing.T) (root string, commits []string) {
	t.Helper()
	root = newGitRepo(t, releaseTagScript, map[string]string{"README.md": "one\n"})
	evidence, err := os.ReadFile(filepath.Join("..", "..", scriptRelPath))
	require(t, err)
	writeFile(t, root, scriptRelPath, string(evidence))
	nightly, err := os.ReadFile(filepath.Join("..", "..", nightlyScript))
	require(t, err)
	writeFile(t, root, nightlyScript, string(nightly))
	commit := func(msg string) string {
		writeFile(t, root, "README.md", msg+"\n")
		runIn(t, root, "git", "add", "-A")
		runIn(t, root, "git", "-c", "user.name=gate", "-c", "user.email=gate@example.com", "commit", "-q", "-m", msg)
		return gitOut(t, root, "rev-parse", "HEAD")
	}
	commits = append(commits, gitOut(t, root, "rev-parse", "HEAD"))
	runIn(t, root, "git", "tag", "v1.138.2")
	commits = append(commits, commit("two"))
	runIn(t, root, "git", "tag", "v1.139.0-rc1")
	commits = append(commits, commit("three"))
	runIn(t, root, "git", "tag", "v1.139.0-rc2")
	commits = append(commits, commit("four"))
	return root, commits
}

// fakeGH puts a gh on PATH that answers the nightly's run list with runs, a
// run's failed log with log, and the issue list with issues, and records every
// other call it is given in calls.txt under root. The env it returns is what
// the script runs under.
func fakeGH(t *testing.T, root, runs, log, issues string) []string {
	t.Helper()
	writeFile(t, root, "fake/runs.json", runs)
	writeFile(t, root, "fake/log.txt", log)
	writeFile(t, root, "fake/issues.json", issues)
	writeFile(t, root, "fake/bin/gh", `#!/bin/sh
case "$1 $2" in
  "run list") cat "$FAKE/runs.json" ;;
  "run view") cat "$FAKE/log.txt" ;;
  "issue list") cat "$FAKE/issues.json" ;;
  *) printf '%s\n' "$*" >> "$FAKE/calls.txt" ;;
esac
`)
	require(t, os.Chmod(filepath.Join(root, "fake", "bin", "gh"), 0o700)) // #nosec G302 -- the fake must be executable
	fake := filepath.Join(root, "fake")
	return []string{"FAKE=" + fake, "PATH=" + filepath.Join(fake, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")}
}

const (
	nightlyPassed = `[{"databaseId": 11, "conclusion": "success", "url": "https://example.com/runs/11", "createdAt": "2026-10-08T07:00:00Z"}]`
	// ghCanceled is the conclusion as GitHub spells it.
	ghCanceled    = "cancelled" //nolint:misspell // GitHub's value, not prose
	nightlyFailed = `[{"databaseId": 12, "conclusion": "` + ghCanceled + `", "url": "https://example.com/runs/12", "createdAt": "2026-10-08T07:00:00Z"},
	                  {"databaseId": 13, "conclusion": "failure", "url": "https://example.com/runs/13", "createdAt": "2026-10-07T07:00:00Z"}]`
	failedLog = "e2e\tRun E2E tests\t2026-10-07T07:10:00Z --- FAIL: TestAdminAPI_BootstrapDB (8.39s)\n" +
		"e2e\tRun E2E tests\t2026-10-07T07:10:00Z     --- FAIL: TestAdminAPI_BootstrapDB/auth_key_create (0.00s)\n" +
		"e2e\tRun E2E tests\t2026-10-07T07:10:00Z --- FAIL: TestAdminAPI_BootstrapDB (8.39s)\n"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // fixed git invocations in a temp repo
	cmd.Dir = dir
	out, err := cmd.Output()
	require(t, err)
	return strings.TrimSpace(string(out))
}

func TestReleaseTag_AFinalTagSitsOnItsLastCandidate(t *testing.T) {
	root, commits := releaseRepo(t)

	env := fakeGH(t, root, nightlyPassed, "", "[]")
	runIn(t, root, "git", "tag", "v1.139.0", commits[2])
	out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.139.0"}, env)
	if !ok {
		t.Fatalf("a final tag on its last candidate's commit was refused: %s", out)
	}

	runIn(t, root, "git", "tag", "-d", "v1.139.0")
	runIn(t, root, "git", "tag", "v1.139.0", commits[3])
	out, ok = runScript(t, root, []string{releaseTagScript, "check", "v1.139.0"}, env)
	if ok || !strings.Contains(out, "v1.139.0-rc2") {
		t.Fatalf("a final tag on a commit after its last candidate was accepted: %s", out)
	}
}

// A final tag with no candidate is a release cut straight from main and
// passes, plain or annotated: v1.140.0 was refused for having none, though no
// candidate had ever been cut.
func TestReleaseTag_AFinalTagWithNoCandidate(t *testing.T) {
	root, commits := releaseRepo(t)
	env := fakeGH(t, root, nightlyPassed, "", "[]")
	runIn(t, root, "git", "tag", "v1.140.0", commits[3])
	if out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.140.0"}, env); !ok {
		t.Fatalf("a final tag with no candidate was refused: %s", out)
	}
	// An annotated tag records a tagger; a CI runner has no git identity.
	runIn(t, root, "git", "-c", "user.name=gate", "-c", "user.email=gate@example.com",
		"tag", "-a", "v1.140.1", "-m", "release-without-rc: emergency fix", commits[3])
	if out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.140.1"}, env); !ok {
		t.Fatalf("an annotated release with no candidate was refused: %s", out)
	}
}

func TestReleaseTag_CandidatesAndMalformedTags(t *testing.T) {
	root, _ := releaseRepo(t)
	if out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.139.0-rc2"}, nil); !ok {
		t.Fatalf("a candidate was refused: %s", out)
	}
	for _, tag := range []string{"v1.139", "release-1", "v1.139.0-rc0", "v1.139.0-beta1"} {
		if out, ok := runScript(t, root, []string{releaseTagScript, "check", tag}, nil); ok {
			t.Errorf("%s was accepted: %s", tag, out)
		}
	}
}

// TestReleaseTag_PreviousIsTheLastFinalRelease is what the changelog measures
// from: the final release before the tag, for a candidate and a final alike.
func TestReleaseTag_PreviousIsTheLastFinalRelease(t *testing.T) {
	root, commits := releaseRepo(t)
	runIn(t, root, "git", "tag", "v1.139.0", commits[2])
	for _, tag := range []string{"v1.139.0", "v1.139.0-rc2", "v1.139.0-rc1"} {
		out, ok := runScript(t, root, []string{releaseTagScript, "previous", tag}, nil)
		if !ok || strings.TrimSpace(out) != "v1.138.2" {
			t.Errorf("previous %s = %q, want v1.138.2", tag, out)
		}
	}
	if out, _ := runScript(t, root, []string{releaseTagScript, "previous", "v1.138.2"}, nil); strings.TrimSpace(out) != "" {
		t.Errorf("the first release has no previous one, got %q", out)
	}
	if _, ok := runScript(t, root, []string{releaseTagScript, "previous", "nope"}, nil); ok {
		t.Error("a malformed tag has a previous release")
	}
}

// TestAcceptanceReleaseGate_MeasuresFromTheLastFinalRelease is #2012's gate
// acceptance: with v1.138.2, v1.139.0-rc1 and v1.139.0-rc2 in the history, the
// release gate measures from v1.138.2, so an acceptance file changed before the
// candidates is still checked.
func TestAcceptanceReleaseGate_MeasuresFromTheLastFinalRelease(t *testing.T) {
	root, _ := releaseRepo(t)
	require(t, os.MkdirAll(filepath.Join(root, "test", "acceptance"), 0o750))
	writeCriteria(t, root, "4242", "TestIssue4242_Criterion")
	runIn(t, root, "git", "add", "-A")
	runIn(t, root, "git", "-c", "user.name=gate", "-c", "user.email=gate@example.com", "commit", "-q", "-m", "acceptance")
	runIn(t, root, "git", "tag", "v1.139.0-rc3")

	out, ok := gate(t, root, "release")
	if ok || !strings.Contains(out, "4242") {
		t.Fatalf("the gate did not measure from v1.138.2 past the candidates: %s", out)
	}
}

// TestReleaseTag_ARedNightlyRefusesAFinalTag is #2036's first criterion: a
// final tag is refused while the most recent nightly on main that passed or
// failed (a canceled one says nothing) failed, naming the run and its failing
// tests; release-without-nightly in the annotation releases over it, and a
// candidate is not held to the rule.
func TestReleaseTag_ARedNightlyRefusesAFinalTag(t *testing.T) {
	root, commits := releaseRepo(t)
	env := fakeGH(t, root, nightlyFailed, failedLog, "[]")

	runIn(t, root, "git", "tag", "v1.140.0", commits[3])
	out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.140.0"}, env)
	assertOutput(t, out, ok, expect{
		want:    []string{"https://example.com/runs/13", "TestAdminAPI_BootstrapDB, TestAdminAPI_BootstrapDB/auth_key_create.", "release-without-nightly"},
		mustNot: []string{"runs/12"},
	})

	runIn(t, root, "git", "-c", "user.name=gate", "-c", "user.email=gate@example.com",
		"tag", "-a", "v1.140.1", "-m", "release-without-nightly: the fix is in this release", commits[3])
	if out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.140.1"}, env); !ok {
		t.Fatalf("release-without-nightly did not release over a red nightly: %s", out)
	}
	// The override is a line of its own with a reason, not a mention.
	runIn(t, root, "git", "-c", "user.name=gate", "-c", "user.email=gate@example.com",
		"tag", "-a", "v1.140.2", "-m", "notes: no release-without-nightly: here\n\nrelease-without-nightly:", commits[3])
	if out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.140.2"}, env); ok {
		t.Fatalf("an override with no reason released over a red nightly: %s", out)
	}

	runIn(t, root, "git", "tag", "v1.141.0-rc1", commits[3])
	if out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.141.0-rc1"}, env); !ok {
		t.Fatalf("a candidate was refused over a red nightly: %s", out)
	}
}

// A nightly result that cannot be read refuses the tag rather than passing it:
// a gate that cannot see is not a gate that saw green.
func TestReleaseTag_AnUnreadableNightlyRefuses(t *testing.T) {
	root, commits := releaseRepo(t)
	env := fakeGH(t, root, "[]", "", "[]")
	runIn(t, root, "git", "tag", "v1.140.0", commits[3])
	out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.140.0"}, env)
	assertOutput(t, out, ok, expect{want: []string{"no E2E Nightly on main has finished"}})

	require(t, os.Remove(filepath.Join(root, "fake", "runs.json")))
	out, ok = runScript(t, root, []string{releaseTagScript, "check", "v1.140.0"}, env)
	assertOutput(t, out, ok, expect{want: []string{"could not be read"}})
}

// TestNightlyReport_OneTrackingIssue is #2036's second criterion: a failed
// night opens the tracking issue naming its failing tests, a later failed night
// comments on the open one, and a passing night closes it.
func TestNightlyReport_OneTrackingIssue(t *testing.T) {
	root, _ := releaseRepo(t)
	writeFile(t, root, "test-output.txt", "=== RUN TestA\n--- FAIL: TestAdminAPI_BootstrapDB (8.39s)\nFAIL\n")
	report := func(env []string, conclusion string) string {
		t.Helper()
		env = append(env, "GITHUB_REPOSITORY=txn2/example", "GITHUB_RUN_ID=99", "GITHUB_SERVER_URL=https://github.com")
		out, ok := runScript(t, root, []string{nightlyScript, "report", conclusion, "test-output.txt"}, env)
		if !ok {
			t.Fatalf("report %s failed: %s", conclusion, out)
		}
		calls, _ := os.ReadFile(filepath.Join(root, "fake", "calls.txt")) // #nosec G304 -- under the test's temp root
		require(t, os.RemoveAll(filepath.Join(root, "fake", "calls.txt")))
		return string(calls)
	}

	calls := report(fakeGH(t, root, "[]", "", "[]"), "failure")
	for _, w := range []string{"issue create --title E2E Nightly is failing", "https://github.com/txn2/example/actions/runs/99", "`TestAdminAPI_BootstrapDB`"} {
		if !strings.Contains(calls, w) {
			t.Errorf("a first red night did not open the issue (%q):\n%s", w, calls)
		}
	}

	open := `[{"number": 7, "title": "E2E Nightly is failing"}, {"number": 8, "title": "E2E Nightly is failing again?"}]`
	calls = report(fakeGH(t, root, "[]", "", open), "failure")
	if !strings.HasPrefix(calls, "issue comment 7 ") || strings.Contains(calls, "issue create") {
		t.Errorf("a second red night did not comment on the open issue:\n%s", calls)
	}

	calls = report(fakeGH(t, root, "[]", "", open), "success")
	if !strings.Contains(calls, "issue comment 7 --body The E2E Nightly passed") || !strings.Contains(calls, "issue close 7") {
		t.Errorf("a green night did not close the issue:\n%s", calls)
	}
	if calls = report(fakeGH(t, root, "[]", "", "[]"), "success"); calls != "" {
		t.Errorf("a green night with no issue open wrote to GitHub:\n%s", calls)
	}
}
