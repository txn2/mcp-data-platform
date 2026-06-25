package middleware

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultKnowledgePageEnrichmentLimit bounds how many referencing pages are appended
// per tool result, so the enrichment stays small even for a widely documented entity.
const defaultKnowledgePageEnrichmentLimit = 5

// maxKnowledgeEnrichmentURNs bounds how many distinct entity URNs are looked up per
// tool result. Each URN is a reverse-lookup query, so this caps the fan-out for a
// result that names many entities (e.g. a large search response).
const maxKnowledgeEnrichmentURNs = 10

// KnowledgePageProvider returns the canonical knowledge pages that reference a set of
// entity URNs, for cross-enrichment into entity tool responses (#634).
type KnowledgePageProvider interface {
	PagesForEntities(ctx context.Context, urns []string, limit int) ([]KnowledgePageSnippet, error)
}

// KnowledgePageSnippet is a lightweight knowledge-page reference for enrichment.
type KnowledgePageSnippet struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// enrichWithKnowledgePages appends a knowledge_pages block listing the canonical
// pages that document the entities named in the result (#634). It is given the
// entity URNs the tool itself returned (extracted before other enrichment appends
// blocks), and both the looked-up URN count and the returned page count are bounded,
// so the appended block and the reverse-lookup fan-out both stay small.
func enrichWithKnowledgePages(ctx context.Context, kp KnowledgePageProvider, result *mcp.CallToolResult, urns []string) *mcp.CallToolResult {
	if kp == nil || result == nil || len(urns) == 0 {
		return result
	}

	if len(urns) > maxKnowledgeEnrichmentURNs {
		slog.Debug("knowledge-page enrichment urn set truncated", "have", len(urns), "cap", maxKnowledgeEnrichmentURNs)
		urns = urns[:maxKnowledgeEnrichmentURNs]
	}

	pages, err := kp.PagesForEntities(ctx, urns, defaultKnowledgePageEnrichmentLimit)
	if err != nil {
		slog.Debug("knowledge-page enrichment failed", "error", err)
		return result
	}

	if len(pages) == 0 {
		return result
	}

	data, err := json.Marshal(map[string]any{"knowledge_pages": pages})
	if err != nil {
		slog.Debug("failed to marshal knowledge pages", "error", err)
		return result
	}

	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(data),
	})

	return result
}
