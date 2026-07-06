package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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

// MemoryProvider retrieves relevant memories for cross-enrichment into toolkit responses.
type MemoryProvider interface {
	RecallForEntities(ctx context.Context, urns []string, persona string, limit int) ([]MemorySnippet, error)
}

// MemorySnippet is a lightweight memory representation for cross-enrichment.
type MemorySnippet struct {
	ID string `json:"id"`
	// Reference is the canonical fetch handle for the full record
	// (mcp:memory:<id>), surfaced so the agent can retrieve the untruncated
	// content when a summary is not enough. Empty when the provider does not
	// supply one. Issue #761.
	Reference  string    `json:"reference,omitempty"`
	Content    string    `json:"content"`
	Dimension  string    `json:"dimension"`
	Category   string    `json:"category"`
	Confidence string    `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}

// memoryStub is the reference-only form of a budget-omitted record: enough for
// the agent to fetch the full record without spending the summary's bytes. It
// keeps every recalled record retrievable even when the byte budget trims the
// rendered summaries. Issue #761.
type memoryStub struct {
	ID        string `json:"id"`
	Reference string `json:"reference,omitempty"`
}

// renderedMemory is the summary-first shape of a memory record as it appears in
// the memory_context enrichment block. It carries a bounded Summary plus the
// Reference the agent uses to fetch the full record, rather than the record's
// full content. Issue #761.
type renderedMemory struct {
	ID         string    `json:"id"`
	Reference  string    `json:"reference,omitempty"`
	Summary    string    `json:"summary"`
	Truncated  bool      `json:"truncated,omitempty"`
	Dimension  string    `json:"dimension,omitempty"`
	Category   string    `json:"category,omitempty"`
	Confidence string    `json:"confidence,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// enrichWithMemories appends memory context to a tool call result.
// It extracts entity URNs from the result content and recalls related memories,
// then renders them summary-first under a configurable record limit and total
// byte budget so the enrichment does not crowd out the data the agent is
// analyzing (issue #761).
func enrichWithMemories(ctx context.Context, mp MemoryProvider, result *mcp.CallToolResult, pc *PlatformContext, cfg EnrichmentConfig) *mcp.CallToolResult {
	if mp == nil || result == nil || pc == nil {
		return result
	}

	urns := extractEntityURNsFromResult(result)
	if len(urns) == 0 {
		return result
	}

	limit := cfg.MemoryLimit
	if limit <= 0 {
		limit = defaultMemoryEnrichmentLimit
	}

	memories, err := mp.RecallForEntities(ctx, urns, pc.PersonaName, limit)
	if err != nil {
		slog.Debug("memory enrichment failed", "error", err)
		return result
	}
	if len(memories) == 0 {
		return result
	}

	return appendMemoryContextBlock(result, memories, cfg)
}

// appendMemoryContextBlock renders recalled memories summary-first and appends
// the memory_context block (with an omitted-stub list when the budget trims
// records) to result. Returns result unchanged if nothing is rendered or the
// block fails to marshal. Split out of enrichWithMemories to keep each function
// under the cyclomatic-complexity ceiling.
func appendMemoryContextBlock(result *mcp.CallToolResult, memories []MemorySnippet, cfg EnrichmentConfig) *mcp.CallToolResult {
	rendered, omitted := renderMemoryContext(memories, cfg.MemorySummaryBytes, cfg.MemoryContextBudgetBytes)
	if len(rendered) == 0 {
		return result
	}

	block := map[string]any{"memory_context": rendered}
	if len(omitted) > 0 {
		// Omitted records are still listed as compact id+reference stubs so the
		// agent can fetch any of them — the note promises exactly that (#761).
		block["memory_context_omitted"] = omitted
		block["memory_context_note"] = fmt.Sprintf(
			"%d additional related memory record(s) omitted to stay within the enrichment budget; "+
				"fetch any by its reference in memory_context_omitted for full detail.", len(omitted))
	}

	data, err := json.Marshal(block)
	if err != nil {
		slog.Debug("failed to marshal memory context", "error", err)
		return result
	}

	result.Content = append(result.Content, &mcp.TextContent{
		Text: string(data),
	})

	return result
}

// renderMemoryContext turns recalled snippets into the summary-first
// memory_context payload, applying (in order) near-duplicate collapsing, summary
// truncation, and a byte budget over the rendered summaries. Records beyond the
// budget are returned as compact id+reference stubs (not dropped) so they stay
// fetchable. At least one record is always rendered so a small budget never
// suppresses memory enrichment entirely.
//
// The budget bounds the rendered summaries only; the small, memory_limit-bounded
// stub list and the note deliberately sit outside it so the budget can be set
// low without making any recalled record unretrievable.
func renderMemoryContext(memories []MemorySnippet, summaryBytes, budgetBytes int) (rendered []renderedMemory, omitted []memoryStub) {
	seen := make(map[string]bool, len(memories))
	total := 0

	for _, m := range memories {
		// Dedup on the FULL content, not the truncated summary: two distinct
		// records that merely share a common prefix must not collapse to one
		// (that would silently drop the second). Only genuine near-duplicates
		// (same text, cosmetic differences) share a full-content key.
		key := dedupKey(m.Content)
		if key != "" && seen[key] {
			continue
		}
		seen[key] = true

		summary, truncated := summarizeMemory(m.Content, summaryBytes)
		rec := renderedMemory{
			ID:         m.ID,
			Reference:  m.Reference,
			Summary:    summary,
			Truncated:  truncated,
			Dimension:  m.Dimension,
			Category:   m.Category,
			Confidence: m.Confidence,
			CreatedAt:  m.CreatedAt,
		}

		size := recordSizeEstimate(rec)
		if budgetBytes > 0 && len(rendered) > 0 && total+size > budgetBytes {
			omitted = append(omitted, memoryStub{ID: m.ID, Reference: m.Reference})
			continue
		}

		total += size
		rendered = append(rendered, rec)
	}

	return rendered, omitted
}

// summarizeMemory reduces a memory record's content to a summary-first excerpt:
// the first paragraph when it fits, otherwise the first summaryBytes bytes on a
// rune boundary. The bool reports whether any content was dropped, signaling
// the agent to fetch the full record when the excerpt is not enough. A
// non-positive summaryBytes disables truncation.
func summarizeMemory(content string, summaryBytes int) (summary string, truncated bool) {
	content = strings.TrimSpace(content)

	// Prefer the first paragraph as a natural summary boundary.
	if idx := strings.Index(content, "\n\n"); idx > 0 {
		firstPara := strings.TrimSpace(content[:idx])
		if firstPara != "" && (summaryBytes <= 0 || len(firstPara) <= summaryBytes) {
			return firstPara, len(firstPara) < len(content)
		}
	}

	if summaryBytes <= 0 || len(content) <= summaryBytes {
		return content, false
	}
	// clampUTF8 (mcp_reflexive_capture.go) does the rune-boundary back-off; trim
	// any whitespace the cut exposed.
	return strings.TrimSpace(clampUTF8(content, summaryBytes)), true
}

// dedupKey normalizes text to a whitespace- and case-insensitive form so
// near-identical records (the same text captured twice, or the same fact with
// cosmetic differences) collapse to one at render time.
func dedupKey(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}

// recordSizeEstimate approximates a rendered record's serialized JSON size for
// budget accounting, without a throwaway marshal on the enrichment hot path: the
// variable field lengths plus a fixed allowance for the JSON keys, quoting, and
// the RFC-3339 created_at value.
func recordSizeEstimate(rec renderedMemory) int {
	const fixedKeyOverhead = 120
	return len(rec.ID) + len(rec.Reference) + len(rec.Summary) +
		len(rec.Dimension) + len(rec.Category) + len(rec.Confidence) + fixedKeyOverhead
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
