package memorylayer

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
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
		insight := ms.Dimension == memory.DimensionKnowledge
		snippets[i] = middleware.MemorySnippet{
			ID: ms.ID,
			// Canonical fetch handle so a summary-first rendering can point the
			// agent at the full record (#761), in the namespace that actually
			// dereferences for the reader.
			Reference:  recordRef(ms.ID, insight, ms.InsightStatus),
			Content:    ms.Content,
			Dimension:  ms.Dimension,
			Category:   ms.Category,
			Confidence: ms.Confidence,
			CreatedAt:  ms.CreatedAt,
			EntityURNs: ms.EntityURNs,
			Insight:    insight,
		}
	}
	return snippets, nil
}

// recordRef is the canonical fetch handle for a recalled record, or empty when
// no handle would resolve for the reader.
//
// A knowledge-dimension record is an insight, and the two namespaces are read
// from different stores: fetch declines a knowledge-dimension record offered as
// mcp:memory:, so that form dereferences to nothing for anyone. mcp:insight: is
// the right form, but fetch serves an insight only to its capturer or, once
// applied, to everyone — and this push path is persona-scoped rather than
// caller-scoped, so it delivers other people's records and cannot know which
// case it is in. Applied is therefore the only status whose handle is certain to
// resolve for whoever receives it; anything else is published with no handle
// rather than with one that answers not-found.
func recordRef(id string, insight bool, insightStatus string) string {
	if !insight {
		return knowledgepage.MemoryRef(id)
	}
	if insightStatus == knowledgekit.StatusApplied {
		return knowledgepage.InsightRef(id)
	}
	return ""
}
