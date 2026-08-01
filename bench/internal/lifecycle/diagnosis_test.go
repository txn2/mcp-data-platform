package lifecycle

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/capture"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
	"github.com/txn2/mcp-data-platform/bench/internal/stats"
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

// TestCaptureSplitAttribution is the issue #1136 acceptance criterion on the S5
// scorecard: capture is reported with an attempted/landed split over the same
// denominator as the headline rate, and every capture miss in the run is
// attributed to exactly one cause.
func TestCaptureSplitAttribution(t *testing.T) {
	res := &Results{
		Manifest: Manifest{K: 1},
		Runs: []ProtocolRun{
			{ProtocolID: "a", Captured: new(true), CaptureAttempted: new(true), TeachBudgetExhausted: new(false)},
			{ProtocolID: "b", Captured: new(true), CaptureAttempted: new(true), TeachBudgetExhausted: new(false)},
			// miss: capture ran, the insight did not land (the capture path itself)
			{ProtocolID: "c", Captured: new(false), CaptureAttempted: new(true), TeachBudgetExhausted: new(false)},
			// miss: never reached capture, budget spent on discovery (the harness budget)
			{ProtocolID: "d", Captured: new(false), CaptureAttempted: new(false), TeachBudgetExhausted: new(true)},
			// miss: never called capture with budget to spare (the model or its steering)
			{ProtocolID: "e", Captured: new(false), CaptureAttempted: new(false), TeachBudgetExhausted: new(false)},
			// miss on the claude-cli path: budget exhaustion is not observable
			{ProtocolID: "f", Captured: new(false), CaptureAttempted: new(false)},
			// harness failure: excluded from every capture denominator
			{ProtocolID: "g", Error: "teach: connect refused"},
		},
	}
	res.Aggregate()
	m := res.Metrics

	if m.CaptureRate.Num != 2 || m.CaptureRate.Den != 6 {
		t.Errorf("capture rate = %d/%d, want 2/6", m.CaptureRate.Num, m.CaptureRate.Den)
	}
	if m.Capture.AttemptRate.Num != 3 || m.Capture.AttemptRate.Den != 6 {
		t.Errorf("capture attempted = %d/%d, want 3/6 (same denominator as the capture rate)",
			m.Capture.AttemptRate.Num, m.Capture.AttemptRate.Den)
	}
	if m.Capture.GivenAttempted.Num != 2 || m.Capture.GivenAttempted.Den != 3 {
		t.Errorf("landed given attempt = %d/%d, want 2/3", m.Capture.GivenAttempted.Num, m.Capture.GivenAttempted.Den)
	}
	misses := m.Capture.Misses
	want := capture.Misses{Total: 4, AttemptedFailed: 1, BudgetStarved: 1, NeverAttempted: 1, BudgetUnobservable: 1}
	if misses != want {
		t.Errorf("capture misses = %+v, want %+v", misses, want)
	}
	if sum := misses.AttemptedFailed + misses.BudgetStarved + misses.NeverAttempted + misses.BudgetUnobservable + misses.Unattributed; sum != misses.Total {
		t.Errorf("cause buckets sum to %d of %d misses — a miss was left unattributed", sum, misses.Total)
	}
	// The breakdown must agree with the #964 starvation rate it decomposes: both
	// count the same starved miss, over the three misses whose budget is observable.
	if m.CaptureBudgetStarved.Num != misses.BudgetStarved || m.CaptureBudgetStarved.Den != 3 {
		t.Errorf("capture budget-starved = %d/%d, want %d/3 (must agree with the miss breakdown)",
			m.CaptureBudgetStarved.Num, m.CaptureBudgetStarved.Den, misses.BudgetStarved)
	}
	// The split carries the same uncertainty signal the headline rate does.
	if m.Capture.AttemptRate.CILow == m.Capture.AttemptRate.CIHigh {
		t.Errorf("capture attempt rate CI = [%v, %v], want a non-degenerate interval on a mixed sample",
			m.Capture.AttemptRate.CILow, m.Capture.AttemptRate.CIHigh)
	}

	summary := res.HumanSummary()
	for _, w := range []string{"capture rate", "capture attempted", "landed given attempt", "capture misses (4)"} {
		if !strings.Contains(summary, w) {
			t.Errorf("summary missing %q:\n%s", w, summary)
		}
	}
}

// TestCaptureRateCIsUnaffectedByTheSplit pins the reproducibility contract the
// split had to preserve: the capture-split rates are filled from the shared RNG
// LAST, so adding them left every previously reported interval identical.
func TestCaptureRateCIsUnaffectedByTheSplit(t *testing.T) {
	runs := []ProtocolRun{
		{ProtocolID: "a", Captured: new(true), RecallCorrect: new(true), CaptureAttempted: new(true), TeachBudgetExhausted: new(false)},
		{ProtocolID: "b", Captured: new(false), RecallCorrect: new(false), CaptureAttempted: new(false), TeachBudgetExhausted: new(true)},
		{ProtocolID: "c", Captured: new(true), RecallCorrect: new(false), CaptureAttempted: new(true), TeachBudgetExhausted: new(false)},
	}
	withSplit := &Results{Manifest: Manifest{K: 1}, Runs: runs}
	withSplit.Aggregate()

	// The pre-#1136 fill order, reproduced exactly: the twelve scorecard rates in
	// their original sequence, with no capture-split rates drawn from the RNG.
	m := withSplit.Metrics
	stats.FillCIs(stats.NewRNG(),
		&m.CaptureRate, &m.PersonalRecall, &m.UnpromptedSurface,
		&m.TransferRate, &m.TransferSurfaced, &m.TransferUsedGivenSurfaced,
		&m.UpdateCorrectness, &m.UpdateCaptureRate, &m.DuplicateRate, &m.AbstentionRate,
		&m.CaptureBudgetStarved, &m.PassK,
	)
	if m.CaptureRate != withSplit.Metrics.CaptureRate || m.PersonalRecall != withSplit.Metrics.PersonalRecall {
		t.Errorf("adding the capture split shifted an existing interval:\n capture   %+v vs %+v\n recall    %+v vs %+v",
			m.CaptureRate, withSplit.Metrics.CaptureRate, m.PersonalRecall, withSplit.Metrics.PersonalRecall)
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

// misfiledProtocol teaches with enough budget to reach capture; its script files
// the insight against the wrong entity, so the capture executes but no linked
// insight lands — the attempted-and-failed miss, which points at the capture
// path rather than at the budget or the agent's willingness to try.
func misfiledProtocol() protocol.Protocol {
	p := okProtocol()
	p.ID = "lc-misfile"
	p.Transfer = nil
	return p
}

func misfiledScript() map[string]llm.Script {
	misfiled := llm.Step{ToolCalls: []llm.ToolCall{{Name: "memory_capture", Args: map[string]any{
		"text": "net revenue fact", "entity_urns": []any{"urn:li:dataset:(urn:li:dataPlatform:trino,bench.other.table,PROD)"},
		"category": "definition",
	}}}}
	return map[string]llm.Script{
		"lc-misfile": {
			StageTeach:   {misfiled, {FinalText: "saved"}},
			StageRecall:  {searchStep(), {FinalText: "FINAL ANSWER: 999.99"}},
			StageAbstain: {{FinalText: "FINAL ANSWER: INSUFFICIENT INFORMATION"}},
		},
	}
}

// TestCaptureMissAttributionEndToEnd drives the real runner over two protocols
// that miss capture for different reasons and asserts the run attributes each
// one to its own cause — the issue #1136 criterion that a miss is never just a
// count. The two causes imply fixes in different layers, so conflating them
// would misdirect any capture work that follows.
func TestCaptureMissAttributionEndToEnd(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	writeProtocols(t, dir, starvedProtocol(), misfiledProtocol())
	scripts := starvedScript()
	maps.Copy(scripts, misfiledScript())
	res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(scripts)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	byProtocol := map[string]ProtocolRun{}
	for _, r := range res.Runs {
		if r.Error != "" {
			t.Fatalf("%s: unexpected harness error: %s", r.ProtocolID, r.Error)
		}
		byProtocol[r.ProtocolID] = r
	}
	starved, misfiled := byProtocol["lc-starve"], byProtocol["lc-misfile"]
	assertFalse(t, "starved captured", starved.Captured)
	assertFalse(t, "starved capture attempted", starved.CaptureAttempted)
	assertFalse(t, "misfiled captured", misfiled.Captured)
	assertTrue(t, "misfiled capture attempted", misfiled.CaptureAttempted)

	m := res.Metrics
	if m.CaptureRate.Num != 0 || m.CaptureRate.Den != 2 {
		t.Fatalf("capture rate = %d/%d, want 0/2", m.CaptureRate.Num, m.CaptureRate.Den)
	}
	if m.Capture.AttemptRate.Num != 1 || m.Capture.AttemptRate.Den != 2 {
		t.Errorf("capture attempted = %d/%d, want 1/2", m.Capture.AttemptRate.Num, m.Capture.AttemptRate.Den)
	}
	if m.Capture.GivenAttempted.Num != 0 || m.Capture.GivenAttempted.Den != 1 {
		t.Errorf("landed given attempt = %d/%d, want 0/1", m.Capture.GivenAttempted.Num, m.Capture.GivenAttempted.Den)
	}
	want := capture.Misses{Total: 2, AttemptedFailed: 1, BudgetStarved: 1}
	if m.Capture.Misses != want {
		t.Errorf("capture misses = %+v, want %+v", m.Capture.Misses, want)
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
