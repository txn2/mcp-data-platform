package gates

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// stepFakeMake is a make that answers each target the way one of verify's
// steps would, counting every run in $RUNS, outside the tree. lint runs
// lint-run through the runner under the key lint, as the Makefile's lint does.
const stepFakeMake = `#!/bin/sh
[ "$1" = --no-print-directory ] || exit 9
echo run >> "$RUNS"
case "$2" in
  good) echo "ran good" ;;
  bad1) echo "ran bad1: the first defect"; exit 1 ;;
  bad2) echo "ran bad2: the second defect"; exit 3 ;;
  write) echo "ran write"; echo built > out.txt ;;
  lint) exec python3 scripts/verify-step.py --key lint ./fake-make lint-run ;;
  lint-run)
    if [ -f crash ]; then echo "golangci-lint: killed"; exit 2; fi
    if [ -f findings ]; then printf 'src/a.go:1: errcheck\n2 issues:\n* errcheck: 2\n'; exit 2; fi
    echo "0 issues." ;;
  *) echo "no target $2"; exit 2 ;;
esac
`

// stepTableFixture is a scripts/verify-steps.tsv naming the fixture's steps
// and what each reads.
const stepTableFixture = "# fixture\n" +
	"preamble\tgood\t1.0\t-\tsrc\n" +
	"preamble\tbad1\t1.0\t-\tsrc\n" +
	"preamble\tbad2\t1.0\t-\tsrc\n" +
	"preamble\talways\t1.0\t-\t-\n" +
	"preamble\twrite\t1.0\t-\tsrc out:out.txt\n" +
	"preamble\tlint\t1.0\t-\tsrc replay\n"

// stepRepo is a repository holding the real runner, the fixture table, the
// fake make, an input under src/ and a file no step reads under other/. It
// returns the root and the file the fake make counts runs in.
func stepRepo(t *testing.T) (root, runs string) {
	t.Helper()
	root = newGitRepo(t, "scripts/verify-step.py", map[string]string{
		"scripts/verify-steps.tsv": stepTableFixture,
		"fake-make":                stepFakeMake,
		"Makefile":                 "all:\n",
		"src/a.go":                 "package a\n",
		"other/b.txt":              "unread\n",
		".gitignore":               "build/\nout.txt\ncrash\nfindings\n",
	})
	require(t, os.Chmod(filepath.Join(root, "fake-make"), 0o700)) // #nosec G302 -- a test fixture the test runs
	return root, filepath.Join(t.TempDir(), "runs")
}

// step runs the runner with args in root and returns its output, whether it
// passed, and how many times the fake make has run so far.
func step(t *testing.T, root, runs string, args []string, env ...string) (out string, passed bool, ran int) {
	t.Helper()
	out, ok := runScript(t, root, append([]string{"scripts/verify-step.py"}, args...), append(env, "RUNS="+runs))
	data, err := os.ReadFile(runs) // #nosec G304 -- a counter file under t.TempDir()
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out, ok, strings.Count(string(data), "run\n")
}

func assertRuns(t *testing.T, what string, got, want int, out string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: the step has run %d times, want %d\n%s", what, got, want, out)
	}
}

// TestVerifyStepSkipsAStepWhoseInputsPassed pins #2548's second ask: a step
// that passed records a digest of its inputs, a later run on the same inputs
// skips it and says so, and a change to one input (tracked, untracked, the
// Makefile) runs it again while a change to a file it does not read does not.
// VERIFY_FULL=1 runs it whatever the record says.
func TestVerifyStepSkipsAStepWhoseInputsPassed(t *testing.T) {
	t.Parallel()
	root, runs := stepRepo(t)
	good := []string{"./fake-make", "good"}

	out, ok, n := step(t, root, runs, good)
	assertOutput(t, out, ok, expect{pass: true, want: []string{"ran good", "[step good: ", "s ok]"}})
	assertRuns(t, "first run", n, 1, out)

	out, ok, n = step(t, root, runs, good)
	assertOutput(t, out, ok, expect{pass: true, want: []string{"[step good: skipped, its inputs passed at "}, mustNot: []string{"ran good"}})
	assertRuns(t, "unchanged inputs", n, 1, out)

	writeFile(t, root, "other/b.txt", "changed, and no step reads it\n")
	_, _, n = step(t, root, runs, good)
	assertRuns(t, "a file the step does not read changed", n, 1, out)

	for i, change := range []struct{ rel, body string }{
		{"src/a.go", "package a\n\nvar X = 1\n"},
		{"src/new.go", "package a\n"},
		{"Makefile", "all:\n\t@true\n"},
	} {
		writeFile(t, root, change.rel, change.body)
		out, ok, n = step(t, root, runs, good)
		assertOutput(t, out, ok, expect{pass: true, want: []string{"ran good"}})
		assertRuns(t, change.rel+" changed", n, 2+i, out)
	}

	out, _, n = step(t, root, runs, good, "VERIFY_FULL=1")
	assertRuns(t, "VERIFY_FULL=1", n, 5, out)

	// A step whose inputs column is "-" runs every time.
	step(t, root, runs, []string{"./fake-make", "always"})
	_, _, n = step(t, root, runs, []string{"./fake-make", "always"})
	assertRuns(t, "a step that always runs", n, 7, out)
}

// TestVerifyStepRunsAgainWhenItsOutputIsGone pins out: in the inputs column:
// a step another step reads the output of (test writes coverage.out) is not
// skipped once that output has changed or gone, since the reader would read
// something the pass did not write.
func TestVerifyStepRunsAgainWhenItsOutputIsGone(t *testing.T) {
	t.Parallel()
	root, runs := stepRepo(t)
	write := []string{"./fake-make", "write"}

	step(t, root, runs, write)
	out, _, n := step(t, root, runs, write)
	assertRuns(t, "output in place", n, 1, out)

	require(t, os.Remove(filepath.Join(root, "out.txt")))
	out, ok, n := step(t, root, runs, write)
	assertOutput(t, out, ok, expect{pass: true, want: []string{"ran write"}})
	assertRuns(t, "output removed", n, 2, out)
}

// TestVerifyStepKeepsGoingAndReportsEveryFailure pins #2548's first ask: a
// preamble step that fails is recorded and lets the next run, and the report
// after the last one fails, naming every failed step with its output and no
// step that passed. --begin clears the record, so a run reports its own.
func TestVerifyStepKeepsGoingAndReportsEveryFailure(t *testing.T) {
	t.Parallel()
	root, runs := stepRepo(t)

	_, ok, _ := step(t, root, runs, []string{"--begin"})
	if !ok {
		t.Fatal("--begin failed")
	}
	for _, target := range []string{"bad1", "good", "bad2"} {
		out, ok, _ := step(t, root, runs, []string{"--keep-going", "./fake-make", target})
		if !ok {
			t.Fatalf("--keep-going %s stopped the run:\n%s", target, out)
		}
	}
	out, ok, _ := step(t, root, runs, []string{"--report"})
	assertOutput(t, out, ok, expect{
		pass: false,
		want: []string{
			"2 step(s) failed: bad1, bad2",
			"--- bad1: failed (1)", "ran bad1: the first defect",
			"--- bad2: failed (3)", "ran bad2: the second defect",
			"The lanes do not start after a failed step.",
		},
		mustNot: []string{"--- good", "ran good"},
	})

	// Without --keep-going a failure is the step's exit code, as before.
	out, ok, _ = step(t, root, runs, []string{"./fake-make", "bad1"})
	assertOutput(t, out, ok, expect{pass: false, want: []string{"[step bad1: ", "s failed (1)]"}})

	step(t, root, runs, []string{"--begin"})
	out, ok, _ = step(t, root, runs, []string{"--report"})
	assertOutput(t, out, ok, expect{pass: true, mustNot: []string{"failed"}})
}

// TestVerifyStepReplaysALintFailure pins #2548's fifth ask: lint on inputs
// that already failed with findings reports that failure without running, on
// inputs that already passed answers at once, and a failure with no findings
// (the linter killed, a base it could not reach) is run again rather than
// replayed.
func TestVerifyStepReplaysALintFailure(t *testing.T) {
	t.Parallel()
	root, runs := stepRepo(t)
	lint := []string{"--key", "lint", "./fake-make", "lint-run"}

	writeFile(t, root, "findings", "")
	out, ok, n := step(t, root, runs, lint)
	assertOutput(t, out, ok, expect{pass: false, want: []string{"2 issues:", "[step lint: "}})
	assertRuns(t, "first lint", n, 1, out)

	out, ok, n = step(t, root, runs, lint)
	assertOutput(t, out, ok, expect{pass: false, want: []string{"its inputs failed at", "not run again", "src/a.go:1: errcheck", "2 issues:", "replayed"}})
	assertRuns(t, "the same failing inputs", n, 1, out)

	writeFile(t, root, "src/a.go", "package a\n\n// fixed\n")
	require(t, os.Remove(filepath.Join(root, "findings")))
	out, ok, n = step(t, root, runs, lint)
	assertOutput(t, out, ok, expect{pass: true, want: []string{"0 issues."}})
	assertRuns(t, "fixed inputs", n, 2, out)

	out, ok, n = step(t, root, runs, lint)
	assertOutput(t, out, ok, expect{pass: true, want: []string{"[step lint: skipped"}})
	assertRuns(t, "inputs that passed", n, 2, out)

	writeFile(t, root, "src/a.go", "package a\n\n// crash\n")
	writeFile(t, root, "crash", "")
	step(t, root, runs, lint)
	out, _, n = step(t, root, runs, lint)
	assertRuns(t, "a failure with no findings is run again", n, 4, out)
}

// TestVerifyAndMakeLintShareOneRecord pins #2548's fifth ask from the other
// side: verify runs lint as a step, and the Makefile's lint runs lint-run
// under the key lint. The outer runner marks the key it runs, so the inner one
// runs lint-run with no second digest and no second line; and a lint run by
// hand afterwards finds the record verify's run left.
func TestVerifyAndMakeLintShareOneRecord(t *testing.T) {
	t.Parallel()
	root, runs := stepRepo(t)

	out, ok, n := step(t, root, runs, []string{"./fake-make", "lint"})
	assertOutput(t, out, ok, expect{pass: true, want: []string{"0 issues.", "[step lint: "}})
	if c := strings.Count(out, "[step lint"); c != 1 {
		t.Errorf("verify's lint printed %d step lines, want 1:\n%s", c, out)
	}
	// The fake counts its own lint target and the lint-run it execs.
	assertRuns(t, "verify's lint", n, 2, out)

	out, ok, n = step(t, root, runs, []string{"--key", "lint", "./fake-make", "lint-run"})
	assertOutput(t, out, ok, expect{pass: true, want: []string{"[step lint: skipped"}})
	assertRuns(t, "a lint by hand after verify's", n, 2, out)

	out, ok, n = step(t, root, runs, []string{"--key", "lint", "./fake-make", "lint-run"}, "VERIFY_STEP=lint")
	assertOutput(t, out, ok, expect{pass: true, want: []string{"0 issues."}, mustNot: []string{"[step "}})
	assertRuns(t, "inside a step running under the key", n, 3, out+" ("+strconv.Itoa(n)+")")
}
