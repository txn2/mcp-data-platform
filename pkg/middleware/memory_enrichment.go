package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultMemoryEnrichmentLimit is the number of memories to inject per tool call.
const defaultMemoryEnrichmentLimit = 5

// minURNLength is the minimum string length to qualify as a DataHub URN candidate.
const minURNLength = 10

// urnPrefixLen is the length of the "urn:li:" prefix used for URN detection.
const urnPrefixLen = 7

// maxURNLength is the maximum reasonable length for a DataHub URN string.
const maxURNLength = 500

// MemoryProvider retrieves relevant memories for cross-injection into toolkit responses.
type MemoryProvider interface {
	RecallForEntities(ctx context.Context, urns []string, persona string, limit int) ([]MemorySnippet, error)
}

// MemorySnippet is a lightweight memory representation for cross-injection.
type MemorySnippet struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Dimension  string    `json:"dimension"`
	Category   string    `json:"category"`
	Confidence string    `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}

// enrichWithMemories appends memory context to a tool call result.
// It extracts entity URNs from the result content and recalls related memories.
func enrichWithMemories(ctx context.Context, mp MemoryProvider, result *mcp.CallToolResult, pc *PlatformContext) *mcp.CallToolResult {
	if mp == nil || result == nil || pc == nil {
		return result
	}

	urns := extractEntityURNsFromResult(result)
	if len(urns) == 0 {
		return result
	}

	memories, err := mp.RecallForEntities(ctx, urns, pc.PersonaName, defaultMemoryEnrichmentLimit)
	if err != nil {
		slog.Debug("memory enrichment failed", "error", err)
		return result
	}

	if len(memories) == 0 {
		return result
	}

	memoryContext := map[string]any{
		"memory_context": memories,
	}

	data, err := json.Marshal(memoryContext)
	if err != nil {
		slog.Debug("failed to marshal memory context", "error", err)
		return result
	}

	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(data),
	})

	return result
}

// extractEntityURNsFromResult scans result content for DataHub URNs.
func extractEntityURNsFromResult(result *mcp.CallToolResult) []string {
	var urns []string
	seen := make(map[string]bool)

	for _, content := range result.Content {
		tc, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}

		// Try to parse as JSON and extract URNs.
		var data map[string]any
		if err := json.Unmarshal([]byte(tc.Text), &data); err != nil {
			continue
		}

		extractMemoryURNsFromMap(data, seen, &urns)
	}

	return urns
}

// extractMemoryURNsFromMap recursively extracts URN strings from a JSON structure.
func extractMemoryURNsFromMap(data map[string]any, seen map[string]bool, urns *[]string) {
	for _, val := range data {
		collectURNsFromValue(val, seen, urns)
	}
}

// collectURNsFromValue extracts URNs from a single JSON value (string, map, or slice).
func collectURNsFromValue(val any, seen map[string]bool, urns *[]string) {
	switch v := val.(type) {
	case string:
		if isDataHubURN(v) && !seen[v] {
			seen[v] = true
			*urns = append(*urns, v)
		}
	case map[string]any:
		extractMemoryURNsFromMap(v, seen, urns)
	case []any:
		for _, item := range v {
			collectURNsFromValue(item, seen, urns)
		}
	}
}

// isDataHubURN checks if a string looks like a DataHub URN.
func isDataHubURN(s string) bool {
	return len(s) > minURNLength && s[:urnPrefixLen] == "urn:li:" && len(s) < maxURNLength
}
