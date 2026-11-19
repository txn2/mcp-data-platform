package gates

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// gateStep is one measured cost in the local gate.
type gateStep struct {
	name    string
	minutes float64
}

// verifyMinutes is verify's measured wall clock from scripts/verify-steps.tsv:
// the serial steps (rewrite and preamble) in turn, then the slowest lane, the
// static and UI groups being one lane (verify-static-ui). It returns the total
// and the steps that make it up.
func verifyMinutes(t *testing.T, table string) (float64, []gateStep) {
	t.Helper()
	groups := map[string][]gateStep{}
	for line := range strings.SplitSeq(table, "\n") {
		cols := strings.Split(line, "\t")
		if strings.HasPrefix(line, "#") || len(cols) < 3 {
			continue
		}
		secs, err := strconv.ParseFloat(cols[2], 64)
		if err != nil {
			t.Fatalf("verify-steps.tsv: %q has no seconds: %v", line, err)
		}
		group := cols[0]
		if group == "verify-static" || group == "verify-ui" {
			group = "verify-static-ui"
		}
		groups[group] = append(groups[group], gateStep{name: group + "/" + cols[1], minutes: secs / 60})
	}
	sum := func(steps []gateStep) (m float64) {
		for _, s := range steps {
			m += s.minutes
		}
		return m
	}
	steps := append(append([]gateStep{}, groups["rewrite"]...), groups["preamble"]...)
	total := sum(steps)
	slowest := ""
	for _, lane := range []string{"verify-go", "verify-static-ui", "verify-docker"} {
		if slowest == "" || sum(groups[lane]) > sum(groups[slowest]) {
			slowest = lane
		}
	}
	return total + sum(groups[slowest]), append(steps, groups[slowest]...)
}

// acceptanceRuns reads scripts/acceptance-runs.tsv: the budget and the newest
// run's tickets and minutes (zero when none is recorded).
func acceptanceRuns(t *testing.T, table string) (budget float64, tickets int, minutes float64) {
	t.Helper()
	for line := range strings.SplitSeq(table, "\n") {
		cols := strings.Split(line, "\t")
		switch {
		case cols[0] == "budget" && len(cols) == 2:
			b, err := strconv.ParseFloat(cols[1], 64)
			if err != nil {
				t.Fatalf("acceptance-runs.tsv: budget %q: %v", cols[1], err)
			}
			budget = b
		case cols[0] == "run" && len(cols) == 4:
			n, err := strconv.Atoi(cols[2])
			if err != nil {
				t.Fatalf("acceptance-runs.tsv: tickets %q: %v", cols[2], err)
			}
			m, err := strconv.ParseFloat(cols[3], 64)
			if err != nil {
				t.Fatalf("acceptance-runs.tsv: minutes %q: %v", cols[3], err)
			}
			tickets, minutes = n, m
		}
	}
	if budget <= 0 {
		t.Fatal("scripts/acceptance-runs.tsv states no budget row")
	}
	return budget, tickets, minutes
}

// TestLocalGateTimeIsWithinBudget holds #2710: the local gate time a change
// requires is verify plus the recordings acceptance-check demands, and both
// are measured. On #2709 the recordings took about four of the day's 4h50m of
// gate time and nothing failed when they grew.
func TestLocalGateTimeIsWithinBudget(t *testing.T) {
	steps, err := os.ReadFile("../../scripts/verify-steps.tsv")
	require(t, err)
	runs, err := os.ReadFile("../../scripts/acceptance-runs.tsv")
	require(t, err)
	verify, parts := verifyMinutes(t, string(steps))
	budget, tickets, recording := acceptanceRuns(t, string(runs))
	if total := verify + recording; total > budget {
		sort.Slice(parts, func(i, j int) bool { return parts[i].minutes > parts[j].minutes })
		var b strings.Builder
		for _, p := range parts {
			_, _ = fmt.Fprintf(&b, "\n  %-48s %6.1f min", p.name, p.minutes)
		}
		_, _ = fmt.Fprintf(&b, "\n  %-48s %6.1f min", fmt.Sprintf("acceptance recording (%d tickets)", tickets), recording)
		t.Errorf("the local gate takes %.1f minutes, over the %.0f-minute budget in scripts/acceptance-runs.tsv:%s",
			total, budget, b.String())
	}
}

// TestVerifyMinutesTakesTheSlowestLane checks the arithmetic the budget is
// judged by: serial steps add, lanes overlap.
func TestVerifyMinutesTakesTheSlowestLane(t *testing.T) {
	table := "# comment\nrewrite\tfmt\t60\t-\t-\npreamble\tlint\t120\t-\t-\n" +
		"verify-go\ttest\t600\t-\t.\nverify-static\tdead-code\t60\t-\t.\nverify-ui\tfrontend-e2e\t600\t-\tui\n" +
		"verify-docker\ttest-realdb\t300\t-\t.\n"
	got, parts := verifyMinutes(t, table)
	if want := 3.0 + 11.0; got != want {
		t.Fatalf("verify minutes = %v, want %v (serial 3 + the static-ui lane 11)", got, want)
	}
	if len(parts) != 4 {
		t.Fatalf("parts = %v, want the two serial steps and the slowest lane's two", parts)
	}
}

// TestSplitRecordsTheRunsCost: `make acceptance` pipes its stream into split,
// which appends the run's ticket count and minutes and keeps the newest 20.
func TestSplitRecordsTheRunsCost(t *testing.T) {
	root := newRepo(t)
	runs := filepath.Join(root, "scripts", "acceptance-runs.tsv")
	var seed strings.Builder
	_, _ = seed.WriteString("# head\nbudget\t45\n")
	for i := range 20 {
		_, _ = fmt.Fprintf(&seed, "run\t2026-11-0%dT00:00:00Z\t1\t%d\n", i%9+1, i)
	}
	require(t, os.WriteFile(runs, []byte(seed.String()), 0o600))

	stream := `{"Time":"2026-11-19T02:00:00Z","Action":"run","Test":"TestIssue1_A"}
{"Time":"2026-11-19T02:01:30Z","Action":"pass","Test":"TestIssue1_A"}
{"Time":"2026-11-19T02:03:00Z","Action":"pass","Test":"TestIssue2_B"}
`
	cmd := exec.CommandContext(t.Context(), "python3", filepath.Join(root, scriptRelPath), "split") //nolint:gosec // the script under a temp root this test wrote
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stream)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("split: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(runs) //nolint:gosec // a path under t.TempDir
	require(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if lines[0] != "# head" || lines[1] != "budget\t45" {
		t.Fatalf("the head was not kept: %q", lines[:2])
	}
	var rows []string
	for _, l := range lines {
		if strings.HasPrefix(l, "run\t") {
			rows = append(rows, l)
		}
	}
	if len(rows) != 20 {
		t.Fatalf("%d run rows kept, want 20", len(rows))
	}
	last := strings.Split(rows[len(rows)-1], "\t")
	if last[2] != "2" || last[3] != "3.0" {
		t.Fatalf("the newest row is %q, want 2 tickets in 3.0 minutes", rows[len(rows)-1])
	}
	if strings.Contains(string(raw), "\t1\t0\n") {
		t.Fatal("the oldest row was kept past the newest 20")
	}
}

// TestCheckEstimatesTheRecordingTime: acceptance-check says how many tickets
// it requires locally and what they will cost at the measured minutes per
// ticket.
func TestCheckEstimatesTheRecordingTime(t *testing.T) {
	root := newRepo(t)
	require(t, os.WriteFile(filepath.Join(root, "scripts", "acceptance-runs.tsv"),
		[]byte("budget\t45\nrun\t2026-11-18T00:00:00Z\t4\t8.0\nrun\t2026-11-18T01:00:00Z\t6\t12.0\n"), 0o600))
	a := writeCriteria(t, root, "7", "TestIssue7_A")
	b := writeCriteria(t, root, "8", "TestIssue8_A")
	out, _ := gate(t, root, "check", a, b)
	want := "2 ticket(s) require a local recording; at 2.0 minutes per ticket"
	if !strings.Contains(out, want) || !strings.Contains(out, "about 4 minutes") {
		t.Fatalf("check did not estimate the recording time; want %q and about 4 minutes:\n%s", want, out)
	}
}
