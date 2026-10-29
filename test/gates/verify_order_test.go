package gates

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// stepRunner is the command every step of verify and its lanes runs through,
// which prints what the step cost (#2369).
const stepRunner = "python3 scripts/verify-step.py $(MAKE) "

// timedStep is one row of scripts/verify-steps.tsv.
type timedStep struct {
	group, name, after string
	seconds            float64
}

// stepTable reads scripts/verify-steps.tsv, keeping each group's rows in order.
func stepTable(t *testing.T) map[string][]timedStep {
	t.Helper()
	groups := map[string][]timedStep{}
	for n, line := range strings.Split(readRepoFile(t, "scripts/verify-steps.tsv"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			t.Fatalf("scripts/verify-steps.tsv line %d has %d columns, want 4: %q", n+1, len(f), line)
		}
		secs, err := strconv.ParseFloat(f[2], 64)
		if err != nil {
			t.Fatalf("scripts/verify-steps.tsv line %d: seconds %q: %v", n+1, f[2], err)
		}
		groups[f[0]] = append(groups[f[0]], timedStep{group: f[0], name: f[1], seconds: secs, after: f[3]})
	}
	return groups
}

// steps returns the targets a recipe runs through the step runner, in order,
// and fails on a recipe line that runs a target without it.
func steps(t *testing.T, makefile, target string) []string {
	t.Helper()
	var out []string
	for _, l := range recipe(t, makefile, target) {
		if name, ok := strings.CutPrefix(strings.TrimPrefix(l, "@"), stepRunner); ok {
			out = append(out, name)
			continue
		}
		if strings.Contains(l, "--no-print-directory") && !strings.Contains(l, "-j4 verify-checks") {
			t.Errorf("%s runs %q without %s, so its cost is not printed", target, l, stepRunner)
		}
	}
	return out
}

// assertGroupOrder checks that a recipe runs exactly the table's steps for the
// group, in the table's order, and that the table runs them cheapest first,
// apart from a step that reads another step's output.
func assertGroupOrder(t *testing.T, target string, ran []string, rows []timedStep, ascending bool) {
	t.Helper()
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.name
	}
	if !slices.Equal(ran, names) {
		t.Errorf("%s runs\n  %v\nscripts/verify-steps.tsv lists\n  %v", target, ran, names)
	}
	last := -1.0
	for i, r := range rows {
		if r.after != "-" {
			if j := slices.Index(names, r.after); j < 0 || j > i {
				t.Errorf("%s: %s reads %s's output but runs before it", target, r.name, r.after)
			}
			continue
		}
		if ascending && r.seconds < last {
			t.Errorf("%s: %s (%.1fs) runs after a step that costs %.1fs; the cheapest step runs first", target, r.name, r.seconds, last)
		}
		last = max(last, r.seconds)
	}
}

// TestVerifyRunsTheCheapestStepFirst pins #2369: verify and each of its lanes
// run their steps in the order scripts/verify-steps.tsv records, the table is
// in ascending order of measured cost (a step reading another's output aside,
// and the steps that rewrite the tree, which run first in their own order), and
// every step prints its cost.
func TestVerifyRunsTheCheapestStepFirst(t *testing.T) {
	raw, err := os.ReadFile("../../Makefile")
	require(t, err)
	makefile := string(raw)
	table := stepTable(t)

	verify := steps(t, makefile, "verify")
	rewrite := len(table["rewrite"])
	if len(verify) < rewrite {
		t.Fatalf("verify runs %v; it must start with the %d steps that rewrite the tree", verify, rewrite)
	}
	assertGroupOrder(t, "verify (rewrite)", verify[:rewrite], table["rewrite"], false)
	assertGroupOrder(t, "verify (preamble)", verify[rewrite:], table["preamble"], true)
	for _, lane := range []string{"verify-go", "verify-static", "verify-docker", "verify-ui"} {
		assertGroupOrder(t, lane, steps(t, makefile, lane), table[lane], true)
	}
	if len(table) != 6 {
		t.Errorf("scripts/verify-steps.tsv has groups %v; want rewrite, preamble and the four lanes", slices.Sorted(maps.Keys(table)))
	}
	if !strings.Contains(strings.Join(recipe(t, makefile, "verify"), "\n"), "-j4 verify-checks") {
		t.Error("verify no longer starts its lanes together")
	}
	if strings.Contains(makefile, "\npreverify") {
		t.Error("the Makefile still has a preverify target; verify is the one gate (#2369)")
	}

	// Lint runs once, in the preamble, not again inside a lane.
	for _, l := range recipe(t, makefile, "verify-static") {
		if strings.HasSuffix(l, " lint") {
			t.Errorf("verify-static still runs lint inside the lanes")
		}
	}
	// The UI lint is decided in the preamble (#2030) and runs once.
	if stepAt(recipe(t, makefile, "verify-ui"), "frontend-lint") >= 0 {
		t.Error("verify-ui still runs frontend-lint, which the preamble already ran")
	}
	if !strings.Contains(strings.Join(recipe(t, makefile, "test-realdb"), "\n"), "$(REALDB_PKGS)") {
		t.Errorf("test-realdb does not run $(REALDB_PKGS), so realdb-lane cannot narrow it")
	}
	assertOneUnitRun(t, makefile)
	assertStaticChecksInALane(t, makefile)
}

// TestVerifyStepPrintsItsCost pins #2369's runner: a step's cost is printed
// as it ends, and its exit code is verify's.
func TestVerifyStepPrintsItsCost(t *testing.T) {
	root := newGitRepo(t, "scripts/verify-step.py", map[string]string{
		"fake-make": "#!/bin/sh\n[ \"$1\" = --no-print-directory ] || exit 9\necho \"ran $2\"\n[ \"$2\" = good ]\n",
	})
	require(t, os.Chmod(filepath.Join(root, "fake-make"), 0o700)) // #nosec G302 -- a test fixture the test runs
	fake := filepath.Join(root, "fake-make")

	out, ok := runScript(t, root, []string{"scripts/verify-step.py", fake, "good"}, nil)
	assertOutput(t, out, ok, expect{pass: true, want: []string{"ran good", "[step good: ", "s ok]"}})

	out, ok = runScript(t, root, []string{"scripts/verify-step.py", fake, "bad"}, nil)
	assertOutput(t, out, ok, expect{pass: false, want: []string{"ran bad", "[step bad: ", "s failed (1)]"}})
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

// TestVerifyChecksMigrationsOnBothMajors pins #2239: verify runs the
// migration gate on every pinned PostgreSQL major in its preamble when the
// diff touches a migration, the step reads the diff (tracked and untracked) to
// decide, and the Docker lane does not check the same migrations again (#2369).
func TestVerifyChecksMigrationsOnBothMajors(t *testing.T) {
	data, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)
	if stepAt(recipe(t, makefile, "verify"), "migrate-check-diff") < 0 {
		t.Fatal("verify does not run migrate-check-diff")
	}
	if stepAt(recipe(t, makefile, "verify-docker"), "migrate-check") >= 0 || stepAt(recipe(t, makefile, "verify-docker"), "migrate-check-lane") < 0 {
		t.Error("verify-docker runs migrate-check unconditionally; a migration the preamble checked is checked twice")
	}
	_, macro, found := strings.Cut(makefile, "\nmigrations_changed = ")
	macro, _, _ = strings.Cut(macro, "\nmigrate-check-diff:")
	for _, want := range []string{"git diff --name-only", "git ls-files --others", "$(MIGRATIONS_DIR)"} {
		if !found || !strings.Contains(macro, want) {
			t.Errorf("migrations_changed does not contain %q, so it cannot decide from the diff", want)
		}
	}
	diff := strings.Join(recipe(t, makefile, "migrate-check-diff"), "\n")
	lane := strings.Join(recipe(t, makefile, "migrate-check-lane"), "\n")
	if !strings.Contains(diff, "$(migrations_changed)") || !strings.Contains(diff, `[ -z "$$changed" ]`) || !strings.Contains(diff, "migrate-check") {
		t.Errorf("migrate-check-diff does not run migrate-check on a changed migration:\n%s", diff)
	}
	if !strings.Contains(lane, "$(migrations_changed)") || !strings.Contains(lane, `[ -n "$$changed" ]`) || !strings.Contains(lane, "migrate-check") {
		t.Errorf("migrate-check-lane does not run migrate-check only when the preamble did not:\n%s", lane)
	}
	if !strings.Contains(makefile, "MIGRATIONS_DIR := pkg/database/migrate/migrations") {
		t.Error("MIGRATIONS_DIR does not name the migration directory")
	}
	if !strings.Contains(makefile, "MIGRATE_PG_IMAGES") {
		t.Error("migrate-check no longer runs over MIGRATE_PG_IMAGES")
	}
}

// TestVerifyRunsTheLaneGatesFirstOnTheDiff pins #2186: three gates that failed
// verify in its lanes on the #2171-#2174 branch run first in its preamble,
// scoped to the diff. The standalone gosec is invoked as verify-static invokes
// it, the route-template gate runs alone and only on a changed registration,
// and the mocked-route conformance test runs by itself.
func TestVerifyRunsTheLaneGatesFirstOnTheDiff(t *testing.T) {
	raw, err := os.ReadFile("../../Makefile")
	require(t, err)
	makefile := string(raw)

	pre := recipe(t, makefile, "verify")
	for _, gate := range []string{"security-diff", "route-template-diff", "route-conformance-check"} {
		if stepAt(pre, gate) < 0 {
			t.Errorf("verify does not run %s", gate)
		}
	}
	if got := strings.Join(recipe(t, makefile, "route-conformance-check"), "\n"); !strings.Contains(got, "vitest run src/mocks/route-conformance.test.ts") {
		t.Errorf("route-conformance-check runs %q, not the conformance test alone", got)
	}

	gosec := strings.Join(recipe(t, makefile, "security-diff"), "\n")
	if !strings.Contains(gosec, "verify-diff.py gosec-packages") || !strings.Contains(gosec, "GOOS=linux gosec $$pkgs") {
		t.Errorf("security-diff does not run verify's gosec over the changed packages:\n%s", gosec)
	}
	if !strings.Contains(strings.Join(recipe(t, makefile, "security"), "\n"), "GOOS=linux gosec ./...") {
		t.Error("security no longer runs GOOS=linux gosec ./...; security-diff must run the same invocation")
	}

	routes := strings.Join(recipe(t, makefile, "route-template-diff"), "\n")
	for _, want := range []string{"verify-diff.py routes-changed", "REALDB_PKGS=./internal/httpserver/", "REALDB_RUN=TestEveryRouteReportsItsTemplate_RealDB"} {
		if !strings.Contains(routes, want) {
			t.Errorf("route-template-diff does not contain %q", want)
		}
	}
	if !strings.Contains(strings.Join(recipe(t, makefile, "test-realdb"), "\n"), "-run '$(REALDB_RUN)'") {
		t.Error("test-realdb does not run -run '$(REALDB_RUN)', so route-template-diff cannot narrow it")
	}
}

// TestVerifySmokesAChangedDevStack pins #2248 and #2369: verify runs dev-smoke
// when the diff (tracked and untracked) changes a file under dev/ and skips it
// otherwise, and dev-smoke fails, saying so, when no stack answers.
func TestVerifySmokesAChangedDevStack(t *testing.T) {
	raw, err := os.ReadFile("../../Makefile")
	require(t, err)
	makefile := string(raw)

	if stepAt(recipe(t, makefile, "verify"), "dev-smoke-diff") < 0 {
		t.Fatal("verify does not run dev-smoke-diff")
	}
	step := strings.Join(recipe(t, makefile, "dev-smoke-diff"), "\n")
	for _, want := range []string{"git diff --name-only", "git ls-files --others", "$(DEV_DIR)", "skipped", "dev-smoke"} {
		if !strings.Contains(step, want) {
			t.Errorf("dev-smoke-diff does not contain %q, so it cannot decide from the diff", want)
		}
	}
	if !strings.Contains(makefile, "DEV_DIR := dev\n") {
		t.Error("DEV_DIR does not name the dev stack's directory")
	}
	smoke := strings.Join(recipe(t, makefile, "dev-smoke"), "\n")
	for _, want := range []string{"/healthz", "no dev stack answers", "exit 1", "-run '^TestDevSmoke_'"} {
		if !strings.Contains(smoke, want) {
			t.Errorf("dev-smoke does not contain %q", want)
		}
	}
}

// TestRealDBLaneReportsItsConnections pins #2249: the real-database lane's
// server is started with the limit the Makefile sets from measurement, and
// every run reports its peak connections beside it.
func TestRealDBLaneReportsItsConnections(t *testing.T) {
	raw, err := os.ReadFile("../../Makefile")
	require(t, err)
	makefile := string(raw)

	lane := strings.Join(recipe(t, makefile, "test-realdb"), "\n")
	for _, want := range []string{"max_connections=$(REALDB_MAX_CONNECTIONS)", "scripts/realdb-connections.py $(REALDB_PG_CONTAINER)", "trap finish EXIT", "-p 4"} {
		if !strings.Contains(lane, want) {
			t.Errorf("test-realdb does not contain %q", want)
		}
	}
	if !strings.Contains(makefile, "REALDB_MAX_CONNECTIONS ?= ") {
		t.Error("REALDB_MAX_CONNECTIONS is not set")
	}
}
