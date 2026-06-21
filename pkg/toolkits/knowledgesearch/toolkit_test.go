package knowledgesearch

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

func TestToolkit_TrivialAccessors(t *testing.T) {
	tk := New("inst", knowledge.NewRouter(nil))
	if tk.Name() != "inst" {
		t.Errorf("Name = %q", tk.Name())
	}
	if tk.Connection() != "" {
		t.Errorf("Connection = %q, want empty", tk.Connection())
	}
	// No-op setters must not panic.
	tk.SetSemanticProvider(nil)
	tk.SetQueryProvider(nil)
	if err := tk.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

// erroringProvider is a shared provider that always fails, so the router's
// all-providers-failed path returns an error to handleSearch.
type erroringProvider struct{}

func (erroringProvider) Name() string           { return "boom" }
func (erroringProvider) Scope() knowledge.Scope { return knowledge.ScopeShared }
func (erroringProvider) Search(context.Context, knowledge.Query) ([]knowledge.Hit, error) {
	return nil, errors.New("store down")
}

func TestHandleSearch_RouterErrorBecomesToolError(t *testing.T) {
	tk := New("inst", knowledge.NewRouter(nil, erroringProvider{}))
	res, _, err := tk.handleSearch(context.Background(), &mcp.CallToolRequest{}, searchInput{Intent: "q"})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a tool error result when the router fails")
	}
}
