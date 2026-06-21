package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
)

// errBoom is a sentinel error for failure-path tests.
var errBoom = errors.New("boom")

// fakeRecallChecker returns a fixed match for recall-first tests and records the
// embedding it was handed (recall reuses the capture's precomputed vector).
type fakeRecallChecker struct {
	id           string
	score        float64
	err          error
	gotEmbedding []float32
}

func (f *fakeRecallChecker) ExistingMatch(_ context.Context, q RecallQuery) (id string, score float64, err error) {
	f.gotEmbedding = q.Embedding
	return f.id, f.score, f.err
}

// captureToolkitEmbedded builds a toolkit whose embedder produces a non-empty
// vector, so the recall-first path (which is skipped without an embedding) runs.
func captureToolkitEmbedded(t *testing.T) (*Toolkit, *mockStore) {
	t.Helper()
	store := &mockStore{}
	tk := newTestToolkit(store, &mockEmbedder{embedResult: []float32{0.1, 0.2, 0.3}})
	return tk, store
}

// fakeThreadLinker records the link call and reports which ids linked.
type fakeThreadLinker struct {
	linked   []string
	err      error
	gotID    string
	gotThird []string
}

func (f *fakeThreadLinker) LinkInsight(_ context.Context, threadIDs []string, insightID, _, _ string) ([]string, error) {
	f.gotID = insightID
	f.gotThird = threadIDs
	return f.linked, f.err
}

func captureToolkit(t *testing.T) (*Toolkit, *mockStore) {
	t.Helper()
	store := &mockStore{}
	tk := newTestToolkit(store, nil)
	return tk, store
}

func TestMemoryCapture_LiveClassWritesActiveMemory(t *testing.T) {
	for _, sc := range []struct {
		typ, dim string
	}{
		{memstore.SinkPersonalPreference, memstore.DimensionPreference},
		{memstore.SinkEpisodicEvent, memstore.DimensionEvent},
	} {
		t.Run(sc.typ, func(t *testing.T) {
			tk, store := captureToolkit(t)
			res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
				Type: sc.typ, Content: "I prefer CTEs over nested subqueries.",
			})
			require.NoError(t, err)
			require.False(t, res.IsError)
			require.Len(t, store.insertedRecords, 1)
			rec := store.insertedRecords[0]
			assert.Equal(t, sc.typ, rec.SinkClass)
			assert.Equal(t, sc.dim, rec.Dimension)
			assert.Equal(t, memstore.StatusActive, rec.Status)
			assert.Equal(t, "a@example.com", rec.CreatedBy)
			// live classes carry no pending-insight overlay
			_, pending := rec.Metadata[memstore.MetaKeyInsightStatus]
			assert.False(t, pending)
		})
	}
}

func TestMemoryCapture_ReviewedClassWritesPendingInsight(t *testing.T) {
	tk, store := captureToolkit(t)
	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type:       memstore.SinkSchemaEntity,
		Content:    "The amount column excludes returns.",
		EntityURNs: []string{"urn:li:dataset:orders"},
		SuggestedActions: []suggestedActionInput{
			{ActionType: "update_description", Target: "amount", Detail: "Gross margin before returns"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, store.insertedRecords, 1)
	rec := store.insertedRecords[0]
	assert.Equal(t, memstore.SinkSchemaEntity, rec.SinkClass)
	assert.Equal(t, memstore.DimensionKnowledge, rec.Dimension)
	// AC: pending insight overlay so apply_knowledge surfaces it
	assert.Equal(t, memstore.InsightStatusPending, rec.Metadata[memstore.MetaKeyInsightStatus])
	// AC3: catalog-proposal payload preserved
	actions, ok := rec.Metadata[memstore.MetaKeySuggestedActions].([]suggestedActionInput)
	require.True(t, ok)
	assert.Len(t, actions, 1)
	assert.Equal(t, "update_description", actions[0].ActionType)
}

func TestMemoryCapture_RecallFirstSupersedes(t *testing.T) {
	tk, store := captureToolkitEmbedded(t)
	rc := &fakeRecallChecker{id: "old-mem", score: 0.95}
	tk.SetRecallChecker(rc)

	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkPersonalPreference, Content: "I prefer CTEs.",
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, store.insertedRecords, 1)
	newID := store.insertedRecords[0].ID
	assert.Equal(t, "old-mem", store.supersededOld)
	assert.Equal(t, newID, store.supersededNew)
	// Recall must reuse the precomputed embedding, not re-embed.
	assert.NotEmpty(t, rc.gotEmbedding, "recall must receive the capture's embedding")
}

func TestMemoryCapture_RecallNoMatchDoesNotSupersede(t *testing.T) {
	tk, store := captureToolkitEmbedded(t)
	// ExistingMatch applies the threshold/URN gate itself; "" means no qualifying match.
	tk.SetRecallChecker(&fakeRecallChecker{id: "", score: 0})

	_, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "Stores close at 9pm.",
	})
	require.NoError(t, err)
	assert.Empty(t, store.supersededOld, "no qualifying match must not supersede")
}

func TestMemoryCapture_NoEmbeddingSkipsRecall(t *testing.T) {
	// New swaps a nil embedder for a noop provider (IsConfigured == false), so no
	// embedding is computed and recall must be skipped.
	store := &mockStore{}
	tk, err := New("test", store, nil)
	require.NoError(t, err)
	rc := &fakeRecallChecker{id: "old-mem", score: 0.99}
	tk.SetRecallChecker(rc)

	_, _, err = tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "Stores close at 9pm.",
	})
	require.NoError(t, err)
	assert.Empty(t, store.supersededOld, "recall must be skipped when there is no embedding")
	assert.Nil(t, rc.gotEmbedding, "recall checker must not be consulted without an embedding")
}

func TestMemoryCapture_LinksThreadsForReviewedClass(t *testing.T) {
	tk, _ := captureToolkit(t)
	tl := &fakeThreadLinker{linked: []string{"th-1"}}
	tk.SetThreadLinker(tl)

	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "Churn = cancels / active.", ThreadIDs: []string{"th-1", "th-2"},
	})
	require.NoError(t, err)
	out := extractJSON(t, res)
	assert.Equal(t, float64(1), out["linked_thread_count"])
	assert.Equal(t, []string{"th-1", "th-2"}, tl.gotThird)
	// th-2 matched nothing -> reported unlinked
	assert.Contains(t, out, "unlinked_thread_ids")
}

func TestMemoryCapture_Validation(t *testing.T) {
	tk, _ := captureToolkit(t)
	ctx := ctxWithPC("a@example.com", "analyst")

	const ok = "valid content here ok"
	tooMany := make([]suggestedActionInput, maxSuggestedActions+1)
	for i := range tooMany {
		tooMany[i] = suggestedActionInput{ActionType: "add_tag", Detail: "pii"}
	}
	tests := []struct {
		name  string
		input memoryCaptureInput
	}{
		{"invalid type", memoryCaptureInput{Type: "bogus", Content: ok}},
		{"empty content", memoryCaptureInput{Type: memstore.SinkBusinessKnowledge, Content: "  "}},
		{"bad confidence", memoryCaptureInput{Type: memstore.SinkBusinessKnowledge, Content: ok, Confidence: "absolutely"}},
		{"bad source", memoryCaptureInput{Type: memstore.SinkBusinessKnowledge, Content: ok, Source: "made_up"}},
		{"bad category", memoryCaptureInput{Type: memstore.SinkBusinessKnowledge, Content: ok, Category: "nonsense"}},
		{"bad action type", memoryCaptureInput{Type: memstore.SinkSchemaEntity, Content: ok, SuggestedActions: []suggestedActionInput{{ActionType: "drop_table"}}}},
		{"curated query missing sql", memoryCaptureInput{Type: memstore.SinkSchemaEntity, Content: ok, SuggestedActions: []suggestedActionInput{{ActionType: "add_curated_query", Detail: "q"}}}},
		{"too many actions", memoryCaptureInput{Type: memstore.SinkSchemaEntity, Content: ok, SuggestedActions: tooMany}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, _, err := tk.handleMemoryCapture(ctx, nil, tt.input)
			require.NoError(t, err)
			assert.True(t, res.IsError, "expected validation error for %s", tt.name)
		})
	}
}

func TestMemoryCapture_PersistsCategoryAndRelatedColumns(t *testing.T) {
	tk, store := captureToolkit(t)
	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type:           memstore.SinkSchemaEntity,
		Content:        "The amount column excludes returns.",
		Category:       "data_quality",
		RelatedColumns: []memstore.RelatedColumn{{Column: "amount", Relevance: "direct"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, store.insertedRecords, 1)
	rec := store.insertedRecords[0]
	assert.Equal(t, "data_quality", rec.Category)
	require.Len(t, rec.RelatedColumns, 1)
	assert.Equal(t, "amount", rec.RelatedColumns[0].Column)
}

func TestMemoryCapture_RequiresIdentity(t *testing.T) {
	tk, _ := captureToolkit(t)
	// No platform context -> anonymous -> rejected.
	res, _, err := tk.handleMemoryCapture(context.Background(), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "valid content here ok",
	})
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestMemoryCapture_StampsEmbedding(t *testing.T) {
	store := &mockStore{}
	tk := newTestToolkit(store, &mockEmbedder{embedResult: []float32{0.1, 0.2, 0.3}, model: "nomic"})
	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "Churn excludes trials.",
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, store.insertedRecords, 1)
	rec := store.insertedRecords[0]
	assert.Equal(t, []float32{0.1, 0.2, 0.3}, rec.Embedding)
	assert.Equal(t, "nomic", rec.EmbeddingModel)
	assert.NotEmpty(t, rec.EmbeddingTextHash)
}

func TestMemoryCapture_RecallErrorToleratedNoSupersede(t *testing.T) {
	tk, store := captureToolkitEmbedded(t)
	tk.SetRecallChecker(&fakeRecallChecker{err: errBoom})

	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "Stores close at 9pm.",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "a recall-check error must not fail the capture")
	assert.Empty(t, store.supersededOld)
}

func TestMemoryCapture_SupersedeErrorTolerated(t *testing.T) {
	store := &mockStore{supersedeErr: errBoom}
	tk := newTestToolkit(store, &mockEmbedder{embedResult: []float32{0.1, 0.2, 0.3}})
	tk.SetRecallChecker(&fakeRecallChecker{id: "old-mem", score: 0.95})

	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "Stores close at 9pm.",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "a supersede failure must not fail the capture")
	out := extractJSON(t, res)
	assert.NotContains(t, out, "superseded", "a failed supersede must not be claimed")
}

func TestMemoryCapture_ThreadIDsWithoutLinkerReportedUnlinked(t *testing.T) {
	tk, _ := captureToolkit(t) // no thread linker wired
	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "Churn = cancels / active.", ThreadIDs: []string{"th-1"},
	})
	require.NoError(t, err)
	out := extractJSON(t, res)
	assert.Equal(t, []any{"th-1"}, out["unlinked_thread_ids"])
}

func TestMemoryCapture_StoreInsertError(t *testing.T) {
	store := &mockStore{insertErr: errBoom}
	tk := newTestToolkit(store, nil)
	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "valid content here ok",
	})
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestMemoryCapture_EmbedErrorTolerated(t *testing.T) {
	store := &mockStore{}
	tk := newTestToolkit(store, &mockEmbedder{embedErr: errBoom})
	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "Churn excludes trials.",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "an embed failure must not fail the capture")
	require.Len(t, store.insertedRecords, 1)
	assert.Empty(t, store.insertedRecords[0].Embedding)
}

func TestMemoryCapture_ThreadLinkerErrorReportsUnlinked(t *testing.T) {
	tk, _ := captureToolkit(t)
	tk.SetThreadLinker(&fakeThreadLinker{err: errBoom})
	res, _, err := tk.handleMemoryCapture(ctxWithPC("a@example.com", "analyst"), nil, memoryCaptureInput{
		Type: memstore.SinkBusinessKnowledge, Content: "Churn = cancels / active.", ThreadIDs: []string{"th-1"},
	})
	require.NoError(t, err)
	out := extractJSON(t, res)
	assert.Equal(t, []any{"th-1"}, out["unlinked_thread_ids"])
}

func TestMemoryCapture_RegisteredInTools(t *testing.T) {
	tk, _ := captureToolkit(t)
	assert.Contains(t, tk.Tools(), memoryCaptureToolName)
}

func TestToolkit_RegisterTools(t *testing.T) {
	tk, _ := captureToolkit(t)
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	tk.RegisterTools(srv) // registers memory_manage + memory_capture without panicking
	tools := tk.Tools()
	assert.Contains(t, tools, memoryCaptureToolName)
	assert.Contains(t, tools, manageToolName)
}
