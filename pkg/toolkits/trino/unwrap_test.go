package trino

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

func TestUnwrapJSONMiddleware_Before(t *testing.T) {
	mw := &UnwrapJSONMiddleware{}
	ctx := context.Background()

	t.Run("sets UnwrapJSON on QueryInput", func(t *testing.T) {
		input := &trinotools.QueryInput{SQL: "SELECT 1"}
		tc := trinotools.NewToolContext(trinotools.ToolQuery, input)
		tc.Input = input

		got, err := mw.Before(ctx, tc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("returned context must not be nil")
		}
		if !input.UnwrapJSON {
			t.Error("expected UnwrapJSON to be true on QueryInput")
		}
	})

	t.Run("sets UnwrapJSON on ExecuteInput", func(t *testing.T) {
		input := &trinotools.ExecuteInput{SQL: "SELECT 1"}
		tc := trinotools.NewToolContext(trinotools.ToolExecute, input)
		tc.Input = input

		_, err := mw.Before(ctx, tc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !input.UnwrapJSON {
			t.Error("expected UnwrapJSON to be true on ExecuteInput")
		}
	})

	t.Run("ignores other tool inputs", func(t *testing.T) {
		input := &trinotools.ExplainInput{SQL: "SELECT 1"}
		tc := trinotools.NewToolContext(trinotools.ToolExplain, input)
		tc.Input = input

		_, err := mw.Before(ctx, tc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// No panic, no error — just a no-op.
	})
}

func TestUnwrapJSONMiddleware_After(t *testing.T) {
	mw := &UnwrapJSONMiddleware{}
	result := &mcp.CallToolResult{}

	got, err := mw.After(context.Background(), nil, result, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != result {
		t.Error("After should pass result through unchanged")
	}
}

func TestBuildToolkitOptions_UnwrapJSON(t *testing.T) {
	t.Run("includes middleware when enabled", func(t *testing.T) {
		opts := buildToolkitOptions(Config{UnwrapJSONDefault: true}, nil, nil)
		if len(opts) == 0 {
			t.Error("expected at least one option for UnwrapJSONDefault")
		}
	})

	t.Run("excludes middleware when disabled", func(t *testing.T) {
		opts := buildToolkitOptions(Config{UnwrapJSONDefault: false}, nil, nil)
		if len(opts) != 0 {
			t.Errorf("expected no options, got %d", len(opts))
		}
	})
}
