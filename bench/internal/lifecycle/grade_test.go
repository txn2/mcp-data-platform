package lifecycle

import (
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

func TestAbstains(t *testing.T) {
	cases := map[string]bool{
		"FINAL ANSWER: INSUFFICIENT INFORMATION":                true,
		"reasoning...\nFINAL ANSWER: I do not know":             true,
		"FINAL ANSWER: The data cannot be determined from here": true,
		"FINAL ANSWER: unknown":                                 true,
		"FINAL ANSWER: 42.0":                                    false,
		"FINAL ANSWER: memory.bench.orders":                     false,
		"no marker at all, just prose":                          false,
	}
	for answer, want := range cases {
		if got := abstains(answer); got != want {
			t.Errorf("abstains(%q) = %v, want %v", answer, got, want)
		}
	}
}

func TestGradeRecall(t *testing.T) {
	num := task.Grading{Kind: task.GradeNumeric, Value: new(100.0), AbsTolerance: 0.5}
	if !gradeRecall("FINAL ANSWER: 100.2", num) {
		t.Error("numeric within tolerance should be correct")
	}
	if gradeRecall("FINAL ANSWER: 105", num) {
		t.Error("numeric outside tolerance should be incorrect")
	}
	ent := task.Grading{Kind: task.GradeEntity, Aliases: []string{"memory.bench.orders"}, WrongAliases: []string{"legacy_orders"}}
	if !gradeRecall("FINAL ANSWER: memory.bench.orders", ent) {
		t.Error("entity alias should match")
	}
	if gradeRecall("FINAL ANSWER: memory.bench.legacy_orders", ent) {
		t.Error("wrong alias should fail")
	}
	if gradeRecall("FINAL ANSWER: whatever", task.Grading{Kind: "unknown"}) {
		t.Error("unknown grading kind should be incorrect")
	}
}

func TestGradeUpdate(t *testing.T) {
	base := protocol.UpdateStage{
		Recall:          protocol.RecallStage{Grading: task.Grading{Kind: task.GradeNumeric, Value: new(200.0), AbsTolerance: 0.5}},
		SupersededValue: new(123.45),
	}
	// Correct new value, distinct from the superseded one -> pass.
	if got := gradeUpdate("FINAL ANSWER: 200.0", base); !*got {
		t.Error("flipped-to-new answer should pass")
	}
	// Wrong value -> fail.
	if got := gradeUpdate("FINAL ANSWER: 999", base); *got {
		t.Error("wrong value should fail")
	}
	// New value coincides with the superseded value within tolerance -> the
	// stale guard rejects it even though it matches the "new" grade.
	stale := base
	stale.Recall.Grading.Value = new(123.45)
	stale.SupersededValue = new(123.45)
	if got := gradeUpdate("FINAL ANSWER: 123.45", stale); *got {
		t.Error("answer equal to the superseded value must not pass")
	}
}
