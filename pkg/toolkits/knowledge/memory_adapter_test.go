package knowledge

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

// mockMemoryStore implements memory.Store for testing.
type mockMemoryStore struct {
	insertCalled    bool
	insertedRecord  memory.Record
	insertErr       error
	getCalled       bool
	getID           string
	getRecord       *memory.Record
	getErr          error
	updateCalled    bool
	updateID        string
	updateData      memory.RecordUpdate
	updateErr       error
	listCalled      bool
	listFilter      memory.Filter
	listRecords     []memory.Record
	listTotal       int
	listErr         error
	supersedeCalled bool
	supersedeOldID  string
	supersedeNewID  string
	supersedeErr    error
	supersedeCalls  []struct{ OldID, NewID string }
}

func (m *mockMemoryStore) Insert(_ context.Context, record memory.Record) error {
	m.insertCalled = true
	m.insertedRecord = record
	return m.insertErr
}

func (m *mockMemoryStore) Get(_ context.Context, id string) (*memory.Record, error) {
	m.getCalled = true
	m.getID = id
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.getRecord, nil
}

func (m *mockMemoryStore) Update(_ context.Context, id string, updates memory.RecordUpdate) error {
	m.updateCalled = true
	m.updateID = id
	m.updateData = updates
	return m.updateErr
}

func (*mockMemoryStore) Delete(_ context.Context, _ string) error { return nil }

func (m *mockMemoryStore) List(_ context.Context, filter memory.Filter) ([]memory.Record, int, error) {
	m.listCalled = true
	m.listFilter = filter
	return m.listRecords, m.listTotal, m.listErr
}

func (*mockMemoryStore) VectorSearch(_ context.Context, _ memory.VectorQuery) ([]memory.ScoredRecord, error) {
	return nil, nil //nolint:nilnil // mock returns nil for both
}

func (*mockMemoryStore) EntityLookup(_ context.Context, _, _ string) ([]memory.Record, error) {
	return nil, nil //nolint:nilnil // mock returns nil for both
}

func (*mockMemoryStore) MarkStale(_ context.Context, _ []string, _ string) error { return nil }

func (*mockMemoryStore) MarkVerified(_ context.Context, _ []string) error { return nil }

func (m *mockMemoryStore) Supersede(_ context.Context, oldID, newID string) error {
	m.supersedeCalled = true
	m.supersedeOldID = oldID
	m.supersedeNewID = newID
	m.supersedeCalls = append(m.supersedeCalls, struct{ OldID, NewID string }{oldID, newID})
	return m.supersedeErr
}

var _ memory.Store = (*mockMemoryStore)(nil)

func TestMemoryInsightAdapter_Insert(t *testing.T) {
	store := &mockMemoryStore{}
	adapter := NewMemoryInsightAdapter(store)

	insight := Insight{
		ID:          "ins-001",
		CapturedBy:  "user@example.com",
		Persona:     "analyst",
		Source:      "user",
		Category:    "business_context",
		InsightText: "Revenue is calculated differently for Q4",
		Confidence:  "high",
		EntityURNs:  []string{"urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)"},
		RelatedColumns: []RelatedColumn{
			{URN: "urn:li:dataset:abc", Column: "revenue", Relevance: "primary"},
		},
		SuggestedActions: []SuggestedAction{
			{ActionType: "update_description", Target: "revenue", Detail: "Add Q4 note"},
		},
		SessionID:    "sess-abc",
		Status:       StatusPending,
		ReviewedBy:   "reviewer@example.com",
		ReviewNotes:  "Looks good",
		AppliedBy:    "admin@example.com",
		ChangesetRef: "cs-123",
	}

	err := adapter.Insert(context.Background(), insight)
	require.NoError(t, err)
	assert.True(t, store.insertCalled)

	rec := store.insertedRecord
	assert.Equal(t, "ins-001", rec.ID)
	assert.Equal(t, "user@example.com", rec.CreatedBy)
	assert.Equal(t, "analyst", rec.Persona)
	assert.Equal(t, memory.DimensionKnowledge, rec.Dimension)
	assert.Equal(t, "Revenue is calculated differently for Q4", rec.Content)
	assert.Equal(t, "business_context", rec.Category)
	assert.Equal(t, "high", rec.Confidence)
	assert.Equal(t, "user", rec.Source)
	assert.Equal(t, insight.EntityURNs, rec.EntityURNs)
	assert.Len(t, rec.RelatedColumns, 1)
	assert.Equal(t, "urn:li:dataset:abc", rec.RelatedColumns[0].URN)
	assert.Equal(t, "revenue", rec.RelatedColumns[0].Column)
	assert.Equal(t, "primary", rec.RelatedColumns[0].Relevance)

	// Metadata fields
	assert.Equal(t, "sess-abc", rec.Metadata["session_id"])
	assert.Equal(t, "reviewer@example.com", rec.Metadata["reviewed_by"])
	assert.Equal(t, "Looks good", rec.Metadata["review_notes"])
	assert.Equal(t, "admin@example.com", rec.Metadata["applied_by"])
	assert.Equal(t, "cs-123", rec.Metadata["changeset_ref"])
	assert.NotNil(t, rec.Metadata["suggested_actions"])

	// Status mapping: pending -> active
	assert.Equal(t, memory.StatusActive, rec.Status)
}

func TestMemoryInsightAdapter_Insert_Error(t *testing.T) {
	store := &mockMemoryStore{insertErr: fmt.Errorf("db error")}
	adapter := NewMemoryInsightAdapter(store)

	err := adapter.Insert(context.Background(), Insight{ID: "ins-001"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inserting insight record")
}

func TestMemoryInsightAdapter_Get(t *testing.T) {
	now := time.Now()
	store := &mockMemoryStore{
		getRecord: &memory.Record{
			ID:         "ins-002",
			CreatedAt:  now,
			CreatedBy:  "user@example.com",
			Persona:    "admin",
			Content:    "This table is deprecated",
			Category:   "data_quality",
			Confidence: "medium",
			Source:     "agent_discovery",
			EntityURNs: []string{"urn:li:dataset:abc"},
			RelatedColumns: []memory.RelatedColumn{
				{URN: "urn:li:dataset:abc", Column: "col1", Relevance: "secondary"},
			},
			Status: memory.StatusActive,
			Metadata: map[string]any{
				"session_id":    "sess-xyz",
				"reviewed_by":   "admin@example.com",
				"review_notes":  "Confirmed",
				"applied_by":    "ops@example.com",
				"changeset_ref": "cs-456",
				"suggested_actions": []map[string]any{
					{"action_type": "add_tag", "target": "deprecated", "detail": "Mark deprecated"},
				},
			},
		},
	}
	adapter := NewMemoryInsightAdapter(store)

	insight, err := adapter.Get(context.Background(), "ins-002")
	require.NoError(t, err)
	require.NotNil(t, insight)

	assert.Equal(t, "ins-002", insight.ID)
	assert.Equal(t, now, insight.CreatedAt)
	assert.Equal(t, "user@example.com", insight.CapturedBy)
	assert.Equal(t, "admin", insight.Persona)
	assert.Equal(t, "This table is deprecated", insight.InsightText)
	assert.Equal(t, "data_quality", insight.Category)
	assert.Equal(t, "medium", insight.Confidence)
	assert.Equal(t, "agent_discovery", insight.Source)
	assert.Equal(t, []string{"urn:li:dataset:abc"}, insight.EntityURNs)
	assert.Len(t, insight.RelatedColumns, 1)
	assert.Equal(t, "col1", insight.RelatedColumns[0].Column)

	// Metadata extraction
	assert.Equal(t, "sess-xyz", insight.SessionID)
	assert.Equal(t, "admin@example.com", insight.ReviewedBy)
	assert.Equal(t, "Confirmed", insight.ReviewNotes)
	assert.Equal(t, "ops@example.com", insight.AppliedBy)
	assert.Equal(t, "cs-456", insight.ChangesetRef)

	// Status: active -> pending (default mapping)
	assert.Equal(t, StatusPending, insight.Status)
}

func TestMemoryInsightAdapter_Get_NotFound(t *testing.T) {
	store := &mockMemoryStore{getErr: fmt.Errorf("record not found")}
	adapter := NewMemoryInsightAdapter(store)

	insight, err := adapter.Get(context.Background(), "missing")
	require.Error(t, err)
	assert.Nil(t, insight)
	assert.Contains(t, err.Error(), "getting insight record")
}

func TestMemoryInsightAdapter_List_FilterMapping(t *testing.T) {
	since := time.Now().Add(-24 * time.Hour)
	until := time.Now()
	store := &mockMemoryStore{
		listRecords: []memory.Record{
			{
				ID:         "r1",
				Category:   "correction",
				Confidence: "high",
				Content:    "Some insight",
				Status:     memory.StatusActive,
				Metadata:   map[string]any{},
			},
		},
		listTotal: 1,
	}
	adapter := NewMemoryInsightAdapter(store)

	filter := InsightFilter{
		Status:     StatusPending,
		Category:   "correction",
		EntityURN:  "urn:li:dataset:abc",
		CapturedBy: "user@example.com",
		Source:     "user",
		Since:      &since,
		Until:      &until,
		Limit:      10,
		Offset:     5,
	}

	insights, total, err := adapter.List(context.Background(), filter)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Len(t, insights, 1)

	// Verify filter mapping
	mf := store.listFilter
	assert.Equal(t, "correction", mf.Category)
	assert.Equal(t, "urn:li:dataset:abc", mf.EntityURN)
	assert.Equal(t, "user@example.com", mf.CreatedBy)
	assert.Equal(t, "user", mf.Source)
	assert.Equal(t, &since, mf.Since)
	assert.Equal(t, &until, mf.Until)
	assert.Equal(t, 10, mf.Limit)
	assert.Equal(t, 5, mf.Offset)
	assert.Equal(t, memory.StatusActive, mf.Status) // pending -> active
}

func TestMemoryInsightAdapter_List_ConfidencePostFiltering(t *testing.T) {
	store := &mockMemoryStore{
		listRecords: []memory.Record{
			{ID: "r1", Confidence: "high", Content: "a", Status: memory.StatusActive, Metadata: map[string]any{}},
			{ID: "r2", Confidence: "low", Content: "b", Status: memory.StatusActive, Metadata: map[string]any{}},
			{ID: "r3", Confidence: "high", Content: "c", Status: memory.StatusActive, Metadata: map[string]any{}},
		},
		listTotal: 3,
	}
	adapter := NewMemoryInsightAdapter(store)

	insights, total, err := adapter.List(context.Background(), InsightFilter{Confidence: "high"})
	require.NoError(t, err)
	assert.Equal(t, 3, total) // total from store (pre-filter)
	assert.Len(t, insights, 2)
	for _, ins := range insights {
		assert.Equal(t, "high", ins.Confidence)
	}
}

func TestMemoryInsightAdapter_List_Error(t *testing.T) {
	store := &mockMemoryStore{listErr: fmt.Errorf("db error")}
	adapter := NewMemoryInsightAdapter(store)

	_, _, err := adapter.List(context.Background(), InsightFilter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing insight records")
}

func TestMemoryInsightAdapter_UpdateStatus(t *testing.T) {
	store := &mockMemoryStore{}
	adapter := NewMemoryInsightAdapter(store)

	err := adapter.UpdateStatus(context.Background(), "ins-001", StatusApproved, "reviewer@example.com", "LGTM")
	require.NoError(t, err)
	assert.True(t, store.updateCalled)
	assert.Equal(t, "ins-001", store.updateID)
	assert.Equal(t, "reviewer@example.com", store.updateData.Metadata["reviewed_by"])
	assert.Equal(t, "LGTM", store.updateData.Metadata["review_notes"])
	assert.Equal(t, StatusApproved, store.updateData.Metadata["insight_status"])
}

func TestMemoryInsightAdapter_UpdateStatus_Error(t *testing.T) {
	store := &mockMemoryStore{updateErr: fmt.Errorf("update failed")}
	adapter := NewMemoryInsightAdapter(store)

	err := adapter.UpdateStatus(context.Background(), "ins-001", StatusApproved, "rev", "notes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "updating insight status")
}

func TestMemoryInsightAdapter_Update(t *testing.T) {
	store := &mockMemoryStore{}
	adapter := NewMemoryInsightAdapter(store)

	err := adapter.Update(context.Background(), "ins-001", InsightUpdate{
		InsightText: "Updated text here",
		Category:    "correction",
		Confidence:  "low",
	})
	require.NoError(t, err)
	assert.True(t, store.updateCalled)
	assert.Equal(t, "ins-001", store.updateID)
	assert.Equal(t, "Updated text here", store.updateData.Content)
	assert.Equal(t, "correction", store.updateData.Category)
	assert.Equal(t, "low", store.updateData.Confidence)
}

func TestMemoryInsightAdapter_Update_Error(t *testing.T) {
	store := &mockMemoryStore{updateErr: fmt.Errorf("update failed")}
	adapter := NewMemoryInsightAdapter(store)

	err := adapter.Update(context.Background(), "ins-001", InsightUpdate{InsightText: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "updating insight")
}

func TestMemoryInsightAdapter_Stats(t *testing.T) {
	store := &mockMemoryStore{
		listRecords: []memory.Record{
			{ID: "r1", Category: "correction", Confidence: "high", Status: memory.StatusActive},
			{ID: "r2", Category: "correction", Confidence: "low", Status: memory.StatusActive},
			{ID: "r3", Category: "data_quality", Confidence: "high", Status: memory.StatusArchived},
		},
		listTotal: 3,
	}
	adapter := NewMemoryInsightAdapter(store)

	stats, err := adapter.Stats(context.Background(), InsightFilter{})
	require.NoError(t, err)
	require.NotNil(t, stats)

	assert.Equal(t, 3, stats.TotalPending)
	assert.Equal(t, 2, stats.ByCategory["correction"])
	assert.Equal(t, 1, stats.ByCategory["data_quality"])
	assert.Equal(t, 2, stats.ByConfidence["high"])
	assert.Equal(t, 1, stats.ByConfidence["low"])
	assert.Equal(t, 2, stats.ByStatus[memory.StatusActive])
	assert.Equal(t, 1, stats.ByStatus[memory.StatusArchived])
}

func TestMemoryInsightAdapter_Stats_Error(t *testing.T) {
	store := &mockMemoryStore{listErr: fmt.Errorf("db error")}
	adapter := NewMemoryInsightAdapter(store)

	_, err := adapter.Stats(context.Background(), InsightFilter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing records for insight stats")
}

func TestMemoryInsightAdapter_MarkApplied(t *testing.T) {
	store := &mockMemoryStore{}
	adapter := NewMemoryInsightAdapter(store)

	err := adapter.MarkApplied(context.Background(), "ins-001", "admin@example.com", "cs-789")
	require.NoError(t, err)
	assert.True(t, store.updateCalled)
	assert.Equal(t, "ins-001", store.updateID)
	assert.Equal(t, "admin@example.com", store.updateData.Metadata["applied_by"])
	assert.Equal(t, "cs-789", store.updateData.Metadata["changeset_ref"])
}

func TestMemoryInsightAdapter_MarkApplied_Error(t *testing.T) {
	store := &mockMemoryStore{updateErr: fmt.Errorf("update failed")}
	adapter := NewMemoryInsightAdapter(store)

	err := adapter.MarkApplied(context.Background(), "ins-001", "admin", "cs")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marking insight applied")
}

func TestMemoryInsightAdapter_Supersede(t *testing.T) {
	store := &mockMemoryStore{
		listRecords: []memory.Record{
			{ID: "old-1"},
			{ID: "old-2"},
			{ID: "new-1"}, // This is the excludeID
		},
		listTotal: 3,
	}
	adapter := NewMemoryInsightAdapter(store)

	count, err := adapter.Supersede(context.Background(), "urn:li:dataset:abc", "new-1")
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.True(t, store.supersedeCalled)

	// Verify the filter used
	assert.Equal(t, "urn:li:dataset:abc", store.listFilter.EntityURN)
	assert.Equal(t, memory.StatusActive, store.listFilter.Status)
	assert.Equal(t, memory.MaxLimit, store.listFilter.Limit)

	// Verify supersede was called for old-1 and old-2 but not new-1
	assert.Len(t, store.supersedeCalls, 2)
	assert.Equal(t, "old-1", store.supersedeCalls[0].OldID)
	assert.Equal(t, "new-1", store.supersedeCalls[0].NewID)
	assert.Equal(t, "old-2", store.supersedeCalls[1].OldID)
	assert.Equal(t, "new-1", store.supersedeCalls[1].NewID)
}

func TestMemoryInsightAdapter_Supersede_ListError(t *testing.T) {
	store := &mockMemoryStore{listErr: fmt.Errorf("list failed")}
	adapter := NewMemoryInsightAdapter(store)

	_, err := adapter.Supersede(context.Background(), "urn:li:dataset:abc", "new-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing records for supersede")
}

func TestMemoryInsightAdapter_Supersede_SupersedeError(t *testing.T) {
	store := &mockMemoryStore{
		listRecords:  []memory.Record{{ID: "old-1"}},
		listTotal:    1,
		supersedeErr: fmt.Errorf("supersede failed"),
	}
	adapter := NewMemoryInsightAdapter(store)

	count, err := adapter.Supersede(context.Background(), "urn:li:dataset:abc", "new-1")
	require.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "superseding old-1")
}

func TestInsightToRecord_FieldMapping(t *testing.T) {
	insight := Insight{
		ID:          "ins-100",
		CapturedBy:  "user@example.com",
		Persona:     "analyst",
		Source:      "user",
		Category:    "business_context",
		InsightText: "Important business rule about revenue",
		Confidence:  "high",
		EntityURNs:  []string{"urn:li:dataset:abc", "urn:li:dataset:def"},
		RelatedColumns: []RelatedColumn{
			{URN: "urn:li:dataset:abc", Column: "revenue", Relevance: "primary"},
			{URN: "urn:li:dataset:def", Column: "cost", Relevance: "secondary"},
		},
		SuggestedActions: []SuggestedAction{
			{ActionType: "add_tag", Target: "revenue", Detail: "Tag as KPI"},
		},
		SessionID:    "sess-001",
		Status:       StatusApproved,
		ReviewedBy:   "reviewer@example.com",
		ReviewNotes:  "Approved",
		AppliedBy:    "ops@example.com",
		ChangesetRef: "cs-001",
	}

	record := insightToRecord(insight)

	assert.Equal(t, "ins-100", record.ID)
	assert.Equal(t, "user@example.com", record.CreatedBy)
	assert.Equal(t, "analyst", record.Persona)
	assert.Equal(t, memory.DimensionKnowledge, record.Dimension)
	assert.Equal(t, "Important business rule about revenue", record.Content)
	assert.Equal(t, "business_context", record.Category)
	assert.Equal(t, "high", record.Confidence)
	assert.Equal(t, "user", record.Source)
	assert.Equal(t, []string{"urn:li:dataset:abc", "urn:li:dataset:def"}, record.EntityURNs)
	assert.Len(t, record.RelatedColumns, 2)
	assert.Equal(t, "revenue", record.RelatedColumns[0].Column)
	assert.Equal(t, "cost", record.RelatedColumns[1].Column)
	assert.Equal(t, memory.StatusActive, record.Status) // approved -> active

	// Metadata
	assert.Equal(t, "sess-001", record.Metadata["session_id"])
	assert.Equal(t, "reviewer@example.com", record.Metadata["reviewed_by"])
	assert.Equal(t, "Approved", record.Metadata["review_notes"])
	assert.Equal(t, "ops@example.com", record.Metadata["applied_by"])
	assert.Equal(t, "cs-001", record.Metadata["changeset_ref"])
	assert.NotNil(t, record.Metadata["suggested_actions"])
}

func TestInsightToRecord_EmptyOptionalFields(t *testing.T) {
	insight := Insight{
		ID:          "ins-200",
		InsightText: "Minimal insight",
		Status:      StatusPending,
	}

	record := insightToRecord(insight)

	assert.Equal(t, "ins-200", record.ID)
	assert.Equal(t, "Minimal insight", record.Content)
	// Empty optional fields should NOT be in metadata
	assert.NotContains(t, record.Metadata, "session_id")
	assert.NotContains(t, record.Metadata, "reviewed_by")
	assert.NotContains(t, record.Metadata, "review_notes")
	assert.NotContains(t, record.Metadata, "applied_by")
	assert.NotContains(t, record.Metadata, "changeset_ref")
	assert.NotContains(t, record.Metadata, "suggested_actions")
	assert.Len(t, record.RelatedColumns, 0)
}

func TestRecordToInsight_FieldMapping(t *testing.T) {
	now := time.Now()
	record := memory.Record{
		ID:         "rec-001",
		CreatedAt:  now,
		CreatedBy:  "user@example.com",
		Persona:    "analyst",
		Content:    "Test insight content here",
		Category:   "correction",
		Confidence: "low",
		Source:     "enrichment_gap",
		EntityURNs: []string{"urn:li:dataset:abc"},
		RelatedColumns: []memory.RelatedColumn{
			{URN: "urn:li:dataset:abc", Column: "col1", Relevance: "primary"},
		},
		Status: memory.StatusActive,
		Metadata: map[string]any{
			"session_id":    "sess-999",
			"reviewed_by":   "reviewer@example.com",
			"review_notes":  "Needs work",
			"applied_by":    "admin@example.com",
			"changeset_ref": "cs-xyz",
		},
	}

	insight := recordToInsight(record)

	assert.Equal(t, "rec-001", insight.ID)
	assert.Equal(t, now, insight.CreatedAt)
	assert.Equal(t, "user@example.com", insight.CapturedBy)
	assert.Equal(t, "analyst", insight.Persona)
	assert.Equal(t, "Test insight content here", insight.InsightText)
	assert.Equal(t, "correction", insight.Category)
	assert.Equal(t, "low", insight.Confidence)
	assert.Equal(t, "enrichment_gap", insight.Source)
	assert.Equal(t, []string{"urn:li:dataset:abc"}, insight.EntityURNs)
	assert.Len(t, insight.RelatedColumns, 1)
	assert.Equal(t, "col1", insight.RelatedColumns[0].Column)
	assert.Equal(t, "primary", insight.RelatedColumns[0].Relevance)

	// Metadata extracted
	assert.Equal(t, "sess-999", insight.SessionID)
	assert.Equal(t, "reviewer@example.com", insight.ReviewedBy)
	assert.Equal(t, "Needs work", insight.ReviewNotes)
	assert.Equal(t, "admin@example.com", insight.AppliedBy)
	assert.Equal(t, "cs-xyz", insight.ChangesetRef)

	// Status: active -> pending (default mapping, no insight_status in metadata)
	assert.Equal(t, StatusPending, insight.Status)
}

func TestRecordToInsight_NilSlicesBecomEmpty(t *testing.T) {
	record := memory.Record{
		ID:       "rec-002",
		Status:   memory.StatusActive,
		Metadata: map[string]any{},
	}

	insight := recordToInsight(record)

	assert.NotNil(t, insight.EntityURNs)
	assert.Empty(t, insight.EntityURNs)
	assert.NotNil(t, insight.RelatedColumns)
	assert.Empty(t, insight.RelatedColumns)
	assert.NotNil(t, insight.SuggestedActions)
	assert.Empty(t, insight.SuggestedActions)
}

func TestRecordToInsight_NilMetadata(t *testing.T) {
	record := memory.Record{
		ID:     "rec-003",
		Status: memory.StatusArchived,
	}

	insight := recordToInsight(record)

	assert.Equal(t, StatusRejected, insight.Status) // archived -> rejected
	assert.Empty(t, insight.SessionID)
	assert.Empty(t, insight.ReviewedBy)
}

func TestResolveInsightStatus_MetadataOverride(t *testing.T) {
	tests := []struct {
		name     string
		record   memory.Record
		expected string
	}{
		{
			name:     "active with no metadata",
			record:   memory.Record{Status: memory.StatusActive},
			expected: StatusPending,
		},
		{
			name:     "active with nil metadata",
			record:   memory.Record{Status: memory.StatusActive, Metadata: nil},
			expected: StatusPending,
		},
		{
			name: "active with insight_status approved",
			record: memory.Record{
				Status:   memory.StatusActive,
				Metadata: map[string]any{"insight_status": StatusApproved},
			},
			expected: StatusApproved,
		},
		{
			name: "active with insight_status applied",
			record: memory.Record{
				Status:   memory.StatusActive,
				Metadata: map[string]any{"insight_status": StatusApplied},
			},
			expected: StatusApplied,
		},
		{
			name: "archived with insight_status rolled_back",
			record: memory.Record{
				Status:   memory.StatusArchived,
				Metadata: map[string]any{"insight_status": StatusRolledBack},
			},
			expected: StatusRolledBack,
		},
		{
			name: "empty insight_status does not override",
			record: memory.Record{
				Status:   memory.StatusSuperseded,
				Metadata: map[string]any{"insight_status": ""},
			},
			expected: StatusSuperseded,
		},
		{
			name: "non-string insight_status does not override",
			record: memory.Record{
				Status:   memory.StatusActive,
				Metadata: map[string]any{"insight_status": 42},
			},
			expected: StatusPending,
		},
		{
			name: "legacy_status from migration",
			record: memory.Record{
				Status:   memory.StatusActive,
				Metadata: map[string]any{"legacy_status": StatusApproved},
			},
			expected: StatusApproved,
		},
		{
			name: "legacy_status applied from migration",
			record: memory.Record{
				Status:   memory.StatusActive,
				Metadata: map[string]any{"legacy_status": StatusApplied},
			},
			expected: StatusApplied,
		},
		{
			name: "insight_status takes precedence over legacy_status",
			record: memory.Record{
				Status: memory.StatusActive,
				Metadata: map[string]any{
					"insight_status": StatusRejected,
					"legacy_status":  StatusApproved,
				},
			},
			expected: StatusRejected,
		},
		{
			name: "empty legacy_status does not override",
			record: memory.Record{
				Status:   memory.StatusActive,
				Metadata: map[string]any{"legacy_status": ""},
			},
			expected: StatusPending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveInsightStatus(tt.record)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapInsightStatusToMemory(t *testing.T) {
	tests := []struct {
		insight string
		memory  string
	}{
		{StatusPending, memory.StatusActive},
		{StatusApproved, memory.StatusActive},
		{StatusRejected, memory.StatusArchived},
		{StatusRolledBack, memory.StatusArchived},
		{StatusSuperseded, memory.StatusSuperseded},
		{StatusApplied, memory.StatusActive},
		{"unknown", "unknown"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s->%s", tt.insight, tt.memory), func(t *testing.T) {
			assert.Equal(t, tt.memory, mapInsightStatusToMemory(tt.insight))
		})
	}
}

func TestMapMemoryStatusToInsight(t *testing.T) {
	tests := []struct {
		memory  string
		insight string
	}{
		{memory.StatusActive, StatusPending},
		{memory.StatusArchived, StatusRejected},
		{memory.StatusSuperseded, StatusSuperseded},
		{"stale", "stale"},
		{"unknown", "unknown"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s->%s", tt.memory, tt.insight), func(t *testing.T) {
			assert.Equal(t, tt.insight, mapMemoryStatusToInsight(tt.memory))
		})
	}
}

func TestNewMemoryInsightAdapter(t *testing.T) {
	store := &mockMemoryStore{}
	adapter := NewMemoryInsightAdapter(store)
	assert.NotNil(t, adapter)
}
