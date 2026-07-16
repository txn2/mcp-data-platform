package lifecycle

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// TestSupersedeCIComplement guards the exact-complement relationship the
// SupersedeMetrics report advertises: SupersedeRate's interval must be the
// reflection of DuplicateRate's, not an independent bootstrap that drifts a
// resampling step away from it (issue #965 review finding).
func TestSupersedeCIComplement(t *testing.T) {
	res := &SupersedeResults{Manifest: Manifest{K: 1}}
	for i := range 20 {
		res.Runs = append(res.Runs, ProtocolRun{
			ProtocolID: "lc-x", Attempt: i, Captured: new(true), Duplicated: new(i%4 == 0),
		})
	}
	res.Aggregate()
	m := res.Metrics
	if m.DuplicateRate.CILow == m.DuplicateRate.CIHigh {
		t.Fatalf("duplicate CI is degenerate, test cannot check the complement: %+v", m.DuplicateRate)
	}
	if m.SupersedeRate.CILow != 1-m.DuplicateRate.CIHigh || m.SupersedeRate.CIHigh != 1-m.DuplicateRate.CILow {
		t.Fatalf("supersede CI [%v,%v] is not the reflection of duplicate CI [%v,%v]",
			m.SupersedeRate.CILow, m.SupersedeRate.CIHigh, m.DuplicateRate.CILow, m.DuplicateRate.CIHigh)
	}
	// With no captured attempts, SupersedeRate must not reflect into a spurious [1,1].
	empty := &SupersedeResults{Manifest: Manifest{K: 1}, Runs: []ProtocolRun{{ProtocolID: "lc-x", Captured: new(false)}}}
	empty.Aggregate()
	if sr := empty.Metrics.SupersedeRate; sr.CILow != 0 || sr.CIHigh != 0 {
		t.Fatalf("empty supersede CI = [%v,%v], want [0,0]", sr.CILow, sr.CIHigh)
	}
}

// supersedeOnlyScript plays just the stages the isolated sub-benchmark drives:
// teach (capture), update (correct), and the post-correction recall. Recall,
// transfer, and abstain never run, so they are absent by design.
func supersedeOnlyScript(protocolID, updateCategory string) map[string]llm.Script {
	return map[string]llm.Script{
		protocolID: {
			StageTeach:        {captureStep("definition"), {FinalText: "saved"}},
			StageUpdate:       {captureStep(updateCategory), {FinalText: "saved"}},
			StageUpdateRecall: {searchStep(), {FinalText: "FINAL ANSWER: 200.00"}},
		},
	}
}

func TestRunSupersedeCleanSupersede(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	p := updateProtocol()
	writeProtocols(t, dir, p)
	// "correction" makes the fake supersede the prior pending insight: no duplicate.
	res, err := RunSupersede(context.Background(), runOptions(fp, dir, scriptFactory(supersedeOnlyScript(p.ID, "correction"))))
	if err != nil {
		t.Fatalf("run supersede: %v", err)
	}
	m := res.Metrics
	if m.Protocols != 1 || m.Attempts != 1 || m.HarnessFailures != 0 {
		t.Fatalf("counts = protocols %d attempts %d failures %d, want 1/1/0", m.Protocols, m.Attempts, m.HarnessFailures)
	}
	if m.CaptureRate.Rate != 1 || m.SupersedeRate.Rate != 1 || m.DuplicateRate.Rate != 0 || m.UpdateCorrectness.Rate != 1 {
		t.Fatalf("metrics = capture %v supersede %v duplicate %v update %v, want 1/1/0/1",
			m.CaptureRate.Rate, m.SupersedeRate.Rate, m.DuplicateRate.Rate, m.UpdateCorrectness.Rate)
	}
	if m.PassK.Rate != 1 {
		t.Fatalf("pass^k = %v, want 1", m.PassK.Rate)
	}
	if len(m.PerProtocol) != 1 {
		t.Fatalf("per-protocol stats = %d, want 1", len(m.PerProtocol))
	}
	s := m.PerProtocol[0]
	if s.Captured != 1 || s.Superseded != 1 || s.Duplicated != 0 {
		t.Fatalf("per-protocol = cap %d superseded %d dup %d, want 1/1/0", s.Captured, s.Superseded, s.Duplicated)
	}
}

func TestRunSupersedeDuplicate(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	p := updateProtocol()
	writeProtocols(t, dir, p)
	// "definition" (not "correction") leaves the prior insight live: a duplicate.
	res, err := RunSupersede(context.Background(), runOptions(fp, dir, scriptFactory(supersedeOnlyScript(p.ID, "definition"))))
	if err != nil {
		t.Fatalf("run supersede: %v", err)
	}
	m := res.Metrics
	if m.SupersedeRate.Rate != 0 || m.DuplicateRate.Rate != 1 {
		t.Fatalf("metrics = supersede %v duplicate %v, want 0/1", m.SupersedeRate.Rate, m.DuplicateRate.Rate)
	}
	if m.PassK.Rate != 0 {
		t.Fatalf("pass^k = %v, want 0 (a duplicate fails)", m.PassK.Rate)
	}
	if s := m.PerProtocol[0]; s.Duplicated != 1 || s.Superseded != 0 {
		t.Fatalf("per-protocol = superseded %d dup %d, want 0/1", s.Superseded, s.Duplicated)
	}
}

// TestRunSupersedeCaptureMiss proves a supersede attempt whose teach never
// captures is excluded from the supersede/duplicate denominators (the gate can
// only be measured on a fact that actually landed).
func TestRunSupersedeCaptureMiss(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	p := updateProtocol()
	writeProtocols(t, dir, p)
	scripts := map[string]llm.Script{
		p.ID: {StageTeach: {{FinalText: "I will not save anything"}}}, // no capture
	}
	res, err := RunSupersede(context.Background(), runOptions(fp, dir, scriptFactory(scripts)))
	if err != nil {
		t.Fatalf("run supersede: %v", err)
	}
	m := res.Metrics
	if m.CaptureRate.Num != 0 || m.CaptureRate.Den != 1 {
		t.Fatalf("capture rate = %d/%d, want 0/1", m.CaptureRate.Num, m.CaptureRate.Den)
	}
	if m.SupersedeRate.Den != 0 || m.DuplicateRate.Den != 0 {
		t.Fatalf("supersede/duplicate denominators = %d/%d, want 0/0 (no captured attempt to supersede)",
			m.SupersedeRate.Den, m.DuplicateRate.Den)
	}
	if s := m.PerProtocol[0]; s.Captured != 0 || s.Superseded != 0 || s.Duplicated != 0 {
		t.Fatalf("per-protocol = cap %d superseded %d dup %d, want 0/0/0", s.Captured, s.Superseded, s.Duplicated)
	}
}

// TestRunSupersedeUpdateCaptureMiss proves an attempt whose UPDATE episode never
// executes the correction capture is excluded from the supersede/duplicate
// denominators rather than misclassified as a duplicate: the platform never
// received the correction, so its supersede gate never ran and the taught
// insight legitimately stays pending. The miss is measured on its own rate and
// still fails pass^k even when the recall answer happens to be correct.
func TestRunSupersedeUpdateCaptureMiss(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	p := updateProtocol()
	writeProtocols(t, dir, p)
	scripts := map[string]llm.Script{
		p.ID: {
			StageTeach:  {captureStep("definition"), {FinalText: "saved"}},
			StageUpdate: {{FinalText: "noted, but I did not save the correction"}}, // no capture call
			// No StageUpdateRecall script: the recall must be SKIPPED on a
			// capture miss (it would grade staleness and dilute update
			// correctness), so the runner must never request this stage.
		},
	}
	res, err := RunSupersede(context.Background(), runOptions(fp, dir, scriptFactory(scripts)))
	if err != nil {
		t.Fatalf("run supersede: %v", err)
	}
	m := res.Metrics
	if m.CaptureRate.Num != 1 || m.CaptureRate.Den != 1 {
		t.Fatalf("capture rate = %d/%d, want 1/1 (teach captured)", m.CaptureRate.Num, m.CaptureRate.Den)
	}
	if m.UpdateCaptureRate.Num != 0 || m.UpdateCaptureRate.Den != 1 {
		t.Fatalf("update capture rate = %d/%d, want 0/1", m.UpdateCaptureRate.Num, m.UpdateCaptureRate.Den)
	}
	if m.SupersedeRate.Den != 0 || m.DuplicateRate.Den != 0 {
		t.Fatalf("supersede/duplicate denominators = %d/%d, want 0/0 (no executed supersede to score)",
			m.SupersedeRate.Den, m.DuplicateRate.Den)
	}
	if m.UpdateCorrectness.Den != 0 {
		t.Fatalf("update correctness denominator = %d, want 0 (recall skipped, no staleness dilution)", m.UpdateCorrectness.Den)
	}
	if m.PassK.Rate != 0 {
		t.Fatalf("pass^k = %v, want 0 (a missed correction capture fails the lifecycle)", m.PassK.Rate)
	}
	if s := m.PerProtocol[0]; s.UpdateCaptureMissed != 1 || s.Superseded != 0 || s.Duplicated != 0 {
		t.Fatalf("per-protocol = update-capture missed %d superseded %d dup %d, want 1/0/0",
			s.UpdateCaptureMissed, s.Superseded, s.Duplicated)
	}
	run := res.Runs[0]
	if run.Duplicated != nil {
		t.Fatalf("Duplicated = %v, want nil (excluded, not scored)", *run.Duplicated)
	}
	if got := len(run.Episodes); got != 2 {
		t.Fatalf("episodes = %d, want 2 (teach + update; the recall episode must not be spent on a capture miss)", got)
	}
}

// TestCorrectionCapturedAPIArbiter pins the platform-truth fallback: when the
// transcript carries no executed-capture signal (a claude-cli stream can drop
// the paired tool_result of a call that really ran), the insights API decides —
// and the teach-stage insight, which the skew-widened since window can reach
// back to, is excluded by ID rather than trusted by time.
func TestCorrectionCapturedAPIArbiter(t *testing.T) {
	fp := newFakePlatform(t)
	env := &runEnv{
		opts: Options{Target: target.Target{BaseURL: fp.httpSrv.URL, Credential: "testkey"}, HTTPTimeout: 10 * time.Second},
		life: lifecycleapi.New(fp.httpSrv.URL, fp.httpSrv.Client()),
	}
	p := updateProtocol()
	teacherSeq := 1
	upd := EpisodeRecord{CaptureAttempted: false}
	updStart := time.Now()

	got, err := env.correctionCaptured(context.Background(), upd, p, teacherSeq, updStart, "in-teach")
	if err != nil || got {
		t.Fatalf("no insights: captured = %v err = %v, want false", got, err)
	}

	// The teach insight alone — inside the skew window but excluded by ID —
	// must not count as the correction.
	fp.mu.Lock()
	fp.insights = append(fp.insights, lifecycleapi.Insight{
		ID: "in-teach", CreatedAt: time.Now().UTC(), CapturedBy: poolEmail(teacherSeq),
		Status: "pending", EntityURNs: []string{p.EntityURN},
	})
	fp.mu.Unlock()
	got, err = env.correctionCaptured(context.Background(), upd, p, teacherSeq, updStart, "in-teach")
	if err != nil || got {
		t.Fatalf("teach insight only: captured = %v err = %v, want false (excluded by ID)", got, err)
	}

	// A distinct fresh insight proves the correction landed even though the
	// transcript never showed it.
	fp.mu.Lock()
	fp.insights = append(fp.insights, lifecycleapi.Insight{
		ID: "in-corr", CreatedAt: time.Now().UTC(), CapturedBy: poolEmail(teacherSeq),
		Status: "pending", EntityURNs: []string{p.EntityURN},
	})
	fp.mu.Unlock()
	got, err = env.correctionCaptured(context.Background(), upd, p, teacherSeq, updStart, "in-teach")
	if err != nil || !got {
		t.Fatalf("fresh correction insight: captured = %v err = %v, want true", got, err)
	}

	// The transcript fast path never consults the API.
	got, err = env.correctionCaptured(context.Background(), EpisodeRecord{CaptureAttempted: true}, p, teacherSeq, updStart, "in-teach")
	if err != nil || !got {
		t.Fatalf("transcript-attempted: captured = %v err = %v, want true", got, err)
	}
}

func TestRunSupersedeRejectsNonSupersedeProtocols(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	writeProtocols(t, dir, okProtocol()) // promote/transfer protocol, no update stage
	_, err := RunSupersede(context.Background(), runOptions(fp, dir, scriptFactory(okScript())))
	if err == nil || !strings.Contains(err.Error(), "no supersede protocols") {
		t.Fatalf("expected no-supersede-protocols error, got %v", err)
	}
}

func TestSupersedeResultsRoundTrip(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	p := updateProtocol()
	writeProtocols(t, dir, p)
	res, err := RunSupersede(context.Background(), runOptions(fp, dir, scriptFactory(supersedeOnlyScript(p.ID, "correction"))))
	if err != nil {
		t.Fatalf("run supersede: %v", err)
	}
	out := filepath.Join(t.TempDir(), "supersede.json")
	if err := res.WriteJSON(out); err != nil {
		t.Fatalf("write: %v", err)
	}
	loaded, err := LoadSupersedeJSON(out)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Metrics.SupersedeRate.Rate != res.Metrics.SupersedeRate.Rate {
		t.Fatalf("round-trip supersede rate = %v, want %v", loaded.Metrics.SupersedeRate.Rate, res.Metrics.SupersedeRate.Rate)
	}
	if summary := loaded.HumanSummary(); !strings.Contains(summary, "supersede") {
		t.Fatalf("summary missing supersede header:\n%s", summary)
	}
}

func TestRunSupersedeCheckpoints(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	p := updateProtocol()
	writeProtocols(t, dir, p)
	opts := runOptions(fp, dir, scriptFactory(supersedeOnlyScript(p.ID, "correction")))
	var snapshots int
	opts.OnSupersede = func(*SupersedeResults) { snapshots++ }
	if _, err := RunSupersede(context.Background(), opts); err != nil {
		t.Fatalf("run supersede: %v", err)
	}
	if snapshots != 1 {
		t.Fatalf("checkpoints = %d, want 1", snapshots)
	}
}
