package lifecycle

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

// TestAggregateDecomposition verifies the transfer-gap and capture-budget
// decompositions from synthesized runs, independent of the live harness.
func TestAggregateDecomposition(t *testing.T) {
	res := &Results{
		Manifest: Manifest{K: 1},
		Runs: []ProtocolRun{
			// transfer: surfaced and used
			{ProtocolID: "a", Captured: new(true), TransferSurfaced: new(true), TransferCorrect: new(true)},
			// transfer: surfaced but not used
			{ProtocolID: "b", Captured: new(true), TransferSurfaced: new(true), TransferCorrect: new(false)},
			// transfer: not surfaced
			{ProtocolID: "c", Captured: new(true), TransferSurfaced: new(false), TransferCorrect: new(false)},
			// capture miss, budget-starved before any capture call
			{ProtocolID: "d", Captured: new(false), CaptureAttempted: new(false), TeachBudgetExhausted: new(true)},
			// capture miss, but capture was attempted (not a budget-starvation miss)
			{ProtocolID: "e", Captured: new(false), CaptureAttempted: new(true), TeachBudgetExhausted: new(false)},
			// capture miss on a path where budget is not observable (claude-cli:
			// TeachBudgetExhausted nil) — excluded from the starvation denominator.
			{ProtocolID: "f", Captured: new(false), CaptureAttempted: new(false), TeachBudgetExhausted: nil},
		},
	}
	res.Aggregate()
	m := res.Metrics

	if m.TransferSurfaced.Num != 2 || m.TransferSurfaced.Den != 3 {
		t.Errorf("transfer surfaced = %d/%d, want 2/3", m.TransferSurfaced.Num, m.TransferSurfaced.Den)
	}
	if m.TransferUsedGivenSurfaced.Num != 1 || m.TransferUsedGivenSurfaced.Den != 2 {
		t.Errorf("used given surfaced = %d/%d, want 1/2", m.TransferUsedGivenSurfaced.Num, m.TransferUsedGivenSurfaced.Den)
	}
	// Capture misses are runs d and e; only d was budget-starved before capture.
	if m.CaptureBudgetStarved.Num != 1 || m.CaptureBudgetStarved.Den != 2 {
		t.Errorf("capture budget-starved = %d/%d, want 1/2", m.CaptureBudgetStarved.Num, m.CaptureBudgetStarved.Den)
	}
}

// TestTransferSurfacedWiring drives the full lifecycle and asserts TransferSurfaced
// tracks whether the promoted fact reached the learner in a tool result.
func TestTransferSurfacedWiring(t *testing.T) {
	t.Run("surfaced and used", func(t *testing.T) {
		fp := newFakePlatform(t)
		fp.surfaceText = okProtocol().Fact // cross-enrichment carries the fact into search results
		dir := t.TempDir()
		writeProtocols(t, dir, okProtocol())
		res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(okScript())))
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		run := res.Runs[0]
		assertTrue(t, "transfer surfaced", run.TransferSurfaced)
		assertTrue(t, "transfer correct", run.TransferCorrect)
		if res.Metrics.TransferUsedGivenSurfaced.Rate != 1 {
			t.Fatalf("used-given-surfaced = %v, want 1", res.Metrics.TransferUsedGivenSurfaced.Rate)
		}
	})

	t.Run("surfaced but not used", func(t *testing.T) {
		fp := newFakePlatform(t)
		fp.surfaceText = okProtocol().Fact
		dir := t.TempDir()
		writeProtocols(t, dir, okProtocol())
		scripts := okScript()
		// The learner sees the fact but answers wrong: surfaced, not used.
		scripts["lc-ok"][StageTransfer] = []llm.Step{searchStep(), {FinalText: "FINAL ANSWER: 0.00"}}
		res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(scripts)))
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		run := res.Runs[0]
		assertTrue(t, "transfer surfaced", run.TransferSurfaced)
		assertFalse(t, "transfer correct", run.TransferCorrect)
	})

	t.Run("promoted but not surfaced", func(t *testing.T) {
		fp := newFakePlatform(t) // surfaceText empty: the fact never reaches the learner
		dir := t.TempDir()
		writeProtocols(t, dir, okProtocol())
		res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(okScript())))
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		assertFalse(t, "transfer surfaced", res.Runs[0].TransferSurfaced)
	})
}

// starvedProtocol teaches under a tight budget, so a teacher that searches
// without capturing runs out of budget before ever reaching the capture tool.
func starvedProtocol() protocol.Protocol {
	p := okProtocol()
	p.ID = "lc-starve"
	p.Transfer = nil // capture fails, so the advanced stages never run anyway
	p.BudgetToolCalls = 2
	return p
}

// starvedScript never calls capture and burns the whole budget searching.
func starvedScript() map[string]llm.Script {
	return map[string]llm.Script{
		"lc-starve": {
			StageTeach:   {searchStep(), searchStep(), searchStep(), {FinalText: "could not save in time"}},
			StageRecall:  {searchStep(), {FinalText: "FINAL ANSWER: 999.99"}},
			StageAbstain: {{FinalText: "FINAL ANSWER: INSUFFICIENT INFORMATION"}},
		},
	}
}

func TestCaptureBudgetStarvation(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	writeProtocols(t, dir, starvedProtocol())
	res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(starvedScript())))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	run := res.Runs[0]
	if run.Error != "" {
		t.Fatalf("unexpected harness error: %s", run.Error)
	}
	assertFalse(t, "captured", run.Captured)
	assertFalse(t, "capture attempted", run.CaptureAttempted)
	assertTrue(t, "teach budget exhausted", run.TeachBudgetExhausted)
	if res.Metrics.CaptureBudgetStarved.Num != 1 || res.Metrics.CaptureBudgetStarved.Den != 1 {
		t.Fatalf("capture budget-starved = %d/%d, want 1/1",
			res.Metrics.CaptureBudgetStarved.Num, res.Metrics.CaptureBudgetStarved.Den)
	}
}

// TestTeachBudgetOverrideEnablesCapture shows the capture-budget lever: the same
// teacher that starves at the protocol budget captures when given a larger one.
func TestTeachBudgetOverrideEnablesCapture(t *testing.T) {
	dir := t.TempDir()
	p := starvedProtocol()
	p.ID = "lc-lever"
	writeProtocols(t, dir, p)
	// A teacher that searches three times, THEN captures — reaching capture only
	// if the budget allows a fourth tool call.
	scripts := map[string]llm.Script{
		"lc-lever": {
			StageTeach:   {searchStep(), searchStep(), searchStep(), captureStep("definition"), {FinalText: "saved"}},
			StageRecall:  {searchStep(), {FinalText: "FINAL ANSWER: 123.45"}},
			StageAbstain: {{FinalText: "FINAL ANSWER: INSUFFICIENT INFORMATION"}},
		},
	}

	t.Run("starves at protocol budget", func(t *testing.T) {
		fp := newFakePlatform(t)
		res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(scripts)))
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		run := res.Runs[0]
		assertFalse(t, "captured without override", run.Captured)
		// The teacher does emit a capture call, but only after the budget is spent,
		// so it is budget-refused (never executed): this is a budget-starvation
		// miss, not an attempted-but-unlanded capture.
		assertFalse(t, "capture attempted (budget-refused capture does not count)", run.CaptureAttempted)
		if res.Metrics.CaptureBudgetStarved.Num != 1 || res.Metrics.CaptureBudgetStarved.Den != 1 {
			t.Fatalf("capture budget-starved = %d/%d, want 1/1 (a budget-refused capture is a starvation miss)",
				res.Metrics.CaptureBudgetStarved.Num, res.Metrics.CaptureBudgetStarved.Den)
		}
	})

	t.Run("captures with a larger teach budget", func(t *testing.T) {
		fp := newFakePlatform(t)
		opts := runOptions(fp, dir, scriptFactory(scripts))
		opts.TeachBudget = 10
		res, err := Run(context.Background(), opts)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		run := res.Runs[0]
		assertTrue(t, "captured with override", run.Captured)
		assertTrue(t, "capture attempted", run.CaptureAttempted)
		assertFalse(t, "teach budget exhausted", run.TeachBudgetExhausted)
	})
}
