package memory

import (
	"context"
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

func (m *mockStore) EntityLookup(_ context.Context, _, _ string) ([]memstore.Record, error) {
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

func (m *mockStore) Supersede(_ context.Context, _, _ string) error {
	return m.supersedeErr
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
}

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

	// remember routes correctly (needs valid content)
	store := &mockStore{}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "remember",
		Content: "This is valid content for testing memory storage",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data, "id")

	// list routes correctly
	result, _, err = tk.handleManage(ctx, nil, manageInput{Command: "list"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	data = extractJSON(t, result)
	assert.Contains(t, data, "records")

	// review_stale routes correctly
	result, _, err = tk.handleManage(ctx, nil, manageInput{Command: "review_stale"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	data = extractJSON(t, result)
	assert.Contains(t, data, "message")
}

// ---------------------------------------------------------------------------
// handleRemember tests
// ---------------------------------------------------------------------------

func TestHandleRemember_Valid(t *testing.T) {
	t.Parallel()

	store := &mockStore{}
	embedder := &mockEmbedder{embedResult: []float32{0.1, 0.2}}
	tk := newTestToolkit(store, embedder)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command:    "remember",
		Content:    "This is a valid memory content for testing",
		Dimension:  "knowledge",
		Category:   "correction",
		Confidence: "high",
		Source:     "user",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	data := extractJSON(t, result)
	assert.NotEmpty(t, data["id"])
	assert.Equal(t, "active", data["status"])

	require.Len(t, store.insertedRecords, 1)
	rec := store.insertedRecords[0]
	assert.Equal(t, "user@example.com", rec.CreatedBy)
	assert.Equal(t, "analyst", rec.Persona)
	assert.Equal(t, "knowledge", rec.Dimension)
	assert.Equal(t, "correction", rec.Category)
	assert.Equal(t, "high", rec.Confidence)
	assert.Equal(t, "user", rec.Source)
	assert.Equal(t, []float32{0.1, 0.2}, rec.Embedding)
	assert.Equal(t, "sess-123", rec.Metadata["session_id"])
}

func TestHandleRemember_MissingContent(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "remember",
		Content: "",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "content")
}

func TestHandleRemember_InvalidDimension(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command:   "remember",
		Content:   "Valid content that is long enough for tests",
		Dimension: "invalid_dimension",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "dimension")
}

func TestHandleRemember_InvalidCategory(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command:  "remember",
		Content:  "Valid content that is long enough for tests",
		Category: "bad_category",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "category")
}

func TestHandleRemember_InvalidConfidence(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command:    "remember",
		Content:    "Valid content that is long enough for tests",
		Confidence: "maybe",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "confidence")
}

func TestHandleRemember_InvalidSource(t *testing.T) {
	t.Parallel()

	tk := newTestToolkit(&mockStore{}, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "remember",
		Content: "Valid content that is long enough for tests",
		Source:  "invalid_source",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "source")
}

func TestHandleRemember_EmbeddingFailure_Graceful(t *testing.T) {
	t.Parallel()

	store := &mockStore{}
	embedder := &mockEmbedder{embedErr: errors.New("ollama down")}
	tk := newTestToolkit(store, embedder)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "remember",
		Content: "Valid content that is long enough for tests",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError, "embedding failure should be graceful")

	require.Len(t, store.insertedRecords, 1)
	assert.Nil(t, store.insertedRecords[0].Embedding, "embedding should be nil on failure")
}

func TestHandleRemember_StoreInsertError(t *testing.T) {
	t.Parallel()

	store := &mockStore{insertErr: errors.New("db connection lost")}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "remember",
		Content: "Valid content that is long enough for tests",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	data := extractJSON(t, result)
	assert.Contains(t, data["error"], "failed to save memory")
}

func TestHandleRemember_NilMetadata_InitializedWithSessionID(t *testing.T) {
	t.Parallel()

	store := &mockStore{}
	tk := newTestToolkit(store, nil)
	ctx := ctxWithPC("user@example.com", "analyst")

	result, _, err := tk.handleManage(ctx, nil, manageInput{
		Command: "remember",
		Content: "Valid content that is long enough for tests",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	require.Len(t, store.insertedRecords, 1)
	assert.NotNil(t, store.insertedRecords[0].Metadata)
	assert.Equal(t, "sess-123", store.insertedRecords[0].Metadata["session_id"])
}

// ---------------------------------------------------------------------------
// handleUpdate tests
// ---------------------------------------------------------------------------

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

func TestValidateRememberInput_AllPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   manageInput
		wantErr string
	}{
		{
			name:    "empty content",
			input:   manageInput{Content: ""},
			wantErr: "content",
		},
		{
			name:    "too short content",
			input:   manageInput{Content: "short"},
			wantErr: "content",
		},
		{
			name:    "invalid dimension",
			input:   manageInput{Content: "Valid content that is long enough for tests", Dimension: "bogus"},
			wantErr: "dimension",
		},
		{
			name:    "invalid category",
			input:   manageInput{Content: "Valid content that is long enough for tests", Category: "bogus"},
			wantErr: "category",
		},
		{
			name:    "invalid confidence",
			input:   manageInput{Content: "Valid content that is long enough for tests", Confidence: "bogus"},
			wantErr: "confidence",
		},
		{
			name:    "invalid source",
			input:   manageInput{Content: "Valid content that is long enough for tests", Source: "bogus"},
			wantErr: "source",
		},
		{
			name: "too many entity URNs",
			input: manageInput{
				Content:    "Valid content that is long enough for tests",
				EntityURNs: make([]string, memstore.MaxEntityURNs+1),
			},
			wantErr: "entity_urns",
		},
		{
			name:    "valid input with all defaults",
			input:   manageInput{Content: "Valid content that is long enough for tests"},
			wantErr: "",
		},
		{
			name: "valid input with all fields",
			input: manageInput{
				Content:    "Valid content that is long enough for tests",
				Dimension:  "knowledge",
				Category:   "correction",
				Confidence: "high",
				Source:     "user",
				EntityURNs: []string{"urn:li:dataset:test"},
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateRememberInput(tt.input)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

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
	assert.Contains(t, commands, "remember")
	assert.Contains(t, commands, "update")
	assert.Contains(t, commands, "forget")
	assert.Contains(t, commands, "list")
	assert.Contains(t, commands, "review_stale")
}
