package main

import (
	"path/filepath"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycle"
	"github.com/txn2/mcp-data-platform/bench/internal/report"
)

// writeLifecyclePass writes a single-pass lifecycle result (one passing run per
// protocol) to a temp file, standing in for one isolated pass to be merged.
func writeLifecyclePass(t *testing.T, name, arm string, k int, protocols ...string) string {
	t.Helper()
	res := &lifecycle.Results{Manifest: lifecycle.Manifest{Arm: arm, K: k, ProtocolSetHash: "h1", Model: "claude-sonnet-5", Seed: 930}}
	for _, id := range protocols {
		res.Runs = append(res.Runs, lifecycle.ProtocolRun{ProtocolID: id, Captured: new(true), RecallCorrect: new(true), AbstainCorrect: new(true)})
	}
	p := filepath.Join(t.TempDir(), name+".json")
	if err := res.WriteJSON(p); err != nil {
		t.Fatalf("write lifecycle pass: %v", err)
	}
	return p
}

// TestMergeRejectsNonSinglePass proves the merge refuses a k>1 input rather than
// silently miscounting pass^k (a k=3 input would put 3 runs per protocol into a
// pass that is supposed to contribute one).
func TestMergeRejectsNonSinglePass(t *testing.T) {
	bad := writeLifecyclePass(t, "bad", "a3", 3, "lc-x")
	err := runMergeLifecycle(config{lifecycle: true, merge: bad, out: filepath.Join(t.TempDir(), "m.json")})
	if err == nil {
		t.Fatal("merge accepted a k>1 input")
	}
}

// TestMergeRejectsConfigMismatch proves the merge refuses passes from different
// configurations, so a merged scorecard is never mislabeled with pass-1's arm.
func TestMergeRejectsConfigMismatch(t *testing.T) {
	p1 := writeLifecyclePass(t, "p1", "a3", 1, "lc-x")
	p2 := writeLifecyclePass(t, "p2", "a2", 1, "lc-x") // different arm
	err := runMergeLifecycle(config{lifecycle: true, merge: p1 + "," + p2, out: filepath.Join(t.TempDir(), "m.json")})
	if err == nil {
		t.Fatal("merge accepted passes from different arms")
	}
}

// TestMergeSucceedsAndComputesPassK proves three valid k=1 passes merge to k=3
// with pass^k computed over the passes (each protocol has one run per pass).
func TestMergeSucceedsAndComputesPassK(t *testing.T) {
	out := filepath.Join(t.TempDir(), "merged.json")
	p1 := writeLifecyclePass(t, "p1", "a3", 1, "lc-x", "lc-y")
	p2 := writeLifecyclePass(t, "p2", "a3", 1, "lc-x", "lc-y")
	p3 := writeLifecyclePass(t, "p3", "a3", 1, "lc-x", "lc-y")
	if err := runMergeLifecycle(config{lifecycle: true, merge: p1 + "," + p2 + "," + p3, out: out}); err != nil {
		t.Fatalf("valid merge failed: %v", err)
	}
	res, err := lifecycle.LoadJSON(out)
	if err != nil {
		t.Fatal(err)
	}
	if res.Manifest.K != 3 {
		t.Errorf("merged K = %d, want 3", res.Manifest.K)
	}
	if res.Metrics.PassK.Num != 2 || res.Metrics.PassK.Den != 2 {
		t.Errorf("pass^k = %d/%d, want 2/2 (both protocols pass all 3 passes)", res.Metrics.PassK.Num, res.Metrics.PassK.Den)
	}
}

// TestMergeRequiresLifecycle proves a bare -merge errors instead of silently
// falling through to a live, paid benchmark run.
func TestMergeRequiresLifecycle(t *testing.T) {
	handled, err := runReadOnly(config{merge: "x.json"}) // lifecycle: false
	if !handled || err == nil {
		t.Fatalf("bare -merge did not error (handled=%v err=%v)", handled, err)
	}
}

// baselineFile writes a results JSON to a temp path and returns it, standing in
// for a committed baseline that gateOnBaseline loads from disk.
func baselineFile(t *testing.T, r *report.Results) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "baseline.json")
	if err := r.WriteJSON(p); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	return p
}

func results(arm string, suites ...report.SuiteSummary) *report.Results {
	return &report.Results{Manifest: report.Manifest{Arm: arm}, Suites: suites}
}

// TestGateOnBaselinePasses proves the -baseline exit wiring returns nil (exit 0)
// when the candidate holds the line — the gate must not fail a healthy run.
func TestGateOnBaselinePasses(t *testing.T) {
	suite := report.SuiteSummary{Suite: "s3", Graded: 10, Accuracy: 0.98, PassKRate: 0.98, MedianToolCalls: 10}
	cand := results("a2", suite)
	if err := gateOnBaseline(cand, baselineFile(t, results("a2", suite))); err != nil {
		t.Fatalf("clean candidate was gated: %v", err)
	}
}

// TestGateOnBaselineFailsOnRegression is the exit-wiring counterpart to
// TestCheckRegressionFailsOnDegradedConfig: it proves gateOnBaseline translates a
// real regression into a non-nil error (nonzero process exit), not just that
// CheckRegression detects it.
func TestGateOnBaselineFailsOnRegression(t *testing.T) {
	base := results("a2", report.SuiteSummary{Suite: "s3", Graded: 10, Accuracy: 0.98, PassKRate: 0.98, MedianToolCalls: 10})
	cand := results("a2", report.SuiteSummary{Suite: "s3", Graded: 10, Accuracy: 0.43, PassKRate: 0.40, MedianToolCalls: 10})
	if err := gateOnBaseline(cand, baselineFile(t, base)); err == nil {
		t.Fatal("degraded candidate passed the gate (exit 0)")
	}
}

// TestGateOnBaselineRejectsArmMismatch proves an incompatible baseline (different
// arm) is refused rather than silently mis-compared.
func TestGateOnBaselineRejectsArmMismatch(t *testing.T) {
	base := results("a2", report.SuiteSummary{Suite: "s3", Graded: 10, Accuracy: 0.98, PassKRate: 0.98, MedianToolCalls: 10})
	cand := results("a0", report.SuiteSummary{Suite: "s3", Graded: 10, Accuracy: 0.43, PassKRate: 0.40, MedianToolCalls: 16})
	if err := gateOnBaseline(cand, baselineFile(t, base)); err == nil {
		t.Fatal("cross-arm baseline was accepted")
	}
}
