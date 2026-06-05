package knowledge

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// recallToolName is the MCP tool name for relevance recall over insights.
const recallToolName = "recall_insight"

// Ranking modes reported on the recall_insight response so the caller knows
// whether results were ranked semantically or by keyword only.
const (
	rankingHybrid  = "hybrid"
	rankingLexical = "lexical"
)

// recallInsightInput is the deserialized input for the recall_insight tool.
type recallInsightInput struct {
	Query  string `json:"query"`
	Status string `json:"status,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// scoredInsightOutput pairs an insight with its relevance score in the
// recall_insight response.
type scoredInsightOutput struct {
	Insight Insight `json:"insight"`
	Score   float64 `json:"score"`
}

// recallInsightOutput is the recall_insight tool response: the ranked
// insights, their count, and the ranking mode used.
type recallInsightOutput struct {
	Insights []scoredInsightOutput `json:"insights"`
	Count    int                   `json:"count"`
	Ranking  string                `json:"ranking"`
}

// handleRecallInsight searches the caller's captured insights by relevance
// to the query. It owner-scopes on the caller's email, the canonical
// identity for captured insights, and fails closed when that identity is
// absent so a recall never runs unscoped across other users' knowledge.
func (t *Toolkit) handleRecallInsight(ctx context.Context, _ *mcp.CallToolRequest, input recallInsightInput) (*mcp.CallToolResult, any, error) {
	searcher, ok := t.store.(InsightSearcher)
	if !ok {
		return errorResult("insight recall is unavailable: the memory layer is not enabled"), nil, nil
	}

	query := strings.TrimSpace(input.Query)
	if query == "" {
		return errorResult("query is required for recall_insight"), nil, nil
	}

	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.UserEmail == "" {
		return errorResult("a user identity (email) is required to scope insight recall"), nil, nil
	}

	// One shared hybrid-vs-lexical decision across every search surface: a
	// non-nil embedding selects hybrid ranking, nil selects lexical-only.
	emb := embedding.EmbedForSearch(ctx, t.embedder, query)
	ranking := rankingLexical
	if len(emb) > 0 {
		ranking = rankingHybrid
	}

	scored, err := searcher.Search(ctx, InsightSearchQuery{
		QueryText:  query,
		Embedding:  emb,
		CapturedBy: pc.UserEmail,
		Status:     strings.TrimSpace(input.Status),
		Limit:      (&InsightFilter{Limit: input.Limit}).EffectiveLimit(),
	})
	if err != nil {
		return errorResult("failed to search insights: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	out := recallInsightOutput{
		Insights: make([]scoredInsightOutput, 0, len(scored)),
		Count:    len(scored),
		Ranking:  ranking,
	}
	for i := range scored {
		out.Insights = append(out.Insights, scoredInsightOutput{
			Insight: scored[i].Insight,
			Score:   scored[i].Score,
		})
	}
	return jsonResult(out)
}
