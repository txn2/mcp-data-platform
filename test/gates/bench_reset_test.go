package gates

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestBenchRunsResetWhatAPriorRunLeft is #2380: every bench target that drives
// benchrun against a running stack truncates BENCH_RUN_RESET first, and that
// list holds everything keyed by identity that search can hand back to the
// same pool identity on the next run. The identity pool repeats identities on
// every run, so a table left out is a prior run's answers in the next run's
// search results.
func TestBenchRunsResetWhatAPriorRunLeft(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(raw)

	m := regexp.MustCompile(`(?m)^BENCH_RUN_RESET := (.+)$`).FindStringSubmatch(makefile)
	if m == nil {
		t.Fatal("the Makefile defines no BENCH_RUN_RESET")
	}
	reset := map[string]bool{}
	for table := range strings.SplitSeq(m[1], ",") {
		reset[strings.TrimSpace(table)] = true
	}
	for _, table := range []string{
		"search_gate_discovery", // the search-first gate's discovery scopes
		"audit_logs",            // the session read model behind search source "sessions"
		"call_records",          // search source "calls"
	} {
		if !reset[table] {
			t.Errorf("BENCH_RUN_RESET does not truncate %s", table)
		}
	}

	for _, target := range []string{"bench-run", "bench-lifecycle", "bench-supersede", "bench-cold-start"} {
		lines := recipe(t, makefile, target)
		reset, run := -1, -1
		for i, l := range lines {
			if strings.Contains(l, "TRUNCATE $(BENCH_RUN_RESET)") && reset < 0 {
				reset = i
			}
			if strings.HasPrefix(l, "$(BUILD_DIR)/benchrun") && run < 0 {
				run = i
			}
		}
		switch {
		case reset < 0:
			t.Errorf("%s does not truncate $(BENCH_RUN_RESET)", target)
		case run >= 0 && run < reset:
			t.Errorf("%s runs benchrun before it truncates $(BENCH_RUN_RESET)", target)
		}
	}
}
