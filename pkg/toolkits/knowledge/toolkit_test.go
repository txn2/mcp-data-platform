package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

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
	testEntityURN    = "urn:li:dataset:(urn:li:dataPlatform:trino,test.table,PROD)"
)

// ---------------------------------------------------------------------------
// Spy types
// ---------------------------------------------------------------------------

// fullSpyStore implements InsightStore for toolkit handler tests.
// It stores insights in-memory and supports configurable errors.
type fullSpyStore struct {
	Insights       []Insight
	InsertErr      error
	GetErr         error
	ListErr        error
	StatsErr       error
	UpdateErr      error
	StatusErr      error
	MarkAppliedErr error
	SupersedeErr   error

	// Track calls
	StatusCalls      []statusCall
	MarkAppliedCalls []markAppliedCall
	SupersedeCalls   []supersedeCall

	// Configurable returns for Stats
	StatsResult *InsightStats
}

type statusCall struct {
	ID          string
	Status      string
	ReviewedBy  string
	ReviewNotes string
}

type markAppliedCall struct {
	ID           string
	AppliedBy    string
	ChangesetRef string
}

type supersedeCall struct {
	EntityURN string
	ExcludeID string
}

func (s *fullSpyStore) Insert(_ context.Context, insight Insight) error {
	if s.InsertErr != nil {
		return s.InsertErr
	}
	s.Insights = append(s.Insights, insight)
	return nil
}

func (s *fullSpyStore) Get(_ context.Context, id string) (*Insight, error) {
	if s.GetErr != nil {
		return nil, s.GetErr
	}
	for i := range s.Insights {
		if s.Insights[i].ID != id {
			continue
		}
		return &s.Insights[i], nil
	}
	return nil, fmt.Errorf("insight not found: %s", id)
}

func (s *fullSpyStore) List(_ context.Context, filter InsightFilter) ([]Insight, int, error) {
	if s.ListErr != nil {
		return nil, 0, s.ListErr
	}
	var result []Insight
	for _, ins := range s.Insights {
		if filter.Status != "" && ins.Status != filter.Status {
			continue
		}
		if filter.EntityURN != "" && !slices.Contains(ins.EntityURNs, filter.EntityURN) {
			continue
		}
		result = append(result, ins)
	}
	return result, len(result), nil
}

func (s *fullSpyStore) UpdateStatus(_ context.Context, id, status, reviewedBy, reviewNotes string) error {
	if s.StatusErr != nil {
		return s.StatusErr
	}
	s.StatusCalls = append(s.StatusCalls, statusCall{
		ID: id, Status: status, ReviewedBy: reviewedBy, ReviewNotes: reviewNotes,
	})
	for i := range s.Insights {
		if s.Insights[i].ID != id {
			continue
		}
		s.Insights[i].Status = status
		s.Insights[i].ReviewedBy = reviewedBy
		s.Insights[i].ReviewNotes = reviewNotes
		now := time.Now()
		s.Insights[i].ReviewedAt = &now
		return nil
	}
	return fmt.Errorf("insight not found: %s", id)
}

func (s *fullSpyStore) Update(_ context.Context, _ string, _ InsightUpdate) error {
	return s.UpdateErr
}

func (s *fullSpyStore) Stats(_ context.Context, _ InsightFilter) (*InsightStats, error) {
	if s.StatsErr != nil {
		return nil, s.StatsErr
	}
	if s.StatsResult != nil {
		return s.StatsResult, nil
	}
	return &InsightStats{
		TotalPending: len(s.Insights),
		ByCategory:   map[string]int{},
		ByConfidence: map[string]int{},
		ByStatus:     map[string]int{},
	}, nil
}

func (s *fullSpyStore) MarkApplied(_ context.Context, id, appliedBy, changesetRef string) error {
	if s.MarkAppliedErr != nil {
		return s.MarkAppliedErr
	}
	s.MarkAppliedCalls = append(s.MarkAppliedCalls, markAppliedCall{
		ID: id, AppliedBy: appliedBy, ChangesetRef: changesetRef,
	})
	for i := range s.Insights {
		if s.Insights[i].ID == id {
			s.Insights[i].Status = StatusApplied
			s.Insights[i].AppliedBy = appliedBy
			s.Insights[i].ChangesetRef = changesetRef
			return nil
		}
	}
	return nil
}

func (s *fullSpyStore) Supersede(_ context.Context, entityURN, excludeID string) (int, error) {
	if s.SupersedeErr != nil {
		return 0, s.SupersedeErr
	}
	s.SupersedeCalls = append(s.SupersedeCalls, supersedeCall{
		EntityURN: entityURN, ExcludeID: excludeID,
	})
	return 0, nil
}

// Verify interface compliance at compile time.
var _ InsightStore = (*fullSpyStore)(nil)

// spyChangesetStore implements ChangesetStore for tests.
type spyChangesetStore struct {
	Changesets  []Changeset
	InsertErr   error
	GetErr      error
	ListErr     error
	RollbackErr error
}

func (s *spyChangesetStore) InsertChangeset(_ context.Context, cs Changeset) error {
	if s.InsertErr != nil {
		return s.InsertErr
	}
	s.Changesets = append(s.Changesets, cs)
	return nil
}

func (s *spyChangesetStore) GetChangeset(_ context.Context, id string) (*Changeset, error) {
	if s.GetErr != nil {
		return nil, s.GetErr
	}
	for i := range s.Changesets {
		if s.Changesets[i].ID == id {
			return &s.Changesets[i], nil
		}
	}
	return nil, fmt.Errorf("changeset not found: %s", id)
}

func (s *spyChangesetStore) ListChangesets(_ context.Context, _ ChangesetFilter) ([]Changeset, int, error) {
	if s.ListErr != nil {
		return nil, 0, s.ListErr
	}
	return s.Changesets, len(s.Changesets), nil
}

func (s *spyChangesetStore) RollbackChangeset(_ context.Context, id, _ string) error {
	if s.RollbackErr != nil {
		return s.RollbackErr
	}
	for i := range s.Changesets {
		if s.Changesets[i].ID == id {
			s.Changesets[i].RolledBack = true
			return nil
		}
	}
	return fmt.Errorf("changeset not found: %s", id)
}

var _ ChangesetStore = (*spyChangesetStore)(nil)

// spyWriter implements DataHubWriter for tests.
type spyWriter struct {
	Metadata    *EntityMetadata
	MetaErr     error
	WriteCalls  []writerCall
	FailAtCall  int // If > 0, fail on the Nth write call
	currentCall int
}

type writerCall struct {
	Method string
	URN    string
	Arg1   string
	Arg2   string
}

func (w *spyWriter) GetCurrentMetadata(_ context.Context, _ string) (*EntityMetadata, error) {
	if w.MetaErr != nil {
		return nil, w.MetaErr
	}
	if w.Metadata != nil {
		return w.Metadata, nil
	}
	return &EntityMetadata{
		Tags:          []string{},
		GlossaryTerms: []string{},
		Owners:        []string{},
	}, nil
}

func (w *spyWriter) recordAndCheck(method, urn, arg1, arg2 string) error {
	w.currentCall++
	w.WriteCalls = append(w.WriteCalls, writerCall{
		Method: method, URN: urn, Arg1: arg1, Arg2: arg2,
	})
	if w.FailAtCall > 0 && w.currentCall >= w.FailAtCall {
		return fmt.Errorf("simulated DataHub write failure at call %d", w.currentCall)
	}
	return nil
}

func (w *spyWriter) UpdateDescription(_ context.Context, urn, desc string) error {
	return w.recordAndCheck("UpdateDescription", urn, desc, "")
}

func (w *spyWriter) AddTag(_ context.Context, urn, tag string) error {
	return w.recordAndCheck("AddTag", urn, tag, "")
}

func (w *spyWriter) RemoveTag(_ context.Context, urn, tag string) error {
	return w.recordAndCheck("RemoveTag", urn, tag, "")
}

func (w *spyWriter) AddGlossaryTerm(_ context.Context, urn, termURN string) error {
	return w.recordAndCheck("AddGlossaryTerm", urn, termURN, "")
}

func (w *spyWriter) AddDocumentationLink(_ context.Context, urn, url, desc string) error {
	return w.recordAndCheck("AddDocumentationLink", urn, url, desc)
}

var _ DataHubWriter = (*spyWriter)(nil)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newApplyToolkit creates a Toolkit with apply enabled and the given dependencies.
func newApplyToolkit(t *testing.T, store InsightStore, csStore ChangesetStore, writer DataHubWriter) *Toolkit {
	t.Helper()
	tk, err := New(testName, store)
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true}, csStore, writer)
	return tk
}

// parseJSONResult extracts the JSON body from a CallToolResult.
func parseJSONResult(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.NotEmpty(t, result.Content)
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected *mcp.TextContent")
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

// ctxWithUser creates a context with a PlatformContext carrying the given user.
func ctxWithUser(userID, sessionID, persona string) context.Context {
	pc := &middleware.PlatformContext{
		SessionID:   sessionID,
		UserID:      userID,
		PersonaName: persona,
	}
	return middleware.WithPlatformContext(context.Background(), pc)
}

// ---------------------------------------------------------------------------
// AC-1: Tool registration
// ---------------------------------------------------------------------------

func TestToolkit_Tools_WithoutApply(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)

	tools := tk.Tools()
	assert.Contains(t, tools, "capture_insight")
	assert.NotContains(t, tools, "apply_knowledge")
}

func TestToolkit_Tools_WithApply(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true}, nil, nil)

	tools := tk.Tools()
	assert.Contains(t, tools, "capture_insight")
	assert.Contains(t, tools, "apply_knowledge")
	assert.Len(t, tools, 2)
}

func TestToolkit_RegisterTools_WithoutApply(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)

	s := mcp.NewServer(&mcp.Implementation{Name: testName, Version: testVersion}, nil)
	tk.RegisterTools(s)
	// Should not panic; only capture_insight registered.
	assert.Len(t, tk.Tools(), 1)
}

func TestToolkit_RegisterTools_WithApply(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true}, nil, nil)

	s := mcp.NewServer(&mcp.Implementation{Name: testName, Version: testVersion}, nil)
	tk.RegisterTools(s)
	assert.Len(t, tk.Tools(), 2)
}

// ---------------------------------------------------------------------------
// Toolkit interface compliance
// ---------------------------------------------------------------------------

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

func TestSetApplyConfig_NilDeps(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true}, nil, nil)

	assert.True(t, tk.applyEnabled)
	assert.NotNil(t, tk.changesetStore, "nil changeset store should be replaced with noop")
	assert.NotNil(t, tk.datahubWriter, "nil writer should be replaced with noop")
}

func TestSetApplyConfig_WithDeps(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)

	cs := &spyChangesetStore{}
	w := &spyWriter{}
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, cs, w)

	assert.True(t, tk.applyEnabled)
	assert.True(t, tk.requireConfirmation)
	assert.Same(t, cs, tk.changesetStore)
	assert.Same(t, w, tk.datahubWriter)
}

// ---------------------------------------------------------------------------
// AC-14: Context injection from PlatformContext
// ---------------------------------------------------------------------------

func TestHandleCaptureInsight_ContextInjection(t *testing.T) {
	spy := &fullSpyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	ctx := ctxWithUser("user-456", "sess-123", testPersona)

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
	spy := &fullSpyStore{}
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

// ---------------------------------------------------------------------------
// capture_insight: all fields populated
// ---------------------------------------------------------------------------

func TestHandleCaptureInsight_AllFieldsPopulated(t *testing.T) {
	spy := &fullSpyStore{}
	tk, err := New(testName, spy)
	require.NoError(t, err)

	ctx := ctxWithUser("user-1", "sess-1", "admin")

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

// ---------------------------------------------------------------------------
// capture_insight: unique IDs
// ---------------------------------------------------------------------------

func TestHandleCaptureInsight_UniqueIDs(t *testing.T) {
	spy := &fullSpyStore{}
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

// ---------------------------------------------------------------------------
// capture_insight: success response format
// ---------------------------------------------------------------------------

func TestHandleCaptureInsight_SuccessResponse(t *testing.T) {
	spy := &fullSpyStore{}
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

// ---------------------------------------------------------------------------
// capture_insight: validation errors
// ---------------------------------------------------------------------------

func TestHandleCaptureInsight_ValidationError(t *testing.T) {
	spy := &fullSpyStore{}
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
	spy := &fullSpyStore{InsertErr: errors.New("db connection lost")}
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

// ---------------------------------------------------------------------------
// capture_insight: confidence defaults
// ---------------------------------------------------------------------------

func TestHandleCaptureInsight_ConfidenceDefaults(t *testing.T) {
	spy := &fullSpyStore{}
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

// ---------------------------------------------------------------------------
// capture_insight: nil slice normalization
// ---------------------------------------------------------------------------

func TestHandleCaptureInsight_NilSlicesNormalized(t *testing.T) {
	spy := &fullSpyStore{}
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

// ---------------------------------------------------------------------------
// AC-2: Category validation (table-driven)
// ---------------------------------------------------------------------------

func TestHandleCaptureInsight_CategoryValidation(t *testing.T) {
	validCats := []string{
		"correction", "business_context", "data_quality",
		"usage_guidance", "relationship", "enhancement",
	}
	invalidCats := []string{
		"", "invalid", "CORRECTION", "business-context",
	}

	spy := &fullSpyStore{}
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

// ---------------------------------------------------------------------------
// InsightText validation
// ---------------------------------------------------------------------------

func TestHandleCaptureInsight_InsightTextValidation(t *testing.T) {
	spy := &fullSpyStore{}
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

// ---------------------------------------------------------------------------
// ID generation
// ---------------------------------------------------------------------------

func TestGenerateID(t *testing.T) {
	id, err := generateID()
	require.NoError(t, err)
	assert.Len(t, id, hexIDLen) // 16 bytes = 32 hex chars
	assert.NotEmpty(t, id)
}

// ---------------------------------------------------------------------------
// Prompt registration
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// validateInput
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// buildInsight
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// errorResult / successResult / jsonResult
// ---------------------------------------------------------------------------

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
	require.True(t, ok, "expected *mcp.TextContent") //nolint:revive // test value

	var output captureInsightOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &output))
	assert.Equal(t, "abc123", output.InsightID)
	assert.Equal(t, testStatusVal, output.Status)
}

func TestJsonResult(t *testing.T) {
	data := map[string]any{"key": "value", "count": float64(42)} //nolint:revive // test value
	result, _, err := jsonResult(data)
	require.Nil(t, err)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, "value", m["key"])
	assert.Equal(t, float64(42), m["count"]) //nolint:revive // test value
}

// ---------------------------------------------------------------------------
// AC-2: Action parameter validation
// ---------------------------------------------------------------------------

func TestHandleApplyKnowledge_ActionValidation(t *testing.T) {
	store := &fullSpyStore{}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	tests := []struct {
		name    string
		action  string
		wantErr bool
	}{
		{name: "empty action", action: "", wantErr: true},
		{name: "invalid action", action: "destroy", wantErr: true},
		{name: "valid bulk_review", action: "bulk_review", wantErr: false},
		{name: "valid review needs entity_urn", action: "review", wantErr: true}, // missing entity_urn
		{name: "valid approve needs insight_ids", action: "approve", wantErr: true},
		{name: "valid reject needs insight_ids", action: "reject", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := applyKnowledgeInput{Action: tc.action}
			result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
			require.Nil(t, callErr) // MCP protocol: errors in result
			if tc.wantErr {
				assert.True(t, result.IsError, "action %q should produce an error result", tc.action)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AC-3: bulk_review returns summary with mock store
// ---------------------------------------------------------------------------

func TestHandleBulkReview_ReturnsSummary(t *testing.T) {
	store := &fullSpyStore{
		StatsResult: &InsightStats{
			TotalPending: 3,                                                      //nolint:revive // test value
			ByCategory:   map[string]int{"correction": 2, "business_context": 1}, //nolint:revive // test values
			ByConfidence: map[string]int{"high": 1, "medium": 2},                 //nolint:revive // test values
			ByStatus:     map[string]int{"pending": 3},                           //nolint:revive // test values
		},
		Insights: []Insight{
			{ID: "i1", Status: StatusPending, EntityURNs: []string{testEntityURN}, Category: "correction", CreatedAt: time.Now()},                //nolint:revive // test values
			{ID: "i2", Status: StatusPending, EntityURNs: []string{testEntityURN}, Category: "correction", CreatedAt: time.Now()},                //nolint:revive // test values
			{ID: "i3", Status: StatusPending, EntityURNs: []string{"urn:li:dataset:other"}, Category: "business_context", CreatedAt: time.Now()}, //nolint:revive // test values
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{Action: "bulk_review"}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, float64(3), m["total_pending"]) //nolint:revive // test value
	assert.NotNil(t, m["by_category"])
	assert.NotNil(t, m["by_confidence"])
	assert.NotNil(t, m["by_entity"])

	byEntity, ok := m["by_entity"].([]any)
	require.True(t, ok, "by_entity should be an array")
	assert.Len(t, byEntity, 2, "should have 2 distinct entities")
}

func TestHandleBulkReview_StatsError(t *testing.T) {
	store := &fullSpyStore{StatsErr: errors.New("stats query failed")}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{Action: "bulk_review"}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)
}

func TestHandleBulkReview_ListError(t *testing.T) {
	store := &fullSpyStore{ListErr: errors.New("list query failed")}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{Action: "bulk_review"} //nolint:revive // test value
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)
}

// ---------------------------------------------------------------------------
// AC-4: review returns entity insights + metadata
// ---------------------------------------------------------------------------

func TestHandleReview_ReturnsInsightsAndMetadata(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "i1", Status: StatusPending, EntityURNs: []string{testEntityURN}, Category: "correction", InsightText: "Column desc is wrong"},
			{ID: "i2", Status: StatusApproved, EntityURNs: []string{testEntityURN}, Category: "business_context", InsightText: "MRR excludes trials"},
		},
	}
	writer := &spyWriter{
		Metadata: &EntityMetadata{
			Description:   "Original description",
			Tags:          []string{"important"},
			GlossaryTerms: []string{"urn:li:glossaryTerm:mrr"},
			Owners:        []string{"user-1"},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, writer)

	input := applyKnowledgeInput{Action: "review", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, testEntityURN, m["entity_urn"])
	assert.NotNil(t, m["current_metadata"])
	assert.NotNil(t, m["insights"])

	insights, ok := m["insights"].([]any)
	require.True(t, ok)
	assert.Len(t, insights, 2)

	meta, ok := m["current_metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Original description", meta["description"])
}

func TestHandleReview_NilWriter(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "i1", Status: StatusPending, EntityURNs: []string{testEntityURN}},
		},
	}
	tk, err := New(testName, store)
	require.NoError(t, err)
	// Apply enabled but datahubWriter is nil (not set via SetApplyConfig)
	tk.applyEnabled = true

	input := applyKnowledgeInput{Action: "review", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Nil(t, m["current_metadata"])
}

func TestHandleReview_MetadataError(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "i1", Status: StatusPending, EntityURNs: []string{testEntityURN}}, //nolint:revive // test value
		},
	}
	writer := &spyWriter{MetaErr: errors.New("datahub unavailable")}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, writer)

	input := applyKnowledgeInput{Action: "review", EntityURN: testEntityURN} //nolint:revive // test value
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	// Metadata error is silently ignored
	assert.Nil(t, m["current_metadata"])
}

func TestHandleReview_ListError(t *testing.T) {
	store := &fullSpyStore{ListErr: errors.New("db error")}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{Action: "review", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)
}

// ---------------------------------------------------------------------------
// AC-5: review requires entity_urn
// ---------------------------------------------------------------------------

func TestHandleReview_RequiresEntityURN(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{Action: "review"}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)

	m := parseJSONResult(t, result)
	errMsg, _ := m["error"].(string)
	assert.Contains(t, errMsg, "entity_urn is required")
}

// ---------------------------------------------------------------------------
// AC-15: synthesize returns proposal
// ---------------------------------------------------------------------------

func TestHandleSynthesize_ReturnsProposal(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{
				ID:         "i1",
				Status:     StatusApproved,
				EntityURNs: []string{testEntityURN},
				Category:   "correction",
				SuggestedActions: []SuggestedAction{
					{ActionType: "update_description", Target: testEntityURN, Detail: "New description"},
				},
			},
			{
				ID:         "i2",
				Status:     StatusApproved,
				EntityURNs: []string{testEntityURN},
				Category:   "enhancement",
				SuggestedActions: []SuggestedAction{
					{ActionType: "add_tag", Target: testEntityURN, Detail: "important"},
				},
			},
		},
	}
	writer := &spyWriter{
		Metadata: &EntityMetadata{
			Description: "Old description",
			Tags:        []string{},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, writer)

	input := applyKnowledgeInput{Action: "synthesize", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, testEntityURN, m["entity_urn"])
	assert.NotNil(t, m["proposed_changes"])
	assert.NotNil(t, m["approved_insights"])

	proposed, ok := m["proposed_changes"].([]any)
	require.True(t, ok)
	assert.Len(t, proposed, 2, "should have 2 proposed changes from 2 insights")

	// Check that update_description has current_value populated from metadata
	firstChange, ok := proposed[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "update_description", firstChange["change_type"])
	assert.Equal(t, "Old description", firstChange["current_value"])
}

// ---------------------------------------------------------------------------
// AC-16: synthesize only includes approved insights
// ---------------------------------------------------------------------------

func TestHandleSynthesize_OnlyApprovedInsights(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{
				ID: "approved1", Status: StatusApproved, EntityURNs: []string{testEntityURN},
				SuggestedActions: []SuggestedAction{{ActionType: "add_tag", Detail: "tag1"}},
			},
			{
				ID: "pending1", Status: StatusPending, EntityURNs: []string{testEntityURN},
				SuggestedActions: []SuggestedAction{{ActionType: "add_tag", Detail: "tag2"}},
			},
			{
				ID: "rejected1", Status: StatusRejected, EntityURNs: []string{testEntityURN},
				SuggestedActions: []SuggestedAction{{ActionType: "add_tag", Detail: "tag3"}}, //nolint:revive // test value
			},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{Action: "synthesize", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	insights, ok := m["approved_insights"].([]any)
	require.True(t, ok)
	assert.Len(t, insights, 1, "only approved insight should be included")

	firstInsight, ok := insights[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "approved1", firstInsight["id"])
}

// ---------------------------------------------------------------------------
// AC-17: synthesize with insight_ids filtering
// ---------------------------------------------------------------------------

func TestHandleSynthesize_InsightIDsFiltering(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{
				ID: "a1", Status: StatusApproved, EntityURNs: []string{testEntityURN},
				SuggestedActions: []SuggestedAction{{ActionType: "add_tag", Detail: "tag1"}},
			},
			{
				ID: "a2", Status: StatusApproved, EntityURNs: []string{testEntityURN},
				SuggestedActions: []SuggestedAction{{ActionType: "add_tag", Detail: "tag2"}},
			},
			{
				ID: "a3", Status: StatusApproved, EntityURNs: []string{testEntityURN},
				SuggestedActions: []SuggestedAction{{ActionType: "add_tag", Detail: "tag3"}},
			},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{
		Action:     "synthesize",
		EntityURN:  testEntityURN,
		InsightIDs: []string{"a1", "a3"}, // Only include a1 and a3
	}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	insights, ok := m["approved_insights"].([]any)
	require.True(t, ok)
	assert.Len(t, insights, 2, "should only include insights matching the given IDs")

	// Verify the correct IDs
	ids := make([]string, 0, len(insights))
	for _, ins := range insights {
		insMap, ok := ins.(map[string]any)
		require.True(t, ok, "expected map[string]any")
		idStr, ok := insMap["id"].(string)
		require.True(t, ok, "expected string id")
		ids = append(ids, idStr)
	}
	assert.Contains(t, ids, "a1")
	assert.Contains(t, ids, "a3")
	assert.NotContains(t, ids, "a2")
}

// ---------------------------------------------------------------------------
// AC-18: synthesize requires entity_urn
// ---------------------------------------------------------------------------

func TestHandleSynthesize_RequiresEntityURN(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{Action: "synthesize"} //nolint:revive // test value
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)

	m := parseJSONResult(t, result)
	errMsg, _ := m["error"].(string)
	assert.Contains(t, errMsg, "entity_urn is required")
}

func TestHandleSynthesize_ListError(t *testing.T) {
	store := &fullSpyStore{ListErr: errors.New("db error")}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{Action: "synthesize", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)
}

func TestHandleSynthesize_MetadataError(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "i1", Status: StatusApproved, EntityURNs: []string{testEntityURN}},
		},
	}
	writer := &spyWriter{MetaErr: errors.New("datahub unavailable")}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, writer)

	input := applyKnowledgeInput{Action: "synthesize", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Nil(t, m["current_metadata"])
}

// ---------------------------------------------------------------------------
// AC-19: apply writes to DataHub via spy writer
// ---------------------------------------------------------------------------

func TestHandleApply_WritesToDataHub(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{}
	tk := newApplyToolkit(t, store, csStore, writer)

	ctx := ctxWithUser("admin-1", "sess-1", "admin")

	input := applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes: []ApplyChange{
			{ChangeType: "update_description", Detail: "New description"}, //nolint:revive // test value
			{ChangeType: "add_tag", Detail: "important"},
			{ChangeType: "add_glossary_term", Detail: "urn:li:glossaryTerm:revenue"},
			{ChangeType: "add_documentation", Detail: "https://docs.example.com", Target: "Revenue docs"},
			{ChangeType: "flag_quality_issue", Detail: "missing_values"},
		},
		InsightIDs: []string{"ins-1", "ins-2"},
	}

	result, _, callErr := tk.handleApplyKnowledge(ctx, nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError, "expected success but got error: %v", result.Content)

	// Verify all 5 write calls were made
	assert.Len(t, writer.WriteCalls, 5) //nolint:revive // test value
	assert.Equal(t, "UpdateDescription", writer.WriteCalls[0].Method)
	assert.Equal(t, "AddTag", writer.WriteCalls[1].Method)
	assert.Equal(t, "AddGlossaryTerm", writer.WriteCalls[2].Method)
	assert.Equal(t, "AddDocumentationLink", writer.WriteCalls[3].Method)
	assert.Equal(t, "AddTag", writer.WriteCalls[4].Method) // flag_quality_issue -> AddTag with prefix

	// Verify flag_quality_issue gets tag prefix
	assert.Equal(t, "quality_issue:missing_values", writer.WriteCalls[4].Arg1)

	// Verify all writes target the correct URN
	for _, wc := range writer.WriteCalls {
		assert.Equal(t, testEntityURN, wc.URN)
	}
}

// ---------------------------------------------------------------------------
// AC-20: apply records changeset
// ---------------------------------------------------------------------------

func TestHandleApply_RecordsChangeset(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{
		Metadata: &EntityMetadata{
			Description: "Old desc",
			Tags:        []string{"existing"},
		},
	}
	tk := newApplyToolkit(t, store, csStore, writer)

	ctx := ctxWithUser("admin-1", "sess-1", "admin") //nolint:revive // test values

	input := applyKnowledgeInput{
		Action:     "apply",
		EntityURN:  testEntityURN,
		Changes:    []ApplyChange{{ChangeType: "update_description", Detail: "New desc"}},
		InsightIDs: []string{"ins-1"},
	}

	result, _, callErr := tk.handleApplyKnowledge(ctx, nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	// Verify changeset was recorded
	require.Len(t, csStore.Changesets, 1)
	cs := csStore.Changesets[0]
	assert.NotEmpty(t, cs.ID)
	assert.Equal(t, testEntityURN, cs.TargetURN)
	assert.Equal(t, "update_description", cs.ChangeType)
	assert.Equal(t, "admin-1", cs.AppliedBy)
	assert.Equal(t, []string{"ins-1"}, cs.SourceInsightIDs)

	// Verify previous_value from metadata
	prevVal, ok := cs.PreviousValue["description"]
	require.True(t, ok)
	assert.Equal(t, "Old desc", prevVal)

	// Verify response includes changeset_id
	m := parseJSONResult(t, result)
	assert.NotEmpty(t, m["changeset_id"])
	assert.Equal(t, testEntityURN, m["entity_urn"])
	assert.Equal(t, float64(1), m["changes_applied"])
	assert.Equal(t, float64(1), m["insights_marked_applied"])
}

func TestHandleApply_MultipleChangeTypes(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{}
	tk := newApplyToolkit(t, store, csStore, writer)

	input := applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes: []ApplyChange{
			{ChangeType: "update_description", Detail: "desc"},
			{ChangeType: "add_tag", Detail: "tag1"},
		},
	}

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	require.Len(t, csStore.Changesets, 1)
	assert.Equal(t, "multiple", csStore.Changesets[0].ChangeType)
}

func TestHandleApply_NilInsightIDs(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{}
	tk := newApplyToolkit(t, store, csStore, writer)

	input := applyKnowledgeInput{
		Action:    "apply", //nolint:revive // test value
		EntityURN: testEntityURN,
		Changes:   []ApplyChange{{ChangeType: "add_tag", Detail: "tag1"}}, //nolint:revive // test values
		// InsightIDs is nil
	}

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	require.Len(t, csStore.Changesets, 1)
	assert.NotNil(t, csStore.Changesets[0].SourceInsightIDs, "nil insight_ids should be normalized")
	assert.Empty(t, csStore.Changesets[0].SourceInsightIDs)
}

// ---------------------------------------------------------------------------
// AC-21: apply marks insights as applied
// ---------------------------------------------------------------------------

func TestHandleApply_MarksInsightsAsApplied(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{}
	tk := newApplyToolkit(t, store, csStore, writer)

	ctx := ctxWithUser("admin-1", "sess-1", "admin") //nolint:revive // test values

	input := applyKnowledgeInput{
		Action:     "apply",
		EntityURN:  testEntityURN,
		Changes:    []ApplyChange{{ChangeType: "add_tag", Detail: "tag1"}},
		InsightIDs: []string{"ins-1", "ins-2", "ins-3"},
	}

	result, _, callErr := tk.handleApplyKnowledge(ctx, nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	// Verify MarkApplied was called for each insight
	require.Len(t, store.MarkAppliedCalls, 3) //nolint:revive // test value
	for i, call := range store.MarkAppliedCalls {
		assert.Equal(t, fmt.Sprintf("ins-%d", i+1), call.ID)
		assert.Equal(t, "admin-1", call.AppliedBy)
		assert.NotEmpty(t, call.ChangesetRef, "changeset ref should be set")
	}

	// All calls should reference the same changeset ID
	csID := store.MarkAppliedCalls[0].ChangesetRef
	for _, call := range store.MarkAppliedCalls {
		assert.Equal(t, csID, call.ChangesetRef)
	}
}

func TestHandleApply_NoPlatformContext(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{}
	tk := newApplyToolkit(t, store, csStore, writer)

	input := applyKnowledgeInput{
		Action:     "apply",
		EntityURN:  testEntityURN,
		Changes:    []ApplyChange{{ChangeType: "add_tag", Detail: "tag1"}},
		InsightIDs: []string{"ins-1"}, //nolint:revive // test value
	}

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	// AppliedBy should be empty when no platform context
	require.Len(t, csStore.Changesets, 1)
	assert.Equal(t, "", csStore.Changesets[0].AppliedBy)
}

// ---------------------------------------------------------------------------
// AC-22: apply atomicity - writer fails mid-batch
// ---------------------------------------------------------------------------

func TestHandleApply_WriterFailsMidBatch(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{FailAtCall: 2} // Fail on second write
	tk := newApplyToolkit(t, store, csStore, writer)

	input := applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes: []ApplyChange{
			{ChangeType: "update_description", Detail: "new desc"},
			{ChangeType: "add_tag", Detail: "tag1"},        // This one will fail
			{ChangeType: "add_tag", Detail: "should-skip"}, // Never reached
		},
		InsightIDs: []string{"ins-1"},
	}

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError, "should fail when writer fails")

	m := parseJSONResult(t, result)
	errMsg, _ := m["error"].(string)
	assert.Contains(t, errMsg, "datahub write failed")
	assert.Contains(t, errMsg, "2 of 3")

	// No changeset should be recorded on failure
	assert.Empty(t, csStore.Changesets)
	// No insights should be marked as applied
	assert.Empty(t, store.MarkAppliedCalls)
}

func TestHandleApply_ChangesetStoreError(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{InsertErr: errors.New("db write failed")}
	writer := &spyWriter{}
	tk := newApplyToolkit(t, store, csStore, writer)

	input := applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes:   []ApplyChange{{ChangeType: "add_tag", Detail: "tag1"}},
	}

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)

	m := parseJSONResult(t, result)
	errMsg, _ := m["error"].(string) //nolint:revive // test value
	assert.Contains(t, errMsg, "failed to record changeset")
}

func TestHandleApply_MetadataFetchError(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{MetaErr: errors.New("datahub unavailable")}
	tk := newApplyToolkit(t, store, csStore, writer)

	input := applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes:   []ApplyChange{{ChangeType: "add_tag", Detail: "tag1"}},
	}

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)

	m := parseJSONResult(t, result)
	errMsg, _ := m["error"].(string)
	assert.Contains(t, errMsg, "failed to get current metadata")
}

// ---------------------------------------------------------------------------
// AC-23: apply requires changes
// ---------------------------------------------------------------------------

func TestHandleApply_RequiresChanges(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})

	tests := []struct {
		name    string
		changes []ApplyChange
	}{
		{name: "nil changes", changes: nil},
		{name: "empty changes", changes: []ApplyChange{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := applyKnowledgeInput{
				Action:    "apply",
				EntityURN: testEntityURN,
				Changes:   tc.changes,
			}
			result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
			require.Nil(t, callErr)
			assert.True(t, result.IsError)
		})
	}
}

// ---------------------------------------------------------------------------
// AC-24: apply requires entity_urn
// ---------------------------------------------------------------------------

func TestHandleApply_RequiresEntityURN(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{
		Action:  "apply",
		Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "tag1"}},
	}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)

	m := parseJSONResult(t, result)
	errMsg, _ := m["error"].(string)
	assert.Contains(t, errMsg, "entity_urn is required")
}

// ---------------------------------------------------------------------------
// AC-25: apply change_type validation
// ---------------------------------------------------------------------------

func TestHandleApply_ChangeTypeValidation(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})

	tests := []struct {
		name       string
		changeType string
		wantErr    bool
	}{
		{name: "valid update_description", changeType: "update_description", wantErr: false},
		{name: "valid add_tag", changeType: "add_tag", wantErr: false},
		{name: "valid add_glossary_term", changeType: "add_glossary_term", wantErr: false},
		{name: "valid flag_quality_issue", changeType: "flag_quality_issue", wantErr: false},
		{name: "valid add_documentation", changeType: "add_documentation", wantErr: false},
		{name: "invalid remove_tag", changeType: "remove_tag", wantErr: true},
		{name: "invalid empty", changeType: "", wantErr: true},
		{name: "invalid arbitrary", changeType: "delete_dataset", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := applyKnowledgeInput{
				Action:    "apply",
				EntityURN: testEntityURN,
				Changes:   []ApplyChange{{ChangeType: tc.changeType, Detail: "detail"}},
			}
			result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
			require.Nil(t, callErr)
			if tc.wantErr {
				assert.True(t, result.IsError, "change_type %q should be rejected", tc.changeType)
			} else {
				assert.False(t, result.IsError, "change_type %q should be accepted", tc.changeType)
			}
		})
	}
}

func TestHandleApply_TooManyChanges(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})

	changes := make([]ApplyChange, MaxApplyChanges+1)
	for i := range changes {
		changes[i] = ApplyChange{ChangeType: "add_tag", Detail: fmt.Sprintf("tag%d", i)}
	}

	input := applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes:   changes,
	}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)
}

// ---------------------------------------------------------------------------
// AC-52: approve/reject actions
// ---------------------------------------------------------------------------

func TestHandleApprove(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "i1", Status: StatusPending, EntityURNs: []string{testEntityURN}},
			{ID: "i2", Status: StatusPending, EntityURNs: []string{testEntityURN}}, //nolint:revive // test value
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	ctx := ctxWithUser("reviewer-1", "sess-1", "admin")

	input := applyKnowledgeInput{
		Action:      "approve",
		InsightIDs:  []string{"i1", "i2"},
		ReviewNotes: "Looks good",
	}
	result, _, callErr := tk.handleApplyKnowledge(ctx, nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, "approve", m["action"])
	assert.Equal(t, float64(2), m["updated"])
	assert.Equal(t, float64(2), m["total"])

	// Verify status transitions were recorded
	require.Len(t, store.StatusCalls, 2)
	assert.Equal(t, StatusApproved, store.StatusCalls[0].Status)
	assert.Equal(t, "reviewer-1", store.StatusCalls[0].ReviewedBy)
	assert.Equal(t, "Looks good", store.StatusCalls[0].ReviewNotes)
}

func TestHandleReject(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "i1", Status: StatusPending, EntityURNs: []string{testEntityURN}},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	ctx := ctxWithUser("reviewer-1", "sess-1", "admin")

	input := applyKnowledgeInput{
		Action:      "reject",
		InsightIDs:  []string{"i1"},
		ReviewNotes: "Not accurate",
	}
	result, _, callErr := tk.handleApplyKnowledge(ctx, nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, "reject", m["action"])
	assert.Equal(t, float64(1), m["updated"])

	require.Len(t, store.StatusCalls, 1)
	assert.Equal(t, StatusRejected, store.StatusCalls[0].Status)
}

func TestHandleApproveReject_RequiresInsightIDs(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})

	for _, action := range []string{"approve", "reject"} { //nolint:revive // test values
		t.Run(action, func(t *testing.T) {
			input := applyKnowledgeInput{
				Action: action,
				// InsightIDs is empty
			}
			result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
			require.Nil(t, callErr)
			assert.True(t, result.IsError)

			m := parseJSONResult(t, result)
			errMsg, _ := m["error"].(string)
			assert.Contains(t, errMsg, "insight_ids is required")
		})
	}
}

func TestHandleApproveReject_InvalidTransition(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "already-approved", Status: StatusApproved, EntityURNs: []string{testEntityURN}},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	// Try to approve an already-approved insight
	input := applyKnowledgeInput{
		Action:     "approve",
		InsightIDs: []string{"already-approved"},
	}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError) // partial success

	m := parseJSONResult(t, result)
	assert.Equal(t, float64(0), m["updated"])
	assert.Equal(t, float64(1), m["total"])

	errList, ok := m["errors"].([]any)
	require.True(t, ok)
	assert.Len(t, errList, 1)
	errStr, ok := errList[0].(string)
	require.True(t, ok, "expected string error element")
	assert.Contains(t, errStr, "invalid status transition")
}

func TestHandleApproveReject_InsightNotFound(t *testing.T) {
	store := &fullSpyStore{}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{
		Action:     "approve",
		InsightIDs: []string{"nonexistent"},
	}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, float64(0), m["updated"])
	errList, ok := m["errors"].([]any)
	require.True(t, ok)
	assert.Len(t, errList, 1)
	errStr, ok := errList[0].(string)
	require.True(t, ok, "expected string error element")
	assert.Contains(t, errStr, "not found")
}

func TestHandleApproveReject_UpdateStatusError(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "i1", Status: StatusPending},
		},
		StatusErr: errors.New("db write error"),
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{
		Action:     "approve",
		InsightIDs: []string{"i1"},
	}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, float64(0), m["updated"])
	errList, ok := m["errors"].([]any)
	require.True(t, ok)
	assert.Len(t, errList, 1)
	errStr, ok := errList[0].(string)
	require.True(t, ok, "expected string error element")
	assert.Contains(t, errStr, "db write error")
}

func TestHandleApproveReject_NoPlatformContext(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "i1", Status: StatusPending},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{
		Action:     "approve",
		InsightIDs: []string{"i1"},
	}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	require.Len(t, store.StatusCalls, 1)
	assert.Equal(t, "", store.StatusCalls[0].ReviewedBy, "should be empty when no platform context")
}

func TestHandleApproveReject_PartialSuccess(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "i1", Status: StatusPending},
			{ID: "i2", Status: StatusApproved}, // Cannot be approved again
			{ID: "i3", Status: StatusPending},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})

	input := applyKnowledgeInput{
		Action:     "approve",
		InsightIDs: []string{"i1", "i2", "i3"},
	}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, float64(2), m["updated"])
	assert.Equal(t, float64(3), m["total"]) //nolint:revive // test value

	errList, ok := m["errors"].([]any) //nolint:revive // test value
	require.True(t, ok)
	assert.Len(t, errList, 1)
}

// ---------------------------------------------------------------------------
// AC-53: require_confirmation config
// ---------------------------------------------------------------------------

func TestHandleApply_RequireConfirmation(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{}
	tk, err := New(testName, store)
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, csStore, writer)

	t.Run("without confirm flag", func(t *testing.T) {
		input := applyKnowledgeInput{
			Action:    "apply",
			EntityURN: testEntityURN,
			Changes:   []ApplyChange{{ChangeType: "add_tag", Detail: "tag1"}},
			Confirm:   false,
		}
		result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
		require.Nil(t, callErr)
		require.False(t, result.IsError, "should not be error, just a confirmation prompt")

		m := parseJSONResult(t, result)
		assert.Equal(t, true, m["confirmation_required"])
		assert.Equal(t, testEntityURN, m["entity_urn"])
		assert.Equal(t, float64(1), m["changes_count"])
		assert.NotEmpty(t, m["message"])

		// Nothing should be written
		assert.Empty(t, writer.WriteCalls)
		assert.Empty(t, csStore.Changesets)
	})

	t.Run("with confirm flag", func(t *testing.T) {
		writer.WriteCalls = nil
		csStore.Changesets = nil

		input := applyKnowledgeInput{
			Action:    "apply",
			EntityURN: testEntityURN,
			Changes:   []ApplyChange{{ChangeType: "add_tag", Detail: "tag1"}},
			Confirm:   true,
		}
		result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
		require.Nil(t, callErr)
		require.False(t, result.IsError)

		m := parseJSONResult(t, result)
		assert.NotEmpty(t, m["changeset_id"])
		assert.Len(t, writer.WriteCalls, 1)
		assert.Len(t, csStore.Changesets, 1)
	})
}

func TestHandleApply_NoConfirmationRequired(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{}
	tk := newApplyToolkit(t, store, csStore, writer)
	// Default: requireConfirmation is false

	input := applyKnowledgeInput{
		Action:    "apply",
		EntityURN: testEntityURN,
		Changes:   []ApplyChange{{ChangeType: "add_tag", Detail: "tag1"}},
		Confirm:   false, // Should still proceed without confirmation
	}

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.NotEmpty(t, m["changeset_id"])
	assert.Len(t, writer.WriteCalls, 1)
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func TestContainsString(t *testing.T) {
	assert.True(t, containsString([]string{"a", "b", "c"}, "b"))
	assert.False(t, containsString([]string{"a", "b", "c"}, "d"))
	assert.False(t, containsString(nil, "a"))
	assert.False(t, containsString([]string{}, "a")) //nolint:revive // test value
}

func TestFilterByIDs(t *testing.T) {
	insights := []Insight{
		{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"},
	}

	result := filterByIDs(insights, []string{"b", "d"})
	assert.Len(t, result, 2)
	assert.Equal(t, "b", result[0].ID)
	assert.Equal(t, "d", result[1].ID)
}

func TestFilterByIDs_NoMatch(t *testing.T) {
	insights := []Insight{{ID: "a"}, {ID: "b"}} //nolint:revive // test values
	result := filterByIDs(insights, []string{"x", "y"})
	assert.Empty(t, result)
}

func TestSummarizeChangeTypes(t *testing.T) {
	t.Run("single type", func(t *testing.T) {
		changes := []ApplyChange{
			{ChangeType: "add_tag"}, {ChangeType: "add_tag"},
		}
		assert.Equal(t, "add_tag", summarizeChangeTypes(changes))
	})

	t.Run("multiple types", func(t *testing.T) {
		changes := []ApplyChange{
			{ChangeType: "add_tag"}, {ChangeType: "update_description"},
		}
		assert.Equal(t, "multiple", summarizeChangeTypes(changes))
	})
}

func TestMetadataToMap(t *testing.T) {
	t.Run("nil metadata", func(t *testing.T) {
		m := metadataToMap(nil)
		assert.NotNil(t, m)
		assert.Empty(t, m)
	})

	t.Run("populated metadata", func(t *testing.T) {
		meta := &EntityMetadata{
			Description:   "desc",
			Tags:          []string{"tag1"},
			GlossaryTerms: []string{"urn:li:glossaryTerm:t1"},
			Owners:        []string{"user-1"},
		}
		m := metadataToMap(meta)
		assert.Equal(t, "desc", m["description"])
		assert.Len(t, m["tags"], 1)
		assert.Len(t, m["glossary_terms"], 1)
		assert.Len(t, m["owners"], 1)
	})
}

func TestChangesToMap(t *testing.T) {
	changes := []ApplyChange{
		{ChangeType: "add_tag", Target: "tgt1", Detail: "tag1"},
		{ChangeType: "update_description", Target: "tgt2", Detail: "new desc"},
	}
	m := changesToMap(changes)
	assert.Len(t, m, 2)

	c0, ok := m["change_0"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "add_tag", c0["change_type"])
	assert.Equal(t, "tgt1", c0["target"])
	assert.Equal(t, "tag1", c0["detail"])
}

func TestBuildEntitySummaries(t *testing.T) {
	now := time.Now()
	insights := []Insight{
		{ID: "i1", EntityURNs: []string{"urn:a", "urn:b"}, Category: "correction", CreatedAt: now},
		{ID: "i2", EntityURNs: []string{"urn:a"}, Category: "enhancement", CreatedAt: now.Add(time.Hour)},
		{ID: "i3", EntityURNs: []string{"urn:c"}, Category: "correction", CreatedAt: now}, //nolint:revive // test values
	}

	summaries := buildEntitySummaries(insights)
	assert.Len(t, summaries, 3, "should have summaries for urn:a, urn:b, urn:c") //nolint:revive // test value

	// Find urn:a summary
	var urnA *EntityInsightSummary
	for i := range summaries {
		if summaries[i].EntityURN == "urn:a" {
			urnA = &summaries[i]
			break
		}
	}
	require.NotNil(t, urnA, "should find summary for urn:a")
	assert.Equal(t, 2, urnA.Count)
	assert.Contains(t, urnA.Categories, "correction")
	assert.Contains(t, urnA.Categories, "enhancement") //nolint:revive // test value
	assert.NotEmpty(t, urnA.LatestAt)
}

func TestBuildEntitySummaries_Empty(t *testing.T) {
	summaries := buildEntitySummaries(nil)
	assert.Empty(t, summaries)
}

func TestBuildEntitySummaries_NoEntityURNs(t *testing.T) {
	insights := []Insight{
		{ID: "i1", EntityURNs: []string{}, Category: "correction", CreatedAt: time.Now()},
	}
	summaries := buildEntitySummaries(insights)
	assert.Empty(t, summaries)
}

func TestBuildProposedChanges(t *testing.T) {
	insights := []Insight{
		{
			ID: "i1",
			SuggestedActions: []SuggestedAction{
				{ActionType: "update_description", Target: testEntityURN, Detail: "New desc"},
			},
		},
		{
			ID: "i2",
			SuggestedActions: []SuggestedAction{
				{ActionType: "add_tag", Target: testEntityURN, Detail: "important"}, //nolint:revive // test value
			},
		},
	}
	meta := &EntityMetadata{Description: "Old desc"} //nolint:revive // test value

	proposed := buildProposedChanges(insights, meta)
	assert.Len(t, proposed, 2)

	// update_description should have current_value populated
	assert.Equal(t, "update_description", proposed[0].ChangeType)
	assert.Equal(t, "Old desc", proposed[0].CurrentValue)   //nolint:revive // test value
	assert.Equal(t, "New desc", proposed[0].SuggestedValue) //nolint:revive // test value
	assert.Equal(t, []string{"i1"}, proposed[0].SourceInsightIDs)

	// add_tag should have empty current_value
	assert.Equal(t, "add_tag", proposed[1].ChangeType)
	assert.Equal(t, "", proposed[1].CurrentValue)
}

func TestBuildProposedChanges_NilMeta(t *testing.T) {
	insights := []Insight{
		{
			ID: "i1",
			SuggestedActions: []SuggestedAction{
				{ActionType: "update_description", Detail: "New desc"}, //nolint:revive // test value
			},
		},
	}

	proposed := buildProposedChanges(insights, nil)
	assert.Len(t, proposed, 1)
	assert.Equal(t, "", proposed[0].CurrentValue, "current_value should be empty when metadata is nil")
}

func TestBuildProposedChanges_NoActions(t *testing.T) {
	insights := []Insight{
		{ID: "i1", SuggestedActions: []SuggestedAction{}},
	}
	proposed := buildProposedChanges(insights, nil)
	assert.Empty(t, proposed)
}

// ---------------------------------------------------------------------------
// executeChanges directly
// ---------------------------------------------------------------------------

func TestExecuteChanges_AllTypes(t *testing.T) {
	writer := &spyWriter{}
	tk := &Toolkit{datahubWriter: writer}

	changes := []ApplyChange{
		{ChangeType: "update_description", Detail: "new desc"},
		{ChangeType: "add_tag", Detail: "tag1"},
		{ChangeType: "add_glossary_term", Detail: "urn:li:glossaryTerm:t1"},
		{ChangeType: "add_documentation", Detail: "https://docs.example.com", Target: "API Docs"},
		{ChangeType: "flag_quality_issue", Detail: "nulls"},
	}

	err := tk.executeChanges(context.Background(), testEntityURN, changes)
	require.NoError(t, err)
	assert.Len(t, writer.WriteCalls, 5) //nolint:revive // test value

	assert.Equal(t, "UpdateDescription", writer.WriteCalls[0].Method)
	assert.Equal(t, "new desc", writer.WriteCalls[0].Arg1) //nolint:revive // test value

	assert.Equal(t, "AddTag", writer.WriteCalls[1].Method) //nolint:revive // test value
	assert.Equal(t, "tag1", writer.WriteCalls[1].Arg1)

	assert.Equal(t, "AddGlossaryTerm", writer.WriteCalls[2].Method)
	assert.Equal(t, "urn:li:glossaryTerm:t1", writer.WriteCalls[2].Arg1)

	assert.Equal(t, "AddDocumentationLink", writer.WriteCalls[3].Method)
	assert.Equal(t, "https://docs.example.com", writer.WriteCalls[3].Arg1)
	assert.Equal(t, "API Docs", writer.WriteCalls[3].Arg2)

	assert.Equal(t, "AddTag", writer.WriteCalls[4].Method)
	assert.Equal(t, "quality_issue:nulls", writer.WriteCalls[4].Arg1)
}

func TestExecuteChanges_FailsOnError(t *testing.T) {
	writer := &spyWriter{FailAtCall: 1}
	tk := &Toolkit{datahubWriter: writer}

	changes := []ApplyChange{
		{ChangeType: "add_tag", Detail: "tag1"},
		{ChangeType: "add_tag", Detail: "tag2"},
	}

	err := tk.executeChanges(context.Background(), testEntityURN, changes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "datahub write failed for change 1 of 2")
}

func TestExecuteChanges_UnknownType(t *testing.T) {
	writer := &spyWriter{}
	tk := &Toolkit{datahubWriter: writer}

	// An unrecognized change type should be a no-op in executeChanges
	// (the switch statement falls through without error)
	changes := []ApplyChange{
		{ChangeType: "unknown_type", Detail: "detail"},
	}

	err := tk.executeChanges(context.Background(), testEntityURN, changes)
	require.NoError(t, err)
	assert.Empty(t, writer.WriteCalls, "unknown type should not produce writer calls")
}
