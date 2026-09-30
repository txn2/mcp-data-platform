package gates

import (
	"os"
	"strings"
	"testing"
)

// recipe returns the lines of one Makefile target's recipe.
func recipe(t *testing.T, makefile, target string) []string {
	t.Helper()
	var lines []string
	in := false
	for line := range strings.SplitSeq(makefile, "\n") {
		switch {
		case strings.HasPrefix(line, target+":"):
			in = true
		case in && strings.HasPrefix(line, "\t"):
			if !strings.HasPrefix(strings.TrimSpace(line), "@#") {
				lines = append(lines, strings.TrimSpace(line))
			}
		case in:
			return lines
		}
	}
	if !in {
		t.Fatalf("no %s target in the Makefile", target)
	}
	return lines
}

// indexOf returns the position of the first recipe line naming step.
func indexOf(lines []string, step string) int {
	for i, l := range lines {
		if strings.Contains(l, " "+step) || strings.HasSuffix(l, step) {
			return i
		}
	}
	return -1
}

// stepAt returns the position of the recipe line that runs exactly step, so
// schedule-lane is not found on the line that runs schedule-lane-ui.
func stepAt(lines []string, step string) int {
	for i, l := range lines {
		if strings.HasSuffix(l, " "+step) {
			return i
		}
	}
	return -1
}

// TestVerifyReportsTheCheapGatesFirst pins #1856: the diff-scoped gates run in
// verify's serial preamble, before the lanes fan out, and the Go lane runs the
// changed-package schedule lane before the whole module's unit run, so either
// kind of failure is reported in minutes rather than at the end of a run.
func TestVerifyReportsTheCheapGatesFirst(t *testing.T) {
	raw, err := os.ReadFile("../../Makefile")
	require(t, err)
	makefile := string(raw)

	verify := recipe(t, makefile, "verify")
	fast, lanes := indexOf(verify, "preverify-fast"), indexOf(verify, "verify-checks")
	if fast < 0 || lanes < 0 || fast > lanes {
		t.Errorf("verify runs preverify-fast at %d and the lanes at %d; the cheap gates must come first", fast, lanes)
	}

	// Lint runs serially after the fast gates and before the lanes, and not
	// again inside them.
	lint := -1
	for i, l := range verify {
		if strings.HasSuffix(l, "--no-print-directory lint") {
			lint = i
		}
	}
	if lint < fast || lint > lanes {
		t.Errorf("verify runs lint at %d, preverify-fast at %d and the lanes at %d; lint must run between them", lint, fast, lanes)
	}
	for _, l := range recipe(t, makefile, "verify-lint") {
		if strings.HasSuffix(l, "--no-print-directory lint") {
			t.Errorf("verify-lint still runs lint inside the lanes")
		}
	}

	for _, gate := range []string{"semgrep-diff", "doc-check", "acceptance-check", "state-readers-check", "e2e-copy-check", "dead-code"} {
		if indexOf(recipe(t, makefile, "preverify-fast"), gate) < 0 {
			t.Errorf("preverify-fast does not run %s", gate)
		}
		if indexOf(recipe(t, makefile, "verify-go"), gate) >= 0 {
			t.Errorf("verify-go still runs %s after the unit run", gate)
		}
	}

	// Both halves of the schedule lane run in preverify, Go first (#1929): a
	// vitest file that fails only beside the full suite otherwise passes a
	// green preverify and fails verify's UI lane. The RealDB tests of the
	// packages a branch can break follow (#1947): preverify otherwise compiles
	// no test behind the integration build tag.
	pre := recipe(t, makefile, "preverify")
	g, u, r := stepAt(pre, "schedule-lane"), stepAt(pre, "schedule-lane-ui"), stepAt(pre, "realdb-lane")
	if g < 0 || u < 0 || g > u {
		t.Errorf("preverify runs schedule-lane at %d and schedule-lane-ui at %d; both must run, Go first", g, u)
	}
	if r < 0 || r < u {
		t.Errorf("preverify runs realdb-lane at %d; it must run, after the schedule lane (%d)", r, u)
	}
	if !strings.Contains(strings.Join(recipe(t, makefile, "test-realdb"), "\n"), "$(REALDB_PKGS)") {
		t.Errorf("test-realdb does not run $(REALDB_PKGS), so realdb-lane cannot narrow it")
	}

	goLane := recipe(t, makefile, "verify-go")
	if s, u := indexOf(goLane, "schedule-lane"), indexOf(goLane, "test"); s < 0 || u < 0 || s > u {
		t.Errorf("verify-go runs schedule-lane at %d and test at %d; the changed packages must come first", s, u)
	}
}

// TestVerifyReleaseRunsInOneInvocation pins #1969: verify-release runs verify,
// whose frontend-e2e wants port 5173, and the acceptance suite, which wants a
// running dev stack. It passes as one command only because frontend-e2e moves
// beside a dev server it finds there instead of refusing, and the acceptance
// step starts a stack when none answers.
func TestVerifyReleaseRunsInOneInvocation(t *testing.T) {
	raw, err := os.ReadFile("../../Makefile")
	require(t, err)
	makefile := string(raw)

	var prereqs string
	for line := range strings.SplitSeq(makefile, "\n") {
		if rest, ok := strings.CutPrefix(line, "verify-release:"); ok {
			prereqs = " " + rest + " "
		}
	}
	if !strings.Contains(prereqs, " acceptance-release ") || strings.Contains(prereqs, " acceptance ") {
		t.Errorf("verify-release prerequisites are %q; it must run acceptance-release, not acceptance", strings.TrimSpace(prereqs))
	}
	if !strings.Contains(strings.Join(recipe(t, makefile, "acceptance-release"), "\n"), "scripts/release-acceptance.sh") {
		t.Error("acceptance-release does not run scripts/release-acceptance.sh")
	}
	e2e := strings.Join(recipe(t, makefile, "frontend-e2e"), "\n")
	if !strings.Contains(e2e, "E2E_PORT=$$port npm run test:e2e") || !strings.Contains(e2e, "next=5199") {
		t.Error("frontend-e2e no longer runs on a free port beside a dev server it finds on :5173")
	}
}
