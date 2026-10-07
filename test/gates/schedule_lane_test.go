package gates

// scripts/schedule-lane.py runs the Test functions in the _test.go files a
// change touched at -race -cpu=1,2 -count=5 (#1711), because a test whose
// assertion depends on goroutine ordering passes one run at the developer
// machine's CPU count and fails a loaded CI runner. What it must never do is
// pass such a test. The unit is the changed test, not its package (#2039):
// a package's untouched tests ran once in `make test` and are not repeated.
//
// The fixtures stand in for an ordering-dependent test deterministically: one
// fails only when GOMAXPROCS is 1, one only on its third run in a process. A
// real race would make the control itself flaky, which is the defect the lane
// exists to catch.

import "testing"

const laneScriptRelPath = "scripts/schedule-lane.py"

const (
	// failsAtOneCPU passes wherever the scheduler has a second CPU, which is
	// every machine `make test` runs on.
	failsAtOneCPU = `package ordering

import (
	"runtime"
	"testing"
)

func TestFailsAtOneCPU(t *testing.T) {
	if runtime.GOMAXPROCS(0) == 1 {
		t.Fatal("the other goroutine had not run")
	}
}
`
	// failsOnThirdRun passes the single run `make test` gives it.
	failsOnThirdRun = `package third

import "testing"

var runs int

func TestFailsOnThirdRun(t *testing.T) {
	runs++
	if runs == 3 {
		t.Fatal("state left behind by an earlier run")
	}
}
`
	steady = `package steady

import "testing"

func TestSteady(t *testing.T) { _ = t }
`
	// broken is committed on the base branch: a package the change did not
	// touch is not the lane's to run, whatever state it is in.
	broken = `package broken

import "testing"

func TestBroken(t *testing.T) { t.Fatal("not touched by the change") }
`
	// untouchedSibling is committed on the base branch beside a test the change
	// adds: a test in a changed package that the change did not write is not
	// repeated either (#2039).
	untouchedSibling = `package steady

import "testing"

func TestUntouchedSibling(t *testing.T) { t.Fatal("not written by the change") }
`
	// exitsMidTest dies without a `--- FAIL`: a runtime fatal error, a
	// `race:` runtime abort and an os.Exit all leave the test running with no
	// terminal event, and what it printed is the only clue to the cause.
	exitsMidTest = `package crash

import (
	"fmt"
	"os"
	"testing"
)

func TestExitsMidTest(t *testing.T) {
	fmt.Println("the last thing the test said")
	os.Exit(3)
}
`

	// sourceOnly is a change to a package's non-test file.
	sourceOnly = `package steady

// Answer is what the change edits.
const Answer = 42
`
)

// newLaneRepo is a Go module with a package the change will not touch.
func newLaneRepo(t *testing.T) string {
	t.Helper()
	return newGitRepo(t, laneScriptRelPath, map[string]string{
		"go.mod":                 "module example.com/lanefixture\n\ngo 1.22\n",
		"broken/broken_test.go":  broken,
		"steady/sibling_test.go": untouchedSibling,
		"steady/steady.go":       "package steady\n",
	})
}

// lane runs the Go lane in root.
func lane(t *testing.T, root string, env ...string) (string, bool) {
	t.Helper()
	return runScript(t, root, []string{laneScriptRelPath, "go"},
		append([]string{"GOWORK=off", "GOTOOLCHAIN=local", "GOFLAGS="}, env...))
}

func TestScheduleLane(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		files    map[string]string
		baseEnvs []string
		expect
	}{
		{
			name:  "a test that fails only at one CPU fails the lane with its reproduction",
			files: map[string]string{"ordering/ordering_test.go": failsAtOneCPU},
			expect: expect{want: []string{
				"FAIL TestFailsAtOneCPU in example.com/lanefixture/ordering: 5 of 5 runs failed at -cpu=1",
				"reproduce: go test -race -cpu=1 -count=300 -run '^TestFailsAtOneCPU$' ./ordering/",
			}, mustNot: []string{"failed at -cpu=2", "TestBroken"}},
		},
		{
			name:  "a test that fails only on a repeated run fails the lane at every CPU setting",
			files: map[string]string{"third/third_test.go": failsOnThirdRun},
			expect: expect{want: []string{
				"FAIL TestFailsOnThirdRun in example.com/lanefixture/third: 1 of 5 runs failed at -cpu=1",
				"FAIL TestFailsOnThirdRun in example.com/lanefixture/third: 1 of 5 runs failed at -cpu=2",
				"reproduce: go test -race -cpu=2 -count=300 -run '^TestFailsOnThirdRun$' ./third/",
			}},
		},
		{
			name:  "a binary that dies mid-test is reported with the test it died in and what that test printed",
			files: map[string]string{"crash/crash_test.go": exitsMidTest},
			expect: expect{want: []string{
				"FAIL example.com/lanefixture/crash at -cpu=1 with no failing test",
				"the binary exited during TestExitsMidTest; its output before that:",
				"the last thing the test said",
			}},
		},
		{
			name:  "a deterministic test passes; a package the change did not touch and a test it did not write are not run",
			files: map[string]string{"steady/steady_test.go": steady},
			expect: expect{pass: true, want: []string{
				"schedule-lane: example.com/lanefixture/steady: 1 test(s) x 5 at -cpu=1 in",
				"schedule-lane: 1 changed test(s) in 1 package(s) passed 5 runs at -cpu=1,2.",
			}, mustNot: []string{"FAIL", "TestBroken", "TestUntouchedSibling"}},
		},
		{
			name:   "a change to a package's source alone repeats none of its tests",
			files:  map[string]string{"steady/steady.go": sourceOnly},
			expect: expect{pass: true, want: []string{"no test function changed against main; nothing to run."}, mustNot: []string{"TestUntouchedSibling"}},
		},
		{
			name:   "a change with no Go package runs nothing",
			files:  map[string]string{"README.md": "text\n"},
			expect: expect{pass: true, want: []string{"no test function changed against main; nothing to run."}},
		},
		{
			name:     "a base branch that cannot be resolved fails rather than skipping",
			files:    map[string]string{"steady/steady_test.go": steady},
			baseEnvs: []string{"BASE_BRANCH=no-such-branch"},
			expect:   expect{want: []string{"FAIL schedule-lane: cannot resolve base branch 'no-such-branch'"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := newLaneRepo(t)
			for rel, body := range tc.files {
				writeFile(t, root, rel, body)
			}
			out, passed := lane(t, root, tc.baseEnvs...)
			assertOutput(t, out, passed, tc.expect)
		})
	}
}
