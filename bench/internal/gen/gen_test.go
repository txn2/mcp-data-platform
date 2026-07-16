package gen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/curriculum"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
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
	ae, _ := a.DataHubMCEsEmpty()
	be, _ := b.DataHubMCEsEmpty()
	if string(ae) != string(be) {
		t.Fatal("empty mce emitter is not deterministic")
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

// TestEmptyMCEsAreBare asserts the cold-start baseline carries the entities but
// none of the knowledge the A2 seed does: every bench table is present as a
// datasetProperties skeleton, and no description, column doc, tag, or
// deprecation leaks in (any of those would let a fresh identity answer a trap
// before the curriculum teaches it, flattening the learning curve).
func TestEmptyMCEsAreBare(t *testing.T) {
	ds := Generate()
	raw, err := ds.DataHubMCEsEmpty()
	if err != nil {
		t.Fatal(err)
	}
	var proposals []map[string]any
	if err := json.Unmarshal(raw, &proposals); err != nil {
		t.Fatalf("empty mces are not valid json: %v", err)
	}
	if len(proposals) != len(benchTables) {
		t.Fatalf("empty seed has %d proposals, want one per table (%d)", len(proposals), len(benchTables))
	}
	present := map[string]bool{}
	for _, p := range proposals {
		if aspect := p["aspectName"]; aspect != "datasetProperties" {
			t.Errorf("empty seed emits aspect %v, want only datasetProperties", aspect)
		}
		urn, _ := p["entityUrn"].(string)
		present[urn] = true
		aspect, _ := p["aspect"].(map[string]any)
		body, _ := aspect["json"].(map[string]any)
		if desc, _ := body["description"].(string); desc != "" {
			t.Errorf("%s carries a description in the empty seed: %q", urn, desc)
		}
	}
	for _, table := range benchTables {
		if !present[benchURN(table)] {
			t.Errorf("empty seed missing entity for %s", table)
		}
	}
	// None of the A2 knowledge markers may appear in the bare baseline.
	for _, needle := range []string{"US CENTS", "deprecation", "GROSS of discounts", "urn:li:tag:bench", "editableSchemaMetadata"} {
		if strings.Contains(string(raw), needle) {
			t.Errorf("empty seed leaks A2 knowledge marker %q", needle)
		}
	}
}

// TestScriptedColdStartSmokeCoversUnits asserts the smoke has a teach playback
// for every lesson and an eval playback ending in a final answer for every eval
// task — so one scripted run exercises the whole cold-start loop.
func TestScriptedColdStartSmokeCoversUnits(t *testing.T) {
	ds := Generate()
	cur := ds.Curriculum()
	var evalTasks []task.Task
	for _, tk := range ds.Tasks() {
		if tk.Suite == cur.EvalSuite {
			evalTasks = append(evalTasks, tk)
		}
	}
	smoke := ScriptedColdStartSmoke(cur, evalTasks)
	for _, l := range cur.Lessons {
		steps := smoke[l.ID]["teach"]
		if len(steps) == 0 || len(steps[0].ToolCalls) == 0 || steps[0].ToolCalls[0].Name != "memory_capture" {
			t.Errorf("lesson %s teach must open with a memory_capture", l.ID)
		}
	}
	for _, tk := range evalTasks {
		steps := smoke[tk.ID]["eval"]
		if len(steps) == 0 || steps[len(steps)-1].FinalText == "" {
			t.Errorf("eval task %s must end in a final answer", tk.ID)
		}
	}
	if len(evalTasks) == 0 {
		t.Fatal("no eval tasks found for the smoke")
	}
}

// TestCurriculumPageLessonsMatchSeedPages asserts every page-sink lesson's page
// payload (slug, title, summary, body) equals the a2 seed row for the same
// slug, so a promoted page is byte-identical to the documented baseline — the
// summary especially, because search renders a page hit as title plus summary
// and the a3 tool surface has no page-body fetch tool.
func TestCurriculumPageLessonsMatchSeedPages(t *testing.T) {
	bySlug := map[string]kpRow{}
	for _, r := range knowledgePageRows() {
		bySlug[r.slug] = r
	}
	pageLessons := 0
	for _, l := range Generate().Curriculum().Lessons {
		if l.Sink != protocol.SinkKnowledgePage {
			continue
		}
		pageLessons++
		row, ok := bySlug[l.Page.Slug]
		if !ok {
			t.Errorf("lesson %s promotes page %q, which is not an a2 seed page", l.ID, l.Page.Slug)
			continue
		}
		if l.Page.Title != row.title || l.Page.Summary != row.summary || l.Page.Body != row.body {
			t.Errorf("lesson %s page payload diverges from the a2 seed row for slug %q", l.ID, l.Page.Slug)
		}
		if l.Page.Summary == "" {
			t.Errorf("lesson %s page has an empty summary", l.ID)
		}
	}
	if pageLessons != 3 {
		t.Errorf("expected 3 page-sink lessons, found %d", pageLessons)
	}
}

// TestProtocolPagePayloadsCarrySummaries asserts every page-sink protocol sends
// a fact-bearing summary, so the promoted page's fact is deliverable through
// search on the a3 tool surface (the #958 page-sink transfer gap).
func TestProtocolPagePayloadsCarrySummaries(t *testing.T) {
	pageSinks := 0
	for _, p := range Generate().Protocols() {
		if p.Sink != protocol.SinkKnowledgePage {
			continue
		}
		pageSinks++
		if p.Page == nil || p.Page.Summary == "" {
			t.Errorf("protocol %s page payload has no summary", p.ID)
			continue
		}
		if p.Page.Summary != p.Fact {
			t.Errorf("protocol %s page summary %q is not its one-sentence fact", p.ID, p.Page.Summary)
		}
	}
	if pageSinks == 0 {
		t.Fatal("no page-sink protocols found")
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
	emptyMCEs, err := ds.DataHubMCEsEmpty()
	if err != nil {
		t.Fatal(err)
	}
	compareFile(t, filepath.Join(root, "seed/trino/setup.sql"), []byte(ds.TrinoSQL()))
	compareFile(t, filepath.Join(root, "seed/datahub/bench_mces.json"), mces)
	compareFile(t, filepath.Join(root, "seed/datahub/bench_mces_empty.json"), emptyMCEs)
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

	committedCur, err := curriculum.Load(filepath.Join(root, "curriculum"))
	if err != nil {
		t.Fatalf("load committed curriculum: %v", err)
	}
	regen := []curriculum.Curriculum{ds.Curriculum()}
	if got, want := curriculum.Hash(committedCur), curriculum.Hash(regen); got != want {
		t.Errorf("committed curriculum hash %s != regenerated %s; run `make bench-gen`", got, want)
	}

	var evalTasks []task.Task
	for _, tk := range ds.Tasks() {
		if tk.Suite == ds.Curriculum().EvalSuite {
			evalTasks = append(evalTasks, tk)
		}
	}
	csSmoke, err := json.MarshalIndent(ScriptedColdStartSmoke(ds.Curriculum(), evalTasks), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareFile(t, filepath.Join(root, "curriculum/scripted-cold-start-smoke.json"), append(csSmoke, '\n'))
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
