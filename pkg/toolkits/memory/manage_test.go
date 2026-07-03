package memory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// ---------------------------------------------------------------------------
// Mock store
// ---------------------------------------------------------------------------

type mockStore struct {
	insertErr       error
	getResult       *memstore.Record
	getErr          error
	updateErr       error
	deleteErr       error
	listRecords     []memstore.Record
	listTotal       int
	listErr         error
	vectorResults   []memstore.ScoredRecord
	vectorErr       error
	hybridResults   []memstore.ScoredRecord
	hybridErr       error
	hybridQueries   []memstore.HybridQuery
	lexicalResults  []memstore.ScoredRecord
	lexicalErr      error
	lexicalQueries  []memstore.LexicalQuery
	entityRecords   []memstore.Record
	entityErr       error
	markStaleErr    error
	markVerifiedErr error
	supersedeErr    error

	// Track calls
	insertedRecords []memstore.Record
	deletedIDs      []string
	updatedID       string
	updatedFields   memstore.RecordUpdate
	supersedeCalls  [][2]string
}

func (m *mockStore) Insert(_ context.Context, record memstore.Record) error {
	if m.insertErr != nil {
		return m.insertErr
	}
	m.insertedRecords = append(m.insertedRecords, record)
	return nil
}

func (m *mockStore) Get(_ context.Context, _ string) (*memstore.Record, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.getResult != nil {
		return m.getResult, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockStore) Update(_ context.Context, id string, updates memstore.RecordUpdate) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updatedID = id
	m.updatedFields = updates
	return nil
}

func (m *mockStore) Delete(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedIDs = append(m.deletedIDs, id)
	return nil
}

func (m *mockStore) List(_ context.Context, _ memstore.Filter) ([]memstore.Record, int, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	return m.listRecords, m.listTotal, nil
}

func (m *mockStore) VectorSearch(_ context.Context, _ memstore.VectorQuery) ([]memstore.ScoredRecord, error) {
	if m.vectorErr != nil {
		return nil, m.vectorErr
	}
	return m.vectorResults, nil
}

func (m *mockStore) HybridSearch(_ context.Context, q memstore.HybridQuery) ([]memstore.ScoredRecord, error) {
	m.hybridQueries = append(m.hybridQueries, q)
	if m.hybridErr != nil {
		return nil, m.hybridErr
	}
	return m.hybridResults, nil
}

func (m *mockStore) LexicalSearch(_ context.Context, q memstore.LexicalQuery) ([]memstore.ScoredRecord, error) {
	m.lexicalQueries = append(m.lexicalQueries, q)
	if m.lexicalErr != nil {
		return nil, m.lexicalErr
	}
	return m.lexicalResults, nil
}

func (m *mockStore) EntityLookup(_ context.Context, _, _, _ string) ([]memstore.Record, error) {
	if m.entityErr != nil {
		return nil, m.entityErr
	}
	return m.entityRecords, nil
}

func (m *mockStore) MarkStale(_ context.Context, _ []string, _ string) error {
	return m.markStaleErr
}

func (m *mockStore) MarkVerified(_ context.Context, _ []string) error {
	return m.markVerifiedErr
}

func (m *mockStore) Supersede(_ context.Context, oldID, newID string) error {
	if m.supersedeErr != nil {
		return m.supersedeErr
	}
	m.supersedeCalls = append(m.supersedeCalls, [2]string{oldID, newID})
	return nil
}

// Verify interface compliance.
var _ memstore.Store = (*mockStore)(nil)

// ---------------------------------------------------------------------------
// Mock embedding provider
// ---------------------------------------------------------------------------

type mockEmbedder struct {
	embedResult []float32
	embedErr    error
	dim         int
	model       string
}

// Model lets ModelName(t.embedder) return a non-empty identifier so the
// write-path embedding breadcrumbs can be asserted.
func (m *mockEmbedder) Model() string { return m.model }

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	if m.embedErr != nil {
		return nil, m.embedErr
	}
	return m.embedResult, nil
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i := range texts {
		results[i] = m.embedResult
	}
	return results, nil
}

func (m *mockEmbedder) Dimension() int {
	if m.dim == 0 {
		return embedding.DefaultDimension
	}
	return m.dim
}

func (*mockEmbedder) Kind() string { return "fake" }

var _ embedding.Provider = (*mockEmbedder)(nil)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestToolkit(store memstore.Store, embedder embedding.Provider) *Toolkit {
	if embedder == nil {
		embedder = &mockEmbedder{embedResult: []float32{0.1, 0.2, 0.3}}
	}
	tk, _ := New("test", store, embedder)
	return tk
}

func ctxWithPC(email, persona string) context.Context {
	pc := middleware.NewPlatformContext("test-req")
	pc.UserEmail = email
	pc.PersonaName = persona
	pc.SessionID = "sess-123"
	return middleware.WithPlatformContext(context.Background(), pc)
}

func extractJSON(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.NotNil(t, result)
	require.NotEmpty(t, result.Content)
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var data map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &data))
	return data
}

// ---------------------------------------------------------------------------
// handleManage dispatch tests
// ---------------------------------------------------------------------------

func TestHandleManage_Dispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		isError bool
		check   func(t *testing.T, data map[string]any)
	}{
		{
			name:    "empty command returns help",
			command: "",
			isError: false,
			check: func(t *testing.T, data map[string]any) {
				t.Helper()
				assert.Contains(t, data, "commands")
			},
		},
		{
			name:    "unknown command",
			command: "destroy",
			isError: true,
			check: func(t *testing.T, data map[string]any) {
				t.Helper()
				assert.Contains(t, data["error"], "unknown command")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := &mockStore{}
			tk := newTestToolkit(store, nil)
			ctx := ctxWithPC("user@example.com", "analyst")

			result, _, err := tk.handleManage(ctx, nil, manageInput{Command: tt.command})
			require.NoError(t, err)
			assert.Equal(t, tt.isError, result.IsError)
			data := extractJSON(t, result)
			tt.check(t, data)
		})
	}
}

func TestHandleManage_RoutesToCorrectHandler(t *testing.T) {
	t.Parallel()

	store := &mockStore{}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	// list routes correctly (creation is handled by memory_capture, #633)
	result, _, err := tk.handleManage(ctx, nil, manageInput{Command: "list"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data, "records")

	// review_stale routes correctly
	result, _, err = tk.handleManage(ctx, nil, manageInput{Command: "review_stale"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	data = extractJSON(t, result)
	assert.Contains(t, data, "message")
}

// ---------------------------------------------------------------------------
func TestHandleUpdate_StampsEmbeddingBreadcrumbs(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{ID: "mem-1", CreatedBy: "user@example.com"},
	}
	embedder := &mockEmbedder{embedResult: []float32{0.3, 0.4}, model: "nomic-embed-text"}
	tk := newTestToolkit(store, embedder)
	ctx := ctxWithPC("user@example.com", "analyst")

	const content = "updated: error_code 42 means a retryable timeout"
	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "update",
		ID:      "mem-1",
		Content: content,
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	assert.Equal(t, "nomic-embed-text", store.updatedFields.EmbeddingModel)
	want := sha256.Sum256([]byte(content))
	assert.Equal(t, want[:], store.updatedFields.EmbeddingTextHash)
}

// TestHandleRemember_NoopEmbedderSkipsEmbed proves the write-path
// guard for #429: when the embedder is the noop placeholder, the
// stored record's Embedding MUST be nil and the embedder's Embed
// method MUST NOT be called (otherwise we'd persist a zero vector
// that the recall-side check at recall.go:127 would later refuse
// to query).
func TestHandleUpdate_Valid(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{
			ID:        "abc123",
			CreatedBy: "user@example.com",
		},
	}
	embedder := &mockEmbedder{embedResult: []float32{0.5, 0.6}}
	tk := newTestToolkit(store, embedder)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "update",
		ID:      "abc123",
		Content: "Updated content that is long enough for tests",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, "abc123", data["id"])
	assert.Equal(t, "abc123", store.updatedID)
	assert.Equal(t, "Updated content that is long enough for tests", store.updatedFields.Content)
	assert.Equal(t, []float32{0.5, 0.6}, store.updatedFields.Embedding)
}

// TestHandleUpdate_NoopEmbedderSkipsEmbed is the symmetric guard test
// for the update path: under the noop placeholder, re-embedding on
// content change MUST NOT overwrite the stored vector with a zero
// vector (#429).
func TestHandleUpdate_NoopEmbedderSkipsEmbed(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{
			ID:        "abc123",
			CreatedBy: "user@example.com",
		},
	}
	tk := newTestToolkit(store, embedding.NewNoopProvider(768))
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "update",
		ID:      "abc123",
		Content: "Updated content that is long enough for tests",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Embedding must remain nil; the postgres store's update path
	// writes the embedding column only when len(Embedding) > 0, so a
	// zero-length nil here keeps the column untouched.
	assert.Nil(t, store.updatedFields.Embedding,
		"noop embedder must not produce a stored vector on update")
}

func TestHandleUpdate_MissingID(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "update",
		ID:      "",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "id is required")
}

func TestHandleUpdate_OwnershipCheckBlocked(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{
			ID:        "abc123",
			CreatedBy: "other@example.com",
		},
	}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "update",
		ID:      "abc123",
		Content: "Updated content that is long enough for tests",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "your own memories")
}

func TestHandleUpdate_StoreError(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{
			ID:        "abc123",
			CreatedBy: "user@example.com",
		},
		updateErr: errors.New("db error"),
	}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "update",
		ID:      "abc123",
		Content: "Updated content that is long enough for tests",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "failed to update memory")
}

// ---------------------------------------------------------------------------
// handleForget tests
// ---------------------------------------------------------------------------

func TestHandleForget_Valid(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{
			ID:        "abc123",
			CreatedBy: "user@example.com",
		},
	}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "forget",
		ID:      "abc123",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, "abc123", data["id"])
	require.Len(t, store.deletedIDs, 1)
	assert.Equal(t, "abc123", store.deletedIDs[0])
}

func TestHandleForget_MissingID(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "forget",
		ID:      "",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "id is required")
}

func TestHandleForget_OwnershipCheckBlocked(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{
			ID:        "abc123",
			CreatedBy: "other@example.com",
		},
	}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "forget",
		ID:      "abc123",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "your own memories")
}

func TestHandleForget_NotFound(t *testing.T) {
	t.Parallel()

	store := &mockStore{getErr: errors.New("not found")}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "forget",
		ID:      "nonexistent",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "memory not found")
}

// ---------------------------------------------------------------------------
// handleList tests
// ---------------------------------------------------------------------------

func TestHandleList_DefaultPersonaScoping(t *testing.T) {
	t.Parallel()

	records := []memstore.Record{
		{ID: "r1", Content: "first record with enough content"},
	}
	store := &mockStore{listRecords: records, listTotal: 1}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{Command: "list"})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, float64(1), data["total"])
	recs, ok := data["records"].([]any)
	require.True(t, ok)
	assert.Len(t, recs, 1)
}

func TestHandleList_WithFilters(t *testing.T) {
	t.Parallel()

	store := &mockStore{listRecords: nil, listTotal: 0}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "admin")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command:         "list",
		FilterDimension: "knowledge",
		FilterCategory:  "correction",
		FilterStatus:    "stale",
		FilterEntityURN: "urn:li:dataset:test",
		Limit:           5,
		Offset:          10,
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, float64(0), data["total"])
	assert.Equal(t, float64(10), data["offset"])
}

func TestHandleList_StoreError(t *testing.T) {
	t.Parallel()

	store := &mockStore{listErr: errors.New("db error")}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{Command: "list"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "failed to list memories")
}

func TestHandleList_Pagination(t *testing.T) {
	t.Parallel()

	store := &mockStore{listRecords: nil, listTotal: 50}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "list",
		Limit:   10,
		Offset:  20,
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, float64(50), data["total"])
	assert.Equal(t, float64(10), data["limit"])
	assert.Equal(t, float64(20), data["offset"])
}

// ---------------------------------------------------------------------------
// handleReviewStale tests
// ---------------------------------------------------------------------------

func TestHandleReviewStale_ReturnsStaleRecords(t *testing.T) {
	t.Parallel()

	staleRecords := []memstore.Record{
		{ID: "s1", Content: "stale record content here", Status: memstore.StatusStale},
		{ID: "s2", Content: "another stale record content", Status: memstore.StatusStale},
	}
	store := &mockStore{listRecords: staleRecords, listTotal: 2}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("admin@example.com", "admin")

	result, _, err := tk.handleManage(ctx, nil, manageInput{Command: "review_stale"})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.Equal(t, float64(2), data["total"])
	assert.Contains(t, data["message"], "2 stale memories found")
}

func TestHandleReviewStale_StoreError(t *testing.T) {
	t.Parallel()

	store := &mockStore{listErr: errors.New("db error")}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("admin@example.com", "admin")

	result, _, err := tk.handleManage(ctx, nil, manageInput{Command: "review_stale"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "failed to list stale memories")
}

// ---------------------------------------------------------------------------
// validateRememberInput tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// verifyOwnership tests
// ---------------------------------------------------------------------------

func TestVerifyOwnership_MatchingUser(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{
			ID:        "abc",
			CreatedBy: "user@example.com",
		},
	}
	ctx := ctxWithPC("user@example.com", "analyst")

	result := verifyOwnership(ctx, store, "abc", "update")
	assert.Nil(t, result, "should allow matching user")
}

func TestVerifyOwnership_NonMatchingUser(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{
			ID:        "abc",
			CreatedBy: "other@example.com",
		},
	}
	ctx := ctxWithPC("user@example.com", "analyst")

	result := verifyOwnership(ctx, store, "abc", "update")
	require.NotNil(t, result, "should block non-matching user")
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "your own memories")
}

func TestVerifyOwnership_EmptyEmail(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{
			ID:        "abc",
			CreatedBy: "other@example.com",
		},
	}
	ctx := ctxWithPC("", "analyst")

	result := verifyOwnership(ctx, store, "abc", "update")
	assert.Nil(t, result, "empty email should skip ownership check")
}

func TestVerifyOwnership_RecordNotFound(t *testing.T) {
	t.Parallel()

	store := &mockStore{getErr: errors.New("not found")}
	ctx := ctxWithPC("user@example.com", "analyst")

	result := verifyOwnership(ctx, store, "nonexistent", "update")
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "memory not found")
}

func TestVerifyOwnership_NilPlatformContext(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult: &memstore.Record{
			ID:        "abc",
			CreatedBy: "someone@example.com",
		},
	}
	// No PlatformContext in this context; GetPlatformContext returns nil.
	// verifyOwnership calls pc.UserEmail which would panic if pc is nil,
	// but the code uses GetPlatformContext which returns nil and then
	// checks pc.UserEmail != "" which would panic.
	// Actually looking at the code, it does pc := middleware.GetPlatformContext(ctx)
	// then pc.UserEmail. If pc is nil this would panic. So this test verifies
	// that the function assumes PlatformContext is always present (which it is
	// in the middleware chain).
	// We test this with an empty-email PlatformContext instead.
	ctx := ctxWithPC("", "analyst")

	result := verifyOwnership(ctx, store, "abc", "archive")
	assert.Nil(t, result, "empty email should allow access")
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestGenerateID(t *testing.T) {
	t.Parallel()

	id, err := generateID()
	require.NoError(t, err)
	assert.Len(t, id, idLength*2, "hex encoding doubles length") // 16 bytes -> 32 hex chars

	// Verify uniqueness (probabilistic but effectively certain).
	id2, err := generateID()
	require.NoError(t, err)
	assert.NotEqual(t, id, id2)
}

func TestJsonResult(t *testing.T) {
	t.Parallel()

	result := jsonResult(map[string]string{"key": "value"})
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, `"key"`)
	assert.Contains(t, tc.Text, `"value"`)
}

func TestErrorResult(t *testing.T) {
	t.Parallel()

	result := errorResult("something went wrong")
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	require.Len(t, result.Content, 1)

	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "something went wrong")
}

func TestHelpResult(t *testing.T) {
	t.Parallel()

	result := helpResult()
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	data := extractJSON(t, result)
	commands, ok := data["commands"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, commands, "remember")
	assert.Contains(t, commands, "update")
	assert.Contains(t, commands, "forget")
	assert.Contains(t, commands, "list")
	assert.Contains(t, commands, "review_stale")
	assert.Contains(t, commands, "review_duplicates")
	assert.Contains(t, commands, "consolidate")
}

// ---------------------------------------------------------------------------
// review_duplicates / consolidate tests (#762)
// ---------------------------------------------------------------------------

// duplicateFinderStore is a mockStore that also implements the optional
// memstore.DuplicateFinder capability.
type duplicateFinderStore struct {
	mockStore
	pairs        []memstore.SimilarPair
	pairsErr     error
	gotCreatedBy string
	gotMinScore  float64
	gotLimit     int
}

func (d *duplicateFinderStore) SimilarActivePairs(_ context.Context, createdBy string, minScore float64, limit int) ([]memstore.SimilarPair, error) {
	d.gotCreatedBy, d.gotMinScore, d.gotLimit = createdBy, minScore, limit
	return d.pairs, d.pairsErr
}

func TestHandleReviewDuplicates_ReturnsPairs(t *testing.T) {
	t.Parallel()

	store := &duplicateFinderStore{pairs: []memstore.SimilarPair{{
		Older: memstore.Record{ID: "dup-old"},
		Newer: memstore.Record{ID: "dup-new"},
		Score: 0.96,
	}}}
	tk := newTestToolkit(store, nil)

	result, _, err := tk.handleManage(ctxWithPC("user@example.com", "analyst"), nil,
		manageInput{Command: "review_duplicates", Limit: 7})
	require.NoError(t, err)
	require.False(t, result.IsError)
	data := extractJSON(t, result)
	assert.Equal(t, float64(1), data["total"])
	assert.Contains(t, data["message"], "consolidate")
	// Memory content is per-user: the listing must be scoped to the caller.
	assert.Equal(t, "user@example.com", store.gotCreatedBy)
	// The listing floor is the capture-time suggest threshold: the backstop
	// exists for pairs below the auto-supersede bar.
	assert.Equal(t, recallSuggestThreshold, store.gotMinScore)
	assert.Equal(t, 7, store.gotLimit)
	pairs, ok := data["pairs"].([]any)
	require.True(t, ok)
	require.Len(t, pairs, 1)
}

func TestHandleReviewDuplicates_RequiresIdentity(t *testing.T) {
	t.Parallel()

	store := &duplicateFinderStore{}
	tk := newTestToolkit(store, nil)
	result, _, err := tk.handleManage(ctxWithPC("", "analyst"), nil,
		manageInput{Command: "review_duplicates"})
	require.NoError(t, err)
	require.True(t, result.IsError, "an anonymous caller must not list duplicate pairs")
	assert.Empty(t, store.gotCreatedBy)
}

func TestHandleReviewDuplicates_StoreWithoutCapability(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	result, _, err := tk.handleManage(ctxWithPC("user@example.com", "analyst"), nil,
		manageInput{Command: "review_duplicates"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "requires the database-backed memory store")
}

func TestHandleReviewDuplicates_FinderError(t *testing.T) {
	t.Parallel()

	store := &duplicateFinderStore{pairsErr: errBoom}
	tk := newTestToolkit(store, nil)
	result, _, err := tk.handleManage(ctxWithPC("user@example.com", "analyst"), nil,
		manageInput{Command: "review_duplicates"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "failed to list duplicate candidates")
}

func TestHandleConsolidate_SupersedesDuplicate(t *testing.T) {
	t.Parallel()

	store := &mockStore{getResult: &memstore.Record{
		ID: "keep", CreatedBy: "user@example.com", Status: memstore.StatusActive,
	}}
	tk := newTestToolkit(store, nil)

	result, _, err := tk.handleManage(ctxWithPC("user@example.com", "analyst"), nil,
		manageInput{Command: "consolidate", ID: "keep", DuplicateID: "dup"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, [][2]string{{"dup", "keep"}}, store.supersedeCalls,
		"the duplicate must be superseded by the kept record")
	data := extractJSON(t, result)
	assert.Equal(t, "keep", data["id"])
	assert.Equal(t, "dup", data["superseded"])
}

// TestHandleConsolidate_KeptRecordMustBeActive guards against silent data
// loss: if the record to keep is itself superseded or archived, consolidating
// would retire the only live copy of the fact behind a dead record.
func TestHandleConsolidate_KeptRecordMustBeActive(t *testing.T) {
	t.Parallel()

	for _, status := range []string{memstore.StatusSuperseded, memstore.StatusArchived, memstore.StatusStale} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			store := &mockStore{getResult: &memstore.Record{
				ID: "keep", CreatedBy: "user@example.com", Status: status,
			}}
			tk := newTestToolkit(store, nil)
			result, _, err := tk.handleManage(ctxWithPC("user@example.com", "analyst"), nil,
				manageInput{Command: "consolidate", ID: "keep", DuplicateID: "dup"})
			require.NoError(t, err)
			require.True(t, result.IsError)
			assert.Empty(t, store.supersedeCalls, "a non-active keeper must not absorb a supersede")
			data := extractJSON(t, result)
			assert.Contains(t, data["error"], "must be active")
		})
	}
}

func TestHandleConsolidate_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input manageInput
	}{
		{"missing id", manageInput{Command: "consolidate", DuplicateID: "dup"}},
		{"missing duplicate_id", manageInput{Command: "consolidate", ID: "keep"}},
		{"identical ids", manageInput{Command: "consolidate", ID: "same", DuplicateID: "same"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := &mockStore{getResult: &memstore.Record{ID: "x", CreatedBy: "user@example.com"}}
			tk := newTestToolkit(store, nil)
			result, _, err := tk.handleManage(ctxWithPC("user@example.com", "analyst"), nil, tt.input)
			require.NoError(t, err)
			assert.True(t, result.IsError)
			assert.Empty(t, store.supersedeCalls)
		})
	}
}

func TestHandleConsolidate_OwnershipEnforced(t *testing.T) {
	t.Parallel()

	store := &mockStore{getResult: &memstore.Record{ID: "keep", CreatedBy: "other@example.com"}}
	tk := newTestToolkit(store, nil)

	result, _, err := tk.handleManage(ctxWithPC("user@example.com", "analyst"), nil,
		manageInput{Command: "consolidate", ID: "keep", DuplicateID: "dup"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Empty(t, store.supersedeCalls, "consolidating another user's records must be blocked")
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "your own memories")
}

func TestHandleConsolidate_SupersedeError(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		getResult:    &memstore.Record{ID: "keep", CreatedBy: "user@example.com", Status: memstore.StatusActive},
		supersedeErr: errBoom,
	}
	tk := newTestToolkit(store, nil)

	result, _, err := tk.handleManage(ctxWithPC("user@example.com", "analyst"), nil,
		manageInput{Command: "consolidate", ID: "keep", DuplicateID: "dup"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "failed to consolidate")
}
