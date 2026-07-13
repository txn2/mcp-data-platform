package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validYAML() string {
	return `
id: s3-test
suite: s3
prompt: what is x?
arms: [a0, a2]
budget_tool_calls: 10
grading:
  kind: numeric
  value: 42.5
  abs_tolerance: 0.01
`
}

func writeTask(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	writeTask(t, dir, "a.yaml", validYAML())
	tasks, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "s3-test" || *tasks[0].Grading.Value != 42.5 {
		t.Errorf("loaded wrong: %+v", tasks)
	}
	if !tasks[0].AppliesTo("a0") || tasks[0].AppliesTo("a1") {
		t.Error("arm applicability wrong")
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	cases := []struct {
		name, yaml, wantErr string
	}{
		{"missing id", "suite: s1\nprompt: p\narms: [a0]\nbudget_tool_calls: 1\ngrading: {kind: entity, aliases: [x]}", "empty id"},
		{"no budget", "id: t\nsuite: s1\nprompt: p\narms: [a0]\ngrading: {kind: entity, aliases: [x]}", "budget"},
		{"numeric no value", "id: t\nsuite: s3\nprompt: p\narms: [a0]\nbudget_tool_calls: 1\ngrading: {kind: numeric}", "requires value"},
		{"entity no aliases", "id: t\nsuite: s1\nprompt: p\narms: [a0]\nbudget_tool_calls: 1\ngrading: {kind: entity}", "requires aliases"},
		{"unknown kind", "id: t\nsuite: s1\nprompt: p\narms: [a0]\nbudget_tool_calls: 1\ngrading: {kind: vibes}", "unknown grading kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTask(t, dir, "t.yaml", c.yaml)
			_, err := Load(dir)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("want error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestLoadRejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeTask(t, dir, "a.yaml", validYAML())
	writeTask(t, dir, "b.yaml", validYAML())
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("want duplicate error, got %v", err)
	}
}

func TestLoadEmptyDir(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Error("want error for empty dir")
	}
}

func TestHashStableAndOrderIndependent(t *testing.T) {
	a := Task{ID: "a", Suite: "s1", Prompt: "p", Arms: []string{"a0"}, BudgetToolCalls: 1,
		Grading: Grading{Kind: GradeEntity, Aliases: []string{"x"}}}
	b := Task{ID: "b", Suite: "s1", Prompt: "q", Arms: []string{"a0"}, BudgetToolCalls: 1,
		Grading: Grading{Kind: GradeEntity, Aliases: []string{"y"}}}
	h1 := Hash([]Task{a, b})
	h2 := Hash([]Task{b, a})
	if h1 != h2 {
		t.Error("hash must be order independent")
	}
	b.Prompt = "changed"
	if Hash([]Task{a, b}) == h1 {
		t.Error("hash must change when a task changes")
	}
}
