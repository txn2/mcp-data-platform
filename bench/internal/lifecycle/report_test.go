package lifecycle

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPassed(t *testing.T) {
	full := ProtocolRun{
		Captured: new(true), RecallCorrect: new(true), Promoted: new(true),
		TransferCorrect: new(true), UpdateCorrect: new(true), Duplicated: new(false), AbstainCorrect: new(true),
	}
	if !full.Passed() {
		t.Error("complete success should pass")
	}
	if (ProtocolRun{Error: "boom", Captured: new(true)}).Passed() {
		t.Error("harness failure never passes")
	}
	if (ProtocolRun{Captured: new(false)}).Passed() {
		t.Error("no capture fails")
	}
	dup := full
	dup.Duplicated = new(true)
	if dup.Passed() {
		t.Error("a duplicate must fail the run")
	}
	// A protocol without optional stages passes on the required three.
	minimal := ProtocolRun{Captured: new(true), RecallCorrect: new(true), Promoted: new(true)}
	if !minimal.Passed() {
		t.Error("minimal lifecycle should pass")
	}
}

func TestAggregate(t *testing.T) {
	res := &Results{
		Manifest: Manifest{K: 2},
		Runs: []ProtocolRun{
			// lc-a: both attempts fully pass.
			fullRun("lc-a", 1), fullRun("lc-a", 2),
			// lc-b: attempt 1 passes, attempt 2 misses recall -> protocol not pass^k.
			fullRun("lc-b", 1), missRecall("lc-b", 2),
			// lc-c: one attempt is a harness failure.
			{ProtocolID: "lc-c", Attempt: 1, Error: "connect failed"},
			fullRun("lc-c", 2),
		},
	}
	res.Aggregate()
	m := res.Metrics
	if m.Protocols != 3 {
		t.Fatalf("protocols = %d, want 3", m.Protocols)
	}
	if m.HarnessFailures != 1 {
		t.Fatalf("harness failures = %d, want 1", m.HarnessFailures)
	}
	if m.Attempts != 5 {
		t.Fatalf("attempts = %d, want 5", m.Attempts)
	}
	// capture: all 5 graded runs captured.
	if m.CaptureRate.Num != 5 || m.CaptureRate.Den != 5 {
		t.Fatalf("capture = %+v", m.CaptureRate)
	}
	// recall: 4 of 5 correct (lc-b attempt 2 missed).
	if m.PersonalRecall.Num != 4 || m.PersonalRecall.Den != 5 {
		t.Fatalf("recall = %+v", m.PersonalRecall)
	}
	// pass^k: only lc-a passes both attempts; lc-b fails attempt 2; lc-c has
	// only one graded run (!= k) so cannot claim pass^k.
	if m.PassK.Num != 1 || m.PassK.Den != 3 {
		t.Fatalf("passK = %+v", m.PassK)
	}
}

func fullRun(id string, attempt int) ProtocolRun {
	return ProtocolRun{
		ProtocolID: id, Attempt: attempt,
		Captured: new(true), RecallCorrect: new(true), RecallSurfaced: new(true), Promoted: new(true),
		TransferCorrect: new(true), UpdateCorrect: new(true), Duplicated: new(false), AbstainCorrect: new(true),
	}
}

func missRecall(id string, attempt int) ProtocolRun {
	r := fullRun(id, attempt)
	r.RecallCorrect = new(false)
	return r
}

func TestAggregateSumsTokens(t *testing.T) {
	res := &Results{
		Manifest: Manifest{K: 1},
		Runs: []ProtocolRun{
			{ProtocolID: "lc-a", Captured: new(true), RecallCorrect: new(true), Episodes: []EpisodeRecord{
				{Stage: StageTeach, InputTokens: 1000, OutputTokens: 50},
				{Stage: StageRecall, InputTokens: 2000, OutputTokens: 80},
			}},
			// A harness-failed run still spent tokens and must count toward the total.
			{ProtocolID: "lc-b", Error: "boom", Episodes: []EpisodeRecord{
				{Stage: StageTeach, InputTokens: 500, OutputTokens: 20},
			}},
		},
	}
	res.Aggregate()
	if res.Metrics.TotalInputTokens != 3500 || res.Metrics.TotalOutputTokens != 150 {
		t.Fatalf("token totals = in %d out %d, want 3500/150", res.Metrics.TotalInputTokens, res.Metrics.TotalOutputTokens)
	}
	if !strings.Contains(res.HumanSummary(), "input 3500") {
		t.Errorf("summary missing token line:\n%s", res.HumanSummary())
	}
}

func TestWriteAndLoadJSON(t *testing.T) {
	res := &Results{
		Manifest: Manifest{Arm: "a3", K: 1, StartedAt: time.Unix(1, 0).UTC(), FinishedAt: time.Unix(2, 0).UTC()},
		Runs:     []ProtocolRun{fullRun("lc-a", 1)},
	}
	res.Aggregate()
	path := filepath.Join(t.TempDir(), "res.json")
	if err := res.WriteJSON(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadJSON(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Manifest.Arm != "a3" || len(got.Runs) != 1 || got.Metrics.CaptureRate.Rate != 1 {
		t.Fatalf("roundtrip mismatch: %+v", got.Metrics)
	}
}

func TestHumanSummaryIncludesMetricsAndFailures(t *testing.T) {
	res := &Results{
		Manifest: Manifest{Arm: "a3", Model: "scripted", K: 1},
		Runs: []ProtocolRun{
			fullRun("lc-a", 1),
			{ProtocolID: "lc-b", Attempt: 1, Error: "connect failed"},
		},
	}
	res.Aggregate()
	out := res.HumanSummary()
	for _, want := range []string{"capture rate", "pass^k", "harness failures", "lc-b", "connect failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}
