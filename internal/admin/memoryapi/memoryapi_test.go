package memoryapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

// fakeLister answers like memory.Store.List: it narrows by author, as the
// real store does, so the test can tell a route that passes created_by
// through from one that ignores it.
type fakeLister struct {
	records []memory.Record
	err     error
	last    memory.Filter
}

func (f *fakeLister) List(_ context.Context, filter memory.Filter) ([]memory.Record, int, error) {
	f.last = filter
	if f.err != nil {
		return nil, 0, f.err
	}
	var out []memory.Record
	for _, r := range f.records {
		if filter.CreatedBy == "" || r.CreatedBy == filter.CreatedBy {
			out = append(out, r)
		}
	}
	return out, len(out), nil
}

func serve(t *testing.T, cfg Config, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, cfg)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody))
	return w
}

func twoAuthors() *fakeLister {
	return &fakeLister{records: []memory.Record{
		{ID: "m1", CreatedBy: "alice@example.com", Content: "a"},
		{ID: "m2", CreatedBy: "bob@example.com", Content: "b"},
	}}
}

func TestListRecords_EveryAuthor(t *testing.T) {
	store := twoAuthors()
	w := serve(t, Config{Records: store}, "/api/v1/admin/memory/records")
	require.Equal(t, http.StatusOK, w.Code)

	var got recordListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, 2, got.Total)
	authors := []string{got.Data[0].CreatedBy, got.Data[1].CreatedBy}
	assert.ElementsMatch(t, []string{"alice@example.com", "bob@example.com"}, authors)
	assert.Empty(t, store.last.CreatedBy, "the admin list must not scope to the caller")
	assert.Equal(t, memory.DefaultLimit, got.Limit)
	assert.Equal(t, 0, got.Offset)
}

func TestListRecords_Filters(t *testing.T) {
	store := twoAuthors()
	w := serve(t, Config{Records: store}, "/api/v1/admin/memory/records?created_by=bob@example.com"+
		"&dimension=knowledge&sink_class=business_knowledge&category=correction&status=active&source=user&limit=5&offset=10")
	require.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, memory.Filter{
		CreatedBy: "bob@example.com", Dimension: "knowledge", SinkClass: "business_knowledge",
		Category: "correction", Status: "active", Source: "user", Limit: 5, Offset: 10,
	}, store.last)

	var got recordListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Data, 1)
	assert.Equal(t, "m2", got.Data[0].ID)
	assert.Equal(t, 5, got.Limit)
	assert.Equal(t, 10, got.Offset)
}

func TestListRecords_BadPagingFallsBack(t *testing.T) {
	store := twoAuthors()
	w := serve(t, Config{Records: store}, "/api/v1/admin/memory/records?limit=x&offset=-4")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, memory.DefaultLimit, store.last.Limit)
	assert.Equal(t, 0, store.last.Offset)
}

func TestListRecords_EmptyIsArray(t *testing.T) {
	w := serve(t, Config{Records: &fakeLister{}}, "/api/v1/admin/memory/records")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"data":[]`)
}

func TestListRecords_StoreError(t *testing.T) {
	w := serve(t, Config{Records: &fakeLister{err: errors.New("db down")}}, "/api/v1/admin/memory/records")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "db down")
}

func TestRegister_NoStoreNoRoute(t *testing.T) {
	w := serve(t, Config{}, "/api/v1/admin/memory/records")
	assert.Equal(t, http.StatusNotFound, w.Code)
}
