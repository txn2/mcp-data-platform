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

	for _, gate := range []string{"semgrep-diff", "doc-check", "acceptance-check", "state-readers-check", "dead-code"} {
		if indexOf(recipe(t, makefile, "preverify-fast"), gate) < 0 {
			t.Errorf("preverify-fast does not run %s", gate)
		}
		if indexOf(recipe(t, makefile, "verify-go"), gate) >= 0 {
			t.Errorf("verify-go still runs %s after the unit run", gate)
		}
	}

	goLane := recipe(t, makefile, "verify-go")
	if s, u := indexOf(goLane, "schedule-lane"), indexOf(goLane, "test"); s < 0 || u < 0 || s > u {
		t.Errorf("verify-go runs schedule-lane at %d and test at %d; the changed packages must come first", s, u)
	}
}
