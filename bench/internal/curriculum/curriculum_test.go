package curriculum

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

// validLesson returns a minimal well-formed datahub-sink lesson.
func validLesson() Lesson {
	return Lesson{
		ID: "cs-a", Title: "A", TrapClass: "units_cents", Fact: "amounts are cents",
		EntityURN: "urn:li:dataset:x", Sink: protocol.SinkDataHub, BudgetToolCalls: 5,
		Teach: protocol.TeachStage{Prompt: "remember this"},
	}
}

func validCurriculum() Curriculum {
	return Curriculum{ID: "cs-traps", Title: "traps", EvalSuite: "s3", Lessons: []Lesson{validLesson()}}
}

func TestValidateAcceptsWellFormed(t *testing.T) {
	if err := validCurriculum().Validate(); err != nil {
		t.Fatalf("valid curriculum rejected: %v", err)
	}
	// A page-sink lesson with a complete payload (summary included) is valid.
	c := validCurriculum()
	c.Lessons[0].Sink = protocol.SinkKnowledgePage
	c.Lessons[0].Page = &protocol.PagePayload{Slug: "s", Title: "T", Summary: "the fact", Body: "B"}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid page-sink curriculum rejected: %v", err)
	}
}

func TestValidateRejectsMalformed(t *testing.T) {
	cases := map[string]func(*Curriculum){
		"empty id":         func(c *Curriculum) { c.ID = "" },
		"empty title":      func(c *Curriculum) { c.Title = "" },
		"empty eval_suite": func(c *Curriculum) { c.EvalSuite = "" },
		"no lessons":       func(c *Curriculum) { c.Lessons = nil },
		"lesson empty id":  func(c *Curriculum) { c.Lessons[0].ID = "" },
		"lesson empty fact": func(c *Curriculum) {
			c.Lessons[0].Fact = ""
		},
		"lesson empty entity": func(c *Curriculum) { c.Lessons[0].EntityURN = "" },
		"lesson empty trap":   func(c *Curriculum) { c.Lessons[0].TrapClass = "" },
		"lesson zero budget":  func(c *Curriculum) { c.Lessons[0].BudgetToolCalls = 0 },
		"lesson empty teach":  func(c *Curriculum) { c.Lessons[0].Teach.Prompt = "" },
		"unknown sink":        func(c *Curriculum) { c.Lessons[0].Sink = "s3" },
		"page sink no payload": func(c *Curriculum) {
			c.Lessons[0].Sink = protocol.SinkKnowledgePage
			c.Lessons[0].Page = nil
		},
		"page sink partial payload": func(c *Curriculum) {
			c.Lessons[0].Sink = protocol.SinkKnowledgePage
			c.Lessons[0].Page = &protocol.PagePayload{Slug: "s", Title: "", Summary: "f", Body: "B"}
		},
		// A page without a summary is a title-only search hit on the a3 tool
		// surface (no page-body fetch), so the fact never reaches an evaluator.
		"page sink empty summary": func(c *Curriculum) {
			c.Lessons[0].Sink = protocol.SinkKnowledgePage
			c.Lessons[0].Page = &protocol.PagePayload{Slug: "s", Title: "T", Body: "B"}
		},
		"duplicate lesson id": func(c *Curriculum) {
			c.Lessons = append(c.Lessons, c.Lessons[0])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := validCurriculum()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Errorf("%s: expected validation error, got nil", name)
			}
		})
	}
}

func TestLoadReadsAndValidates(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, filepath.Join(dir, "cs-traps.yaml"), `
id: cs-traps
title: traps
eval_suite: s3
lessons:
  - id: cs-a
    title: A
    trap_class: units_cents
    fact: amounts are cents
    entity_urn: urn:li:dataset:x
    sink: datahub
    budget_tool_calls: 5
    teach:
      prompt: remember this
`)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].ID != "cs-traps" || len(got[0].Lessons) != 1 {
		t.Fatalf("unexpected load result: %+v", got)
	}
}

func TestLoadRejectsInvalidAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, filepath.Join(dir, "bad.yaml"), "id: x\ntitle: t\neval_suite: s3\nlessons: []\n")
	if _, err := Load(dir); err == nil {
		t.Error("expected load to reject a curriculum with no lessons")
	}

	dir2 := t.TempDir()
	one := `
id: dup
title: t
eval_suite: s3
lessons:
  - {id: l, title: L, trap_class: units_cents, fact: f, entity_urn: u, sink: datahub, budget_tool_calls: 1, teach: {prompt: p}}
`
	writeYAML(t, filepath.Join(dir2, "a.yaml"), one)
	writeYAML(t, filepath.Join(dir2, "b.yaml"), one)
	if _, err := Load(dir2); err == nil {
		t.Error("expected load to reject duplicate curriculum ids")
	}

	if _, err := Load(t.TempDir()); err == nil {
		t.Error("expected load to fail on an empty directory")
	}
}

func TestHashIsStableAndOrderIndependent(t *testing.T) {
	a := validCurriculum()
	b := validCurriculum()
	b.ID = "cs-other"
	if Hash([]Curriculum{a, b}) != Hash([]Curriculum{b, a}) {
		t.Error("hash must not depend on curriculum order")
	}
	if Hash([]Curriculum{a}) == Hash([]Curriculum{b}) {
		t.Error("distinct curricula must hash differently")
	}
	changed := validCurriculum()
	changed.Lessons[0].Fact = "different fact"
	if Hash([]Curriculum{a}) == Hash([]Curriculum{changed}) {
		t.Error("a changed lesson must change the hash")
	}
}

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
