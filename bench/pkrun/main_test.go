package main

import (
	"context"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/pkcell"
	"github.com/txn2/mcp-data-platform/bench/internal/pollutionplant"
)

// The raw-API driver has no client tool surface to close, so a disallow list
// there would be a flag that silently does nothing. Refusing it keeps a
// metered arm from being launched under a tool-surface assumption the run
// cannot hold.
func TestBuildRunnerRejectsDisallowToolsOnTheRawAPIDriver(t *testing.T) {
	_, _, _, err := buildRunner(context.Background(), "anthropic", "claude-sonnet-5", "ToolSearch")
	if err == nil {
		t.Fatal("-disallow-tools with -llm anthropic was accepted")
	}
	if !strings.Contains(err.Error(), "claude-cli") {
		t.Errorf("error does not name the driver the flag belongs to: %v", err)
	}
}

func TestBuildRunnerRejectsABadDisallowList(t *testing.T) {
	if _, _, _, err := buildRunner(context.Background(), "claude-cli", "sonnet", "ToolSearch ReadMcpResourceTool"); err == nil {
		t.Error("a space-separated disallow list was accepted")
	}
}

func TestBuildRunnerRejectsAnUnknownDriver(t *testing.T) {
	if _, _, _, err := buildRunner(context.Background(), "nope", "sonnet", ""); err == nil {
		t.Error("an unknown -llm value was accepted")
	}
}

func TestRunRequiresAnOutputDirectory(t *testing.T) {
	if err := run(runConfig{}); err == nil {
		t.Error("a run without -out was accepted")
	}
}

func TestSelectCellsAndScaffoldRejectUnknownNames(t *testing.T) {
	if _, _, err := selectCells("not-a-cell-set"); err == nil {
		t.Error("an unknown -cells value was accepted")
	}
	if _, err := selectScaffold("not-a-scaffold"); err == nil {
		t.Error("an unknown -scaffold value was accepted")
	}
}

// The knowledge-pollution cross-fixture unit must derive as answerable: its
// convention arrives through the shared store rather than a per-episode seed,
// and a cell that called it unanswerable would grade every episode against a
// refusal the fixture does not warrant.
func TestPollutionCoverageCellIsAnswerable(t *testing.T) {
	cells, exploratory, err := selectCells("pollution-coverage")
	if err != nil {
		t.Fatalf("selectCells: %v", err)
	}
	if len(cells) != 1 {
		t.Fatalf("got %d cells, want the one cross-fixture unit", len(cells))
	}
	c := cells[0]
	if !c.Answerable || c.Behavior != pkcell.BehaviorAnswer {
		t.Errorf("cell derives answerable=%v behavior=%s; want an answerable cell", c.Answerable, c.Behavior)
	}
	if c.Seed != nil {
		t.Error("the cell must plant no per-episode belief; the claim comes from the shared store")
	}
	if c.Question.ID != pollutionplant.QuestionCoverageDays {
		t.Errorf("cell asks %q, want the study's cross-fixture question", c.Question.ID)
	}
	if exploratory {
		t.Error("the cross-fixture arm is confirmatory for the pollution study")
	}
}
