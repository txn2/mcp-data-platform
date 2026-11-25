package knowledge

import (
	"context"
	"encoding/json"
	"strings"
)

// SourceTools is the provenance label for tool hits (#2809).
const SourceTools = "tools"

// ToolMatch is one of the platform's tools ranked against an intent.
type ToolMatch struct {
	Name        string
	Description string
	Score       float64
	// Example is one call of the tool that fits the intent.
	Example map[string]any
}

// ToolRanker ranks the platform's tools a caller with roles may call against
// what it says it wants to do. The platform implements it over the tools index
// (internal/platform/toolsearch); the knowledge package defines it so the
// federation engine does not import the platform.
type ToolRanker interface {
	RankTools(ctx context.Context, intent string, roles []string, limit int) []ToolMatch
}

// defaultToolHits is how many tools a search returns when it names no limit.
const defaultToolHits = 5

// toolHitSentenceLimit bounds the description a tool hit carries: enough to
// choose the tool, with the tool's own description one tools/list away.
const toolHitSentenceLimit = 240

// ToolsProvider answers "which tool does this" from search (#2809). Agents call
// search first and stop there, so a separate tool for capability questions was
// rarely reached; with tools as a source, "publish a static site" finds
// manage_site where the agent already asks.
//
// It is shared: tools are deployment-level. It is not unfiltered: the ranker
// returns only tools the caller's persona may call, by the predicate tools/list
// applies.
type ToolsProvider struct {
	ranker ToolRanker
}

// NewToolsProvider builds the tools provider over a ranker.
func NewToolsProvider(r ToolRanker) *ToolsProvider { return &ToolsProvider{ranker: r} }

// Name is SourceTools.
func (*ToolsProvider) Name() string { return SourceTools }

// Scope is shared.
func (*ToolsProvider) Scope() Scope { return ScopeShared }

// Search ranks the tools against the intent. A hit's text names the tool, says
// what it does in a sentence, and gives one call of it that fits the intent.
func (p *ToolsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if p == nil || p.ranker == nil || strings.TrimSpace(q.Intent) == "" {
		return nil, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultToolHits
	}
	matches := p.ranker.RankTools(ctx, q.Intent, q.Caller.Roles, limit)
	hits := make([]Hit, 0, len(matches))
	for _, m := range matches {
		hits = append(hits, Hit{
			Text:   toolHitText(m),
			Source: SourceTools,
			Ref:    m.Name,
			Score:  m.Score,
		})
	}
	return hits, nil
}

// toolHitText is "Tool <name>: <first sentence>. Example call: <json>".
func toolHitText(m ToolMatch) string {
	desc := m.Description
	if i := strings.Index(desc, ". "); i >= 0 {
		desc = desc[:i+1]
	}
	if r := []rune(desc); len(r) > toolHitSentenceLimit {
		desc = string(r[:toolHitSentenceLimit]) + "..."
	}
	text := "Tool " + m.Name + ": " + desc
	if len(m.Example) > 0 {
		if b, err := json.Marshal(m.Example); err == nil {
			text += " Example call: " + string(b)
		}
	}
	return text
}
