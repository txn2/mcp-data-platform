package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMemoryProvider implements MemoryProvider for testing.
type mockMemoryProvider struct {
	recallURNs    []string
	recallPersona string
	recallLimit   int
	recallResult  []MemorySnippet
	recallErr     error
}

func (m *mockMemoryProvider) RecallForEntities(_ context.Context, urns []string, persona string, limit int) ([]MemorySnippet, error) {
	m.recallURNs = urns
	m.recallPersona = persona
	m.recallLimit = limit
	return m.recallResult, m.recallErr
}

func TestEnrichWithMemories_NilProvider(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "hello"}},
	}
	pc := &PlatformContext{PersonaName: "analyst"}

	got := enrichWithMemories(context.Background(), nil, result, pc, EnrichmentConfig{})
	assert.Equal(t, result, got)
	assert.Len(t, got.Content, 1) // unchanged
}

func TestEnrichWithMemories_NilResult(t *testing.T) {
	mp := &mockMemoryProvider{}
	pc := &PlatformContext{PersonaName: "analyst"}

	got := enrichWithMemories(context.Background(), mp, nil, pc, EnrichmentConfig{})
	assert.Nil(t, got)
}

func TestEnrichWithMemories_NilContext(t *testing.T) {
	mp := &mockMemoryProvider{}
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "hello"}},
	}

	got := enrichWithMemories(context.Background(), mp, result, nil, EnrichmentConfig{})
	assert.Equal(t, result, got)
	assert.Len(t, got.Content, 1)
}

func TestEnrichWithMemories_NoURNsInResult(t *testing.T) {
	mp := &mockMemoryProvider{}
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "plain text without any URNs"}},
	}
	pc := &PlatformContext{PersonaName: "analyst"}

	got := enrichWithMemories(context.Background(), mp, result, pc, EnrichmentConfig{})
	assert.Len(t, got.Content, 1) // no enrichment
	assert.Nil(t, mp.recallURNs)  // RecallForEntities was not called
}

func TestEnrichWithMemories_URNsFound_MemoriesAttached(t *testing.T) {
	mp := &mockMemoryProvider{
		recallResult: []MemorySnippet{
			{
				ID:         "mem-001",
				Content:    "Revenue includes deferred amounts",
				Dimension:  "knowledge",
				Category:   "business_context",
				Confidence: "high",
				CreatedAt:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	data := map[string]any{
		"table": "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.revenue,PROD)",
	}
	jsonBytes, _ := json.Marshal(data)
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
	}
	pc := &PlatformContext{PersonaName: "analyst"}

	got := enrichWithMemories(context.Background(), mp, result, pc, EnrichmentConfig{})
	require.Len(t, got.Content, 2) // original + memory context

	// Verify the recall was called with correct args
	assert.Equal(t, "analyst", mp.recallPersona)
	assert.Equal(t, defaultMemoryEnrichmentLimit, mp.recallLimit)
	require.Len(t, mp.recallURNs, 1)
	assert.Equal(t, "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.revenue,PROD)", mp.recallURNs[0])

	// Verify the appended content is valid JSON with memory_context
	tc, ok := got.Content[1].(*mcp.TextContent)
	require.True(t, ok)
	var memCtx map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &memCtx))
	assert.Contains(t, memCtx, "memory_context")
}

func TestEnrichWithMemories_RecallError(t *testing.T) {
	mp := &mockMemoryProvider{
		recallErr: fmt.Errorf("connection refused"),
	}

	data := map[string]any{
		"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t1,PROD)",
	}
	jsonBytes, _ := json.Marshal(data)
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
	}
	pc := &PlatformContext{PersonaName: "analyst"}

	got := enrichWithMemories(context.Background(), mp, result, pc, EnrichmentConfig{})
	assert.Len(t, got.Content, 1) // no enrichment appended on error
}

func TestEnrichWithMemories_EmptyMemories(t *testing.T) {
	mp := &mockMemoryProvider{
		recallResult: []MemorySnippet{}, // empty
	}

	data := map[string]any{
		"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t1,PROD)",
	}
	jsonBytes, _ := json.Marshal(data)
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
	}
	pc := &PlatformContext{PersonaName: "analyst"}

	got := enrichWithMemories(context.Background(), mp, result, pc, EnrichmentConfig{})
	assert.Len(t, got.Content, 1) // no enrichment for empty memories
}

func TestExtractEntityURNsFromResult_JSONWithURNs(t *testing.T) {
	data := map[string]any{
		"dataset":   "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.table1,PROD)",
		"lineage":   "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.table2,PROD)",
		"unrelated": "not a urn",
	}
	jsonBytes, _ := json.Marshal(data)
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
	}

	urns := extractEntityURNsFromResult(result)
	assert.Len(t, urns, 2)
	assert.Contains(t, urns, "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.table1,PROD)")
	assert.Contains(t, urns, "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.table2,PROD)")
}

func TestExtractEntityURNsFromResult_NonJSONContent(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "this is plain text, not JSON"},
		},
	}

	urns := extractEntityURNsFromResult(result)
	assert.Empty(t, urns)
}

func TestExtractEntityURNsFromResult_NestedURNs(t *testing.T) {
	data := map[string]any{
		"result": map[string]any{
			"metadata": map[string]any{
				"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.nested,PROD)",
			},
		},
	}
	jsonBytes, _ := json.Marshal(data)
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
	}

	urns := extractEntityURNsFromResult(result)
	assert.Len(t, urns, 1)
	assert.Equal(t, "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.nested,PROD)", urns[0])
}

func TestExtractEntityURNsFromResult_ArrayURNs(t *testing.T) {
	data := map[string]any{
		"datasets": []any{
			"urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t1,PROD)",
			"urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t2,PROD)",
			"not-a-urn",
		},
	}
	jsonBytes, _ := json.Marshal(data)
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
	}

	urns := extractEntityURNsFromResult(result)
	assert.Len(t, urns, 2)
}

func TestExtractEntityURNsFromResult_Deduplication(t *testing.T) {
	data := map[string]any{
		"a": "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t1,PROD)",
		"b": "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t1,PROD)",
	}
	jsonBytes, _ := json.Marshal(data)
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
	}

	urns := extractEntityURNsFromResult(result)
	assert.Len(t, urns, 1)
}

func TestExtractEntityURNsFromResult_NonTextContent(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.ImageContent{Data: []byte("base64data"), MIMEType: "image/png"},
		},
	}

	urns := extractEntityURNsFromResult(result)
	assert.Empty(t, urns)
}

func TestExtractMemoryURNsFromMap(t *testing.T) {
	data := map[string]any{
		"top_level": "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t1,PROD)",
		"nested": map[string]any{
			"deep": "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t2,PROD)",
		},
		"list": []any{
			"urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t3,PROD)",
		},
		"plain": "just a string",
	}

	seen := make(map[string]bool)
	var urns []string
	extractMemoryURNsFromMap(data, seen, &urns)

	assert.Len(t, urns, 3)
}

func TestCollectURNsFromValue(t *testing.T) {
	t.Run("string URN", func(t *testing.T) {
		seen := make(map[string]bool)
		var urns []string
		collectURNsFromValue("urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t1,PROD)", seen, &urns)
		assert.Len(t, urns, 1)
	})

	t.Run("string not URN", func(t *testing.T) {
		seen := make(map[string]bool)
		var urns []string
		collectURNsFromValue("not a URN", seen, &urns)
		assert.Empty(t, urns)
	})

	t.Run("map value", func(t *testing.T) {
		seen := make(map[string]bool)
		var urns []string
		collectURNsFromValue(map[string]any{
			"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t1,PROD)",
		}, seen, &urns)
		assert.Len(t, urns, 1)
	})

	t.Run("slice value", func(t *testing.T) {
		seen := make(map[string]bool)
		var urns []string
		collectURNsFromValue([]any{
			"urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t1,PROD)",
			"urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t2,PROD)",
		}, seen, &urns)
		assert.Len(t, urns, 2)
	})

	t.Run("other type ignored", func(t *testing.T) {
		seen := make(map[string]bool)
		var urns []string
		collectURNsFromValue(42, seen, &urns)
		assert.Empty(t, urns)
	})

	t.Run("duplicate skipped", func(t *testing.T) {
		seen := make(map[string]bool)
		var urns []string
		urn := "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.t1,PROD)"
		collectURNsFromValue(urn, seen, &urns)
		collectURNsFromValue(urn, seen, &urns)
		assert.Len(t, urns, 1)
	})
}

func TestIsDataHubURN(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid dataset URN",
			input:    "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.table,PROD)",
			expected: true,
		},
		{
			name:     "valid corpuser URN",
			input:    "urn:li:corpuser:user@example.com",
			expected: true,
		},
		{
			name:     "too short",
			input:    "urn:li:abc",
			expected: false,
		},
		{
			name:     "exactly minURNLength",
			input:    "urn:li:abcd",
			expected: true,
		},
		{
			name:     "wrong prefix",
			input:    "urn:xx:dataset:something-longer-than-ten",
			expected: false,
		},
		{
			name:     "not a URN at all",
			input:    "just some string that is long enough",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "short string",
			input:    "abc",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isDataHubURN(tt.input))
		})
	}
}

func TestIsDataHubURN_TooLong(t *testing.T) {
	// Build a string that starts with "urn:li:" but is >= maxURNLength
	long := "urn:li:dataset:"
	for len(long) < maxURNLength {
		long += "x"
	}
	assert.False(t, isDataHubURN(long))
}

// --- Issue #761: summary-first rendering, budget, dedup ---

func TestSummarizeMemory(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		summaryBytes int
		wantSummary  string
		wantTrunc    bool
	}{
		{
			name:         "no truncation when disabled",
			content:      "the full content stays intact",
			summaryBytes: 0,
			wantSummary:  "the full content stays intact",
			wantTrunc:    false,
		},
		{
			name:         "short content under cap is untouched",
			content:      "brief note",
			summaryBytes: 100,
			wantSummary:  "brief note",
			wantTrunc:    false,
		},
		{
			name:         "first paragraph preferred as summary boundary",
			content:      "Revenue is recognized on delivery.\n\nSee the policy doc for edge cases and deferrals.",
			summaryBytes: 100,
			wantSummary:  "Revenue is recognized on delivery.",
			wantTrunc:    true,
		},
		{
			name:         "byte truncation when over cap and no paragraph break",
			content:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			summaryBytes: 10,
			wantSummary:  "aaaaaaaaaa",
			wantTrunc:    true,
		},
		{
			name:         "leading and trailing whitespace trimmed",
			content:      "   padded content   ",
			summaryBytes: 0,
			wantSummary:  "padded content",
			wantTrunc:    false,
		},
		{
			name:         "first paragraph too long falls through to byte truncation",
			content:      "this first paragraph is quite long and exceeds the cap\n\nsecond",
			summaryBytes: 20,
			wantSummary:  "this first paragraph",
			wantTrunc:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, trunc := summarizeMemory(tt.content, tt.summaryBytes)
			assert.Equal(t, tt.wantSummary, got)
			assert.Equal(t, tt.wantTrunc, trunc)
		})
	}
}

func TestSummarizeMemory_RuneBoundary(t *testing.T) {
	// "héllo" — é is 2 bytes (0xc3 0xa9); a cap of 2 would split it, so
	// summarizeMemory must back off to the rune start and stay valid UTF-8.
	got, trunc := summarizeMemory("héllo", 2)
	assert.Equal(t, "h", got)
	assert.True(t, trunc)
	assert.True(t, utf8.ValidString(got))
}

func TestDedupKey(t *testing.T) {
	// Case and whitespace differences normalize to the same key.
	assert.Equal(t, dedupKey("Revenue  Includes\nDeferred"), dedupKey("revenue includes deferred"))
	assert.Equal(t, "", dedupKey("   "))
	assert.NotEqual(t, dedupKey("alpha"), dedupKey("beta"))
}

func TestRenderMemoryContext_Dedup(t *testing.T) {
	memories := []MemorySnippet{
		{ID: "a", Content: "Revenue includes deferred amounts"},
		{ID: "b", Content: "revenue  includes   deferred amounts"}, // near-identical
		{ID: "c", Content: "Distinct fact about churn"},
	}
	rendered, omitted := renderMemoryContext(memories, 0, 0)
	require.Len(t, rendered, 2)
	assert.Empty(t, omitted)
	assert.Equal(t, "a", rendered[0].ID)
	assert.Equal(t, "c", rendered[1].ID)
}

// TestRenderMemoryContext_DedupKeysOnFullContent guards the finding-#1 fix:
// two DISTINCT records that share an identical prefix longer than the summary
// cap must NOT collapse — dedup keys on full content, not the truncated summary.
func TestRenderMemoryContext_DedupKeysOnFullContent(t *testing.T) {
	prefix := strings.Repeat("shared boilerplate heading ", 5) // > 50 bytes
	memories := []MemorySnippet{
		{ID: "a", Content: prefix + "FIRST record distinct tail"},
		{ID: "b", Content: prefix + "SECOND record distinct tail"},
	}
	// Summary cap shorter than the shared prefix: both summaries are identical.
	rendered, omitted := renderMemoryContext(memories, 30, 0)
	require.Len(t, rendered, 2, "records sharing only a truncated-summary prefix must not collapse")
	assert.Empty(t, omitted)
	assert.Equal(t, "a", rendered[0].ID)
	assert.Equal(t, "b", rendered[1].ID)
}

func TestRenderMemoryContext_BudgetOmitsAsStubs(t *testing.T) {
	memories := []MemorySnippet{
		{ID: "a", Reference: "mcp:memory:a", Content: strings.Repeat("x", 200)},
		{ID: "b", Reference: "mcp:memory:b", Content: strings.Repeat("y", 200)},
		{ID: "c", Reference: "mcp:memory:c", Content: strings.Repeat("z", 200)},
	}
	// Budget large enough for one full summary only.
	rendered, omitted := renderMemoryContext(memories, 0, 120)
	require.Len(t, rendered, 1)
	assert.Equal(t, "a", rendered[0].ID)

	// The over-budget records survive as fetchable stubs carrying references.
	require.Len(t, omitted, 2)
	assert.Equal(t, "b", omitted[0].ID)
	assert.Equal(t, "mcp:memory:b", omitted[0].Reference)
	assert.Equal(t, "c", omitted[1].ID)
	assert.Equal(t, "mcp:memory:c", omitted[1].Reference)
}

func TestRenderMemoryContext_AlwaysRendersAtLeastOne(t *testing.T) {
	memories := []MemorySnippet{
		{ID: "a", Content: strings.Repeat("x", 5000)},
	}
	// Budget smaller than the single record; it must still be rendered.
	rendered, omitted := renderMemoryContext(memories, 0, 10)
	require.Len(t, rendered, 1)
	assert.Empty(t, omitted)
}

func TestRenderMemoryContext_SummaryAndTruncationFlag(t *testing.T) {
	memories := []MemorySnippet{
		{ID: "a", Reference: "mcp:memory:a", Content: strings.Repeat("w", 500)},
	}
	rendered, _ := renderMemoryContext(memories, 100, 0)
	require.Len(t, rendered, 1)
	assert.True(t, rendered[0].Truncated)
	assert.Equal(t, "mcp:memory:a", rendered[0].Reference)
	assert.LessOrEqual(t, len(rendered[0].Summary), 100)
}

func TestEnrichWithMemories_SummaryFirstWithReferenceAndBudget(t *testing.T) {
	mp := &mockMemoryProvider{
		recallResult: []MemorySnippet{
			{ID: "m1", Reference: "mcp:memory:m1", Content: strings.Repeat("a", 400), Dimension: "knowledge"},
			{ID: "m2", Reference: "mcp:memory:m2", Content: strings.Repeat("b", 400), Dimension: "knowledge"},
			{ID: "m3", Reference: "mcp:memory:m3", Content: strings.Repeat("c", 400), Dimension: "knowledge"},
		},
	}
	data, _ := json.Marshal(map[string]any{
		"table": "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.revenue,PROD)",
	})
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
	pc := &PlatformContext{PersonaName: "analyst"}

	cfg := EnrichmentConfig{MemoryLimit: 10, MemorySummaryBytes: 80, MemoryContextBudgetBytes: 200}
	got := enrichWithMemories(context.Background(), mp, result, pc, cfg)
	require.Len(t, got.Content, 2)

	// The configured limit is passed through to recall.
	assert.Equal(t, 10, mp.recallLimit)

	tc, ok := got.Content[1].(*mcp.TextContent)
	require.True(t, ok)
	var block map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &block))

	records, ok := block["memory_context"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, records)

	first, ok := records[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "mcp:memory:m1", first["reference"])
	assert.Equal(t, true, first["truncated"])
	summary, ok := first["summary"].(string)
	require.True(t, ok)
	assert.LessOrEqual(t, len(summary), 80)
	// summary-first: the full 400-char content is NOT present.
	assert.NotContains(t, first, "content")

	// Budget forced omissions, so the note AND the fetchable stub list are present.
	assert.Contains(t, block, "memory_context_note")
	omitted, ok := block["memory_context_omitted"].([]any)
	require.True(t, ok, "omitted records must be listed as fetchable stubs")
	require.NotEmpty(t, omitted)
	stub, ok := omitted[0].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, stub["id"])
	assert.NotEmpty(t, stub["reference"])
}

func TestEnrichmentContentBytes(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "original"},
			&mcp.TextContent{Text: "12345"},
			&mcp.ImageContent{Data: []byte("ignored"), MIMEType: "image/png"},
			&mcp.TextContent{Text: "678"},
		},
	}
	// From index 1: "12345" (5) + image (0) + "678" (3) = 8.
	assert.Equal(t, 8, enrichmentContentBytes(result, 1))
	// Out-of-range indices are safe.
	assert.Equal(t, 0, enrichmentContentBytes(result, 99))
	assert.Equal(t, 0, enrichmentContentBytes(result, -1))
	assert.Equal(t, 0, enrichmentContentBytes(nil, 0))
}
