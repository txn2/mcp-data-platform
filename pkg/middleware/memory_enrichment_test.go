package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

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

	got := enrichWithMemories(context.Background(), nil, result, pc)
	assert.Equal(t, result, got)
	assert.Len(t, got.Content, 1) // unchanged
}

func TestEnrichWithMemories_NilResult(t *testing.T) {
	mp := &mockMemoryProvider{}
	pc := &PlatformContext{PersonaName: "analyst"}

	got := enrichWithMemories(context.Background(), mp, nil, pc)
	assert.Nil(t, got)
}

func TestEnrichWithMemories_NilContext(t *testing.T) {
	mp := &mockMemoryProvider{}
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "hello"}},
	}

	got := enrichWithMemories(context.Background(), mp, result, nil)
	assert.Equal(t, result, got)
	assert.Len(t, got.Content, 1)
}

func TestEnrichWithMemories_NoURNsInResult(t *testing.T) {
	mp := &mockMemoryProvider{}
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "plain text without any URNs"}},
	}
	pc := &PlatformContext{PersonaName: "analyst"}

	got := enrichWithMemories(context.Background(), mp, result, pc)
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

	got := enrichWithMemories(context.Background(), mp, result, pc)
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

	got := enrichWithMemories(context.Background(), mp, result, pc)
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

	got := enrichWithMemories(context.Background(), mp, result, pc)
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
