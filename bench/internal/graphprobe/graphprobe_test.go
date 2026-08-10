package graphprobe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// testPlanted maps every fixture key onto a stable id for the reading tests.
func testPlanted() Planted {
	pages := map[string]string{}
	for _, p := range graphfix.Pages() {
		pages[p.Key] = "kp_" + p.Key
	}
	return Planted{Pages: pages}
}

// completionCell fetches one fixture cell for a test.
func completionCell(t *testing.T, id string) graphfix.CompletionCell {
	t.Helper()
	for _, c := range graphfix.CompletionCells() {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("cell %s is not in the fixture", id)
	return graphfix.CompletionCell{}
}

// ref renders a fixture key as the platform reference the agent would pass.
func ref(key string) string { return "mcp:knowledge_page:kp_" + key }

// call builds an assistant turn with one tool call.
func call(id, name string, args map[string]any) llm.Message {
	return llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: id, Name: name, Args: args}}}
}

// result builds the user turn carrying one tool result.
func result(id, text string) llm.Message {
	return llm.Message{Role: "user", ToolResults: []llm.ToolResult{{CallID: id, Text: text}}}
}

// passingGate builds the smallest graph-arm gate report a run accepts; arm
// mismatch cases flip Stripped on the returned value.
func passingGate() GateReport {
	return GateReport{Results: []GateResult{{CellID: "gc-incident", Pass: true}}, Pass: true}
}

// TestReadCompletionCreditsPageProvenanceOnlyForReferencesSearchDidNotHand is
// the probe's central distinction, unchanged from the lookup instrument. A
// reference the agent could only have got from a page it had already read is
// navigation; the same reference handed over by search is not, however deep
// the page it points at sits.
func TestReadCompletionCreditsPageProvenanceOnlyForReferencesSearchDidNotHand(t *testing.T) {
	t.Parallel()
	cell := completionCell(t, "gc-incident")
	transcript := []llm.Message{
		call("s1", "mcp__bench__search", map[string]any{"intent": "ledger reconcile failures", "limit": float64(25)}),
		result("s1", `{"groups":[{"source":"knowledge_pages","hits":[{"text":"Ledger Reconcile Job Runbook","reference":"`+ref("ledger-reconcile-runbook")+`"}]}]}`),
		call("f1", "mcp__bench__fetch", map[string]any{"reference": ref("ledger-reconcile-runbook")}),
		result("f1", `{"document":{"references":[{"reference":"`+ref("incident-severity-bands")+`"}]}}`),
		call("f2", "mcp__bench__fetch", map[string]any{"reference": ref("incident-severity-bands")}),
		result("f2", `{"document":{"references":[{"reference":"`+ref("escalation-ladders")+`"}]}}`),
	}
	got := ReadCompletion(transcript, graphfix.Default(), cell, testPlanted())
	if len(got.Fetches) != 2 {
		t.Fatalf("recorded %d fetches, want 2", len(got.Fetches))
	}
	if got.Fetches[0].Provenance != ProvenanceSearch {
		t.Errorf("entry fetch provenance = %s, want %s", got.Fetches[0].Provenance, ProvenanceSearch)
	}
	if got.Fetches[1].Provenance != ProvenancePage {
		t.Errorf("hop fetch provenance = %s, want %s", got.Fetches[1].Provenance, ProvenancePage)
	}
	if !got.ReadEntry {
		t.Error("ReadEntry is false after the entry page was fetched")
	}
	if got.MaxDepthRead != 1 || got.MaxTraversalDepth != 1 {
		t.Errorf("depths = (%d, %d), want (1, 1)", got.MaxDepthRead, got.MaxTraversalDepth)
	}
	if got.ConstraintPagesRead != 2 {
		t.Errorf("ConstraintPagesRead = %d, want 2", got.ConstraintPagesRead)
	}
	if len(got.Searches) != 1 || got.Searches[0].Intent != "ledger reconcile failures" || got.Searches[0].Limit != 25 {
		t.Errorf("Searches = %+v, want the one intent with its limit", got.Searches)
	}
}

// TestReadCompletionDoesNotCreditAReferenceTheSameCallReturned guards the
// ordering rule: a fetch is classified against what was known BEFORE it, so a
// result cannot justify the call that produced it.
func TestReadCompletionDoesNotCreditAReferenceTheSameCallReturned(t *testing.T) {
	t.Parallel()
	cell := completionCell(t, "gc-export-onboarding")
	transcript := []llm.Message{
		call("f1", "mcp__bench__fetch", map[string]any{"reference": ref("clickstream-export-runbook")}),
		result("f1", `{"document":{"references":[{"reference":"`+ref("clickstream-export-runbook")+`"}]}}`),
	}
	got := ReadCompletion(transcript, graphfix.Default(), cell, testPlanted())
	if got.Fetches[0].Provenance != ProvenanceUnseen {
		t.Errorf("provenance = %s, want %s: nothing had returned that reference before the call",
			got.Fetches[0].Provenance, ProvenanceUnseen)
	}
}

// TestReadCompletionTrimsReferencePunctuation: a reference copied out of page
// prose can carry the sentence's full stop both when learned and when passed
// back. The reading must still credit the page that supplied it.
func TestReadCompletionTrimsReferencePunctuation(t *testing.T) {
	t.Parallel()
	cell := completionCell(t, "gc-export-onboarding")
	transcript := []llm.Message{
		call("f1", "mcp__bench__fetch", map[string]any{"reference": ref("clickstream-export-runbook")}),
		result("f1", "the classes are held in "+ref("storage-class-register")+"."),
		call("f2", "mcp__bench__fetch", map[string]any{"reference": ref("storage-class-register") + "."}),
		result("f2", "{}"),
	}
	got := ReadCompletion(transcript, graphfix.Default(), cell, testPlanted())
	if got.Fetches[1].Provenance != ProvenancePage {
		t.Errorf("provenance = %s, want %s", got.Fetches[1].Provenance, ProvenancePage)
	}
	if got.Fetches[1].PageKey != "storage-class-register" {
		t.Errorf("PageKey = %q, want the page the reference names", got.Fetches[1].PageKey)
	}
	if got.Fetches[1].Reference != ref("storage-class-register")+"." {
		t.Errorf("Reference = %q, want the argument the agent actually passed", got.Fetches[1].Reference)
	}
}

// TestReadCompletionCountsOffSetAndIgnoresFailedAndForeign: a read of a
// planted page outside the constraint set is browsing cost, a fetch that
// errored read nothing, and a non-corpus reference is recorded without a page.
func TestReadCompletionCountsOffSetAndIgnoresFailedAndForeign(t *testing.T) {
	t.Parallel()
	cell := completionCell(t, "gc-incident")
	transcript := []llm.Message{
		call("f1", "mcp__bench__fetch", map[string]any{"reference": ref("platform-glossary")}),
		result("f1", "{}"),
		call("f2", "mcp__bench__fetch", map[string]any{"reference": ref("duty-manager-matrix")}),
		{Role: "user", ToolResults: []llm.ToolResult{{CallID: "f2", Text: "not found", IsError: true}}},
		call("f3", "mcp__bench__fetch", map[string]any{"reference": "mcp:insight:abc"}),
		result("f3", "{}"),
	}
	got := ReadCompletion(transcript, graphfix.Default(), cell, testPlanted())
	if got.OffSetFetches != 1 {
		t.Errorf("OffSetFetches = %d, want 1 (the glossary)", got.OffSetFetches)
	}
	if got.ConstraintPagesRead != 0 {
		t.Errorf("ConstraintPagesRead = %d, want 0: the matrix fetch failed", got.ConstraintPagesRead)
	}
	if got.Fetches[2].PageKey != "" {
		t.Errorf("foreign reference PageKey = %q, want empty", got.Fetches[2].PageKey)
	}
	if got.MaxDepthRead != -1 {
		t.Errorf("MaxDepthRead = %d, want -1: the glossary is off the entry's graph", got.MaxDepthRead)
	}
}

// TestGradeCoverageGroundsInPagesRead covers the grading rules end to end:
// a signature in the document covers its constraint, coverage grounds only in
// a read source page, entry constraints tally separately, and a covered
// constraint with no source page read is unread coverage, never grounded.
func TestGradeCoverageGroundsInPagesRead(t *testing.T) {
	t.Parallel()
	cell := completionCell(t, "gc-incident")
	reading := CompletionReading{PagesRead: []string{"ledger-reconcile-runbook", "incident-severity-bands"}}
	doc := "This opens a severity band B incident. The amber route applies: the owning team " +
		"and the platform shift lead are notified, and the first rung may hold at most 20 minutes."
	got := GradeCoverage(doc, cell, reading)
	byID := map[string]ConstraintResult{}
	for _, r := range got.Constraints {
		byID[r.ID] = r
	}
	if r := byID["ic-band"]; !r.Covered || !r.Entry {
		t.Errorf("ic-band = %+v, want covered entry control", r)
	}
	if r := byID["ic-route"]; !r.Covered || !r.Grounded {
		t.Errorf("ic-route = %+v, want covered and grounded via the bands page", r)
	}
	if r := byID["ic-clock-first"]; !r.Covered || r.Grounded {
		t.Errorf("ic-clock-first = %+v, want covered but NOT grounded: the matrix was never read", r)
	}
	if r := byID["ic-record"]; r.Covered {
		t.Errorf("ic-record = %+v, want uncovered: the document never states it", r)
	}
	if got.UnreadCovered != 1 {
		t.Errorf("UnreadCovered = %d, want 1 (the clock)", got.UnreadCovered)
	}
	if got.OffEntryGrounded >= got.OffEntryCovered {
		t.Errorf("grounded %d must be below covered %d here", got.OffEntryGrounded, got.OffEntryCovered)
	}
	if got.EntryTotal == 0 || got.EntryCovered != 1 {
		t.Errorf("entry tally = %d/%d, want 1 covered", got.EntryCovered, got.EntryTotal)
	}
}

// TestGateResultReadAppliesTheLeakCondition covers the sweep gate's decision:
// a hit whose rendered text carries an off-entry signature fails the reading,
// constraint pages merely surfacing are recorded as ranks without failing,
// and a hit outside the corpus is recorded by its raw reference.
func TestGateResultReadAppliesTheLeakCondition(t *testing.T) {
	t.Parallel()
	planted := testPlanted()
	cell := completionCell(t, "gc-incident")
	hit := func(key, text string) searchHit { return searchHit{Text: text, Reference: ref(key)} }

	t.Run("surfacing constraint pages is recorded, not failed", func(t *testing.T) {
		t.Parallel()
		var got GateResult
		got.read([]searchHit{
			hit("ledger-reconcile-runbook", "Ledger Reconcile Job Runbook"),
			hit("duty-manager-matrix", "Duty Manager Response Matrix: the clocks each escalation route runs to"),
		}, cell, planted)
		if !got.Pass {
			t.Errorf("read = %+v, want a pass: surfacing is the enumeration profile", got)
		}
		if got.EntryRank != 1 || got.PageRanks["duty-manager-matrix"] != 2 {
			t.Errorf("ranks = entry #%d, matrix #%d; want #1 and #2", got.EntryRank, got.PageRanks["duty-manager-matrix"])
		}
	})

	t.Run("a signature in hit text fails", func(t *testing.T) {
		t.Parallel()
		var got GateResult
		got.read([]searchHit{
			hit("duty-manager-matrix", "Duty Manager Response Matrix: the amber second rung holds 25 minutes"),
		}, cell, planted)
		if got.Pass || len(got.Leaks) == 0 {
			t.Errorf("read = %+v, want a leak failure: search delivered a constraint without a read", got)
		}
	})

	t.Run("an entry signature in hit text is exempt", func(t *testing.T) {
		t.Parallel()
		var got GateResult
		got.read([]searchHit{
			hit("ledger-reconcile-runbook", "failures on three consecutive nights open a severity band B incident"),
		}, cell, planted)
		if !got.Pass {
			t.Errorf("read = %+v, want a pass: entry constraints are the within-episode control", got)
		}
	})

	t.Run("a hit outside the corpus is recorded by reference", func(t *testing.T) {
		t.Parallel()
		var got GateResult
		got.read([]searchHit{{Text: "something else", Reference: "mcp:asset:xyz"}}, cell, planted)
		if len(got.Hits) != 1 || got.Hits[0] != "mcp:asset:xyz" {
			t.Errorf("Hits = %v, want the raw reference", got.Hits)
		}
	})
}

// TestGateResultReadFailsASurfacedDiscontinuityPage covers the study's flip
// of the sweep from recording to requirement (#1250): a discontinuity
// constraint's source page appearing anywhere in a hit list fails the
// reading with the page and its rank recorded, while the same page surfacing
// for an ordinary constraint stays part of the enumeration profile.
func TestGateResultReadFailsASurfacedDiscontinuityPage(t *testing.T) {
	t.Parallel()
	cell := graphfix.CompletionCell{
		ID: "gs-test", EntryKey: "entry-page",
		Constraints: []graphfix.Constraint{
			{ID: "t-entry", Desc: "d", Pages: []string{"entry-page"}, Patterns: []string{`zz-entry-token`}},
			{ID: "t-disc", Desc: "d", Pages: []string{"far-calendar"}, Patterns: []string{`zz-far-token`}, Discontinuity: true},
		},
	}
	planted := Planted{Pages: map[string]string{"entry-page": "kp_entry", "far-calendar": "kp_far"}}
	var got GateResult
	got.read([]searchHit{
		{Text: "Entry Page", Reference: "mcp:knowledge_page:kp_entry"},
		{Text: "Company Calendar", Reference: "mcp:knowledge_page:kp_far"},
	}, cell, planted)
	if got.Pass {
		t.Fatal("read passed with a discontinuity source page in the hit list")
	}
	if got.DiscontinuityHits["far-calendar"] != 2 {
		t.Errorf("DiscontinuityHits = %v, want far-calendar at rank 2", got.DiscontinuityHits)
	}
	if len(got.Leaks) != 0 {
		t.Errorf("Leaks = %v, want none: the page surfaced but no signature was in hit text", got.Leaks)
	}
}

// TestGateReportSerializes keeps the archived gate shape readable by the
// reread path, which decodes the same file a run wrote.
func TestGateReportSerializes(t *testing.T) {
	t.Parallel()
	in := GateReport{RanAt: time.Now().UTC(), Stripped: true, Limits: []int{5, 25, 100}, Pass: true,
		Results: []GateResult{{
			CellID: "gc-incident", Query: "q", Limit: 25, EntryRank: 1,
			PageRanks: map[string]int{"duty-manager-matrix": 4},
			Hits:      []string{"a", "b"}, Pass: true,
		}}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out GateReport
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Pass || !out.Stripped || len(out.Results) != 1 || out.Results[0].PageRanks["duty-manager-matrix"] != 4 {
		t.Errorf("round trip lost data: %+v", out)
	}
}

// stubRunner satisfies EpisodeRunner without driving a client, recording the
// requests it received. The zero value reports a server that never connected,
// which is a harness failure.
type stubRunner struct {
	result   claudecli.Result
	requests *[]claudecli.Request
}

func (s stubRunner) Run(_ context.Context, req claudecli.Request) (claudecli.Result, error) {
	if s.requests != nil {
		*s.requests = append(*s.requests, req)
	}
	return s.result, nil
}
func (stubRunner) Model() string { return "stub" }

// TestRunRefusesAFailedGateAndAnArmMismatch: the gate is a pre-stated
// precondition, and an archive must carry the reading that gated the corpus
// it actually ran on.
func TestRunRefusesAFailedGateAndAnArmMismatch(t *testing.T) {
	t.Parallel()
	base := func() Options {
		return Options{
			Runner: stubRunner{}, Corpus: graphfix.Default(), Cells: graphfix.CompletionCells(), K: 1, OutDir: t.TempDir(),
			Planted: testPlanted(), SearchEnabled: true,
			Gate: passingGate(),
		}
	}
	opts := base()
	opts.Gate.Pass = false
	if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "fixture gate") {
		t.Fatalf("Run error = %v, want a refusal naming the fixture gate", err)
	}
	opts = base()
	opts.Gate = GateReport{}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run accepted a run with no gate reading at all")
	}
	opts = base()
	opts.Gate.Stripped = true
	if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "arm") {
		t.Fatalf("Run error = %v, want a refusal naming the arm mismatch", err)
	}
}

// TestRunWritesAnArchiveAndRefusesToOverwriteIt: a re-run must never destroy
// results already paid for in wall-clock and identities.
func TestRunWritesAnArchiveAndRefusesToOverwriteIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := Options{
		Runner: stubRunner{}, Corpus: graphfix.Default(), Cells: graphfix.CompletionCells(), K: 1,
		OutDir: dir, Planted: testPlanted(), SearchEnabled: true,
		Gate: passingGate(),
	}
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Manifest.Attempts != len(graphfix.CompletionCells()) {
		t.Errorf("Attempts = %d, want one per cell", res.Manifest.Attempts)
	}
	if !res.Manifest.Exploratory || res.Manifest.Probe != probeName || res.Manifest.Arm != "graph" {
		t.Errorf("manifest = %+v, want an exploratory %s graph-arm record", res.Manifest, probeName)
	}
	for _, a := range res.Attempts {
		if a.Error == "" {
			t.Errorf("%s: a runner that never connected should have failed the attempt", a.CellID)
		}
	}
	if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second Run error = %v, want a refusal to overwrite", err)
	}
}

// TestRunComposesTheNoSearchPrompt: the no-search arms open each prompt with
// the cell's planted entry reference and carry the no-search scaffold,
// because an episode that cannot search has no other way to hold a reference.
func TestRunComposesTheNoSearchPrompt(t *testing.T) {
	t.Parallel()
	var requests []claudecli.Request
	_, err := Run(context.Background(), Options{
		Runner: stubRunner{requests: &requests}, Corpus: graphfix.Default(), Cells: graphfix.CompletionCells()[:1], K: 1,
		OutDir: t.TempDir(), Planted: testPlanted(), SearchEnabled: false,
		Gate: passingGate(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("runner saw %d requests, want 1", len(requests))
	}
	cell := graphfix.CompletionCells()[0]
	wantRef := "mcp:knowledge_page:kp_" + cell.EntryKey
	if !strings.HasPrefix(requests[0].Prompt, cell.EntryIntro+" "+wantRef+".") {
		t.Errorf("prompt = %.120q, want it to open with the entry intro and reference", requests[0].Prompt)
	}
	if !strings.HasSuffix(requests[0].Prompt, cell.Prompt) {
		t.Error("prompt lost the cell's task text")
	}
	if requests[0].System != SystemNoSearch {
		t.Error("no-search run did not carry the no-search scaffold")
	}
	if strings.Contains(SystemNoSearch, "search") {
		t.Error("the no-search scaffold names the search tool it cannot have")
	}
}

// TestRunGradesAndArchivesAnEpisode covers the loop end to end on a scripted
// episode: coverage is graded and grounded, the reading is classified, the
// transcript and final document are archived, and RereadCompletion reproduces
// the same numbers offline.
func TestRunGradesAndArchivesAnEpisode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cell := completionCell(t, "gc-incident")
	transcript := []llm.Message{
		call("s1", "mcp__bench__search", map[string]any{"intent": "reconcile failed", "limit": float64(25)}),
		result("s1", `{"groups":[{"source":"knowledge_pages","hits":[{"text":"Ledger Reconcile Job Runbook","reference":"`+ref("ledger-reconcile-runbook")+`"}]}]}`),
		call("f1", "mcp__bench__fetch", map[string]any{"reference": ref("ledger-reconcile-runbook")}),
		result("f1", "the severity standard is "+ref("incident-severity-bands")+"."),
		call("f2", "mcp__bench__fetch", map[string]any{"reference": ref("incident-severity-bands")}),
		result("f2", "band B follows the amber route"),
	}
	doc := "Open a severity band B incident. It follows the amber route, and the first rung holds 20 minutes."
	runner := stubRunner{result: claudecli.Result{
		ServerConnected: true, MCPCalls: 3, FinalText: doc,
		Transcript: transcript, PlatformVersion: "v-test",
	}}
	res, err := Run(context.Background(), Options{
		Runner: runner, Corpus: graphfix.Default(), Cells: []graphfix.CompletionCell{cell}, K: 1, OutDir: dir,
		Planted: testPlanted(), SearchEnabled: true,
		Gate: passingGate(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := res.Attempts[0]
	switch {
	case got.Error != "":
		t.Fatalf("attempt failed: %s", got.Error)
	case got.FinalDoc != doc:
		t.Errorf("FinalDoc not archived")
	case got.Coverage.OffEntryGrounded != 1:
		t.Errorf("OffEntryGrounded = %d, want 1 (the route, via the bands page)", got.Coverage.OffEntryGrounded)
	case got.Coverage.UnreadCovered != 1:
		t.Errorf("UnreadCovered = %d, want 1 (the clock, matrix unread)", got.Coverage.UnreadCovered)
	case got.Reading.MaxTraversalDepth != 1:
		t.Errorf("MaxTraversalDepth = %d, want 1", got.Reading.MaxTraversalDepth)
	case res.Manifest.PlatformVersion != "v-test":
		t.Errorf("PlatformVersion = %q, want the version the episode reported", res.Manifest.PlatformVersion)
	}
	if _, err := os.Stat(filepath.Join(dir, "transcripts", cell.ID+"-r1.json")); err != nil {
		t.Fatalf("transcript not archived: %v", err)
	}
	probe, err := ArchiveProbe(dir)
	if err != nil || probe != probeName {
		t.Fatalf("ArchiveProbe = (%q, %v), want (%q, nil)", probe, err, probeName)
	}
	reread, err := RereadCompletion(dir)
	if err != nil {
		t.Fatalf("RereadCompletion of the run just written: %v", err)
	}
	ra := reread.Attempts[0]
	if ra.Coverage.OffEntryGrounded != got.Coverage.OffEntryGrounded ||
		ra.Reading.MaxTraversalDepth != got.Reading.MaxTraversalDepth {
		t.Errorf("reread disagrees with the run: %+v vs %+v", ra.Coverage, got.Coverage)
	}
}

// TestOptionsValidateRefusesEveryUninterpretableRun: each refusal exists
// because the run would otherwise produce an archive nobody could read.
func TestOptionsValidateRefusesEveryUninterpretableRun(t *testing.T) {
	t.Parallel()
	base := func() Options {
		return Options{
			Runner: stubRunner{}, Corpus: graphfix.Default(), Cells: graphfix.CompletionCells(), K: 1, OutDir: t.TempDir(),
			Planted: testPlanted(), SearchEnabled: true,
			Gate: passingGate(),
		}
	}
	tests := map[string]func(*Options){
		"no runner":              func(o *Options) { o.Runner = nil },
		"no cells":               func(o *Options) { o.Cells = nil },
		"k below one":            func(o *Options) { o.K = 0 },
		"no output directory":    func(o *Options) { o.OutDir = "" },
		"no planted corpus":      func(o *Options) { o.Planted = Planted{} },
		"identity pool too smal": func(o *Options) { o.IdentityKeys = 1 },
		"gate arm mismatch":      func(o *Options) { o.Gate.Stripped = true },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			opts := base()
			mutate(&opts)
			if err := opts.validate(); err == nil {
				t.Errorf("validate accepted a run with %s", name)
			}
		})
	}
	if err := base().validate(); err != nil {
		t.Errorf("validate rejected a well-formed run: %v", err)
	}
}

// TestPlanterRefusesANonEmptyStore: a second corpus beside the first would put
// two pages with the same content in front of the agent.
func TestPlanterRefusesANonEmptyStore(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/portal/knowledge-pages") {
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 3})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := NewPlanter(srv.URL, srv.Client()).Plant(context.Background(), graphfix.Default(), false)
	if err == nil || !strings.Contains(err.Error(), "already holds") {
		t.Fatalf("Plant error = %v, want a refusal naming the existing pages", err)
	}
}

// plantServer scripts the knowledge-page REST surface for planter tests. The
// refs handler decides what every page's reference read-back reports.
func plantServer(refs func() []any) (*httptest.Server, *atomic.Int64) {
	created := new(atomic.Int64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			created.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "kp_stub"})
		case strings.HasSuffix(r.URL.Path, "/refs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"refs": refs()})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 0})
		}
	}))
	return srv, created
}

// TestPlanterRefusesAPageWhoseReferencesTheStoreDidNotRecord: the graph
// plant's completion condition. An edge the platform did not record is an
// edge no episode can follow, and the probe would report a floor that
// belonged to the planter.
func TestPlanterRefusesAPageWhoseReferencesTheStoreDidNotRecord(t *testing.T) {
	t.Parallel()
	srv, created := plantServer(func() []any { return []any{} })
	defer srv.Close()
	_, err := NewPlanter(srv.URL, srv.Client()).Plant(context.Background(), graphfix.Default(), false)
	if err == nil || !strings.Contains(err.Error(), "declares references") {
		t.Fatalf("Plant error = %v, want a refusal naming the missing references", err)
	}
	if created.Load() == 0 {
		t.Error("no page was created, so the reference check never ran")
	}
}

// TestStrippedPlantRefusesALiveReference is the same condition from the other
// arm: a stripped corpus with one recorded edge is not the stripped arm, and
// the run pair would be uninterpretable.
func TestStrippedPlantRefusesALiveReference(t *testing.T) {
	t.Parallel()
	srv, _ := plantServer(func() []any {
		return []any{map[string]any{"urn": "mcp:knowledge_page:kp_stub", "type": knowledgePageRefType, "exists": true}}
	})
	defer srv.Close()
	planted, err := NewPlanter(srv.URL, srv.Client()).Plant(context.Background(), graphfix.Default(), true)
	if err == nil || !strings.Contains(err.Error(), "stripped") {
		t.Fatalf("Plant error = %v, want a refusal naming the stripped arm's live reference", err)
	}
	if !planted.Stripped || planted.Arm() != "stripped" {
		t.Errorf("plant record = %+v, want it marked stripped", planted)
	}
}

// TestStrippedPlantAcceptsAnEdgelessStore: the stripped arm's happy path, and
// the record carries the arm the run derives everything from.
func TestStrippedPlantAcceptsAnEdgelessStore(t *testing.T) {
	t.Parallel()
	srv, created := plantServer(func() []any { return []any{} })
	defer srv.Close()
	planted, err := NewPlanter(srv.URL, srv.Client()).Plant(context.Background(), graphfix.Default(), true)
	if err != nil {
		t.Fatalf("Plant: %v", err)
	}
	if created.Load() != int64(len(graphfix.Pages())) {
		t.Errorf("created %d pages, want the whole corpus (%d)", created.Load(), len(graphfix.Pages()))
	}
	if planted.Arm() != "stripped" {
		t.Errorf("Arm = %q, want stripped", planted.Arm())
	}
}

// TestPlanterDeleteRemovesEveryPlantedPage covers the operator reset path: the
// other arm is planted after it, so a page left behind would put two corpora
// in front of the next run.
func TestPlanterDeleteRemovesEveryPlantedPage(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	deleted := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		deleted[strings.TrimPrefix(r.URL.Path, "/api/v1/portal/knowledge-pages/")] = true
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	planted := Planted{Pages: map[string]string{"a": "kp_a", "b": "kp_b"}}
	if err := NewPlanter(srv.URL, srv.Client()).Delete(context.Background(), planted); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !deleted["kp_a"] || !deleted["kp_b"] {
		t.Errorf("deleted = %v, want both planted pages", deleted)
	}
}

// TestDeclaredKeysRefusesAnythingButALivePageOfThisCorpus guards the plant's
// completion condition against each way it can be violated.
func TestDeclaredKeysRefusesAnythingButALivePageOfThisCorpus(t *testing.T) {
	t.Parallel()
	planted := Planted{Pages: map[string]string{"a": "kp_a"}}
	good := []refView{{URN: "mcp:knowledge_page:kp_a", Type: knowledgePageRefType, Exists: true}}
	keys, err := declaredKeys("p", good, planted)
	if err != nil || len(keys) != 1 || keys[0] != "a" {
		t.Fatalf("declaredKeys = (%v, %v), want ([a], nil)", keys, err)
	}
	for name, bad := range map[string][]refView{
		"wrong type":   {{URN: "urn:li:dataset:x", Type: "dataset", Exists: true}},
		"dead link":    {{URN: "mcp:knowledge_page:kp_a", Type: knowledgePageRefType, Exists: false}},
		"foreign page": {{URN: "mcp:knowledge_page:kp_other", Type: knowledgePageRefType, Exists: true}},
	} {
		if _, err := declaredKeys("p", bad, planted); err == nil {
			t.Errorf("declaredKeys accepted %s", name)
		}
	}
}

// TestKeyForReference pins the reference-to-page lookup every reading depends on.
func TestKeyForReference(t *testing.T) {
	t.Parallel()
	planted := Planted{Pages: map[string]string{"a": "kp_a"}}
	if key, ok := planted.KeyForReference("mcp:knowledge_page:kp_a"); !ok || key != "a" {
		t.Errorf("KeyForReference = (%q, %t), want (a, true)", key, ok)
	}
	for _, bad := range []string{"mcp:knowledge_page:kp_x", "mcp:asset:kp_a", "kp_a", ""} {
		if _, ok := planted.KeyForReference(bad); ok {
			t.Errorf("KeyForReference accepted %q", bad)
		}
	}
}

// TestKnowledgePageHitsReadsOnlyThePageGroup: the gate reads the knowledge-page
// group and nothing else, in the order the platform ranked it.
func TestKnowledgePageHitsReadsOnlyThePageGroup(t *testing.T) {
	t.Parallel()
	text := `{"groups":[
		{"source":"assets","hits":[{"text":"asset","reference":"mcp:asset:1"}]},
		{"source":"knowledge_pages","hits":[{"text":"one","reference":"r1"},{"text":"two","reference":"r2"}]}
	]}`
	hits, err := knowledgePageHits(text)
	if err != nil || len(hits) != 2 || hits[0].Reference != "r1" {
		t.Fatalf("knowledgePageHits = (%v, %v), want the two page hits in rank order", hits, err)
	}
	if _, err := knowledgePageHits("not json"); err == nil {
		t.Error("knowledgePageHits accepted a non-JSON result")
	}
	empty, err := knowledgePageHits(`{"groups":[]}`)
	if err != nil || empty != nil {
		t.Errorf("no page group = (%v, %v), want (nil, nil)", empty, err)
	}
}

// TestRereadLookupRecomputesFromTranscripts: the retired instrument's
// archives stay re-readable offline, and a succeeded attempt with no
// transcript is refused because its reading cannot be reproduced.
func TestRereadLookupRecomputesFromTranscripts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cell := graphfix.Cell{
		ID: "gt-d1-clickstream", Depth: 1,
		Chain: []string{"clickstream-export-runbook", "storage-class-register"},
	}
	archive := map[string]any{
		"manifest": map[string]any{"model": "stub"},
		"planted":  testPlanted(),
		"cells":    []graphfix.Cell{cell},
		"attempts": []LookupAttempt{{CellID: cell.ID, Replicate: 1, Depth: cell.Depth}},
	}
	writeJSONFile(t, dir, "results.json", archive)
	probe, err := ArchiveProbe(dir)
	if err != nil || probe != "" {
		t.Fatalf("ArchiveProbe = (%q, %v), want a lookup-era blank", probe, err)
	}
	if _, err := RereadLookup(dir); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("RereadLookup error = %v, want a refusal naming the missing transcript", err)
	}
	transcript := []llm.Message{
		call("f1", "mcp__bench__fetch", map[string]any{"reference": ref("clickstream-export-runbook")}),
		result("f1", "the classes are held in "+ref("storage-class-register")+"."),
		call("f2", "mcp__bench__fetch", map[string]any{"reference": ref("storage-class-register")}),
		result("f2", "{}"),
	}
	if err := os.MkdirAll(filepath.Join(dir, "transcripts"), 0o750); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	writeJSONFile(t, filepath.Join(dir, "transcripts"), cell.ID+"-r1.json", transcript)
	got, err := RereadLookup(dir)
	if err != nil {
		t.Fatalf("RereadLookup: %v", err)
	}
	if len(got) != 1 || got[0].Reading.MaxTraversalDepth != 1 || !got[0].Reading.ReadAnswerPage {
		t.Errorf("reread reading = %+v, want traversal depth 1 with the answer page read", got[0].Reading)
	}
}

// writeJSONFile writes one archive file for the reread tests.
func writeJSONFile(t *testing.T, dir, name string, v any) {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
