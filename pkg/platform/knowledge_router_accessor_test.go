package platform

import (
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

func TestKnowledgeRouter_Accessor(t *testing.T) {
	p := &Platform{}
	if p.KnowledgeRouter() != nil {
		t.Fatal("KnowledgeRouter should be nil before initSearch wires it")
	}
	router := knowledge.NewRouter(nil, nil)
	p.knowledgeRouter = router
	if p.KnowledgeRouter() != router {
		t.Fatal("KnowledgeRouter should return the wired router")
	}
}
