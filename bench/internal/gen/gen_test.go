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
	if len(tasks) != 10 {
		t.Fatalf("pilot task set = %d tasks, want 10", len(tasks))
	}
	suites := map[string]int{}
	for _, tk := range tasks {
		if err := tk.Validate(); err != nil {
			t.Errorf("task %s invalid: %v", tk.ID, err)
		}
		suites[tk.Suite]++
	}
	if suites["s1"] != 5 || suites["s3"] != 5 {
		t.Errorf("suite split %v, want 5 s1 / 5 s3", suites)
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
		if tk.ExpectedSQL != "" && !strings.Contains(last.FinalText, "{{last_result}}") {
			t.Errorf("%s: sql-backed task must answer from the live result", tk.ID)
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
	for _, needle := range []string{"US CENTS", "deprecation", "GROSS of discounts"} {
		if !strings.Contains(string(mces), needle) {
			t.Errorf("mces missing %q", needle)
		}
	}
	kp := ds.KnowledgePagesSQL()
	for _, needle := range []string{"revenue-reporting-policy", "bench-warehouse-guide", "ON CONFLICT"} {
		if !strings.Contains(kp, needle) {
			t.Errorf("knowledge pages missing %q", needle)
		}
	}
}
