package protocol

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// validProtocol is a fully-populated protocol used as the mutation base.
func validProtocol() Protocol {
	return Protocol{
		ID:              "lc-example",
		Title:           "Example lifecycle",
		Fact:            "Revenue is amount minus discount over completed orders only.",
		EntityURN:       "urn:li:dataset:(urn:li:dataPlatform:trino,memory.bench.orders,PROD)",
		Sink:            SinkDataHub,
		BudgetToolCalls: 20,
		Teach:           TeachStage{Prompt: "Remember: revenue is net."},
		Recall: RecallStage{
			Prompt:  "What is net revenue for 2025?",
			Grading: task.Grading{Kind: task.GradeNumeric, Value: new(123.45), AbsTolerance: 1},
		},
		Transfer: &RecallStage{
			Prompt:  "What is net revenue for 2025?",
			Grading: task.Grading{Kind: task.GradeNumeric, Value: new(123.45), AbsTolerance: 1},
		},
		Update: &UpdateStage{
			Prompt: "Correction: net revenue also excludes tax.",
			Fact:   "Net revenue excludes tax.",
			Recall: RecallStage{
				Prompt:  "What is net revenue for 2025 now?",
				Grading: task.Grading{Kind: task.GradeNumeric, Value: new(100.0), AbsTolerance: 1},
			},
			SupersededValue: new(123.45),
		},
		Abstain: &AbstainStage{Prompt: "What is the refund rate for the Antarctica region?"},
	}
}

func TestValidateAcceptsCompleteProtocol(t *testing.T) {
	if err := validProtocol().Validate(); err != nil {
		t.Fatalf("valid protocol rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Protocol){
		"empty id":     func(p *Protocol) { p.ID = "" },
		"empty title":  func(p *Protocol) { p.Title = "" },
		"empty fact":   func(p *Protocol) { p.Fact = "" },
		"empty entity": func(p *Protocol) { p.EntityURN = "" },
		"zero budget":  func(p *Protocol) { p.BudgetToolCalls = 0 },
		"unknown sink": func(p *Protocol) { p.Sink = "email" },
		"empty teach":  func(p *Protocol) { p.Teach.Prompt = "" },
		"empty recall": func(p *Protocol) { p.Recall.Prompt = "" },
		"bad recall grade": func(p *Protocol) {
			p.Recall.Grading = task.Grading{Kind: task.GradeNumeric} // no value
		},
		"exec_sql recall": func(p *Protocol) {
			p.Recall.Grading = task.Grading{Kind: task.GradeExecSQL}
		},
		"page sink no payload": func(p *Protocol) {
			p.Sink = SinkKnowledgePage
			p.Page = nil
		},
		"page sink partial payload": func(p *Protocol) {
			p.Sink = SinkKnowledgePage
			p.Page = &PagePayload{Slug: "s", Title: "", Body: "b"}
		},
		"update empty prompt": func(p *Protocol) { p.Update.Prompt = "" },
		"update empty fact":   func(p *Protocol) { p.Update.Fact = "" },
		"update bad recall": func(p *Protocol) {
			p.Update.Recall.Grading = task.Grading{Kind: task.GradeEntity} // no aliases
		},
		"transfer bad grade": func(p *Protocol) {
			p.Transfer.Grading = task.Grading{Kind: "bogus"}
		},
		"abstain empty prompt": func(p *Protocol) { p.Abstain.Prompt = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := validProtocol()
			mutate(&p)
			if err := p.Validate(); err == nil {
				t.Fatalf("expected validation error for %q", name)
			}
		})
	}
}

func TestValidatePageSinkAccepted(t *testing.T) {
	p := validProtocol()
	p.Sink = SinkKnowledgePage
	p.Page = &PagePayload{Slug: "revenue", Title: "Revenue", Body: "net revenue"}
	if err := p.Validate(); err != nil {
		t.Fatalf("page-sink protocol rejected: %v", err)
	}
}

func TestOptionalStagesMayBeAbsent(t *testing.T) {
	p := validProtocol()
	p.Transfer = nil
	p.Update = nil
	p.Abstain = nil
	if err := p.Validate(); err != nil {
		t.Fatalf("protocol without optional stages rejected: %v", err)
	}
}

func TestLoadAndHash(t *testing.T) {
	dir := t.TempDir()
	p := validProtocol()
	writeProtocolYAML(t, dir, "b.yaml", p)
	other := validProtocol()
	other.ID = "lc-other"
	writeProtocolYAML(t, dir, "a.yaml", other)

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d protocols, want 2", len(got))
	}
	// Sorted by filename: a.yaml (lc-other) then b.yaml (lc-example).
	if got[0].ID != "lc-other" || got[1].ID != "lc-example" {
		t.Fatalf("unexpected order: %s, %s", got[0].ID, got[1].ID)
	}

	// Hash is order-independent (sorted by ID) and deterministic.
	h1 := Hash(got)
	reversed := []Protocol{got[1], got[0]}
	if h2 := Hash(reversed); h1 != h2 {
		t.Fatalf("hash not order-independent: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("hash length %d, want 64", len(h1))
	}
}

func TestLoadRejectsDuplicateID(t *testing.T) {
	dir := t.TempDir()
	writeProtocolYAML(t, dir, "a.yaml", validProtocol())
	writeProtocolYAML(t, dir, "b.yaml", validProtocol())
	if _, err := Load(dir); err == nil {
		t.Fatal("expected duplicate-id error")
	}
}

func TestLoadRejectsEmptyDir(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func writeProtocolYAML(t *testing.T, dir, name string, p Protocol) {
	t.Helper()
	raw, err := yaml.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}
