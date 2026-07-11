package memorylayer

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// middlewareBridge adapts memory.Store to middleware.MemoryProvider, converting
// between memory.Snippet and middleware.MemorySnippet. It is the only
// memory↔enrichment bridge; Platform injects it into the semantic-enrichment
// middleware through Handle.MemoryProvider().
type middlewareBridge struct {
	store memory.Store
}

// RecallForEntities converts memory snippets to middleware format.
func (b *middlewareBridge) RecallForEntities(ctx context.Context, urns []string, personaName string, limit int) ([]middleware.MemorySnippet, error) {
	adapter := memory.NewMiddlewareAdapter(b.store)
	memSnippets, err := adapter.RecallForEntities(ctx, urns, personaName, limit)
	if err != nil {
		return nil, fmt.Errorf("recalling memories for entities: %w", err)
	}

	snippets := make([]middleware.MemorySnippet, len(memSnippets))
	for i, ms := range memSnippets {
		snippets[i] = middleware.MemorySnippet{
			ID: ms.ID,
			// Canonical fetch handle (mcp:memory:<id>) so a summary-first
			// rendering can point the agent at the full record (#761).
			Reference:  knowledgepage.MemoryRef(ms.ID),
			Content:    ms.Content,
			Dimension:  ms.Dimension,
			Category:   ms.Category,
			Confidence: ms.Confidence,
			CreatedAt:  ms.CreatedAt,
		}
	}
	return snippets, nil
}
