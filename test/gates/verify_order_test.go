package gates

import (
	"os"
	"slices"
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
	for _, l := range recipe(t, makefile, "verify-static") {
		if strings.HasSuffix(l, "--no-print-directory lint") {
			t.Errorf("verify-static still runs lint inside the lanes")
		}
	}

	for _, gate := range []string{"semgrep-diff", "semgrep-changed", "doc-check", "docs-build", "acceptance-check", "state-readers-check", "builtin-pages-check", "e2e-copy-check", "route-manifest-check", "dead-code"} {
		if indexOf(recipe(t, makefile, "preverify-fast"), gate) < 0 {
			t.Errorf("preverify-fast does not run %s", gate)
		}
		if indexOf(recipe(t, makefile, "verify-go"), gate) >= 0 {
			t.Errorf("verify-go still runs %s after the unit run", gate)
		}
	}

	// The API description and the UI lint are decided in preverify-fast
	// (#2030), and run once in verify: an annotation edited after the last
	// `make swagger`, or a UI function over the complexity limit, otherwise
	// passes preverify and fails verify. swagger-check comes first because
	// it rewrites internal/apidocs, which the conversion and
	// state-readers-check read.
	fastGates := recipe(t, makefile, "preverify-fast")
	sw, conv, st := stepAt(fastGates, "swagger-check"), stepAt(fastGates, "apidocs-openapi3-check"), stepAt(fastGates, "state-readers-check")
	if sw != 0 || conv < sw || st < conv {
		t.Errorf("preverify-fast runs swagger-check at %d, apidocs-openapi3-check at %d and state-readers-check at %d; swagger-check must be first, then the conversion, then the readers", sw, conv, st)
	}
	if stepAt(fastGates, "frontend-lint") < 0 {
		t.Error("preverify-fast does not run frontend-lint")
	}
	for _, gate := range []string{"swagger-check", "apidocs-openapi3-check"} {
		if stepAt(verify, gate) >= 0 {
			t.Errorf("verify runs %s itself as well as through preverify-fast", gate)
		}
	}
	if stepAt(recipe(t, makefile, "verify-ui"), "frontend-lint") >= 0 {
		t.Error("verify-ui still runs frontend-lint, which preverify-fast already ran")
	}

	// preverify runs nothing verify runs again in full (#2039). The schedule
	// lane, its UI half and the real-DB lane used to follow lint here (#1929,
	// #1947); on a change to pkg/platform they cost as much as verify, so the
	// pre-gate doubled the time to a commit instead of saving a run. They run
	// once, in verify and in CI.
	pre := recipe(t, makefile, "preverify")
	for _, lane := range []string{"schedule-lane", "schedule-lane-ui", "realdb-lane"} {
		if stepAt(pre, lane) >= 0 {
			t.Errorf("preverify runs %s, which verify runs again in full; the pre-gate must stay cheap (#2039)", lane)
		}
	}
	if stepAt(pre, "lint") < 0 {
		t.Error("preverify does not run lint")
	}
	if !strings.Contains(strings.Join(recipe(t, makefile, "test-realdb"), "\n"), "$(REALDB_PKGS)") {
		t.Errorf("test-realdb does not run $(REALDB_PKGS), so realdb-lane cannot narrow it")
	}

	goLane := recipe(t, makefile, "verify-go")
	if s, u := indexOf(goLane, "schedule-lane"), indexOf(goLane, "test"); s < 0 || u < 0 || s > u {
		t.Errorf("verify-go runs schedule-lane at %d and test at %d; the changed packages must come first", s, u)
	}
	assertOneUnitRun(t, makefile)
	assertStaticChecksInALane(t, makefile)
}

// assertOneUnitRun pins #2053's first ask: a verify prints "Running tests..."
// once. Every $(MAKE) line is its own invocation, so a step whose target
// lists `test` as a prerequisite runs the race unit suite again; verify-go
// runs `test` once and only steps that read the coverage.out it wrote.
func assertOneUnitRun(t *testing.T, makefile string) {
	t.Helper()
	runs := 0
	for _, step := range recipe(t, makefile, "verify-go") {
		target := step[strings.LastIndex(step, " ")+1:]
		if target == "test" {
			runs++
			continue
		}
		if slices.Contains(prerequisites(makefile, target), "test") {
			t.Errorf("verify-go runs %s, which runs the unit suite again through its test prerequisite", target)
		}
	}
	if runs != 1 {
		t.Errorf("verify-go runs test %d times; it must run once", runs)
	}
	for _, target := range []string{"coverage-report", "patch-coverage"} {
		if !slices.Contains(prerequisites(makefile, target), "test") {
			t.Errorf("make %s run alone no longer runs the tests first", target)
		}
	}
}

// assertStaticChecksInALane pins #2053's second ask: gosec, govulncheck and
// the whole-tree semgrep run in a lane verify-checks starts with the others,
// not after the unit run in verify-go.
func assertStaticChecksInALane(t *testing.T, makefile string) {
	t.Helper()
	lanes := prerequisites(makefile, "verify-checks")
	if !slices.Contains(lanes, "verify-static-ui") || stepAt(recipe(t, makefile, "verify-static-ui"), "verify-static") != 0 {
		t.Fatalf("verify-checks starts %v; the static checks need a lane that starts with them", lanes)
	}
	static := recipe(t, makefile, "verify-static")
	for _, gate := range []string{"security", "semgrep"} {
		if stepAt(static, gate) < 0 {
			t.Errorf("verify-static does not run %s", gate)
		}
		if stepAt(recipe(t, makefile, "verify-go"), gate) >= 0 {
			t.Errorf("verify-go still runs %s after the unit run", gate)
		}
	}
}

// TestVerifyStartsTheUILaneAfterTheStaticLane pins #2243: verify-ui starts
// once verify-static has finished, in the same lane, so vitest's 5-second
// per-test timeout is not run against every lane starting at once; and that
// lane still runs beside verify-go and verify-docker rather than after them.
func TestVerifyStartsTheUILaneAfterTheStaticLane(t *testing.T) {
	raw, err := os.ReadFile("../../Makefile")
	require(t, err)
	makefile := string(raw)

	lanes := prerequisites(makefile, "verify-checks")
	for _, want := range []string{"verify-go", "verify-docker", "verify-static-ui"} {
		if !slices.Contains(lanes, want) {
			t.Errorf("verify-checks starts %v; %s must be one of its concurrent lanes", lanes, want)
		}
	}
	for _, alone := range []string{"verify-ui", "verify-static"} {
		if slices.Contains(lanes, alone) {
			t.Errorf("verify-checks starts %s beside the other lanes; it belongs to verify-static-ui", alone)
		}
	}
	lane := recipe(t, makefile, "verify-static-ui")
	static, ui := stepAt(lane, "verify-static"), stepAt(lane, "verify-ui")
	if static < 0 || ui < 0 || ui < static {
		t.Errorf("verify-static-ui runs %v; it must run verify-static and then verify-ui", lane)
	}
	for _, line := range lane {
		if strings.Contains(line, " -j") {
			t.Errorf("verify-static-ui line %q runs its steps concurrently", line)
		}
	}
	if !strings.Contains(strings.Join(recipe(t, makefile, "verify"), "\n"), "-j4 verify-checks") {
		t.Error("verify no longer runs verify-checks concurrently")
	}
}

// prerequisites returns the names after a target's colon.
func prerequisites(makefile, target string) []string {
	for line := range strings.SplitSeq(makefile, "\n") {
		if rest, ok := strings.CutPrefix(line, target+":"); ok {
			return strings.Fields(rest)
		}
	}
	return nil
}

// TestVerifyReleaseRunsInOneInvocation pins #1969 and #2218: verify-release
// runs verify, whose frontend-e2e wants port 5173 and moves beside a dev
// server it finds there instead of refusing, and requires the release's own
// tickets' recorded acceptance runs. It never runs the whole suite, which runs
// for well over an hour: that is the E2E Nightly's, through acceptance-release,
// which starts a stack when none answers.
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
	if strings.Contains(prereqs, " acceptance-release ") || strings.Contains(prereqs, " acceptance ") ||
		!strings.Contains(prereqs, " acceptance-release-check ") {
		t.Errorf("verify-release prerequisites are %q; it must require acceptance-release-check and run no whole suite", strings.TrimSpace(prereqs))
	}
	if !strings.Contains(strings.Join(recipe(t, makefile, "acceptance-release"), "\n"), "scripts/release-acceptance.sh") {
		t.Error("acceptance-release does not run scripts/release-acceptance.sh")
	}
	e2e := strings.Join(recipe(t, makefile, "frontend-e2e"), "\n")
	if !strings.Contains(e2e, "E2E_PORT=$$port npm run test:e2e") || !strings.Contains(e2e, "next=5199") {
		t.Error("frontend-e2e no longer runs on a free port beside a dev server it finds on :5173")
	}
}

// TestPreverifyChecksMigrationsOnBothMajors pins #2239: preverify runs the
// migration gate on every pinned PostgreSQL major when the diff touches a
// migration, and the step reads the diff (tracked and untracked) to decide.
func TestPreverifyChecksMigrationsOnBothMajors(t *testing.T) {
	data, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)
	if stepAt(recipe(t, makefile, "preverify"), "migrate-check-diff") < 0 {
		t.Fatal("preverify does not run migrate-check-diff")
	}
	step := strings.Join(recipe(t, makefile, "migrate-check-diff"), "\n")
	for _, want := range []string{"git diff --name-only", "git ls-files --others", "$(MIGRATIONS_DIR)", "migrate-check"} {
		if !strings.Contains(step, want) {
			t.Errorf("migrate-check-diff does not contain %q, so it cannot decide from the diff", want)
		}
	}
	if !strings.Contains(makefile, "MIGRATIONS_DIR := pkg/database/migrate/migrations") {
		t.Error("MIGRATIONS_DIR does not name the migration directory")
	}
	if !strings.Contains(makefile, "MIGRATE_PG_IMAGES") {
		t.Error("migrate-check no longer runs over MIGRATE_PG_IMAGES")
	}
}
