package knowledge

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
)

// recordApplyOutcome counts one apply in knowledge_changes_total under its
// sink (#1898): applied, or failed when the apply answered an error. An apply
// that stopped to ask for confirmation changed nothing and is not counted.
func recordApplyOutcome(ctx context.Context, sink string, res *mcp.CallToolResult, err error) {
	if sink == "" {
		sink = sinkDataHub
	}
	switch sink {
	case sinkDataHub, sinkKnowledgePage, sinkAgentInstructions:
	default:
		// An unknown sink is refused before anything is written; it is not a
		// sink, and the caller's word is not a label value.
		return
	}
	result := opsobs.KnowledgeApplied
	switch {
	case err != nil || res == nil || res.IsError:
		result = opsobs.KnowledgeFailed
	case awaitsConfirmation(res):
		return
	}
	opsobs.Metrics().RecordKnowledgeChange(ctx, sink, result)
}

// recordReviews counts the insights one approve or reject moved.
func recordReviews(ctx context.Context, targetStatus string, n int) {
	result := opsobs.KnowledgeApproved
	if targetStatus == StatusRejected {
		result = opsobs.KnowledgeRejected
	}
	for range n {
		opsobs.Metrics().RecordKnowledgeChange(ctx, opsobs.SinkInsight, result)
	}
}

// awaitsConfirmation reports whether res is the confirmation_required answer
// an apply gives before writing.
func awaitsConfirmation(res *mcp.CallToolResult) bool {
	for _, c := range res.Content {
		text, ok := c.(*mcp.TextContent)
		if !ok {
			continue
		}
		var body struct {
			ConfirmationRequired bool `json:"confirmation_required"`
		}
		if json.Unmarshal([]byte(text.Text), &body) == nil && body.ConfirmationRequired {
			return true
		}
	}
	return false
}
