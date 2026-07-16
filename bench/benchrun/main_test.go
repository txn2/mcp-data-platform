package main

import (
	"os"
	"path/filepath"
	"strings"
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

// TestMergeRejectsClientPathMismatch proves the merge refuses to fold an
// anthropic pass and a claude-cli pass into one scorecard: the two client paths
// produce incomparable numbers (BaselineCompatible refuses to gate across them,
// so a merge must not silently blend them either).
func TestMergeRejectsClientPathMismatch(t *testing.T) {
	base := lifecycle.Manifest{Arm: "a3", ProtocolSetHash: "h1", Model: "claude-sonnet-5", Seed: 930, LLMProvider: "anthropic"}
	other := base
	other.LLMProvider = "claude-cli"
	if err := sameConfig(base, other, "p2.json"); err == nil {
		t.Fatal("sameConfig accepted an anthropic pass merged with a claude-cli pass")
	}
	if err := sameConfig(base, base, "p2.json"); err != nil {
		t.Fatalf("sameConfig rejected identical configs: %v", err)
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

// TestTranscriptDirIsolatesByOutputName proves two passes written into the same
// directory under different -out names get distinct transcript directories, so
// one pass never overwrites another's raw transcripts.
func TestTranscriptDirIsolatesByOutputName(t *testing.T) {
	a := transcriptDir("build/bench-results/lifecycle-pass1.json")
	b := transcriptDir("build/bench-results/lifecycle-pass2.json")
	if a == b {
		t.Fatalf("passes in the same dir collided: both -> %s", a)
	}
	// Same stem, different extension must not collide (the extension is kept).
	if transcriptDir("out/results.json") == transcriptDir("out/results.txt") {
		t.Fatal("results.json and results.txt collided on transcript dir")
	}
	if got := transcriptDir("out/results.json"); got != filepath.Join("out", "results.json.transcripts") {
		t.Fatalf("transcriptDir = %s, want out/results.json.transcripts", got)
	}
}

// TestMergeRefusesToOverwriteInput proves a merge whose -out is the same file as
// one of its input passes is rejected, so derived data never clobbers raw pass
// evidence.
func TestMergeRefusesToOverwriteInput(t *testing.T) {
	dir := t.TempDir()
	p1 := writeLifecyclePass(t, "p1", "a3", 1, "lc-x")
	p2 := writeLifecyclePass(t, "p2", "a3", 1, "lc-x")
	// -out is the same file as the first input pass.
	if err := runMergeLifecycle(config{lifecycle: true, merge: p1 + "," + p2, out: p1}); err == nil {
		t.Fatal("merge overwrote an input pass file")
	}
	// A distinct output path is accepted.
	out := filepath.Join(dir, "merged.json")
	if err := runMergeLifecycle(config{lifecycle: true, merge: p1 + "," + p2, out: out}); err != nil {
		t.Fatalf("merge to a distinct path failed: %v", err)
	}
}

// TestMergeGatesOnBaseline proves the -baseline regression gate runs on the
// merged k=N scorecard (the canonical multi-pass artifact), not only on a
// single-process run — the fix for -merge silently skipping the gate.
func TestMergeGatesOnBaseline(t *testing.T) {
	mkFailingPass := func(name string) string {
		r := &lifecycle.Results{Manifest: lifecycle.Manifest{Arm: "a3", K: 1, ProtocolSetHash: "h1", Model: "m", Seed: 930}}
		r.Runs = append(r.Runs, lifecycle.ProtocolRun{ProtocolID: "lc-x", Captured: new(false), RecallCorrect: new(false)})
		p := filepath.Join(t.TempDir(), name+".json")
		if err := r.WriteJSON(p); err != nil {
			t.Fatalf("write pass: %v", err)
		}
		return p
	}
	p1, p2 := mkFailingPass("p1"), mkFailingPass("p2")
	base := lifecycleBaselineFile(t, healthyLifecycleMetrics()) // capture/recall 90%
	out := filepath.Join(t.TempDir(), "merged.json")
	if err := runMergeLifecycle(config{lifecycle: true, merge: p1 + "," + p2, out: out, baseline: base}); err == nil {
		t.Fatal("merge did not gate the merged scorecard against a regressing baseline")
	}
}

// TestEnsureDistinctMergeOutput proves the collision check is by underlying file
// (os.SameFile), so a symlink aliasing an input is caught while a distinct,
// not-yet-existing output is allowed.
func TestEnsureDistinctMergeOutput(t *testing.T) {
	dir := t.TempDir()
	pass := filepath.Join(dir, "pass1.json")
	if err := os.WriteFile(pass, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureDistinctMergeOutput(pass, []string{pass, filepath.Join(dir, "pass2.json")}); err == nil {
		t.Fatal("identical input/output file was allowed")
	}
	// A symlink to an input resolves to the same file, so it is caught.
	link := filepath.Join(dir, "alias.json")
	if err := os.Symlink(pass, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureDistinctMergeOutput(link, []string{pass}); err == nil {
		t.Fatal("a symlinked output aliasing an input was allowed")
	}
	// A distinct, not-yet-existing output is allowed.
	if err := ensureDistinctMergeOutput(filepath.Join(dir, "merged.json"), []string{pass}); err != nil {
		t.Fatalf("distinct output rejected: %v", err)
	}
}

// lifecycleRate builds a lifecycle.Rate for the gate tests.
func lifecycleRate(num, den int) lifecycle.Rate {
	r := lifecycle.Rate{Num: num, Den: den}
	if den > 0 {
		r.Rate = float64(num) / float64(den)
	}
	return r
}

// healthyLifecycleMetrics is a passing S5 scorecard.
func healthyLifecycleMetrics() lifecycle.Metrics {
	return lifecycle.Metrics{
		Attempts:          10,
		CaptureRate:       lifecycleRate(9, 10),
		PersonalRecall:    lifecycleRate(9, 10),
		TransferRate:      lifecycleRate(8, 10),
		UpdateCorrectness: lifecycleRate(5, 5),
		AbstentionRate:    lifecycleRate(9, 10),
		DuplicateRate:     lifecycleRate(0, 5),
		PassK:             lifecycleRate(7, 10),
	}
}

// lifecycleBaselineFile writes an a3 lifecycle results JSON standing in for a
// committed S5 baseline the gate loads from disk.
func lifecycleBaselineFile(t *testing.T, m lifecycle.Metrics) string {
	t.Helper()
	r := &lifecycle.Results{Manifest: lifecycle.Manifest{Arm: "a3"}, Metrics: m}
	p := filepath.Join(t.TempDir(), "life-baseline.json")
	if err := r.WriteJSON(p); err != nil {
		t.Fatalf("write lifecycle baseline: %v", err)
	}
	return p
}

// TestLifecycleGatePasses proves a lifecycle candidate that holds the line
// returns nil (exit 0), so the S5 gate does not fail a healthy run.
func TestLifecycleGatePasses(t *testing.T) {
	base := lifecycleBaselineFile(t, healthyLifecycleMetrics())
	cand := &lifecycle.Results{Manifest: lifecycle.Manifest{Arm: "a3"}, Metrics: healthyLifecycleMetrics()}
	if err := gateOnLifecycleBaseline(cand, base); err != nil {
		t.Fatalf("clean lifecycle candidate was gated: %v", err)
	}
}

// TestLifecycleGateFailsOnRegression proves the S5 gate exits nonzero when a
// lifecycle metric falls below the baseline beyond tolerance.
func TestLifecycleGateFailsOnRegression(t *testing.T) {
	base := lifecycleBaselineFile(t, healthyLifecycleMetrics())
	m := healthyLifecycleMetrics()
	m.TransferRate = lifecycleRate(50, 100) // 80% -> 50%
	cand := &lifecycle.Results{Manifest: lifecycle.Manifest{Arm: "a3"}, Metrics: m}
	if err := gateOnLifecycleBaseline(cand, base); err == nil {
		t.Fatal("a transfer-rate collapse should fail the gate")
	}
}

// TestLifecycleGateRejectsArmMismatch proves the gate refuses a cross-arm
// comparison rather than producing a meaningless verdict.
func TestLifecycleGateRejectsArmMismatch(t *testing.T) {
	base := lifecycleBaselineFile(t, healthyLifecycleMetrics())
	cand := &lifecycle.Results{Manifest: lifecycle.Manifest{Arm: "a2"}, Metrics: healthyLifecycleMetrics()}
	if err := gateOnLifecycleBaseline(cand, base); err == nil {
		t.Fatal("a cross-arm lifecycle gate should be refused")
	}
}

// TestSupersedeRequiresArm proves the supersede sub-benchmark refuses to start
// without an arm rather than launching an unattributable run.
func TestSupersedeRequiresArm(t *testing.T) {
	if err := runSupersede(config{supersede: true}); err == nil {
		t.Fatal("supersede run accepted an empty arm")
	}
}

// TestSupersedeRejectsBaseline proves -baseline is refused for supersede runs:
// the regression gate scores the S1-S3 task shape, not supersede metrics, so a
// silently-ignored -baseline would give a false sense of gating.
func TestSupersedeRejectsBaseline(t *testing.T) {
	err := runSupersede(config{supersede: true, arm: "a3", baseline: "b.json"})
	if err == nil {
		t.Fatal("supersede run accepted -baseline")
	}
}

// TestColdStartRequiresArm proves the cold-start run refuses to start without
// an arm rather than launching an unattributable run.
func TestColdStartRequiresArm(t *testing.T) {
	if err := runColdStart(config{coldStart: true}); err == nil {
		t.Fatal("cold-start run accepted an empty arm")
	}
}

// TestColdStartRejectsBaseline proves -baseline is refused for cold-start runs:
// the regression gate scores the S1-S3 task shape, not the learning curve, so a
// silently-ignored -baseline would give a false sense of gating.
func TestColdStartRejectsBaseline(t *testing.T) {
	if err := runColdStart(config{coldStart: true, arm: "a3", baseline: "b.json"}); err == nil {
		t.Fatal("cold-start run accepted -baseline")
	}
}

// TestColdStartRefusesToOverwriteResults proves a cold-start run whose -out
// already exists is refused before anything is spent: a prior run's results and
// transcripts are paid-for evidence that must never be overwritten, and there
// is deliberately no override flag.
func TestColdStartRefusesToOverwriteResults(t *testing.T) {
	out := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(out, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runColdStart(config{coldStart: true, arm: "a3", out: out})
	if err == nil {
		t.Fatal("cold-start run accepted an -out that already exists")
	}
	if got := err.Error(); !strings.Contains(got, "refusing to overwrite") {
		t.Errorf("error should explain the overwrite refusal, got: %s", got)
	}
}

// TestColdStartRefusesToOverwriteTranscripts proves the refusal also covers the
// sibling transcript directory: a run interrupted before its first checkpoint
// flush leaves transcripts but no results file, and a rerun to the same -out
// would overwrite them episode by episode (deterministic filenames).
func TestColdStartRefusesToOverwriteTranscripts(t *testing.T) {
	out := filepath.Join(t.TempDir(), "results.json")
	if err := os.MkdirAll(transcriptDir(out), 0o750); err != nil {
		t.Fatal(err)
	}
	err := runColdStart(config{coldStart: true, arm: "a3", out: out})
	if err == nil {
		t.Fatal("cold-start run accepted an -out whose transcript directory already exists")
	}
	if got := err.Error(); !strings.Contains(got, "transcript directory") {
		t.Errorf("error should name the transcript directory, got: %s", got)
	}
}

// TestBuildColdStartFactoryErrors proves the factory wiring fails fast on a
// scripted run without a script and on an unknown provider, mirroring the
// lifecycle factory contract.
func TestBuildColdStartFactoryErrors(t *testing.T) {
	if _, err := buildColdStartFactory(config{llmProvider: "scripted"}); err == nil {
		t.Error("scripted provider without -script was accepted")
	}
	if _, err := buildColdStartFactory(config{llmProvider: "nope"}); err == nil {
		t.Error("unknown provider was accepted")
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
