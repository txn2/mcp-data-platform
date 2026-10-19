package storageapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/storagecap"
)

type fakeService struct {
	report  storagecap.Report
	err     error
	store   string
	days    int
	history []storagecap.Point
}

func (f *fakeService) Report(context.Context) (storagecap.Report, error) { return f.report, f.err }

func (f *fakeService) History(_ context.Context, store string, days int) ([]storagecap.Point, error) {
	f.store, f.days = store, days
	return f.history, f.err
}

func serve(t *testing.T, svc Service, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, svc)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
	return rec
}

func TestReport(t *testing.T) {
	at := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	svc := &fakeService{report: storagecap.Report{GeneratedAt: at, Stores: []storagecap.StoreView{{Store: "database", UsedBytes: 5}}}}
	rec := serve(t, svc, "/api/v1/admin/storage")
	require.Equal(t, http.StatusOK, rec.Code)
	var got storagecap.Report
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "database", got.Stores[0].Store)

	svc.err = errors.New("down")
	rec = serve(t, svc, "/api/v1/admin/storage")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "down", "the failure's detail stays in the log")
}

func TestHistory(t *testing.T) {
	svc := &fakeService{history: []storagecap.Point{}}
	rec := serve(t, svc, "/api/v1/admin/storage/history?store=bucket:portal&days=7")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "bucket:portal", svc.store)
	assert.Equal(t, 7, svc.days)
	assert.JSONEq(t, `{"store":"bucket:portal","days":7,"points":[]}`, rec.Body.String())

	rec = serve(t, svc, "/api/v1/admin/storage/history?store=database")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 30, svc.days, "thirty days by default")

	for _, q := range []string{"store=", "store=tables", "store=database&days=0", "store=database&days=x", "store=database&days=5000"} {
		assert.Equal(t, http.StatusBadRequest, serve(t, svc, "/api/v1/admin/storage/history?"+q).Code, q)
	}
	svc.err = errors.New("down")
	assert.Equal(t, http.StatusInternalServerError, serve(t, svc, "/api/v1/admin/storage/history?store=database").Code)
}

func TestRegisterWithoutAService(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/storage", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
