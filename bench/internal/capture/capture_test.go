package capture

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/agent"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/stats"
)

// executedCapture is a two-turn transcript: an assistant capture request paired
// with a non-refusal tool result, i.e. a capture that actually ran.
func executedCapture(name string) []llm.Message {
	return []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: name}}},
		{Role: "user", ToolResults: []llm.ToolResult{{CallID: "c1", Text: "captured in-1"}}},
	}
}

func TestAttempted(t *testing.T) {
	// A capture request the budget refused (only its refusal result is present)
	// must not count as an executed capture.
	budgetRefused := []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "memory_capture"}}},
		{Role: "user", ToolResults: []llm.ToolResult{{CallID: "c1", Text: agent.BudgetRefusalText, IsError: true}}},
	}
	// A capture that ran but errored server-side still counts (a landing failure,
	// not a budget-starvation miss).
	serverError := []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "memory_capture"}}},
		{Role: "user", ToolResults: []llm.ToolResult{{CallID: "c1", Text: "capture failed: entity not found", IsError: true}}},
	}
	cases := []struct {
		name string
		msgs []llm.Message
		want bool
	}{
		{"empty", nil, false},
		{"only search", []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{Name: "search"}}}}, false},
		{"executed memory_capture", executedCapture("memory_capture"), true},
		{"executed suffix capture", executedCapture("knowledge_capture"), true},
		{"namespaced claude-cli capture", executedCapture("mcp__bench__memory_capture"), true},
		{"apply is not capture", []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "apply_knowledge"}}},
			{Role: "user", ToolResults: []llm.ToolResult{{CallID: "c1", Text: "applied"}}}}, false},
		{"capture requested but never executed (no result)",
			[]llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "memory_capture"}}}}, false},
		{"capture budget-refused", budgetRefused, false},
		{"capture ran but errored", serverError, true},
	}
	for _, c := range cases {
		if got := Attempted(c.msgs); got != c.want {
			t.Errorf("%s: Attempted = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name                              string
		captured, attempted, budgetExhaus *bool
		want                              Cause
	}{
		{"never reached", nil, new(true), new(false), CauseNone},
		{"captured", new(true), new(true), new(false), CauseNone},
		{"captured despite no observed attempt", new(true), new(false), new(true), CauseNone},
		{"attempted and failed", new(false), new(true), new(false), CauseAttemptedFailed},
		{"attempted and failed, budget also exhausted", new(false), new(true), new(true), CauseAttemptedFailed},
		{"never attempted, budget exhausted", new(false), new(false), new(true), CauseBudgetStarved},
		{"never attempted, budget remained", new(false), new(false), new(false), CauseNeverAttempted},
		{"never attempted, budget unobservable", new(false), new(false), nil, CauseBudgetUnobservable},
		{"legacy record with no attempt signal", new(false), nil, new(true), CauseUnattributed},
	}
	for _, c := range cases {
		if got := Classify(c.captured, c.attempted, c.budgetExhaus); got != c.want {
			t.Errorf("%s: Classify = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestSplitAttributesEveryMiss is the issue #1136 acceptance criterion: every
// graded capture miss lands in exactly one cause bucket, and the buckets sum to
// the miss total, so a run can never report a miss it cannot attribute.
func TestSplitAttributesEveryMiss(t *testing.T) {
	var s Split
	outcomes := []struct{ captured, attempted, budget *bool }{
		{new(true), new(true), new(false)},   // captured
		{new(true), new(true), new(false)},   // captured
		{new(false), new(true), new(false)},  // attempted, did not land
		{new(false), new(false), new(true)},  // budget starved
		{new(false), new(false), new(false)}, // never attempted, budget left
		{new(false), new(false), nil},        // claude-cli: budget unobservable
		{new(false), nil, nil},               // legacy record
		{nil, new(false), new(false)},        // never reached: excluded entirely
	}
	for _, o := range outcomes {
		s.Add(o.captured, o.attempted, o.budget)
	}

	m := s.Misses
	if m.Total != 5 {
		t.Fatalf("miss total = %d, want 5", m.Total)
	}
	if sum := m.AttemptedFailed + m.BudgetStarved + m.NeverAttempted + m.BudgetUnobservable + m.Unattributed; sum != m.Total {
		t.Errorf("cause buckets sum to %d, want %d — a miss was left unattributed", sum, m.Total)
	}
	want := Misses{Total: 5, AttemptedFailed: 1, BudgetStarved: 1, NeverAttempted: 1, BudgetUnobservable: 1, Unattributed: 1}
	if m != want {
		t.Errorf("misses = %+v, want %+v", m, want)
	}
	// The attempt denominator counts the six outcomes with an attempt signal that
	// were graded; the legacy record (no signal) and the never-reached run are
	// both excluded rather than counted as "not attempted".
	if s.AttemptRate.Num != 3 || s.AttemptRate.Den != 6 {
		t.Errorf("attempt rate = %d/%d, want 3/6", s.AttemptRate.Num, s.AttemptRate.Den)
	}
	if s.GivenAttempted.Num != 2 || s.GivenAttempted.Den != 3 {
		t.Errorf("landed given attempt = %d/%d, want 2/3", s.GivenAttempted.Num, s.GivenAttempted.Den)
	}
}

func TestSplitFillCIsAndRender(t *testing.T) {
	var s Split
	for range 4 {
		s.Add(new(true), new(true), new(false))
	}
	s.Add(new(false), new(false), new(true))
	s.FillCIs(stats.NewRNG())
	if s.AttemptRate.CILow == s.AttemptRate.CIHigh {
		t.Errorf("attempt rate CI = [%v, %v], want a non-degenerate interval on a mixed sample",
			s.AttemptRate.CILow, s.AttemptRate.CIHigh)
	}
	rows := s.Rows()
	for _, want := range []string{"capture attempted", "landed given attempt", "95% CI"} {
		if !strings.Contains(rows, want) {
			t.Errorf("split rows missing %q:\n%s", want, rows)
		}
	}
	block := s.MissBlock()
	if !strings.Contains(block, "capture misses (1)") || !strings.Contains(block, CauseBudgetStarved.String()) {
		t.Errorf("miss block did not attribute the miss:\n%s", block)
	}
}

// TestUnattributedMissWarns proves an unattributed miss is flagged rather than
// silently bucketed: for a file this harness wrote it would mean the attempt
// signal stopped being recorded, which is a wiring regression.
func TestUnattributedMissWarns(t *testing.T) {
	var s Split
	s.Add(new(false), nil, nil)
	block := s.MissBlock()
	if !strings.Contains(block, "WARNING: 1 capture miss(es) carry no attempt signal") {
		t.Errorf("miss block did not warn about the unattributed miss:\n%s", block)
	}
	// A fully attributed run carries no warning.
	var clean Split
	clean.Add(new(false), new(false), new(true))
	if strings.Contains(clean.MissBlock(), "WARNING") {
		t.Errorf("attributed miss must not warn:\n%s", clean.MissBlock())
	}
}

func TestMissBlockEmptyWhenNothingMissed(t *testing.T) {
	var s Split
	s.Add(new(true), new(true), new(false))
	if got := s.MissBlock(); got != "" {
		t.Errorf("miss block = %q, want empty when nothing missed", got)
	}
}

func TestCauseString(t *testing.T) {
	if got := CauseNone.String(); got != "" {
		t.Errorf("CauseNone.String() = %q, want empty", got)
	}
	if got := Cause("future_cause").String(); got != "future_cause" {
		t.Errorf("unknown cause = %q, want its raw value", got)
	}
	for _, c := range []Cause{CauseAttemptedFailed, CauseBudgetStarved, CauseNeverAttempted, CauseBudgetUnobservable, CauseUnattributed} {
		if c.String() == "" || c.String() == string(c) {
			t.Errorf("cause %q has no human phrase", c)
		}
	}
}

func TestMissesAddIgnoresNonMiss(t *testing.T) {
	var m Misses
	m.add(CauseNone)
	m.add(Cause("unknown"))
	if m.Total != 0 {
		t.Errorf("miss total = %d, want 0 for non-miss causes", m.Total)
	}
}
