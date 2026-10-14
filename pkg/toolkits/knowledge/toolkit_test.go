package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
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
	testEntityURN    = "mcp:dataset:(trino,acme,test.table)"
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
	StatusCalls       []statusCall
	MarkAppliedCalls  []markAppliedCall
	ReturnToReviewIDs []string
	SupersedeCalls    []supersedeCall

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

// List models the memory-backed adapter's lossy paging faithfully: the store
// fetches a window of the coarse "memory bucket" the requested status maps to
// (pending, approved and applied all share the active bucket), returns the
// underlying bucket total, and recovers the exact status per record by
// post-filtering. This is what makes a single status-filtered page potentially
// incomplete, so collectPending must walk the whole bucket by the total.
func (s *fullSpyStore) List(_ context.Context, filter InsightFilter) ([]Insight, int, error) {
	if s.ListErr != nil {
		return nil, 0, s.ListErr
	}

	wantBucket := mapInsightStatusToMemory(filter.Status)
	var universe []Insight
	for _, ins := range s.Insights {
		if filter.Status != "" && mapInsightStatusToMemory(ins.Status) != wantBucket {
			continue
		}
		if filter.EntityURN != "" && !slices.Contains(ins.EntityURNs, filter.EntityURN) {
			continue
		}
		universe = append(universe, ins)
	}
	// Newest-first, like the real store's ORDER BY created_at DESC.
	sort.SliceStable(universe, func(i, j int) bool {
		return universe[i].CreatedAt.After(universe[j].CreatedAt)
	})
	total := len(universe)

	lo := min(max(filter.Offset, 0), total)
	hi := total
	if filter.Limit > 0 {
		hi = min(lo+filter.Limit, total)
	}

	var result []Insight
	for _, ins := range universe[lo:hi] {
		if filter.Status != "" && ins.Status != filter.Status {
			continue
		}
		result = append(result, ins)
	}
	return result, total, nil
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

func (s *fullSpyStore) Stats(_ context.Context, filter InsightFilter) (*InsightStats, error) {
	if s.StatsErr != nil {
		return nil, s.StatsErr
	}
	if s.StatsResult != nil {
		return s.StatsResult, nil
	}
	// Tally faithfully: by_category/by_confidence are scoped to the requested
	// status (pending), and TotalPending is the pending count, mirroring the real
	// adapter so the counts path is exercised, not stubbed.
	stats := &InsightStats{
		ByCategory:   map[string]int{},
		ByConfidence: map[string]int{},
		ByStatus:     map[string]int{},
	}
	var pending []Insight
	for _, ins := range s.Insights {
		stats.ByStatus[ins.Status]++
		if ins.Status == StatusPending {
			pending = append(pending, ins)
		}
		if filter.Status != "" && ins.Status != filter.Status {
			continue
		}
		stats.ByCategory[ins.Category]++
		stats.ByConfidence[ins.Confidence]++
	}
	stats.TotalPending = stats.ByStatus[StatusPending]
	// Mirror the real store's pending-queue staleness rollup (#764) so the
	// counts path is exercised faithfully rather than stubbed.
	stats.OldestPendingAt, stats.PendingOver30d = pendingStalenessFromInsights(
		pending, pendingStalenessCutoff(time.Now()),
	)
	return stats, nil
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

// ReturnToReview models the real stores' applied-only gate: an insight in any
// other state is left alone and reported as not returned.
func (s *fullSpyStore) ReturnToReview(_ context.Context, id, rolledBackBy, changesetID string) (bool, error) {
	s.ReturnToReviewIDs = append(s.ReturnToReviewIDs, id)
	for i := range s.Insights {
		if s.Insights[i].ID != id || s.Insights[i].Status != StatusApplied {
			continue
		}
		s.Insights[i].Status = StatusPending
		s.Insights[i].ReviewedBy = rolledBackBy
		s.Insights[i].ReviewNotes = RollbackReviewNote(changesetID)
		return true, nil
	}
	return false, nil
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
	// LastFilter is the filter the last ListChangesets was given.
	LastFilter ChangesetFilter
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

func (s *spyChangesetStore) ListChangesets(_ context.Context, f ChangesetFilter) ([]Changeset, int, error) {
	s.LastFilter = f
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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newApplyToolkit creates a Toolkit with apply enabled and the given dependencies.
func newApplyToolkit(t *testing.T, store InsightStore, csStore ChangesetStore, writer catalog.Store) *Toolkit {
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
// UserEmail is set to the same value as userID so capture_insight (which keys
// ownership on email, issue #515) records the identifier these tests assert on.
func ctxWithUser(userID, sessionID, persona string) context.Context {
	pc := &middleware.PlatformContext{
		SessionID:   sessionID,
		UserID:      userID,
		UserEmail:   userID,
		PersonaName: persona,
	}
	return middleware.WithPlatformContext(context.Background(), pc)
}

// TestAuthorFromContext covers #682 finding 2: authorship fields (page
// created_by/created_email, changeset author) must prefer the email, falling back
// to the user id only when no email is present, never the opaque id when an email
// exists.
func TestAuthorFromContext(t *testing.T) {
	t.Run("prefers email over id", func(t *testing.T) {
		ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
			UserID:    "00f10812-22a1-4549-8a6b-8116b1f3cd06",
			UserEmail: "alice@example.com",
		})
		assert.Equal(t, "alice@example.com", authorFromContext(ctx))
	})

	t.Run("falls back to id when no email", func(t *testing.T) {
		ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
			UserID: "svc-api-key-1",
		})
		assert.Equal(t, "svc-api-key-1", authorFromContext(ctx))
	})

	t.Run("empty when no platform context", func(t *testing.T) {
		assert.Equal(t, "", authorFromContext(context.Background()))
	})
}

// ---------------------------------------------------------------------------
// AC-1: Tool registration
// ---------------------------------------------------------------------------

func TestToolkit_Tools_WithoutApply(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)

	// Capture moved to the memory toolkit (#633); without apply enabled the
	// knowledge toolkit registers no MCP tools (only guidance prompts).
	assert.Empty(t, tk.Tools())
}

func TestToolkit_Tools_WithApply(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true}, nil, nil)

	assert.Equal(t, []string{"apply_knowledge"}, tk.Tools())
}

func TestToolkit_RegisterTools_WithoutApply(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)

	s := mcp.NewServer(&mcp.Implementation{Name: testName, Version: testVersion}, nil)
	tk.RegisterTools(s)
	// No MCP tools without apply (capture moved to the memory toolkit).
	assert.Empty(t, tk.Tools())
}

func TestToolkit_RegisterTools_WithApply(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true}, nil, nil)

	s := mcp.NewServer(&mcp.Implementation{Name: testName, Version: testVersion}, nil)
	tk.RegisterTools(s)
	assert.Len(t, tk.Tools(), 1)
}

// TestApplyKnowledgeDescription_TeachesLoopAndSink asserts the agent-facing
// apply_knowledge description (#674) teaches the review/synthesis workflow and
// references the knowledge-page sink, so a fresh agent can discover the capability
// from the contract rather than only from the JSON schema.
func TestApplyKnowledgeDescription_TeachesLoopAndSink(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true}, nil, nil)

	s := mcp.NewServer(&mcp.Implementation{Name: testName, Version: testVersion}, nil)
	tk.RegisterTools(s)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSess, err := s.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSess.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1.0"}, nil)
	clientSess, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = clientSess.Close() }()

	list, err := clientSess.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	var desc string
	for _, tool := range list.Tools {
		if tool.Name == applyToolName {
			desc = tool.Description
		}
	}
	require.NotEmpty(t, desc, "apply_knowledge tool not advertised")
	// It must teach the loop, the expert workflow, and the page sink mechanism.
	for _, want := range []string{"knowledge loop", "synthesize", "sink=knowledge_page", "'page' object", "update-vs-create"} {
		assert.Contains(t, desc, want, "apply_knowledge description should teach %q", want)
	}
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
	assert.Nil(t, tk.curator.store, "no catalog leaves catalog changes refused")
}

func TestSetApplyConfig_WithDeps(t *testing.T) {
	tk, err := New(testName, nil)
	require.NoError(t, err)

	cs := &spyChangesetStore{}
	w := newFakeCatalog()
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, cs, w)

	assert.True(t, tk.applyEnabled)
	assert.True(t, tk.requireConfirmation)
	assert.Same(t, cs, tk.changesetStore)
	assert.Same(t, w, tk.curator.store)
}

// ---------------------------------------------------------------------------
// AC-14: Context injection from PlatformContext
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
	assert.Contains(t, knowledgeCapturePrompt, "memory_capture")
	assert.Contains(t, knowledgeCapturePrompt, "Agent-Discovered Insights")
	assert.Contains(t, knowledgeCapturePrompt, "agent_discovery")
	assert.Contains(t, knowledgeCapturePrompt, "enrichment_gap")
	assert.Contains(t, knowledgeCapturePrompt, "When to Ask the User")
}

// ---------------------------------------------------------------------------
// validateInput
// ---------------------------------------------------------------------------

func TestErrorResult(t *testing.T) {
	result := toolkit.ErrorResult("something went wrong")
	assert.True(t, result.IsError)
	assert.NotEmpty(t, result.Content)

	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected *mcp.TextContent")
	assert.Contains(t, tc.Text, "something went wrong")
}

func TestJsonResult(t *testing.T) {
	data := map[string]any{"key": "value", "count": float64(42)}
	result, _, err := toolkit.JSONResultTyped(data)
	require.Nil(t, err)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, "value", m["key"])
	assert.Equal(t, float64(42), m["count"])
}

// ---------------------------------------------------------------------------
// AC-2: Action parameter validation
// ---------------------------------------------------------------------------

func TestHandleApplyKnowledge_ActionValidation(t *testing.T) {
	store := &fullSpyStore{}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

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
		Insights: []Insight{
			{ID: "i1", Status: StatusPending, EntityURNs: []string{testEntityURN}, Category: "correction", Confidence: "high", CreatedAt: time.Now()},
			{ID: "i2", Status: StatusPending, EntityURNs: []string{testEntityURN}, Category: "correction", Confidence: "medium", CreatedAt: time.Now()},
			{ID: "i3", Status: StatusPending, EntityURNs: []string{"mcp:dataset:(trino,warehouse,c.s.other)"}, Category: "business_context", Confidence: "medium", CreatedAt: time.Now()},
			// An applied insight must not be counted in the pending queue.
			{ID: "i4", Status: StatusApplied, EntityURNs: []string{testEntityURN}, Category: "correction", Confidence: "high", CreatedAt: time.Now()},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	input := applyKnowledgeInput{Action: "bulk_review"}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, float64(3), m["total_pending"], "applied insight excluded")
	assert.Equal(t, map[string]any{"correction": float64(2), "business_context": float64(1)}, m["by_category"])
	assert.Equal(t, map[string]any{"high": float64(1), "medium": float64(2)}, m["by_confidence"])
	assert.NotEmpty(t, m["note"], "scope note labels the denominators")
	// Counts-only mode does not itemize.
	assert.Nil(t, m["insights"])

	byEntity, ok := m["by_entity"].([]any)
	require.True(t, ok, "by_entity should be an array")
	assert.Len(t, byEntity, 2, "should have 2 distinct entities among pending")
}

// TestHandleBulkReview_StalenessRollup verifies the review-queue staleness
// rollup (#764) is present on both the counts and itemized paths: the oldest
// pending age and the over-30d count let a reviewer (and an agent nudging one)
// see accumulating review debt.
func TestHandleBulkReview_StalenessRollup(t *testing.T) {
	now := time.Now()
	oldest := now.Add(-94 * 24 * time.Hour)
	store := &fullSpyStore{
		Insights: []Insight{
			{ID: "i1", Status: StatusPending, Category: "correction", Confidence: "high", CreatedAt: now.Add(-3 * 24 * time.Hour)},
			{ID: "i2", Status: StatusPending, Category: "correction", Confidence: "low", CreatedAt: oldest},
			{ID: "i3", Status: StatusPending, Category: "business_context", Confidence: "medium", CreatedAt: now.Add(-40 * 24 * time.Hour)},
			// Applied and very old: excluded from the pending staleness rollup.
			{ID: "i4", Status: StatusApplied, Category: "correction", Confidence: "high", CreatedAt: now.Add(-300 * 24 * time.Hour)},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	assertStaleness := func(t *testing.T, m map[string]any) {
		t.Helper()
		require.Contains(t, m, "oldest_pending_at")
		age, ok := m["oldest_pending_age_days"].(float64)
		require.True(t, ok, "oldest_pending_age_days should be numeric")
		assert.InDelta(t, 94, age, 1, "oldest pending is ~94 days old")
		assert.Equal(t, float64(2), m["pending_over_30d"], "two pending insights past 30 days")
	}

	t.Run("counts path", func(t *testing.T) {
		result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
			applyKnowledgeInput{Action: "bulk_review"})
		require.Nil(t, callErr)
		require.False(t, result.IsError)
		assertStaleness(t, parseJSONResult(t, result))
	})

	t.Run("itemized path", func(t *testing.T) {
		result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
			applyKnowledgeInput{Action: "bulk_review", Itemize: true})
		require.Nil(t, callErr)
		require.False(t, result.IsError)
		assertStaleness(t, parseJSONResult(t, result))
	})
}

// TestHandleBulkReview_StalenessOmittedWhenEmpty verifies the staleness fields
// are omitted when the pending queue is empty, so there is no false "0 days"
// signal (#764).
func TestHandleBulkReview_StalenessOmittedWhenEmpty(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{
		{ID: "i1", Status: StatusApplied, Category: "correction", Confidence: "high", CreatedAt: time.Now()},
	}}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "bulk_review"})
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	m := parseJSONResult(t, result)
	assert.Equal(t, float64(0), m["total_pending"])
	assert.NotContains(t, m, "oldest_pending_at")
	assert.NotContains(t, m, "oldest_pending_age_days")
}

func TestHandleBulkReview_StatsError(t *testing.T) {
	store := &fullSpyStore{StatsErr: errors.New("stats query failed")}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	input := applyKnowledgeInput{Action: "bulk_review"}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)
}

func TestHandleBulkReview_ListError(t *testing.T) {
	store := &fullSpyStore{ListErr: errors.New("list query failed")}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	input := applyKnowledgeInput{Action: "bulk_review"}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)
}

// TestCollectPendingStrideInvariant guards the assumption documented on
// collectPending: it strides the underlying offset by MaxLimit, which is correct
// only while MaxLimit does not exceed the backing memory store's page cap. If a
// future change lowered memory.MaxLimit below it, paging would skip records.
// TestHandleBulkReview_CountsMarksByEntityIncomplete covers the cheap counts-only
// path on a large queue: total_pending is accurate (Stats walks everything) while
// by_entity is a one-page sample, so the response flags it as incomplete and stays
// bounded instead of materializing the whole queue.
func TestHandleBulkReview_CountsMarksByEntityIncomplete(t *testing.T) {
	base := time.Now()
	insights := make([]Insight, 0, 180)
	for i := range 150 {
		insights = append(insights, Insight{
			ID: fmt.Sprintf("applied-%d", i), Status: StatusApplied,
			EntityURNs: []string{testEntityURN}, CreatedAt: base.Add(time.Duration(1000+i) * time.Minute),
		})
	}
	for i := range 30 {
		insights = append(insights, Insight{
			ID: fmt.Sprintf("pending-%d", i), Status: StatusPending, Category: "correction",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	store := &fullSpyStore{Insights: insights}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "bulk_review"})
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	m := parseJSONResult(t, result)

	assert.Equal(t, float64(30), m["total_pending"], "Stats counts all pending across the active set")
	assert.Equal(t, false, m["by_entity_complete"], "by_entity is a one-page sample on a large queue")
	assert.Nil(t, m["insights"], "counts-only path does not itemize")
}

func TestCollectPendingStrideInvariant(t *testing.T) {
	if MaxLimit > memory.MaxLimit {
		t.Fatalf("knowledge MaxLimit (%d) must not exceed memory.MaxLimit (%d): collectPending would skip records", MaxLimit, memory.MaxLimit)
	}
}

func TestPageInsights(t *testing.T) {
	mk := func(n int) []Insight {
		out := make([]Insight, n)
		for i := range out {
			out[i] = Insight{ID: fmt.Sprintf("i%d", i)}
		}
		return out
	}
	tests := []struct {
		name                       string
		total, offset, limit       int
		wantLen, wantOff, wantNext int
	}{
		{"first page has more", 30, 0, 10, 10, 0, 10},
		{"middle page", 30, 10, 10, 10, 10, 20},
		{"last partial page", 30, 25, 10, 5, 25, -1},
		{"window ends exactly at len", 30, 20, 10, 10, 20, -1},
		{"offset past end", 5, 10, 10, 0, 10, -1},
		{"non-positive limit defaults", 5, 0, 0, 5, 0, -1},
		{"limit capped at max", MaxLimit + 50, 0, MaxLimit + 10, MaxLimit, 0, MaxLimit},
		{"negative offset treated as zero", 5, -3, 10, 5, 0, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, off, next := pageInsights(mk(tc.total), tc.offset, tc.limit)
			assert.Len(t, page, tc.wantLen)
			assert.Equal(t, tc.wantOff, off)
			assert.Equal(t, tc.wantNext, next)
		})
	}
}

// TestHandleBulkReview_ItemizeReturnsAuthors is the core of the author fix: the
// itemized review queue carries captured_by and sink_class on every insight,
// and the entity-agnostic insight is enumerated even though by_entity omits it.
func TestHandleBulkReview_ItemizeReturnsAuthors(t *testing.T) {
	now := time.Now()
	store := &fullSpyStore{Insights: []Insight{
		{ID: "i1", Status: StatusPending, CapturedBy: "alice@example.com", SinkClass: "schema_entity", Category: "correction", Confidence: "high", EntityURNs: []string{testEntityURN}, CreatedAt: now},
		{ID: "i2", Status: StatusPending, CapturedBy: "bob@example.com", SinkClass: "business_knowledge", Category: "business_context", Confidence: "medium", CreatedAt: now.Add(-time.Minute)},
		{ID: "i3", Status: StatusApplied, CapturedBy: "carol@example.com", Category: "correction", CreatedAt: now},
	}}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "bulk_review", Itemize: true})
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	m := parseJSONResult(t, result)

	assert.Equal(t, float64(2), m["total_pending"])
	items, ok := m["insights"].([]any)
	require.True(t, ok, "itemize returns the insights")
	require.Len(t, items, 2)

	authors := map[string]string{}
	for _, it := range items {
		row, rok := it.(map[string]any)
		require.True(t, rok)
		idStr, _ := row["id"].(string)
		capturedBy, _ := row["captured_by"].(string)
		authors[idStr] = capturedBy
		assert.NotEmpty(t, row["sink_class"], "sink_class surfaced for routing")
	}
	assert.Equal(t, "alice@example.com", authors["i1"])
	assert.Equal(t, "bob@example.com", authors["i2"])

	byEntity, ok := m["by_entity"].([]any)
	require.True(t, ok)
	assert.Len(t, byEntity, 1, "entity-agnostic insight omitted from by_entity but present in insights")
}

// TestHandleBulkReview_ItemizeEnumeratesPendingBehindApplied proves the lossy
// memory mapping does not hide pending insights: with 150 applied insights
// newer than 30 pending ones, a single status-filtered page would surface none,
// yet itemized bulk_review enumerates all 30 across pages with no duplicates.
func TestHandleBulkReview_ItemizeEnumeratesPendingBehindApplied(t *testing.T) {
	base := time.Now()
	insights := make([]Insight, 0, 180)
	for i := range 150 {
		insights = append(insights, Insight{
			ID: fmt.Sprintf("applied-%d", i), Status: StatusApplied,
			CapturedBy: "applier@example.com", Category: "correction", Confidence: "high",
			EntityURNs: []string{testEntityURN}, CreatedAt: base.Add(time.Duration(1000+i) * time.Minute),
		})
	}
	for i := range 30 {
		ins := Insight{
			ID: fmt.Sprintf("pending-%d", i), Status: StatusPending,
			CapturedBy: "analyst@example.com", Category: "business_context", Confidence: "medium",
			EntityURNs: []string{testEntityURN}, CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if i == 0 {
			ins.EntityURNs = nil
		}
		insights = append(insights, ins)
	}
	store := &fullSpyStore{Insights: insights}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	seen := map[string]bool{}
	offset := 0
	for range 100 {
		result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
			applyKnowledgeInput{Action: "bulk_review", Itemize: true, Limit: 10, Offset: offset})
		require.Nil(t, callErr)
		require.False(t, result.IsError)
		m := parseJSONResult(t, result)
		assert.Equal(t, float64(30), m["total_pending"], "all pending counted despite 150 applied ahead")

		insightsAny, _ := m["insights"].([]any)
		for _, it := range insightsAny {
			row, _ := it.(map[string]any)
			id, _ := row["id"].(string)
			require.False(t, seen[id], "no duplicate %s", id)
			seen[id] = true
			assert.True(t, strings.HasPrefix(id, "pending-"), "only pending enumerated, got %s", id)
		}
		next, ok := m["next_offset"]
		if !ok {
			break
		}
		nextF, _ := next.(float64)
		offset = int(nextF)
	}
	assert.Len(t, seen, 30, "every pending insight enumerated across pages")
}

// TestHandleBulkReview_ItemizeIncludesBody locks issue #724 ask 1: an itemized
// bulk_review page carries the full insight_text body so a review pass can read
// every insight without a per-insight fetch.
func TestHandleBulkReview_ItemizeIncludesBody(t *testing.T) {
	body := "The amount column is gross margin before returns, not revenue."
	store := &fullSpyStore{Insights: []Insight{
		{
			ID: "i1", Status: StatusPending, CapturedBy: "alice@example.com", Category: "correction",
			Confidence: "high", InsightText: body, CreatedAt: time.Now(),
		},
	}}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "bulk_review", Itemize: true})
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	m := parseJSONResult(t, result)

	items, ok := m["insights"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	row, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, body, row["insight_text"], "the body is included so no per-insight fetch is needed")
	assert.NotContains(t, m, "page_size_capped", "a small queue is not size-capped")
}

// TestHandleBulkReview_ItemizeBudgetCapsPage locks issue #724 ask 2: a queue of
// large insights returns fewer than the requested limit per page (so the response
// stays under the output cap), flags page_size_capped, and still enumerates every
// insight (with its body) across pages via next_offset.
func TestHandleBulkReview_ItemizeBudgetCapsPage(t *testing.T) {
	base := time.Now()
	const count = 25
	bigBody := strings.Repeat("x", 4000) // near the real per-insight body ceiling
	insights := make([]Insight, 0, count)
	for i := range count {
		insights = append(insights, Insight{
			ID: fmt.Sprintf("pending-%02d", i), Status: StatusPending,
			CapturedBy: "analyst@example.com", Category: "correction", Confidence: "high",
			InsightText: bigBody, CreatedAt: base.Add(time.Duration(count-i) * time.Minute),
		})
	}
	store := &fullSpyStore{Insights: insights}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	// First page: requested limit is the max, but the byte budget cuts it short.
	first, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "bulk_review", Itemize: true, Limit: MaxLimit})
	require.Nil(t, callErr)
	require.False(t, first.IsError)
	fm := parseJSONResult(t, first)
	firstItems, _ := fm["insights"].([]any)
	require.NotEmpty(t, firstItems)
	assert.Less(t, len(firstItems), count, "the byte budget returns fewer than the whole queue")
	assert.Equal(t, true, fm["page_size_capped"], "page_size_capped signals a budget-truncated page")
	require.Contains(t, fm, "next_offset", "more insights remain to page through")

	// Page through to the end; every insight is enumerated once, with its body.
	seen := map[string]bool{}
	offset := 0
	for range count + 5 {
		result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
			applyKnowledgeInput{Action: "bulk_review", Itemize: true, Limit: MaxLimit, Offset: offset})
		require.Nil(t, err)
		m := parseJSONResult(t, result)
		items, _ := m["insights"].([]any)

		// The marshaled insights array stays within the budget (the always-take-one
		// rule allows a single item to exceed it, but here no single body does).
		raw, mErr := json.Marshal(items)
		require.NoError(t, mErr)
		assert.LessOrEqual(t, len(raw), bulkReviewItemBudgetBytes, "page insights fit the byte budget")

		for _, it := range items {
			row, _ := it.(map[string]any)
			id, _ := row["id"].(string)
			require.False(t, seen[id], "no duplicate %s", id)
			seen[id] = true
			assert.Equal(t, bigBody, row["insight_text"], "body carried on every page")
		}
		next, ok := m["next_offset"]
		if !ok {
			break
		}
		nextF, _ := next.(float64)
		offset = int(nextF)
	}
	assert.Len(t, seen, count, "every insight enumerated across budget-capped pages")
}

// TestHandleBulkReview_ItemizeProjectsSuggestedActions locks the projection: the
// uncapped suggested_actions array is replaced by suggested_actions_count so a
// single insight's query SQL cannot overflow the output cap (issue #724 [1]).
func TestHandleBulkReview_ItemizeProjectsSuggestedActions(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{
		{
			ID: "i1", Status: StatusPending, CapturedBy: "alice@example.com", SessionID: "sess-1",
			Persona: "analyst", Category: "correction",
			Confidence: "high", InsightText: "body", CreatedAt: time.Now(),
			EntityURNs:     []string{testEntityURN},
			RelatedColumns: []RelatedColumn{{URN: testEntityURN, Column: "amount", Relevance: "direct"}},
			SuggestedActions: []SuggestedAction{
				{ActionType: testActionAddTag},
				{ActionType: "add_curated_query", QuerySQL: strings.Repeat("SELECT 1 ", 100)},
			},
		},
	}}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "bulk_review", Itemize: true})
	require.Nil(t, callErr)
	m := parseJSONResult(t, result)
	items, _ := m["insights"].([]any)
	require.Len(t, items, 1)
	row, _ := items[0].(map[string]any)
	assert.Equal(t, float64(2), row["suggested_actions_count"], "the count replaces the full array")
	assert.NotContains(t, row, "suggested_actions", "the heavy suggested_actions array is dropped from the listing")
	assert.Contains(t, row, "created_at", "created_at is still present for time-based triage")
	// The full record is preserved (only suggested_actions is substituted), so
	// fields like session_id that callers grouped on are not silently dropped.
	assert.Equal(t, "sess-1", row["session_id"], "session_id preserved on the full record")
	assert.Equal(t, "analyst", row["persona"], "persona preserved on the full record")
}

// TestHandleBulkReview_ItemizeCapsByEntity locks issue #724 [0]: by_entity is built
// over the whole queue and is otherwise unbounded, so it is capped and the cut is
// flagged with by_entity_truncated.
func TestHandleBulkReview_ItemizeCapsByEntity(t *testing.T) {
	base := time.Now()
	// One insight per distinct entity URN, more than the cap.
	total := maxBulkReviewEntitySummaries + 15
	insights := make([]Insight, 0, total)
	for i := range total {
		insights = append(insights, Insight{
			ID: fmt.Sprintf("i%03d", i), Status: StatusPending, CapturedBy: "a@example.com",
			Category: "correction", Confidence: "high", InsightText: "b",
			EntityURNs: []string{fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,db.t%03d,PROD)", i)},
			CreatedAt:  base.Add(time.Duration(i) * time.Minute),
		})
	}
	store := &fullSpyStore{Insights: insights}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "bulk_review", Itemize: true, Limit: 5})
	require.Nil(t, callErr)
	m := parseJSONResult(t, result)

	byEntity, ok := m["by_entity"].([]any)
	require.True(t, ok)
	assert.Len(t, byEntity, maxBulkReviewEntitySummaries, "by_entity is capped")
	assert.Equal(t, true, m["by_entity_truncated"], "the cut is flagged")
	assert.Equal(t, float64(total), m["total_pending"], "the count still reflects the whole queue")
}

// TestPageInsightsBudgeted covers the windowing arithmetic: limit-bound vs
// budget-bound vs end-of-queue termination, the always-take-at-least-one rule for
// an oversized leading insight, and out-of-range offsets.
func TestPageInsightsBudgeted(t *testing.T) {
	// Uniformly sized insights so item size is predictable via reviewItemSize.
	mk := func(n int) []Insight {
		out := make([]Insight, n)
		for i := range out {
			out[i] = Insight{ID: fmt.Sprintf("i%02d", i), InsightText: strings.Repeat("y", 100)}
		}
		return out
	}
	all := mk(10)
	itemSize := reviewItemSize(all[0])

	t.Run("empty input returns empty page and no next", func(t *testing.T) {
		p := pageInsightsBudgeted(nil, 0, 10, 1<<20)
		assert.Empty(t, p.insights)
		assert.Equal(t, 0, p.offset)
		assert.Equal(t, -1, p.next)
		assert.False(t, p.capped)
	})

	t.Run("offset past end returns empty and no next", func(t *testing.T) {
		p := pageInsightsBudgeted(all, 100, 10, 1<<20)
		assert.Empty(t, p.insights)
		assert.Equal(t, 100, p.offset)
		assert.Equal(t, -1, p.next)
		assert.False(t, p.capped)
	})

	t.Run("whole queue under limit and budget", func(t *testing.T) {
		p := pageInsightsBudgeted(all, 0, MaxLimit, 1<<20)
		assert.Len(t, p.insights, 10)
		assert.Equal(t, 0, p.offset)
		assert.Equal(t, -1, p.next)
		assert.False(t, p.capped)
	})

	t.Run("limit-bound page is not flagged capped", func(t *testing.T) {
		p := pageInsightsBudgeted(all, 0, 4, 1<<20)
		assert.Len(t, p.insights, 4)
		assert.Equal(t, 0, p.offset)
		assert.Equal(t, 4, p.next, "next_offset advances by the limit")
		assert.False(t, p.capped, "the limit, not the budget, ended the page")
	})

	t.Run("budget-bound page is flagged capped and pages on", func(t *testing.T) {
		// Budget fits exactly three items, well under the requested limit.
		budget := itemSize*3 + itemSize/2
		p := pageInsightsBudgeted(all, 0, MaxLimit, budget)
		assert.Len(t, p.insights, 3)
		assert.Equal(t, 0, p.offset)
		assert.Equal(t, 3, p.next)
		assert.True(t, p.capped)
	})

	t.Run("single oversized insight is always returned", func(t *testing.T) {
		// A budget smaller than one item must still make progress.
		p := pageInsightsBudgeted(all, 0, MaxLimit, 1)
		assert.Len(t, p.insights, 1, "always take at least one so paging never stalls")
		assert.Equal(t, 0, p.offset)
		assert.Equal(t, 1, p.next)
		assert.True(t, p.capped)
	})

	t.Run("limit above max is clamped to MaxLimit", func(t *testing.T) {
		big := mk(MaxLimit + 5)
		p := pageInsightsBudgeted(big, 0, MaxLimit+50, 1<<30)
		assert.Len(t, p.insights, MaxLimit, "page is clamped to MaxLimit even with a generous budget")
		assert.Equal(t, 0, p.offset)
		assert.Equal(t, MaxLimit, p.next, "the clamped limit, not the budget, ended the page")
		assert.False(t, p.capped)
	})

	t.Run("non-positive limit defaults to DefaultLimit", func(t *testing.T) {
		big := mk(DefaultLimit + 5)
		p := pageInsightsBudgeted(big, 0, 0, 1<<30)
		assert.Len(t, p.insights, DefaultLimit, "a zero limit defaults to DefaultLimit")
		assert.Equal(t, DefaultLimit, p.next)
		assert.False(t, p.capped)
	})

	t.Run("budget cut on the final stretch returns no next", func(t *testing.T) {
		budget := itemSize*3 + itemSize/2
		p := pageInsightsBudgeted(all, 8, MaxLimit, budget)
		assert.Len(t, p.insights, 2, "only two insights remain from offset 8")
		assert.Equal(t, 8, p.offset)
		assert.Equal(t, -1, p.next, "the end of the queue, reached within budget, returns no next")
		assert.False(t, p.capped)
	})
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
	writer := testCatalog()
	writer.SetDescription(testDatasetRef, "Original description")
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
	// Apply enabled but no catalog is set (SetApplyConfig never called)
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
			{ID: "i1", Status: StatusPending, EntityURNs: []string{testEntityURN}},
		},
	}
	writer := newFakeCatalog() // the catalog does not hold the entity
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, writer)

	input := applyKnowledgeInput{Action: "review", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	// Metadata error is silently ignored
	assert.Nil(t, m["current_metadata"])
}

func TestHandleReview_ListError(t *testing.T) {
	store := &fullSpyStore{ListErr: errors.New("db error")}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	input := applyKnowledgeInput{Action: "review", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)
}

// ---------------------------------------------------------------------------
// AC-5: review requires entity_urn
// ---------------------------------------------------------------------------

func TestHandleReview_RequiresEntityURN(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, newFakeCatalog())

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
	writer := testCatalog()
	writer.SetDescription(testDatasetRef, "Old description")
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
				SuggestedActions: []SuggestedAction{{ActionType: "add_tag", Detail: "tag3"}},
			},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

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
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

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
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, newFakeCatalog())

	input := applyKnowledgeInput{Action: "synthesize"}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	assert.True(t, result.IsError)

	m := parseJSONResult(t, result)
	errMsg, _ := m["error"].(string)
	assert.Contains(t, errMsg, "entity_urn is required")
}

func TestHandleSynthesize_ListError(t *testing.T) {
	store := &fullSpyStore{ListErr: errors.New("db error")}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

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
	writer := newFakeCatalog()
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, writer)

	input := applyKnowledgeInput{Action: "synthesize", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Nil(t, m["current_metadata"])
}

// ---------------------------------------------------------------------------
// AC-25: synthesize returns note when proposed_changes is empty
// ---------------------------------------------------------------------------

func TestHandleSynthesize_NoteWhenNoProposedChanges(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{
				ID:         "i1",
				Status:     StatusApproved,
				EntityURNs: []string{testEntityURN},
				Category:   "observation",
				// No SuggestedActions — this triggers the note
			},
		},
	}
	writer := newFakeCatalog()
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, writer)

	input := applyKnowledgeInput{Action: "synthesize", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	proposed, ok := m["proposed_changes"].([]any)
	require.True(t, ok)
	assert.Empty(t, proposed)

	note, ok := m["note"].(string)
	require.True(t, ok, "response should contain a note when proposed_changes is empty")
	assert.Contains(t, note, "without suggested_actions")
}

func TestHandleSynthesize_NoNoteWhenProposalsExist(t *testing.T) {
	store := &fullSpyStore{
		Insights: []Insight{
			{
				ID:         "i1",
				Status:     StatusApproved,
				EntityURNs: []string{testEntityURN},
				SuggestedActions: []SuggestedAction{
					{ActionType: "add_tag", Target: testEntityURN, Detail: "verified"},
				},
			},
		},
	}
	writer := newFakeCatalog()
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, writer)

	input := applyKnowledgeInput{Action: "synthesize", EntityURN: testEntityURN}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Nil(t, m["note"], "note should not be present when proposed_changes is non-empty")
}

// ---------------------------------------------------------------------------
// AC-20: apply records changeset
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// AC-21: apply marks insights as applied
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// AC-22: apply atomicity - writer fails mid-batch
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// AC-23: apply requires changes
// ---------------------------------------------------------------------------

func TestHandleApply_RequiresChanges(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, newFakeCatalog())

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
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, newFakeCatalog())

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
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, testCatalog())

	tests := []struct {
		name       string
		changeType string
		wantErr    bool
	}{
		{name: "valid update_description", changeType: "update_description", wantErr: false},
		{name: "valid add_tag", changeType: "add_tag", wantErr: false},
		{name: "valid remove_tag", changeType: "remove_tag", wantErr: false},
		{name: "valid add_glossary_term", changeType: "add_glossary_term", wantErr: false},
		{name: "valid flag_quality_issue", changeType: "flag_quality_issue", wantErr: false},
		{name: "valid add_documentation", changeType: "add_documentation", wantErr: false},
		{name: "invalid empty", changeType: "", wantErr: true},
		{name: "invalid arbitrary", changeType: "delete_dataset", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := applyKnowledgeInput{
				Action:    "apply",
				EntityURN: testEntityURN,
				Changes:   []ApplyChange{{ChangeType: tc.changeType, Target: "https://example.com/doc", Detail: "detail"}},
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
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, newFakeCatalog())

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
			{ID: "i2", Status: StatusPending, EntityURNs: []string{testEntityURN}},
		},
	}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

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
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

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
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, newFakeCatalog())

	for _, action := range []string{"approve", "reject"} {
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
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

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
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

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
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

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
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

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
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, newFakeCatalog())

	input := applyKnowledgeInput{
		Action:     "approve",
		InsightIDs: []string{"i1", "i2", "i3"},
	}
	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil, input)
	require.Nil(t, callErr)
	require.False(t, result.IsError)

	m := parseJSONResult(t, result)
	assert.Equal(t, float64(2), m["updated"])
	assert.Equal(t, float64(3), m["total"])

	errList, ok := m["errors"].([]any)
	require.True(t, ok)
	assert.Len(t, errList, 1)
}

// ---------------------------------------------------------------------------
// AC-53: require_confirmation config
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// AC: handleApply with add_curated_query returns created_ids
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func TestContainsString(t *testing.T) {
	assert.True(t, containsString([]string{"a", "b", "c"}, "b"))
	assert.False(t, containsString([]string{"a", "b", "c"}, "d"))
	assert.False(t, containsString(nil, "a"))
	assert.False(t, containsString([]string{}, "a"))
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
	insights := []Insight{{ID: "a"}, {ID: "b"}}
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
			GlossaryTerms: []string{"mcp:glossary_term:t1"},
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
	_, hasQuerySQL := c0["query_sql"]
	assert.False(t, hasQuerySQL, "query_sql should be omitted when empty")
}

func TestChangesToMap_WithQueryFields(t *testing.T) {
	changes := []ApplyChange{
		{ChangeType: "add_curated_query", Detail: "Revenue", QuerySQL: "SELECT 1", QueryDescription: "Test query"},
	}
	m := changesToMap(changes)
	require.Len(t, m, 1)

	c0, ok := m["change_0"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "add_curated_query", c0["change_type"])
	assert.Equal(t, "Revenue", c0["detail"])
	assert.Equal(t, "SELECT 1", c0["query_sql"])
	assert.Equal(t, "Test query", c0["query_description"])
}

func TestBuildEntitySummaries(t *testing.T) {
	now := time.Now()
	insights := []Insight{
		{ID: "i1", EntityURNs: []string{"urn:a", "urn:b"}, Category: "correction", CreatedAt: now},
		{ID: "i2", EntityURNs: []string{"urn:a"}, Category: "enhancement", CreatedAt: now.Add(time.Hour)},
		{ID: "i3", EntityURNs: []string{"urn:c"}, Category: "correction", CreatedAt: now},
	}

	summaries := buildEntitySummaries(insights)
	assert.Len(t, summaries, 3, "should have summaries for urn:a, urn:b, urn:c")

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
	assert.Contains(t, urnA.Categories, "enhancement")
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
				{ActionType: "add_tag", Target: testEntityURN, Detail: "important"},
			},
		},
	}
	meta := &EntityMetadata{Description: "Old desc"}

	proposed := buildProposedChanges(insights, meta)
	assert.Len(t, proposed, 2)

	// update_description should have current_value populated
	assert.Equal(t, "update_description", proposed[0].ChangeType)
	assert.Equal(t, "Old desc", proposed[0].CurrentValue)
	assert.Equal(t, "New desc", proposed[0].SuggestedValue)
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
				{ActionType: "update_description", Detail: "New desc"},
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

func TestParseColumnTarget(t *testing.T) {
	tests := []struct {
		name      string
		target    string
		wantField string
		wantOK    bool
	}{
		{"column target", "column:location_type_id", "location_type_id", true},
		{"nested column", "column:address.zip_code", "address.zip_code", true},
		{"empty target", "", "", false},
		{"no prefix", "location_type_id", "", false},
		{"empty after prefix", "column:", "", false},
		{"uppercase", "Column:foo", "", false},
		{"dataset URN", "mcp:dataset:(trino,warehouse,c.s.test)", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, ok := parseColumnTarget(tt.target)
			assert.Equal(t, tt.wantOK, ok, "parseColumnTarget(%q) ok", tt.target)
			assert.Equal(t, tt.wantField, field, "parseColumnTarget(%q) field", tt.target)
		})
	}
}

// ---------------------------------------------------------------------------
// URN normalization
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Non-dataset entity support (issue #180)
// ---------------------------------------------------------------------------

// --- Prompt tests ---

func TestPromptInfos(t *testing.T) {
	tk := &Toolkit{}
	infos := tk.PromptInfos()
	require.Len(t, infos, 3)

	assert.Equal(t, promptName, infos[0].Name)
	assert.NotEmpty(t, infos[0].Description)
	assert.Equal(t, "toolkit", infos[0].Category)

	assert.Equal(t, applyPromptName, infos[1].Name)
	assert.NotEmpty(t, infos[1].Description)
	assert.Equal(t, "toolkit", infos[1].Category)
	// The apply guidance must teach the loop and the route to a knowledge page.
	assert.Contains(t, infos[1].Content, "knowledge_page")
	assert.Contains(t, infos[1].Content, "Synthesize")

	assert.Equal(t, userPromptName, infos[2].Name)
	assert.NotEmpty(t, infos[2].Description)
	assert.Equal(t, "toolkit", infos[2].Category)
}

func TestRegisterPrompts(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)

	tk := &Toolkit{}
	tk.registerPrompt(s)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSess, err := s.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSess.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	clientSess, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = clientSess.Close() }()

	// List prompts
	listResp, err := clientSess.ListPrompts(ctx, &mcp.ListPromptsParams{})
	require.NoError(t, err)
	require.Len(t, listResp.Prompts, 3)

	names := make(map[string]bool)
	for _, p := range listResp.Prompts {
		names[p.Name] = true
	}
	assert.True(t, names[promptName])
	assert.True(t, names[applyPromptName])
	assert.True(t, names[userPromptName])

	// Get each prompt and verify content
	for _, name := range []string{promptName, applyPromptName, userPromptName} {
		resp, err := clientSess.GetPrompt(ctx, &mcp.GetPromptParams{Name: name})
		require.NoError(t, err, "prompt %s", name)
		require.Len(t, resp.Messages, 1)
		textContent, ok := resp.Messages[0].Content.(*mcp.TextContent)
		require.True(t, ok)
		assert.NotEmpty(t, textContent.Text)
	}
}

// ---------------------------------------------------------------------------
// Structured property and incident change types
// ---------------------------------------------------------------------------

func TestParsePropertyValues(t *testing.T) {
	tests := []struct {
		name    string
		detail  string
		want    int
		wantErr bool
	}{
		{name: "plain string", detail: "PII", want: 1},
		{name: "json array", detail: `["PII", "sensitive"]`, want: 2},
		{name: "json number", detail: "90", want: 1},
		{name: "empty", detail: "", wantErr: true},
		{name: "whitespace only", detail: "  ", wantErr: true},
		{name: "invalid json array", detail: `[invalid`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, err := parsePropertyValues(tt.detail)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, values, tt.want)
		})
	}

	t.Run("json number preserves int type", func(t *testing.T) {
		values, err := parsePropertyValues("90")
		require.NoError(t, err)
		require.Len(t, values, 1)
		_, ok := values[0].(int64)
		assert.True(t, ok, "expected int64, got %T", values[0])
		assert.Equal(t, int64(90), values[0])
	})

	t.Run("json float preserves float type", func(t *testing.T) {
		values, err := parsePropertyValues("3.14")
		require.NoError(t, err)
		require.Len(t, values, 1)
		_, ok := values[0].(float64)
		assert.True(t, ok, "expected float64, got %T", values[0])
		assert.InDelta(t, 3.14, values[0], 0.001)
	})

	t.Run("json array preserves int type for integers", func(t *testing.T) {
		values, err := parsePropertyValues(`[90, "PII"]`)
		require.NoError(t, err)
		require.Len(t, values, 2)
		_, ok := values[0].(int64)
		assert.True(t, ok, "expected int64 in array, got %T", values[0])
		assert.Equal(t, int64(90), values[0])
		assert.Equal(t, "PII", values[1])
	})

	t.Run("json array preserves float type for decimals", func(t *testing.T) {
		values, err := parsePropertyValues(`[3.14]`)
		require.NoError(t, err)
		require.Len(t, values, 1)
		_, ok := values[0].(float64)
		assert.True(t, ok, "expected float64 in array, got %T", values[0])
		assert.InDelta(t, 3.14, values[0], 0.001)
	})
}
