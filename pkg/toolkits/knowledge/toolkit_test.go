package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Test constants to avoid repeated string literals.
const (
	testName         = "test"
	testVersion      = "1.0"
	testCategory     = "correction"
	testConfidence   = "medium"
	testStatusVal    = "pending"
	testInsightText  = "Valid text here for testing"
	testActionAddTag = "add_tag"
	testPersona      = "analyst"
	hexIDLen         = 32
)

// --- AC-1: Tool registration ---

func TestToolkit_Tools(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)

	tools := tk.Tools()
	assert.Contains(t, tools, "capture_insight")
}

func TestToolkit_RegisterTools(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)

	s := mcp.NewServer(&mcp.Implementation{Name: testName, Version: testVersion}, nil)
	tk.RegisterTools(s)
	// If RegisterTools panics, this test fails.
}

// --- AC-15: Toolkit interface compliance ---

func TestToolkit_Kind(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	assert.Equal(t, "knowledge", tk.Kind())
}

func TestToolkit_Name(t *testing.T) {
	tk, err := New("myinstance", nil)
	require.NoError(t, err)
	assert.Equal(t, "myinstance", tk.Name())
}

func TestToolkit_Connection(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	assert.Equal(t, "", tk.Connection())
}

func TestToolkit_Close(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	assert.NoError(t, tk.Close())
}

func TestToolkit_SetProviders(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	// No-ops, should not panic.
	tk.SetSemanticProvider(nil)
	tk.SetQueryProvider(nil)
}

func TestToolkit_NilStoreDefaultsToNoop(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	assert.NotNil(t, tk.store)
}

// --- AC-7: Context injection ---

func TestHandleCaptureInsight_ContextInjection(t *testing.T) {
	spy := &spyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	pc := &middleware.PlatformContext{
		SessionID:   "sess-123",
		UserID:      "user-456",
		PersonaName: testPersona,
	}
	ctx := middleware.WithPlatformContext(context.Background(), pc)

	input := captureInsightInput{
		Category:    testCategory,
		InsightText: "The column name is misleading",
	}

	result, _, callErr := tk.handleCaptureInsight(ctx, nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	require.Len(t, spy.Insights, 1)

	insight := spy.Insights[0]
	assert.Equal(t, "sess-123", insight.SessionID)
	assert.Equal(t, "user-456", insight.CapturedBy)
	assert.Equal(t, testPersona, insight.Persona)
}

func TestHandleCaptureInsight_NoPlatformContext(t *testing.T) {
	spy := &spyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	input := captureInsightInput{
		Category:    testCategory,
		InsightText: "The column name is misleading",
	}

	result, _, callErr := tk.handleCaptureInsight(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	require.Len(t, spy.Insights, 1)

	insight := spy.Insights[0]
	assert.Equal(t, "", insight.SessionID)
	assert.Equal(t, "", insight.CapturedBy)
	assert.Equal(t, "", insight.Persona)
}

// --- AC-8: Database write ---

func TestHandleCaptureInsight_AllFieldsPopulated(t *testing.T) {
	spy := &spyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	pc := &middleware.PlatformContext{
		SessionID:   "sess-1",
		UserID:      "user-1",
		PersonaName: "admin",
	}
	ctx := middleware.WithPlatformContext(context.Background(), pc)

	input := captureInsightInput{
		Category:    "business_context",
		InsightText: "MRR excludes trial accounts",
		Confidence:  "high",
		EntityURNs:  []string{"urn:li:dataset:foo"},
		RelatedColumns: []RelatedColumn{
			{URN: "urn:li:dataset:foo", Column: "mrr", Relevance: "primary"},
		},
		SuggestedActions: []SuggestedAction{
			{ActionType: "update_description", Target: "urn:li:dataset:foo", Detail: "Add MRR exclusion note"},
		},
	}

	result, _, callErr := tk.handleCaptureInsight(ctx, nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	require.Len(t, spy.Insights, 1)

	insight := spy.Insights[0]
	assert.NotEmpty(t, insight.ID)
	assert.Equal(t, "sess-1", insight.SessionID)
	assert.Equal(t, "user-1", insight.CapturedBy)
	assert.Equal(t, "admin", insight.Persona)
	assert.Equal(t, "business_context", insight.Category)
	assert.Equal(t, "MRR excludes trial accounts", insight.InsightText)
	assert.Equal(t, "high", insight.Confidence)
	assert.Equal(t, []string{"urn:li:dataset:foo"}, insight.EntityURNs)
	assert.Len(t, insight.RelatedColumns, 1)
	assert.Len(t, insight.SuggestedActions, 1)
	assert.Equal(t, testStatusVal, insight.Status)
}

// --- AC-9: Generated ID ---

func TestHandleCaptureInsight_UniqueIDs(t *testing.T) {
	spy := &spyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	input := captureInsightInput{
		Category:    testCategory,
		InsightText: "First insight text here",
	}

	result1, _, err1 := tk.handleCaptureInsight(context.Background(), nil, input)
	require.Nil(t, err1)
	require.False(t, result1.IsError)

	input.InsightText = "Second insight text here"
	result2, _, err2 := tk.handleCaptureInsight(context.Background(), nil, input)
	require.Nil(t, err2)
	require.False(t, result2.IsError)

	require.Len(t, spy.Insights, 2)
	assert.NotEmpty(t, spy.Insights[0].ID)
	assert.NotEmpty(t, spy.Insights[1].ID)
	assert.NotEqual(t, spy.Insights[0].ID, spy.Insights[1].ID)
}

// --- AC-10: Return value ---

func TestHandleCaptureInsight_SuccessResponse(t *testing.T) {
	spy := &spyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	input := captureInsightInput{
		Category:    "data_quality",
		InsightText: "Timestamps before March 2024 are in UTC",
	}

	result, _, callErr := tk.handleCaptureInsight(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)

	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected *mcp.TextContent")

	var output captureInsightOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &output))

	assert.NotEmpty(t, output.InsightID)
	assert.Equal(t, testStatusVal, output.Status)
	assert.NotEmpty(t, output.Message)
	assert.Contains(t, output.Message, "reviewed")
}

// --- AC-11: Error return ---

func TestHandleCaptureInsight_ValidationError(t *testing.T) {
	spy := &spyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	tests := []struct {
		name  string
		input captureInsightInput
	}{
		{
			name:  "missing category",
			input: captureInsightInput{InsightText: testInsightText},
		},
		{
			name:  "invalid category",
			input: captureInsightInput{Category: "invalid", InsightText: testInsightText},
		},
		{
			name:  "missing insight_text",
			input: captureInsightInput{Category: testCategory},
		},
		{
			name:  "short insight_text",
			input: captureInsightInput{Category: testCategory, InsightText: "short"},
		},
		{
			name:  "invalid confidence",
			input: captureInsightInput{Category: testCategory, InsightText: testInsightText, Confidence: "ultra"},
		},
		{
			name:  "too many entity_urns",
			input: captureInsightInput{Category: testCategory, InsightText: testInsightText, EntityURNs: make([]string, 11)},
		},
		{
			name:  "too many related_columns",
			input: captureInsightInput{Category: testCategory, InsightText: testInsightText, RelatedColumns: make([]RelatedColumn, 21)},
		},
		{
			name: "too many suggested_actions",
			input: captureInsightInput{
				Category:    testCategory,
				InsightText: testInsightText,
				SuggestedActions: []SuggestedAction{
					{ActionType: testActionAddTag},
					{ActionType: testActionAddTag},
					{ActionType: testActionAddTag},
					{ActionType: testActionAddTag},
					{ActionType: testActionAddTag},
					{ActionType: testActionAddTag},
				},
			},
		},
		{
			name: "invalid action_type",
			input: captureInsightInput{
				Category:    testCategory,
				InsightText: testInsightText,
				SuggestedActions: []SuggestedAction{
					{ActionType: "delete_tag"},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spy.Insights = nil // Reset

			result, _, callErr := tk.handleCaptureInsight(context.Background(), nil, tc.input)
			require.Nil(t, callErr) // Go error is nil; MCP error is in result
			assert.True(t, result.IsError)

			// Store must NOT be called on validation error
			assert.Empty(t, spy.Insights, "store.Insert should not be called on validation error")
		})
	}
}

func TestHandleCaptureInsight_StoreError(t *testing.T) {
	spy := &spyStore{Err: errors.New("db connection lost")}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	input := captureInsightInput{
		Category:    testCategory,
		InsightText: "This is a valid insight text",
	}

	result, _, callErr := tk.handleCaptureInsight(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)
}

// --- AC-4: Confidence defaults ---

func TestHandleCaptureInsight_ConfidenceDefaults(t *testing.T) {
	spy := &spyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	input := captureInsightInput{
		Category:    testCategory,
		InsightText: "Insight without confidence specified",
	}

	result, _, callErr := tk.handleCaptureInsight(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	require.Len(t, spy.Insights, 1)
	assert.Equal(t, testConfidence, spy.Insights[0].Confidence)
}

// --- Nil slice normalization ---

func TestHandleCaptureInsight_NilSlicesNormalized(t *testing.T) {
	spy := &spyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	input := captureInsightInput{
		Category:    testCategory,
		InsightText: "Insight with no optional arrays",
	}

	result, _, callErr := tk.handleCaptureInsight(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	require.Len(t, spy.Insights, 1)

	insight := spy.Insights[0]
	assert.NotNil(t, insight.EntityURNs)
	assert.NotNil(t, insight.RelatedColumns)
	assert.NotNil(t, insight.SuggestedActions)
}

// --- AC-2: Category validation (table-driven) ---

func TestHandleCaptureInsight_CategoryValidation(t *testing.T) {
	validCats := []string{
		"correction", "business_context", "data_quality",
		"usage_guidance", "relationship", "enhancement",
	}
	invalidCats := []string{
		"", "invalid", "CORRECTION", "business-context",
	}

	spy := &spyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	for _, cat := range validCats {
		t.Run("valid_"+cat, func(t *testing.T) {
			spy.Insights = nil
			input := captureInsightInput{
				Category:    cat,
				InsightText: "A valid insight for testing",
			}
			result, _, callErr := tk.handleCaptureInsight(context.Background(), nil, input)
			require.Nil(t, callErr)
			assert.False(t, result.IsError, "category %q should be accepted", cat)
		})
	}

	for _, cat := range invalidCats {
		t.Run("invalid_"+cat, func(t *testing.T) {
			spy.Insights = nil
			input := captureInsightInput{
				Category:    cat,
				InsightText: "A valid insight for testing",
			}
			result, _, callErr := tk.handleCaptureInsight(context.Background(), nil, input)
			require.Nil(t, callErr)
			assert.True(t, result.IsError, "category %q should be rejected", cat)
			assert.Empty(t, spy.Insights)
		})
	}
}

// --- AC-3: InsightText validation ---

func TestHandleCaptureInsight_InsightTextValidation(t *testing.T) {
	spy := &spyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	tests := []struct {
		name    string
		text    string
		wantErr bool
	}{
		{name: "empty text", text: "", wantErr: true},
		{name: "too short", text: "short", wantErr: true},
		{name: "minimum", text: "1234567890", wantErr: false},
		{name: "normal", text: "This is a reasonably long insight text", wantErr: false},
		{name: "max length", text: strings.Repeat("a", MaxInsightTextLen), wantErr: false},
		{name: "over max", text: strings.Repeat("a", MaxInsightTextLen+1), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spy.Insights = nil
			input := captureInsightInput{
				Category:    testCategory,
				InsightText: tc.text,
			}
			result, _, callErr := tk.handleCaptureInsight(context.Background(), nil, input)
			require.Nil(t, callErr)
			if tc.wantErr {
				assert.True(t, result.IsError)
				assert.Empty(t, spy.Insights)
			} else {
				assert.False(t, result.IsError)
			}
		})
	}
}

// --- ID generation ---

func TestGenerateID(t *testing.T) {
	id, err := generateID()
	require.NoError(t, err)
	assert.Len(t, id, hexIDLen) // 16 bytes = 32 hex chars
	assert.NotEmpty(t, id)
}

// --- AC-20: Tuning prompt ---

func TestToolkit_RegistersPrompt(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)

	s := mcp.NewServer(&mcp.Implementation{Name: testName, Version: testVersion}, nil)
	tk.RegisterTools(s)

	// Verify the prompt content is defined and non-empty
	assert.NotEmpty(t, knowledgeCapturePrompt)
	assert.Contains(t, knowledgeCapturePrompt, "When to Capture")
	assert.Contains(t, knowledgeCapturePrompt, "When NOT to Capture")
	assert.Contains(t, knowledgeCapturePrompt, "capture_insight")
}

// --- Validate input function ---

func TestValidateInput(t *testing.T) {
	t.Run("valid minimal input", func(t *testing.T) {
		input := captureInsightInput{
			Category:    testCategory,
			InsightText: "A valid insight text",
		}
		assert.NoError(t, validateInput(input))
	})

	t.Run("valid full input", func(t *testing.T) {
		input := captureInsightInput{
			Category:    "business_context",
			InsightText: "A valid insight text",
			Confidence:  "high",
			EntityURNs:  []string{"urn:li:dataset:foo"},
			RelatedColumns: []RelatedColumn{
				{URN: "urn:li:dataset:foo", Column: "col1", Relevance: "primary"},
			},
			SuggestedActions: []SuggestedAction{
				{ActionType: testActionAddTag, Target: "tgt", Detail: "d"},
			},
		}
		assert.NoError(t, validateInput(input))
	})
}

// --- Build insight ---

func TestBuildInsight(t *testing.T) {
	pc := &middleware.PlatformContext{
		SessionID:   "s1",
		UserID:      "u1",
		PersonaName: testPersona,
	}

	input := captureInsightInput{
		Category:    testCategory,
		InsightText: "A valid insight text",
		Confidence:  "",
	}

	insight := buildInsight("id-1", pc, input)
	assert.Equal(t, "id-1", insight.ID)
	assert.Equal(t, "s1", insight.SessionID)
	assert.Equal(t, "u1", insight.CapturedBy)
	assert.Equal(t, testPersona, insight.Persona)
	assert.Equal(t, testConfidence, insight.Confidence) // Default
	assert.Equal(t, testStatusVal, insight.Status)
	assert.NotNil(t, insight.EntityURNs)
	assert.NotNil(t, insight.RelatedColumns)
	assert.NotNil(t, insight.SuggestedActions)
}

func TestBuildInsight_NilContext(t *testing.T) {
	input := captureInsightInput{
		Category:    testCategory,
		InsightText: "A valid insight text",
	}

	insight := buildInsight("id-2", nil, input)
	assert.Equal(t, "", insight.SessionID)
	assert.Equal(t, "", insight.CapturedBy)
	assert.Equal(t, "", insight.Persona)
}

// --- Error/success result helpers ---

func TestErrorResult(t *testing.T) {
	result := errorResult("something went wrong")
	assert.True(t, result.IsError)
	assert.NotEmpty(t, result.Content)

	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected *mcp.TextContent")
	assert.Contains(t, tc.Text, "something went wrong")
}

func TestSuccessResult(t *testing.T) {
	result, _, err := successResult("abc123")
	require.Nil(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)

	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected *mcp.TextContent")

	var output captureInsightOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &output))
	assert.Equal(t, "abc123", output.InsightID)
	assert.Equal(t, testStatusVal, output.Status)
}
