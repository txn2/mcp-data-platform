package search

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

// TestSearchSchema_SourcesDescriptionListsAllKnownSources is the drift guard for the
// `sources` description's "Known sources" list: it must name every Source* provenance
// constant, so adding or renaming a source without updating the prose (leaving agents
// a stale list) fails here. It inspects the sources property's description
// specifically, not the whole schema blob, so a name surviving only in the unrelated
// `intent` prose does not mask its removal here.
func TestSearchSchema_SourcesDescriptionListsAllKnownSources(t *testing.T) {
	var schema struct {
		Properties struct {
			Sources struct {
				Description string `json:"description"`
			} `json:"sources"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(searchSchema, &schema); err != nil {
		t.Fatalf("search schema is not valid JSON: %v", err)
	}
	desc := schema.Properties.Sources.Description
	if desc == "" {
		t.Fatal("sources property has no description")
	}
	// Derive the expected set from the single authority (no hand-maintained list), so
	// a newly added source the prose forgets is caught here.
	for _, s := range knowledge.KnownSources() {
		if !strings.Contains(desc, s) {
			t.Errorf("search schema 'sources' description omits known source %q", s)
		}
	}
}

func TestToolkit_TrivialAccessors(t *testing.T) {
	tk := New("inst", knowledge.NewRouter(nil, nil))
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
	tk := New("inst", knowledge.NewRouter(nil, nil, erroringProvider{}))
	res, _, err := tk.handleSearch(context.Background(), &mcp.CallToolRequest{}, searchInput{Intent: "q"})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a tool error result when the router fails")
	}
}
