package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// --- Mock memory.Store ---

type mockMemoryListResult struct {
	records []memory.Record
	total   int
	err     error
}

type mockMemoryStore struct {
	// Get
	getResult *memory.Record
	getErr    error
	getCalled int
	getLastID string

	// List
	listResult []mockMemoryListResult
	listCalled int

	// Update
	updateErr    error
	updateCalled int
	lastUpdate   memory.RecordUpdate

	// Delete
	deleteErr    error
	deleteCalled int

	// Unused by admin handlers but required by interface.
	insertErr    error
	vectorErr    error
	entityErr    error
	markStaleErr error
	markVerErr   error
	supersedeErr error
}

func (m *mockMemoryStore) Insert(_ context.Context, _ memory.Record) error {
	return m.insertErr
}

func (m *mockMemoryStore) Get(_ context.Context, id string) (*memory.Record, error) {
	m.getCalled++
	m.getLastID = id
	if m.getResult != nil {
		return m.getResult, m.getErr
	}
	return nil, m.getErr
}

func (m *mockMemoryStore) Update(_ context.Context, _ string, u memory.RecordUpdate) error {
	m.updateCalled++
	m.lastUpdate = u
	return m.updateErr
}

func (m *mockMemoryStore) Delete(_ context.Context, _ string) error {
	m.deleteCalled++
	return m.deleteErr
}

func (m *mockMemoryStore) List(_ context.Context, _ memory.Filter) ([]memory.Record, int, error) {
	idx := m.listCalled
	m.listCalled++
	if idx < len(m.listResult) {
		r := m.listResult[idx]
		return r.records, r.total, r.err
	}
	return nil, 0, nil
}

func (m *mockMemoryStore) VectorSearch(_ context.Context, _ memory.VectorQuery) ([]memory.ScoredRecord, error) {
	return nil, m.vectorErr
}

func (m *mockMemoryStore) EntityLookup(_ context.Context, _, _ string) ([]memory.Record, error) {
	return nil, m.entityErr
}

func (m *mockMemoryStore) MarkStale(_ context.Context, _ []string, _ string) error {
	return m.markStaleErr
}

func (m *mockMemoryStore) MarkVerified(_ context.Context, _ []string) error {
	return m.markVerErr
}

func (m *mockMemoryStore) Supersede(_ context.Context, _, _ string) error {
	return m.supersedeErr
}

// Verify interface compliance.
var _ memory.Store = (*mockMemoryStore)(nil)

// --- Tests ---

func TestListRecords(t *testing.T) {
	now := time.Now().UTC()
	rec := memory.Record{
		ID:        "mem-1",
		CreatedAt: now,
		Dimension: "knowledge",
		Category:  "business_context",
		Status:    "active",
		Content:   "test content for listing",
	}

	tests := []struct {
		name       string
		query      string
		listResult mockMemoryListResult
		wantCode   int
		wantTotal  int
		wantLen    int
	}{
		{
			name:       "success with results",
			query:      "?page=1&per_page=10",
			listResult: mockMemoryListResult{records: []memory.Record{rec}, total: 1},
			wantCode:   http.StatusOK,
			wantTotal:  1,
			wantLen:    1,
		},
		{
			name:       "empty results",
			query:      "",
			listResult: mockMemoryListResult{records: nil, total: 0},
			wantCode:   http.StatusOK,
			wantTotal:  0,
			wantLen:    0,
		},
		{
			name:       "store error",
			query:      "",
			listResult: mockMemoryListResult{err: fmt.Errorf("db error")},
			wantCode:   http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockMemoryStore{
				listResult: []mockMemoryListResult{tc.listResult},
			}
			h := NewMemoryHandler(store)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/memory/records"+tc.query, http.NoBody)
			w := httptest.NewRecorder()
			h.ListRecords(w, req)

			assert.Equal(t, tc.wantCode, w.Code)
			if tc.wantCode == http.StatusOK {
				var resp memoryListResponse
				require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
				assert.Equal(t, tc.wantTotal, resp.Total)
				assert.Len(t, resp.Data, tc.wantLen)
			}
		})
	}
}

func TestListRecords_FilterParams(t *testing.T) {
	store := &mockMemoryStore{
		listResult: []mockMemoryListResult{{records: nil, total: 0}},
	}
	h := NewMemoryHandler(store)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/memory/records?persona=analyst&dimension=knowledge&category=correction&status=active&source=user&entity_urn=urn:li:dataset:1&created_by=alice&page=2&per_page=5",
		http.NoBody)
	w := httptest.NewRecorder()
	h.ListRecords(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, store.listCalled)
}

func TestMemoryGetStats(t *testing.T) {
	records := []memory.Record{
		{Dimension: "knowledge", Category: "correction", Status: "active"},
		{Dimension: "knowledge", Category: "business_context", Status: "active"},
		{Dimension: "event", Category: "correction", Status: "stale"},
	}

	tests := []struct {
		name     string
		result   mockMemoryListResult
		wantCode int
		wantDims int
	}{
		{
			name:     "success",
			result:   mockMemoryListResult{records: records, total: 3},
			wantCode: http.StatusOK,
			wantDims: 2,
		},
		{
			name:     "store error",
			result:   mockMemoryListResult{err: fmt.Errorf("db error")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockMemoryStore{
				listResult: []mockMemoryListResult{tc.result},
			}
			h := NewMemoryHandler(store)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/memory/records/stats", http.NoBody)
			w := httptest.NewRecorder()
			h.GetStats(w, req)

			assert.Equal(t, tc.wantCode, w.Code)
			if tc.wantCode == http.StatusOK {
				var resp memoryStatsResponse
				require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
				assert.Equal(t, 3, resp.Total)
				assert.Len(t, resp.ByDimension, tc.wantDims)
			}
		})
	}
}

func TestGetRecord(t *testing.T) {
	now := time.Now().UTC()
	rec := &memory.Record{
		ID:        "mem-1",
		CreatedAt: now,
		Dimension: "knowledge",
		Content:   "test content here for get",
		Status:    "active",
	}

	tests := []struct {
		name     string
		result   *memory.Record
		err      error
		wantCode int
	}{
		{
			name:     "found",
			result:   rec,
			wantCode: http.StatusOK,
		},
		{
			name:     "not found",
			err:      fmt.Errorf("not found"),
			wantCode: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockMemoryStore{getResult: tc.result, getErr: tc.err}
			h := NewMemoryHandler(store)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/memory/records/mem-1", http.NoBody)
			req.SetPathValue(pathParamID, "mem-1")
			w := httptest.NewRecorder()
			h.GetRecord(w, req)

			assert.Equal(t, tc.wantCode, w.Code)
		})
	}
}

func TestUpdateRecord(t *testing.T) {
	activeRec := &memory.Record{ID: "mem-1", Status: "active", Content: "existing content for update"}
	archivedRec := &memory.Record{ID: "mem-2", Status: "archived", Content: "archived content here"}

	tests := []struct {
		name      string
		body      string
		getResult *memory.Record
		getErr    error
		updateErr error
		wantCode  int
	}{
		{
			name:      "success",
			body:      `{"content":"updated content for testing purposes"}`,
			getResult: activeRec,
			wantCode:  http.StatusOK,
		},
		{
			name:     "invalid json",
			body:     `{bad`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "not found",
			body:     `{"content":"something here that is long enough"}`,
			getErr:   fmt.Errorf("not found"),
			wantCode: http.StatusNotFound,
		},
		{
			name:      "archived record",
			body:      `{"content":"something here that is long enough"}`,
			getResult: archivedRec,
			wantCode:  http.StatusConflict,
		},
		{
			name:      "update error",
			body:      `{"content":"something here that is long enough"}`,
			getResult: activeRec,
			updateErr: fmt.Errorf("db error"),
			wantCode:  http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockMemoryStore{
				getResult: tc.getResult,
				getErr:    tc.getErr,
				updateErr: tc.updateErr,
			}
			h := NewMemoryHandler(store)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/admin/memory/records/mem-1", strings.NewReader(tc.body))
			req.SetPathValue(pathParamID, "mem-1")
			w := httptest.NewRecorder()
			h.UpdateRecord(w, req)

			assert.Equal(t, tc.wantCode, w.Code)
			if tc.wantCode == http.StatusOK {
				var resp statusResponse
				require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
				assert.Equal(t, "ok", resp.Status)
			}
		})
	}
}

func TestDeleteRecord(t *testing.T) {
	rec := &memory.Record{ID: "mem-1", Status: "active", Content: "content to delete from store"}

	tests := []struct {
		name      string
		getResult *memory.Record
		getErr    error
		deleteErr error
		wantCode  int
	}{
		{
			name:      "success",
			getResult: rec,
			wantCode:  http.StatusOK,
		},
		{
			name:     "not found",
			getErr:   fmt.Errorf("not found"),
			wantCode: http.StatusNotFound,
		},
		{
			name:      "delete error",
			getResult: rec,
			deleteErr: fmt.Errorf("db error"),
			wantCode:  http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockMemoryStore{
				getResult: tc.getResult,
				getErr:    tc.getErr,
				deleteErr: tc.deleteErr,
			}
			h := NewMemoryHandler(store)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/admin/memory/records/mem-1", http.NoBody)
			req.SetPathValue(pathParamID, "mem-1")
			w := httptest.NewRecorder()
			h.DeleteRecord(w, req)

			assert.Equal(t, tc.wantCode, w.Code)
			if tc.wantCode == http.StatusOK {
				var resp statusResponse
				require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
				assert.Equal(t, "ok", resp.Status)
			}
		})
	}
}

func TestParseMemoryFilter(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/memory/records?persona=analyst&dimension=event&category=correction&status=stale&source=user&entity_urn=urn:x&created_by=alice&page=3&per_page=10&since=2024-01-01T00:00:00Z&until=2024-12-31T23:59:59Z",
		http.NoBody)

	filter := parseMemoryFilter(req)

	assert.Equal(t, "analyst", filter.Persona)
	assert.Equal(t, "event", filter.Dimension)
	assert.Equal(t, "correction", filter.Category)
	assert.Equal(t, "stale", filter.Status)
	assert.Equal(t, "user", filter.Source)
	assert.Equal(t, "urn:x", filter.EntityURN)
	assert.Equal(t, "alice", filter.CreatedBy)
	assert.Equal(t, 10, filter.Limit)
	assert.Equal(t, 20, filter.Offset) // page 3 * 10 per_page = offset 20
	assert.NotNil(t, filter.Since)
	assert.NotNil(t, filter.Until)
}

func TestRegisterMemoryRoutes_Fallback(t *testing.T) {
	enabled := true
	h := NewHandler(Deps{
		Config: &platform.Config{
			Memory: platform.MemoryConfig{Enabled: &enabled},
		},
	}, nil)

	// With Memory nil and config enabled, should get 409.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/memory/records", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestRegisterMemoryRoutes_WithStore(t *testing.T) {
	store := &mockMemoryStore{
		listResult: []mockMemoryListResult{{records: nil, total: 0}},
	}
	h := NewHandler(Deps{
		Memory: NewMemoryHandler(store),
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/memory/records", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, store.listCalled)
}

func TestRegisterMemoryRoutes_DisabledNoFallback(t *testing.T) {
	disabled := false
	h := NewHandler(Deps{
		Config: &platform.Config{
			Memory: platform.MemoryConfig{Enabled: &disabled},
		},
	}, nil)

	// With Memory nil and config explicitly disabled, should get 404 (no route registered).
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/memory/records", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
