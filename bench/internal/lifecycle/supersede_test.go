package lifecycle

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

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
