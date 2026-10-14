package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/catalogfake"
	"github.com/txn2/mcp-data-platform/pkg/catalog"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

const statusApprovedBody = `{"status":"approved"}`

// --- Mock InsightStore ---

type mockListResult struct {
	insights []knowledge.Insight
	total    int
	err      error
}

type mockStatsResult struct {
	stats *knowledge.InsightStats
	err   error
}

var emptyStats = knowledge.InsightStats{
	ByCategory:   map[string]int{},
	ByConfidence: map[string]int{},
	ByStatus:     map[string]int{},
}

type mockInsightStore struct {
	// Get
	getResult *knowledge.Insight
	getErr    error
	getCalled int
	getLastID string

	// List
	listResult []mockListResult
	listCalled int

	// UpdateStatus
	updateStatusErr    error
	updateStatusCalled int

	// Update
	updateErr    error
	updateCalled int

	// Stats
	statsResult *mockStatsResult
	statsCalled int

	// Insert / MarkApplied / Supersede — not used by admin handlers
	insertErr      error
	markAppliedErr error
	supersedeCount int
	supersedeErr   error

	// ReturnToReview — used by the rollback handler
	returnToReviewErr error
	returnedIDs       []string
}

func (m *mockInsightStore) Insert(_ context.Context, _ knowledge.Insight) error {
	return m.insertErr
}

func (m *mockInsightStore) Get(_ context.Context, id string) (*knowledge.Insight, error) {
	m.getCalled++
	m.getLastID = id
	if m.getResult != nil {
		return m.getResult, m.getErr
	}
	return nil, m.getErr
}

func (m *mockInsightStore) List(_ context.Context, _ knowledge.InsightFilter) ([]knowledge.Insight, int, error) {
	idx := m.listCalled
	m.listCalled++
	if idx < len(m.listResult) {
		r := m.listResult[idx]
		return r.insights, r.total, r.err
	}
	return nil, 0, nil
}

func (m *mockInsightStore) UpdateStatus(_ context.Context, _, _, _, _ string) error {
	m.updateStatusCalled++
	return m.updateStatusErr
}

func (m *mockInsightStore) Update(_ context.Context, _ string, _ knowledge.InsightUpdate) error {
	m.updateCalled++
	return m.updateErr
}

func (m *mockInsightStore) Stats(_ context.Context, _ knowledge.InsightFilter) (*knowledge.InsightStats, error) {
	m.statsCalled++
	if m.statsResult != nil {
		return m.statsResult.stats, m.statsResult.err
	}
	return &emptyStats, nil
}

func (m *mockInsightStore) MarkApplied(_ context.Context, _, _, _ string) error {
	return m.markAppliedErr
}

func (m *mockInsightStore) ReturnToReview(_ context.Context, id, _, _ string) (bool, error) {
	if m.returnToReviewErr != nil {
		return false, m.returnToReviewErr
	}
	m.returnedIDs = append(m.returnedIDs, id)
	return true, nil
}

func (m *mockInsightStore) Supersede(_ context.Context, _, _ string) (int, error) {
	return m.supersedeCount, m.supersedeErr
}

// Verify interface compliance.
var _ knowledge.InsightStore = (*mockInsightStore)(nil)

// --- Mock ChangesetStore ---

type mockChangesetListResult struct {
	changesets []knowledge.Changeset
	total      int
	err        error
}

type mockChangesetStore struct {
	// Get
	getResult *knowledge.Changeset
	getErr    error
	getCalled int

	// List
	listResult []mockChangesetListResult
	listCalled int

	// Rollback
	rollbackErr    error
	rollbackCalled int

	// Insert — not used by admin handlers
	insertErr error
}

func (m *mockChangesetStore) InsertChangeset(_ context.Context, _ knowledge.Changeset) error {
	return m.insertErr
}

func (m *mockChangesetStore) GetChangeset(_ context.Context, _ string) (*knowledge.Changeset, error) {
	m.getCalled++
	if m.getResult != nil {
		return m.getResult, m.getErr
	}
	return nil, m.getErr
}

func (m *mockChangesetStore) ListChangesets(_ context.Context, _ knowledge.ChangesetFilter) ([]knowledge.Changeset, int, error) {
	idx := m.listCalled
	m.listCalled++
	if idx < len(m.listResult) {
		r := m.listResult[idx]
		return r.changesets, r.total, r.err
	}
	return nil, 0, nil
}

func (m *mockChangesetStore) RollbackChangeset(_ context.Context, _, _ string) error {
	m.rollbackCalled++
	return m.rollbackErr
}

// Verify interface compliance.
var _ knowledge.ChangesetStore = (*mockChangesetStore)(nil)

// --- Test NewKnowledgeHandler ---

func TestNewKnowledgeHandler(t *testing.T) {
	store := &mockInsightStore{}
	csStore := &mockChangesetStore{}
	cat := catalogfake.New()
	kh := NewKnowledgeHandler(store, csStore, cat, nil, nil, nil)
	require.NotNil(t, kh)
	assert.Equal(t, store, kh.insightStore)
	assert.Equal(t, csStore, kh.changesetStore)
	assert.Equal(t, cat, kh.catalog)
}

// --- ListInsights tests ---

func TestListInsights(t *testing.T) {
	t.Run("returns empty list", func(t *testing.T) {
		store := &mockInsightStore{
			listResult: []mockListResult{{insights: nil, total: 0, err: nil}},
		}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
		w := httptest.NewRecorder()
		kh.ListInsights(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(0), body["total"])
		assert.NotNil(t, body["data"])
		// Empty list should be an empty array, not null.
		data, ok := body["data"].([]any)
		require.True(t, ok)
		assert.Len(t, data, 0)
	})

	t.Run("returns insights with pagination", func(t *testing.T) {
		insights := []knowledge.Insight{
			{ID: "ins-1", InsightText: "test insight 1", Status: "pending"},
			{ID: "ins-2", InsightText: "test insight 2", Status: "approved"},
		}
		store := &mockInsightStore{
			listResult: []mockListResult{{insights: insights, total: 5, err: nil}},
		}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/insights?page=1&per_page=2", http.NoBody)
		w := httptest.NewRecorder()
		kh.ListInsights(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(5), body["total"])
		assert.Equal(t, float64(1), body["page"])
		assert.Equal(t, float64(2), body["per_page"])
		data, ok := body["data"].([]any)
		require.True(t, ok)
		assert.Len(t, data, 2)
	})

	t.Run("filters by query parameters", func(t *testing.T) {
		store := &mockInsightStore{
			listResult: []mockListResult{{insights: nil, total: 0, err: nil}},
		}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/insights?status=pending&category=correction&confidence=high", http.NoBody)
		w := httptest.NewRecorder()
		kh.ListInsights(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, store.listCalled)
	})

	t.Run("returns 500 on store error", func(t *testing.T) {
		store := &mockInsightStore{
			listResult: []mockListResult{{err: fmt.Errorf("db connection failed")}},
		}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/insights", http.NoBody)
		w := httptest.NewRecorder()
		kh.ListInsights(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "db connection failed")
	})
}

// --- GetInsight tests ---

func TestGetInsight(t *testing.T) {
	t.Run("returns insight when found", func(t *testing.T) {
		insight := &knowledge.Insight{
			ID:          "ins-123",
			InsightText: "test insight",
			Status:      "pending",
			Category:    "correction",
		}
		store := &mockInsightStore{getResult: insight}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/insights/ins-123", http.NoBody)
		req.SetPathValue("id", "ins-123")
		w := httptest.NewRecorder()
		kh.GetInsight(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body knowledge.Insight
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "ins-123", body.ID)
		assert.Equal(t, "test insight", body.InsightText)
	})

	t.Run("returns 404 when not found", func(t *testing.T) {
		store := &mockInsightStore{getErr: fmt.Errorf("not found")}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/insights/nonexistent", http.NoBody)
		req.SetPathValue("id", "nonexistent")
		w := httptest.NewRecorder()
		kh.GetInsight(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "insight not found", pd.Detail)
	})
}

// --- UpdateInsightStatus tests ---

func TestUpdateInsightStatus(t *testing.T) {
	t.Run("valid transition pending to approved", func(t *testing.T) {
		insight := &knowledge.Insight{
			ID:     "ins-123",
			Status: knowledge.StatusPending,
		}
		store := &mockInsightStore{getResult: insight}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		body := `{"status":"approved","review_notes":"looks good"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-123/status", strings.NewReader(body))
		req.SetPathValue("id", "ins-123")
		w := httptest.NewRecorder()
		kh.UpdateInsightStatus(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "updated", resp["status"])
		assert.Equal(t, 1, store.updateStatusCalled)
	})

	t.Run("valid transition pending to rejected", func(t *testing.T) {
		insight := &knowledge.Insight{
			ID:     "ins-456",
			Status: knowledge.StatusPending,
		}
		store := &mockInsightStore{getResult: insight}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		body := `{"status":"rejected","review_notes":"not relevant"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-456/status", strings.NewReader(body))
		req.SetPathValue("id", "ins-456")
		w := httptest.NewRecorder()
		kh.UpdateInsightStatus(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("invalid target status returns 400", func(t *testing.T) {
		store := &mockInsightStore{}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		body := `{"status":"applied"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-123/status", strings.NewReader(body))
		req.SetPathValue("id", "ins-123")
		w := httptest.NewRecorder()
		kh.UpdateInsightStatus(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "status must be")
	})

	t.Run("invalid status transition returns 409", func(t *testing.T) {
		insight := &knowledge.Insight{
			ID:     "ins-789",
			Status: knowledge.StatusRejected, // rejected is terminal — cannot approve
		}
		store := &mockInsightStore{getResult: insight}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		body := `{"status":"approved"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-789/status", strings.NewReader(body))
		req.SetPathValue("id", "ins-789")
		w := httptest.NewRecorder()
		kh.UpdateInsightStatus(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Contains(t, pd.Detail, "invalid status transition")
	})

	t.Run("insight not found returns 404", func(t *testing.T) {
		store := &mockInsightStore{getErr: fmt.Errorf("not found")}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/missing/status", strings.NewReader(statusApprovedBody))
		req.SetPathValue("id", "missing")
		w := httptest.NewRecorder()
		kh.UpdateInsightStatus(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid JSON body returns 400", func(t *testing.T) {
		store := &mockInsightStore{}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-123/status", strings.NewReader("{invalid"))
		req.SetPathValue("id", "ins-123")
		w := httptest.NewRecorder()
		kh.UpdateInsightStatus(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("store error on update returns 500", func(t *testing.T) {
		insight := &knowledge.Insight{
			ID:     "ins-500",
			Status: knowledge.StatusPending,
		}
		store := &mockInsightStore{
			getResult:       insight,
			updateStatusErr: fmt.Errorf("db error"),
		}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-500/status", strings.NewReader(statusApprovedBody))
		req.SetPathValue("id", "ins-500")
		w := httptest.NewRecorder()
		kh.UpdateInsightStatus(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("includes admin user as reviewed_by", func(t *testing.T) {
		insight := &knowledge.Insight{
			ID:     "ins-admin",
			Status: knowledge.StatusPending,
		}
		store := &mockInsightStore{getResult: insight}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		ctx := context.WithValue(context.Background(), adminUserKey, &User{UserID: "admin-1", Roles: []string{"admin"}})
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-admin/status", strings.NewReader(statusApprovedBody))
		req = req.WithContext(ctx)
		req.SetPathValue("id", "ins-admin")
		w := httptest.NewRecorder()
		kh.UpdateInsightStatus(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// --- UpdateInsight tests ---

func TestUpdateInsight(t *testing.T) {
	t.Run("successful edit of pending insight", func(t *testing.T) {
		insight := &knowledge.Insight{
			ID:     "ins-edit",
			Status: knowledge.StatusPending,
		}
		store := &mockInsightStore{getResult: insight}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		body := `{"insight_text":"updated text that is long enough","category":"correction"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-edit", strings.NewReader(body))
		req.SetPathValue("id", "ins-edit")
		w := httptest.NewRecorder()
		kh.UpdateInsight(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "updated", resp["status"])
		assert.Equal(t, 1, store.updateCalled)
	})

	t.Run("cannot edit an applied insight returns 409", func(t *testing.T) {
		insight := &knowledge.Insight{
			ID:     "ins-applied",
			Status: knowledge.StatusApplied,
		}
		store := &mockInsightStore{getResult: insight}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		body := `{"insight_text":"new text"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-applied", strings.NewReader(body))
		req.SetPathValue("id", "ins-applied")
		w := httptest.NewRecorder()
		kh.UpdateInsight(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "cannot edit an applied insight", pd.Detail)
	})

	t.Run("insight not found returns 404", func(t *testing.T) {
		store := &mockInsightStore{getErr: fmt.Errorf("not found")}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		body := `{"insight_text":"new text"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/missing", strings.NewReader(body))
		req.SetPathValue("id", "missing")
		w := httptest.NewRecorder()
		kh.UpdateInsight(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid JSON body returns 400", func(t *testing.T) {
		store := &mockInsightStore{}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-123", strings.NewReader("{bad"))
		req.SetPathValue("id", "ins-123")
		w := httptest.NewRecorder()
		kh.UpdateInsight(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("store update error returns 500", func(t *testing.T) {
		insight := &knowledge.Insight{
			ID:     "ins-err",
			Status: knowledge.StatusPending,
		}
		store := &mockInsightStore{
			getResult: insight,
			updateErr: fmt.Errorf("update failed"),
		}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		body := `{"insight_text":"updated text"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/knowledge/insights/ins-err", strings.NewReader(body))
		req.SetPathValue("id", "ins-err")
		w := httptest.NewRecorder()
		kh.UpdateInsight(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// --- GetStats tests ---

func TestGetStats(t *testing.T) {
	t.Run("returns stats", func(t *testing.T) {
		stats := &knowledge.InsightStats{
			TotalPending: 5,
			ByCategory:   map[string]int{"correction": 3, "business_context": 2},
			ByConfidence: map[string]int{"high": 4, "medium": 1},
			ByStatus:     map[string]int{"pending": 5},
		}
		store := &mockInsightStore{
			statsResult: &mockStatsResult{stats: stats, err: nil},
		}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/insights/stats", http.NoBody)
		w := httptest.NewRecorder()
		kh.GetStats(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body knowledge.InsightStats
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, 5, body.TotalPending)
		assert.Equal(t, 3, body.ByCategory["correction"])
	})

	t.Run("returns 500 on store error", func(t *testing.T) {
		store := &mockInsightStore{
			statsResult: &mockStatsResult{stats: nil, err: fmt.Errorf("stats failed")},
		}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/insights/stats", http.NoBody)
		w := httptest.NewRecorder()
		kh.GetStats(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("accepts filter parameters", func(t *testing.T) {
		store := &mockInsightStore{
			statsResult: &mockStatsResult{stats: &emptyStats, err: nil},
		}
		kh := NewKnowledgeHandler(store, nil, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/insights/stats?status=pending&category=correction", http.NoBody)
		w := httptest.NewRecorder()
		kh.GetStats(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, store.statsCalled)
	})
}

// --- ListChangesets tests ---

func TestListChangesets(t *testing.T) {
	t.Run("returns empty list", func(t *testing.T) {
		csStore := &mockChangesetStore{
			listResult: []mockChangesetListResult{{changesets: nil, total: 0, err: nil}},
		}
		kh := NewKnowledgeHandler(nil, csStore, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/changesets", http.NoBody)
		w := httptest.NewRecorder()
		kh.ListChangesets(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(0), body["total"])
		data, ok := body["data"].([]any)
		require.True(t, ok)
		assert.Len(t, data, 0)
	})

	t.Run("returns changesets with pagination", func(t *testing.T) {
		now := time.Now()
		changesets := []knowledge.Changeset{
			{ID: "cs-1", TargetURN: "urn:li:dataset:1", ChangeType: "update_description", CreatedAt: now},
			{ID: "cs-2", TargetURN: "urn:li:dataset:2", ChangeType: "add_tag", CreatedAt: now},
		}
		csStore := &mockChangesetStore{
			listResult: []mockChangesetListResult{{changesets: changesets, total: 10, err: nil}},
		}
		kh := NewKnowledgeHandler(nil, csStore, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/changesets?page=2&per_page=2", http.NoBody)
		w := httptest.NewRecorder()
		kh.ListChangesets(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, float64(10), body["total"])
		assert.Equal(t, float64(2), body["page"])
		assert.Equal(t, float64(2), body["per_page"])
	})

	t.Run("returns 500 on store error", func(t *testing.T) {
		csStore := &mockChangesetStore{
			listResult: []mockChangesetListResult{{err: fmt.Errorf("db error")}},
		}
		kh := NewKnowledgeHandler(nil, csStore, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/changesets", http.NoBody)
		w := httptest.NewRecorder()
		kh.ListChangesets(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("filters by query parameters", func(t *testing.T) {
		csStore := &mockChangesetStore{
			listResult: []mockChangesetListResult{{changesets: nil, total: 0, err: nil}},
		}
		kh := NewKnowledgeHandler(nil, csStore, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/changesets?entity_urn=urn:test&rolled_back=true", http.NoBody)
		w := httptest.NewRecorder()
		kh.ListChangesets(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, csStore.listCalled)
	})
}

// --- GetChangeset tests ---

func TestGetChangeset(t *testing.T) {
	t.Run("returns changeset when found", func(t *testing.T) {
		cs := &knowledge.Changeset{
			ID:            "cs-123",
			TargetURN:     "urn:li:dataset:(urn:li:dataPlatform:trino,test,PROD)",
			ChangeType:    "update_description",
			PreviousValue: map[string]any{"description": "old"},
			NewValue:      map[string]any{"description": "new"},
		}
		csStore := &mockChangesetStore{getResult: cs}
		kh := NewKnowledgeHandler(nil, csStore, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/changesets/cs-123", http.NoBody)
		req.SetPathValue("id", "cs-123")
		w := httptest.NewRecorder()
		kh.GetChangeset(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var body knowledge.Changeset
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
		assert.Equal(t, "cs-123", body.ID)
		assert.Equal(t, "update_description", body.ChangeType)
	})

	t.Run("returns 404 when not found", func(t *testing.T) {
		csStore := &mockChangesetStore{getErr: errors.New("not found")}
		kh := NewKnowledgeHandler(nil, csStore, nil, nil, nil, nil)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/knowledge/changesets/nonexistent", http.NoBody)
		req.SetPathValue("id", "nonexistent")
		w := httptest.NewRecorder()
		kh.GetChangeset(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		pd := decodeProblem(w.Body.Bytes())
		assert.Equal(t, "changeset not found", pd.Detail)
	})
}

// --- RollbackChangeset tests ---

// tagChangeset writes a tag onto a dataset in cat and returns the changeset an
// apply would have recorded for it.
func tagChangeset(t *testing.T, cat *catalogfake.Store, id, tag string) *knowledge.Changeset {
	t.Helper()
	ref := catalog.DatasetRef("trino", "acme", "test", "table")
	c, err := cat.WriteAspect(context.Background(), ref, catalog.AspectTags, []byte(`["`+tag+`"]`), "agent")
	require.NoError(t, err)
	return &knowledge.Changeset{
		ID:        id,
		TargetURN: ref.String(),
		CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		NewValue: map[string]any{
			"change_0":        map[string]any{"change_type": "add_tag", "target": "", "detail": tag},
			"catalog_changes": []any{c.ID},
		},
	}
}

// rollbackRequest posts the admin rollback of a changeset.
func rollbackRequest(ctx context.Context, kh *KnowledgeHandler, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/changesets/"+id+"/rollback", http.NoBody)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	kh.RollbackChangeset(w, req)
	return w
}

func TestRollbackChangeset(t *testing.T) {
	dataset := catalog.DatasetRef("trino", "acme", "test", "table")

	t.Run("reverts the catalog change and records the admin", func(t *testing.T) {
		cat := catalogfake.New()
		cat.AddDataset("acme", "test.table")
		cs := tagChangeset(t, cat, "cs-roll", "pii")
		cs.SourceInsightIDs = []string{"ins-1"}
		csStore := &mockChangesetStore{getResult: cs}
		insightStore := &mockInsightStore{}
		kh := NewKnowledgeHandler(insightStore, csStore, cat, nil, nil, nil)

		ctx := context.WithValue(context.Background(), adminUserKey, &User{UserID: "admin-1", Roles: []string{"admin"}})
		w := rollbackRequest(ctx, kh, "cs-roll")

		assert.Equal(t, http.StatusOK, w.Code)
		var resp knowledge.RollbackResult
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "cs-roll", resp.ChangesetID)
		assert.Equal(t, "admin-1", resp.RolledBackBy)
		assert.Empty(t, cat.Aspects(dataset).Tags)
		assert.Equal(t, 1, csStore.rollbackCalled)
		assert.Equal(t, []string{"ins-1"}, resp.InsightsReturnedToReview,
			"the reverted changeset's source insight goes back to the review queue (#1257)")
	})

	t.Run("already rolled back returns 409", func(t *testing.T) {
		cs := &knowledge.Changeset{ID: "cs-already", RolledBack: true}
		kh := NewKnowledgeHandler(&mockInsightStore{}, &mockChangesetStore{getResult: cs}, catalogfake.New(), nil, nil, nil)
		w := rollbackRequest(context.Background(), kh, "cs-already")
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, "changeset already rolled back", decodeProblem(w.Body.Bytes()).Detail)
	})

	t.Run("changeset not found returns 404", func(t *testing.T) {
		csStore := &mockChangesetStore{getErr: errors.New("not found")}
		kh := NewKnowledgeHandler(&mockInsightStore{}, csStore, catalogfake.New(), nil, nil, nil)
		assert.Equal(t, http.StatusNotFound, rollbackRequest(context.Background(), kh, "missing").Code)
	})

	t.Run("a changeset applied to DataHub returns 409", func(t *testing.T) {
		cs := &knowledge.Changeset{
			ID:        "cs-dh",
			TargetURN: "urn:li:dataset:(urn:li:dataPlatform:trino,test,PROD)",
			NewValue:  map[string]any{"change_0": map[string]any{"change_type": "add_tag", "detail": "pii"}},
		}
		csStore := &mockChangesetStore{getResult: cs}
		kh := NewKnowledgeHandler(&mockInsightStore{}, csStore, catalogfake.New(), nil, nil, nil)
		w := rollbackRequest(context.Background(), kh, "cs-dh")
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, "applied to DataHub")
		assert.Equal(t, 0, csStore.rollbackCalled, "must not record a rollback it did not perform")
	})

	t.Run("a prompt change returns 422", func(t *testing.T) {
		cs := &knowledge.Changeset{
			ID: "cs-prompt",
			NewValue: map[string]any{
				"change_0":        map[string]any{"change_type": "add_prompt", "target": "p", "detail": "b"},
				"catalog_changes": []any{},
			},
		}
		csStore := &mockChangesetStore{getResult: cs}
		kh := NewKnowledgeHandler(&mockInsightStore{}, csStore, catalogfake.New(), nil, nil, nil)
		w := rollbackRequest(context.Background(), kh, "cs-prompt")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, "add_prompt")
	})

	t.Run("an aspect written since returns 409 and reverts nothing", func(t *testing.T) {
		cat := catalogfake.New()
		cat.AddDataset("acme", "test.table")
		cs := tagChangeset(t, cat, "cs-old", "a")
		_, err := cat.WriteAspect(context.Background(), dataset, catalog.AspectTags, []byte(`["a","b"]`), "someone")
		require.NoError(t, err)
		csStore := &mockChangesetStore{getResult: cs}
		kh := NewKnowledgeHandler(&mockInsightStore{}, csStore, cat, nil, nil, nil)
		w := rollbackRequest(context.Background(), kh, "cs-old")
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, "written again by someone")
		assert.Equal(t, []string{"a", "b"}, cat.Aspects(dataset).Tags)
		assert.Equal(t, 0, csStore.rollbackCalled)
	})

	t.Run("a catalog failure returns 500", func(t *testing.T) {
		cat := catalogfake.New()
		cat.AddDataset("acme", "test.table")
		cs := tagChangeset(t, cat, "cs-fail", "a")
		cat.FailWrite = errors.New("catalog down")
		csStore := &mockChangesetStore{getResult: cs}
		kh := NewKnowledgeHandler(&mockInsightStore{}, csStore, cat, nil, nil, nil)
		w := rollbackRequest(context.Background(), kh, "cs-fail")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, decodeProblem(w.Body.Bytes()).Detail, "rollback failed")
		assert.Equal(t, 0, csStore.rollbackCalled)
	})

	t.Run("store rollback error returns 500", func(t *testing.T) {
		cat := catalogfake.New()
		cat.AddDataset("acme", "test.table")
		cs := tagChangeset(t, cat, "cs-storeerr", "a")
		csStore := &mockChangesetStore{getResult: cs, rollbackErr: fmt.Errorf("rollback db error")}
		kh := NewKnowledgeHandler(&mockInsightStore{}, csStore, cat, nil, nil, nil)
		assert.Equal(t, http.StatusInternalServerError, rollbackRequest(context.Background(), kh, "cs-storeerr").Code)
	})
}

// --- parseInsightFilter tests ---

func TestParseInsightFilter(t *testing.T) {
	t.Run("parses all query parameters", func(t *testing.T) {
		since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		until := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
		url := fmt.Sprintf("/insights?status=pending&category=correction&entity_urn=urn:test&captured_by=user1&confidence=high&since=%s&until=%s&per_page=10&page=3",
			since.Format(time.RFC3339), until.Format(time.RFC3339))

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		filter := parseInsightFilter(req)

		assert.Equal(t, "pending", filter.Status)
		assert.Equal(t, "correction", filter.Category)
		assert.Equal(t, "urn:test", filter.EntityURN)
		assert.Equal(t, "user1", filter.CapturedBy)
		assert.Equal(t, "high", filter.Confidence)
		require.NotNil(t, filter.Since)
		assert.Equal(t, since, *filter.Since)
		require.NotNil(t, filter.Until)
		assert.Equal(t, until, *filter.Until)
		assert.Equal(t, 10, filter.Limit)
		// Page 3 with per_page 10 means offset = (3-1)*10 = 20
		assert.Equal(t, 20, filter.Offset)
		assert.False(t, filter.OrderCreatedAsc, "default order is newest-first")
	})

	t.Run("order=oldest sorts oldest-first", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/insights?order=oldest", http.NoBody)
		assert.True(t, parseInsightFilter(req).OrderCreatedAsc)

		req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/insights?order=newest", http.NoBody)
		assert.False(t, parseInsightFilter(req).OrderCreatedAsc, "any non-oldest value stays newest-first")
	})

	t.Run("defaults for empty parameters", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/insights", http.NoBody)
		filter := parseInsightFilter(req)

		assert.Empty(t, filter.Status)
		assert.Empty(t, filter.Category)
		assert.Nil(t, filter.Since)
		assert.Nil(t, filter.Until)
		assert.Equal(t, 0, filter.Limit)
		assert.Equal(t, 0, filter.Offset)
	})

	t.Run("ignores invalid time formats", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/insights?since=not-a-time&until=invalid", http.NoBody)
		filter := parseInsightFilter(req)

		assert.Nil(t, filter.Since)
		assert.Nil(t, filter.Until)
	})

	t.Run("ignores invalid numeric values", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/insights?per_page=abc&page=xyz", http.NoBody)
		filter := parseInsightFilter(req)

		assert.Equal(t, 0, filter.Limit)
		assert.Equal(t, 0, filter.Offset)
	})

	t.Run("page zero or negative is ignored", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/insights?page=0", http.NoBody)
		filter := parseInsightFilter(req)
		assert.Equal(t, 0, filter.Offset)

		req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/insights?page=-1", http.NoBody)
		filter = parseInsightFilter(req)
		assert.Equal(t, 0, filter.Offset)
	})
}

// --- parseChangesetFilter tests ---

func TestParseChangesetFilter(t *testing.T) {
	t.Run("parses all query parameters", func(t *testing.T) {
		since := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
		url := fmt.Sprintf("/changesets?entity_urn=urn:test&applied_by=user1&since=%s&rolled_back=true&per_page=5&page=2",
			since.Format(time.RFC3339))

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		filter := parseChangesetFilter(req)

		assert.Equal(t, "urn:test", filter.EntityURN)
		assert.Equal(t, "user1", filter.AppliedBy)
		require.NotNil(t, filter.Since)
		assert.Equal(t, since, *filter.Since)
		require.NotNil(t, filter.RolledBack)
		assert.True(t, *filter.RolledBack)
		assert.Equal(t, 5, filter.Limit)
		// Page 2 with per_page 5 means offset = (2-1)*5 = 5
		assert.Equal(t, 5, filter.Offset)
	})

	t.Run("defaults for empty parameters", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/changesets", http.NoBody)
		filter := parseChangesetFilter(req)

		assert.Empty(t, filter.EntityURN)
		assert.Empty(t, filter.AppliedBy)
		assert.Nil(t, filter.Since)
		assert.Nil(t, filter.Until)
		assert.Nil(t, filter.RolledBack)
		assert.Equal(t, 0, filter.Limit)
		assert.Equal(t, 0, filter.Offset)
	})

	t.Run("parses rolled_back false", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/changesets?rolled_back=false", http.NoBody)
		filter := parseChangesetFilter(req)

		require.NotNil(t, filter.RolledBack)
		assert.False(t, *filter.RolledBack)
	})

	t.Run("ignores invalid rolled_back", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/changesets?rolled_back=maybe", http.NoBody)
		filter := parseChangesetFilter(req)

		assert.Nil(t, filter.RolledBack)
	})

	t.Run("parses until parameter", func(t *testing.T) {
		until := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
		url := fmt.Sprintf("/changesets?until=%s", until.Format(time.RFC3339))
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		filter := parseChangesetFilter(req)

		require.NotNil(t, filter.Until)
		assert.Equal(t, until, *filter.Until)
	})
}

// A changeset written to DataHub before the catalog has no catalog history: that
// is the changeset's state, answered 409, not a server fault.
func TestWriteRollbackError_DataHubChangeset(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRollbackError(rec, knowledge.ErrDataHubChangeset)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "applied to DataHub")
}

func TestWriteRollbackError_UnmappedIsServerError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRollbackError(rec, errors.New("upstream exploded"))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "upstream exploded")
}
