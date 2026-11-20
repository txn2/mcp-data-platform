package gates

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// scripts/scheduled_report.py is what every scheduled workflow reports through
// (#2730), and where a criterion that failed and passed on one tree is named
// flaky (#2731).

// reportGH puts a gh on PATH that answers the reads scheduled_report.py makes
// from files under root/fake, and records every write in calls.txt:
//
//	jobs.json            gh run view <id> --json jobs
//	headsha-<id>.json    gh run view <id> --json headSha
//	joblog-<id>.txt      a job's log through the jobs API
//	trees/<sha>          a commit's tree through the commits API
//	issues-open.json     the open tracking issue search
//	flaky-open.json      the open "Flaky criteria" search
//	issues-all.json      the search over every state
//	issue-<n>.json       gh issue view <n>
func reportGH(t *testing.T, root string, files map[string]string) []string {
	t.Helper()
	for rel, body := range files {
		writeFile(t, root, filepath.Join("fake", rel), body)
	}
	writeFile(t, root, "fake/bin/gh", `#!/bin/sh
f() { if [ -f "$FAKE/$1" ]; then cat "$FAKE/$1"; else echo "$2"; fi; }
case "$*" in
  "run view "*"--json jobs"*) f jobs.json '{}' ;;
  "run view "*"--json headSha"*) f "headsha-$3.json" '{}' ;;
  "api repos/{owner}/{repo}/actions/jobs/"*) id="${2#repos/\{owner\}/\{repo\}/actions/jobs/}"; f "joblog-${id%/logs}.txt" '' ;;
  "api repos/{owner}/{repo}/git/commits/"*) sha="${2##*/}"; if [ -f "$FAKE/trees/$sha" ]; then cat "$FAKE/trees/$sha"; else exit 1; fi ;;
  "issue list "*"--state all"*) f issues-all.json '[]' ;;
  "issue list "*"Flaky criteria"*) f flaky-open.json '[]' ;;
  "issue list "*) f issues-open.json '[]' ;;
  "issue view "*) f "issue-$3.json" '{}' ;;
  *) printf '%s\n' "$*" >> "$FAKE/calls.txt" ;;
esac
`)
	require(t, os.Chmod(filepath.Join(root, "fake", "bin", "gh"), 0o700)) // #nosec G302 -- the fake must be executable
	fake := filepath.Join(root, "fake")
	return []string{
		"FAKE=" + fake,
		"PATH=" + filepath.Join(fake, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GITHUB_REPOSITORY=txn2/example", "GITHUB_RUN_ID=99", "GITHUB_SERVER_URL=https://github.com",
	}
}

func scheduledReportRepo(t *testing.T) string {
	t.Helper()
	return newGitRepo(t, scheduledReportScript, map[string]string{"README.md": "one\n"})
}

func runReport(t *testing.T, root string, env []string, args ...string) string {
	t.Helper()
	out, ok := runScript(t, root, append([]string{scheduledReportScript, "report"}, args...), env)
	if !ok {
		t.Fatalf("report %v failed: %s", args, out)
	}
	calls, _ := os.ReadFile(filepath.Join(root, "fake", "calls.txt")) // #nosec G304 -- under the test's temp root
	return string(calls)
}

func daysAgo(n int) string {
	return time.Now().UTC().Add(-time.Duration(n) * 24 * time.Hour).Format(time.RFC3339)
}

func TestScheduledReport_AFailureNamesTheRunAndWhatFailed(t *testing.T) {
	root := scheduledReportRepo(t)
	env := reportGH(t, root, map[string]string{
		"jobs.json": `{"jobs": [
			{"databaseId": 5, "name": "Load smoke (indicative)", "conclusion": "failure",
			 "steps": [{"name": "Start stack", "conclusion": "success"}, {"name": "Run scenarios (smoke scale)", "conclusion": "failure"}]},
			{"databaseId": 6, "name": "Harness module checks", "conclusion": "success", "steps": []}]}`,
		"joblog-5.txt": "2026-11-16T17:46:05Z   [FAIL] error-rate:portal_public_view: error rate 100.00% over 5322 calls\n" +
			"--- FAIL: FuzzSplit (0.20s)\n    Failing input written to testdata/fuzz/FuzzSplit/5c1d\n" +
			"\x1b[31m      LIVED CONDITIONALS_NEGATION at internal/hmacsig/sig.go:41:9\x1b[0m\n",
		"joblog-6.txt": "--- FAIL: NotThisJob (0.00s)\n",
	})
	env = append(env, "GITHUB_SHA=0123456789abcdef0123456789abcdef01234567")

	calls := runReport(t, root, env, "failure", "--workflow", "Load Test")
	for _, w := range []string{
		"issue create --title Load Test is failing",
		"The Load Test failed: https://github.com/txn2/example/actions/runs/99",
		"Commit: `0123456789abcdef0123456789abcdef01234567`",
		"- Load smoke (indicative) (step: Run scenarios (smoke scale))",
		"- `error-rate:portal_public_view`",
		"- `FuzzSplit`",
		"- `testdata/fuzz/FuzzSplit/5c1d`",
		"- `CONDITIONALS_NEGATION at internal/hmacsig/sig.go:41:9`",
	} {
		if !strings.Contains(calls, w) {
			t.Errorf("the failure report lacks %q:\n%s", w, calls)
		}
	}
	if strings.Contains(calls, "NotThisJob") || strings.Contains(calls, "Harness module checks") {
		t.Errorf("a job that passed was reported:\n%s", calls)
	}
}

func TestScheduledReport_TheWorkflowNameDefaultsToTheRunningWorkflow(t *testing.T) {
	root := scheduledReportRepo(t)
	env := append(reportGH(t, root, nil), "GITHUB_WORKFLOW=Mutation Testing")
	if calls := runReport(t, root, env, "failure"); !strings.Contains(calls, "issue create --title Mutation Testing is failing") {
		t.Errorf("the report did not take the running workflow's name:\n%s", calls)
	}
}

func TestScheduledReport_APassOnAFailedTreeNamesItsCriteriaFlaky(t *testing.T) {
	root := scheduledReportRepo(t)
	const failedSHA, otherSHA, passSHA = "aaaa111", "cccc333", "bbbb222"
	env := reportGH(t, root, map[string]string{
		"issues-open.json": `[{"number": 7, "title": "E2E Nightly is failing"}]`,
		"issues-all.json":  `[{"number": 7, "title": "E2E Nightly is failing"}]`,
		// The issue's body is a record written before #2731, naming only the
		// run; the comment is a newer one on another tree.
		"issue-7.json": `{"createdAt": "` + daysAgo(1) + `", "body": "The E2E Nightly failed: https://github.com/txn2/example/actions/runs/41\n\nFailing tests:\n- ` +
			"`TestIssue1899_AnUploadRaisesTheResourcesCountBeforeTheListing`" + `\n", "comments": [
			{"createdAt": "` + daysAgo(1) + `", "body": "The E2E Nightly failed: https://github.com/txn2/example/actions/runs/42\nCommit: ` + "`" + otherSHA + "`" + `\n\nFailing:\n- ` + "`TestOtherTree`" + `\n"}]}`,
		"headsha-41.json":    `{"headSha": "` + failedSHA + `"}`,
		"trees/" + failedSHA: "tree-one\n",
		"trees/" + passSHA:   "tree-one\n",
		"trees/" + otherSHA:  "tree-two\n",
	})
	env = append(env, "GITHUB_SHA="+passSHA)

	calls := runReport(t, root, env, "success", "--workflow", "E2E Nightly")
	comment := regexp.MustCompile(`(?s)issue comment 7 --body (.*?)\nissue close 7`).FindStringSubmatch(calls)
	if comment == nil {
		t.Fatalf("the pass did not comment on and close the tracking issue:\n%s", calls)
	}
	if !strings.Contains(comment[1], "flaky") || !strings.Contains(comment[1], "`TestIssue1899_AnUploadRaisesTheResourcesCountBeforeTheListing`") {
		t.Errorf("the tracking issue was not told which criteria are flaky:\n%s", comment[1])
	}
	for _, w := range []string{
		"issue create --title Flaky criteria",
		"`TestIssue1899_AnUploadRaisesTheResourcesCountBeforeTheListing`: E2E Nightly: failed in https://github.com/txn2/example/actions/runs/41 and passed in https://github.com/txn2/example/actions/runs/99",
	} {
		if !strings.Contains(calls, w) {
			t.Errorf("the Flaky criteria issue lacks %q:\n%s", w, calls)
		}
	}
	if strings.Contains(calls, "TestOtherTree") {
		t.Errorf("a criterion that failed on another tree was named flaky:\n%s", calls)
	}
}

func TestScheduledReport_APassOnANewTreeNamesNothingFlaky(t *testing.T) {
	root := scheduledReportRepo(t)
	env := reportGH(t, root, map[string]string{
		"issues-open.json": `[{"number": 7, "title": "E2E Nightly is failing"}]`,
		"issues-all.json":  `[{"number": 7, "title": "E2E Nightly is failing"}]`,
		"issue-7.json":     `{"createdAt": "` + daysAgo(1) + `", "body": "The E2E Nightly failed: https://x/runs/41\nCommit: ` + "`aaaa111`" + `\n\nFailing:\n- ` + "`TestA`" + `\n", "comments": []}`,
		"trees/aaaa111":    "tree-one\n",
		"trees/bbbb222":    "tree-two\n",
	})
	calls := runReport(t, root, append(env, "GITHUB_SHA=bbbb222"), "success", "--workflow", "E2E Nightly")
	if strings.Contains(calls, "Flaky criteria") || strings.Contains(calls, "flaky") {
		t.Errorf("a pass on a tree no run failed on named something flaky:\n%s", calls)
	}
	if !strings.Contains(calls, "issue close 7") {
		t.Errorf("the pass did not close the tracking issue:\n%s", calls)
	}
}

func TestScheduledReport_ACriterionFailingInTwoIssuesWithinTheWindowIsFlaky(t *testing.T) {
	root := scheduledReportRepo(t)
	env := reportGH(t, root, map[string]string{
		"issues-all.json": `[{"number": 3, "title": "E2E Nightly is failing"}, {"number": 4, "title": "E2E Nightly is failing"}]`,
		"issue-3.json": `{"createdAt": "` + daysAgo(3) + `", "body": "The E2E Nightly failed: https://x/runs/30\n\nFailing tests:\n- ` +
			"`TestIssue1847_AScheduledRunWritesItsOutput`" + `\n", "comments": []}`,
		"issue-4.json": `{"createdAt": "` + daysAgo(30) + `", "body": "The E2E Nightly failed: https://x/runs/20\n\nFailing tests:\n- ` +
			"`TestLongAgo`" + `\n", "comments": []}`,
	})
	writeFile(t, root, "out.txt", "--- FAIL: TestIssue1847_AScheduledRunWritesItsOutput (3.00s)\n--- FAIL: TestLongAgo (1.00s)\n")

	calls := runReport(t, root, env, "failure", "out.txt", "--workflow", "E2E Nightly")
	if !strings.Contains(calls, "issue create --title Flaky criteria") ||
		!strings.Contains(calls, "`TestIssue1847_AScheduledRunWritesItsOutput`: E2E Nightly: failed in #3 (https://x/runs/30) and again in https://github.com/txn2/example/actions/runs/99") {
		t.Errorf("a criterion failing in two tracking issues was not listed with both runs:\n%s", calls)
	}
	if strings.Contains(calls, "`TestLongAgo`:") {
		t.Errorf("a failure older than the window was counted:\n%s", calls)
	}
}

func TestScheduledReport_AFlakyCriterionIsListedOnce(t *testing.T) {
	root := scheduledReportRepo(t)
	env := reportGH(t, root, map[string]string{
		"issues-all.json": `[{"number": 3, "title": "E2E Nightly is failing"}]`,
		"issue-3.json": `{"createdAt": "` + daysAgo(3) + `", "body": "The E2E Nightly failed: https://x/runs/30\n\nFailing:\n- ` +
			"`TestA`\\n- `TestB`" + `\n", "comments": []}`,
		"flaky-open.json": `[{"number": 20, "title": "Flaky criteria"}]`,
		"issue-20.json":   `{"body": "intro\n\n- ` + "`TestA`" + `: listed before", "comments": []}`,
	})
	writeFile(t, root, "out.txt", "--- FAIL: TestA (1s)\n--- FAIL: TestB (1s)\n")

	calls := runReport(t, root, env, "failure", "out.txt", "--workflow", "E2E Nightly")
	if strings.Contains(calls, "issue create --title Flaky criteria") {
		t.Errorf("a second Flaky criteria issue was opened:\n%s", calls)
	}
	add := regexp.MustCompile(`(?s)issue comment 20 --body (.*)`).FindStringSubmatch(calls)
	if len(add) < 2 || !strings.Contains(add[1], "`TestB`") || strings.Contains(add[1], "`TestA`") {
		t.Errorf("want only TestB added to the open Flaky criteria issue:\n%s", calls)
	}
}

// workflowFile is the part of a workflow file the report rule reads.
type workflowFile struct {
	On          map[string]any         `yaml:"on"`
	Permissions any                    `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	If          string `yaml:"if"`
	Permissions any    `yaml:"permissions"`
	Steps       []struct {
		Run string `yaml:"run"`
	} `yaml:"steps"`
}

// reportsCommand is a step running the report every scheduled workflow ends in.
var reportsCommand = regexp.MustCompile(`scripts/(scheduled_report|e2e_nightly)\.py report\b`)

// grantsIssuesWrite reports whether a permissions block lets a job write issues.
func grantsIssuesWrite(perms any) bool {
	switch p := perms.(type) {
	case string:
		return p == "write-all"
	case map[string]any:
		return p["issues"] == "write"
	}
	return false
}

// TestEveryScheduledWorkflowReportsItsResult fails when a workflow under
// .github/workflows has a schedule: trigger and no report job: one running
// the report, on main, on success and on failure, with issues: write on that
// job alone (#2730). The weekly Load run failed every week from August to
// November and filed nothing (#2729).
func TestEveryScheduledWorkflowReportsItsResult(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	require(t, err)
	scheduled := 0
	for _, path := range files {
		data, err := os.ReadFile(path) // #nosec G304 -- a workflow file the glob above found
		require(t, err)
		var wf workflowFile
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if _, ok := wf.On["schedule"]; !ok {
			continue
		}
		scheduled++
		name := filepath.Base(path)
		if grantsIssuesWrite(wf.Permissions) {
			t.Errorf("%s grants issues: write to every job; only its report job may hold it", name)
		}
		reporters := 0
		for id, job := range wf.Jobs {
			reports := false
			for _, step := range job.Steps {
				reports = reports || reportsCommand.MatchString(step.Run)
			}
			if !reports {
				if grantsIssuesWrite(job.Permissions) {
					t.Errorf("%s: job %s holds issues: write and does not report", name, id)
				}
				continue
			}
			reporters++
			onBoth := strings.Contains(job.If, "always()") ||
				(strings.Contains(job.If, "success()") && strings.Contains(job.If, "failure()"))
			if !onBoth || !strings.Contains(job.If, "refs/heads/main") {
				t.Errorf("%s: report job %s runs if %q; it must run on success and on failure, on main only", name, id, job.If)
			}
			if !grantsIssuesWrite(job.Permissions) {
				t.Errorf("%s: report job %s has no issues: write", name, id)
			}
		}
		if reporters == 0 {
			t.Errorf("%s runs on a schedule and has no report job running scripts/scheduled_report.py report (#2730)", name)
		}
	}
	if scheduled == 0 {
		t.Fatal("no workflow has a schedule; the glob read nothing")
	}
}
