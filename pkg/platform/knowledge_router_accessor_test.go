package platform

import (
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/searchfed"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

func TestKnowledgeRouter_Accessor(t *testing.T) {
	p := &Platform{}
	if p.KnowledgeRouter() != nil {
		t.Fatal("KnowledgeRouter should be nil before initSearch wires the handle")
	}

	// A non-nil memory store yields a searchable source, so New builds a handle
	// whose Router() the accessor must return unchanged.
	h := searchfed.New(searchfed.Config{
		ToolkitName: "default",
		MemoryStore: memory.NewNoopStore(),
		Registry:    registry.NewRegistry(),
	})
	if h == nil || h.Router() == nil {
		t.Fatal("searchfed.New with a memory store should build a router")
	}
	p.searchFed = h
	if p.KnowledgeRouter() != h.Router() {
		t.Fatal("KnowledgeRouter should return the router the handle owns")
	}
}
