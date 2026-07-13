package gen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

func TestGenerateDeterministic(t *testing.T) {
	a, b := Generate(), Generate()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("Generate is not deterministic")
	}
	if a.TrinoSQL() != b.TrinoSQL() || a.KnowledgePagesSQL() != b.KnowledgePagesSQL() {
		t.Fatal("emitters are not deterministic")
	}
	am, _ := a.DataHubMCEs()
	bm, _ := b.DataHubMCEs()
	if string(am) != string(bm) {
		t.Fatal("mce emitter is not deterministic")
	}
}

func TestTrapInvariants(t *testing.T) {
	ds := Generate()
	gross, net := ds.topRegionGrossAll(), ds.TopRegionNet2025()
	if gross == net {
		t.Fatalf("gross leader %q must differ from net leader %q", gross, net)
	}
	if ds.enterpriseOrderCount() == 0 {
		t.Fatal("no enterprise orders")
	}
	// Ground truths must be non-trivial (nonzero and distinct) so a task is
	// never accidentally answerable by zero or by another task's value.
	values := []float64{ds.TotalAmountQ1USD(), ds.AvgAmountEnterpriseUSD(), ds.NetEastMarchUSD(), ds.NetTotal2025USD()}
	seen := map[float64]bool{}
	for _, v := range values {
		if v <= 0 {
			t.Errorf("ground truth %v not positive", v)
		}
		if seen[v] {
			t.Errorf("duplicate ground truth %v", v)
		}
		seen[v] = true
	}
}

func TestTasksAreValid(t *testing.T) {
	ds := Generate()
	tasks := ds.Tasks()
	if len(tasks) != 87 {
		t.Fatalf("phase-2 task set = %d tasks, want 87", len(tasks))
	}
	suites := map[string]int{}
	arms := map[string]int{}
	for _, tk := range tasks {
		if err := tk.Validate(); err != nil {
			t.Errorf("task %s invalid: %v", tk.ID, err)
		}
		suites[tk.Suite]++
		for _, a := range tk.Arms {
			arms[a]++
		}
	}
	if suites["s1"] != 17 || suites["s2"] != 45 || suites["s3"] != 25 {
		t.Errorf("suite split %v, want 17 s1 / 45 s2 / 25 s3", suites)
	}
	// Every task must run under all four arms (the ablation is the config).
	for _, a := range []string{"a0", "a1", "a2", "a3"} {
		if arms[a] != len(tasks) {
			t.Errorf("arm %s applies to %d tasks, want all %d", a, arms[a], len(tasks))
		}
	}
}

// TestTrapClassCoverage asserts every phase-2 trap class is represented in S3.
func TestTrapClassCoverage(t *testing.T) {
	ds := Generate()
	seen := map[string]int{}
	for _, tk := range ds.Tasks() {
		for _, c := range tk.TrapClasses {
			seen[c]++
		}
	}
	for _, class := range []string{"units_cents", "net_revenue", "fiscal_calendar", "freshness_cutoff", "tier_boundary", "deprecated_table"} {
		if seen[class] == 0 {
			t.Errorf("trap class %q has no tasks", class)
		}
	}
}

func TestScriptedSmokeCoversAllTasks(t *testing.T) {
	ds := Generate()
	tasks := ds.Tasks()
	script := ScriptedSmoke(tasks)
	for _, tk := range tasks {
		steps, ok := script[tk.ID]
		if !ok || len(steps) == 0 {
			t.Errorf("no smoke steps for %s", tk.ID)
			continue
		}
		last := steps[len(steps)-1]
		if last.FinalText == "" {
			t.Errorf("%s: smoke script does not end in a final answer", tk.ID)
		}
		switch {
		case tk.Grading.Kind == task.GradeExecSQL:
			// Exec-SQL tasks answer with the reference SQL itself.
			if !strings.Contains(last.FinalText, tk.ExpectedSQL) {
				t.Errorf("%s: exec_sql task must answer with its reference SQL", tk.ID)
			}
		case tk.ExpectedSQL != "":
			if !strings.Contains(last.FinalText, "{{last_result}}") {
				t.Errorf("%s: sql-backed task must answer from the live result", tk.ID)
			}
		}
	}
}

// TestCommittedArtifactsMatch is the reproducibility gate: the committed seed
// artifacts and task set must regenerate byte-identically from the fixed seed.
func TestCommittedArtifactsMatch(t *testing.T) {
	ds := Generate()
	root := "../.."
	mces, err := ds.DataHubMCEs()
	if err != nil {
		t.Fatal(err)
	}
	compareFile(t, filepath.Join(root, "seed/trino/setup.sql"), []byte(ds.TrinoSQL()))
	compareFile(t, filepath.Join(root, "seed/datahub/bench_mces.json"), mces)
	compareFile(t, filepath.Join(root, "seed/postgres/knowledge_pages.sql"), []byte(ds.KnowledgePagesSQL()))

	committed, err := task.Load(filepath.Join(root, "tasks"))
	if err != nil {
		t.Fatalf("load committed tasks: %v", err)
	}
	if got, want := task.Hash(committed), task.Hash(ds.Tasks()); got != want {
		t.Errorf("committed task set hash %s != regenerated %s; run `make bench-gen`", got, want)
	}
	smoke, err := json.MarshalIndent(ScriptedSmoke(ds.Tasks()), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareFile(t, filepath.Join(root, "tasks/scripted-smoke.json"), append(smoke, '\n'))
}

func compareFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path) // #nosec G304 -- repo-relative test fixture
	if err != nil {
		t.Fatalf("read %s: %v (run `make bench-gen`)", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from regeneration; run `make bench-gen`", path)
	}
}

func TestEmittedContentCarriesTraps(t *testing.T) {
	ds := Generate()
	sql := ds.TrinoSQL()
	for _, table := range []string{"customers", "orders", "legacy_orders", "daily_region_revenue"} {
		if !strings.Contains(sql, "CREATE TABLE memory.bench."+table) {
			t.Errorf("trino sql missing table %s", table)
		}
	}
	mces, _ := ds.DataHubMCEs()
	for _, needle := range []string{"US CENTS", "deprecation", "GROSS of discounts", "2025-11-30"} {
		if !strings.Contains(string(mces), needle) {
			t.Errorf("mces missing %q", needle)
		}
	}
	kp := ds.KnowledgePagesSQL()
	for _, needle := range []string{
		"revenue-reporting-policy", "bench-warehouse-guide", "fiscal-calendar-policy",
		"customer-tier-definitions", "ON CONFLICT",
	} {
		if !strings.Contains(kp, needle) {
			t.Errorf("knowledge pages missing %q", needle)
		}
	}
}
