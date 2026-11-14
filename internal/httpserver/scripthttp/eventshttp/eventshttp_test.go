package eventshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
)

type rig struct {
	sumErr error
	since  time.Time
}

func (g *rig) mux() *http.ServeMux {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	New(Deps{
		Owned: func(w http.ResponseWriter, r *http.Request) (string, string, bool) {
			if r.Header.Get("X-Owner") != "yes" {
				http.Error(w, "not yours", http.StatusForbidden)
				return "", "", false
			}
			return r.PathValue("id"), "jane@example.com", true
		},
		Config: Config{
			Summary: func(_ context.Context, _ string, since time.Time) (scriptevents.EventSummary, error) {
				g.since = since
				if g.sumErr != nil {
					return scriptevents.EventSummary{}, g.sumErr
				}
				return scriptevents.EventSummary{Pending: 3, SkippedSince: 2}, nil
			},
		},
		Now: func() time.Time { return now },
	}).Register(mux, func(h http.Handler) http.Handler { return h })
	return mux
}

func (g *rig) do(t *testing.T, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	g.mux().ServeHTTP(w, req)
	return w
}

// #2434: the webhook source a script runs for is a handler, set through the
// handler routes; the event-source route is gone rather than left answering.
func TestTheEventSourceRouteIsGone(t *testing.T) {
	g := &rig{}
	w := g.do(t, http.MethodPut, "/api/v1/portal/scripts/s1/event-source", `{"source":"form-contact"}`, "X-Owner", "yes")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSummary(t *testing.T) {
	g := &rig{}
	w := g.do(t, http.MethodGet, "/api/v1/portal/scripts/s1/events", "", "X-Owner", "yes")
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.InDelta(t, 3, got["pending"], 0)
	assert.InDelta(t, 2, got["skipped_since"], 0)
	assert.InDelta(t, 7, got["skipped_window_days"], 0)
	assert.InDelta(t, scriptevents.DefaultMaxQueued, got["max_queued"], 0)
	assert.Equal(t, time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC), g.since)

	assert.Equal(t, http.StatusForbidden, g.do(t, http.MethodGet, "/api/v1/portal/scripts/s1/events", "").Code)
	g.sumErr = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, g.do(t, http.MethodGet, "/api/v1/portal/scripts/s1/events", "", "X-Owner", "yes").Code)
}

// TestConfigForAndForPortal: the summary reads the platform's event runs
// with the configured bound, and the portal handler resolves the script in
// the path by scripthttp's owner check.
func TestConfigForAndForPortal(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := ConfigFor(db, 7)
	assert.Equal(t, 7, cfg.MaxQueued)
	require.NotNil(t, cfg.Summary)
	mock.ExpectQuery("FROM script_runs").WillReturnError(errors.New("db down"))
	_, err = cfg.Summary(t.Context(), "s1", time.Now())
	require.Error(t, err, "the summary reads script_runs")

	h := ForPortal(scripthttp.New(scripthttp.Deps{
		PortalUser: func(*http.Request) *scripthttp.PortalIdentity { return nil },
	}), cfg)
	require.NotNil(t, h)
	mux := http.NewServeMux()
	h.Register(mux, func(next http.Handler) http.Handler { return next })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/portal/scripts/s1/events", http.NoBody))
	assert.NotEqual(t, http.StatusOK, rec.Code, "a request with no portal caller is refused by the owner check")
	require.NoError(t, mock.ExpectationsWereMet())
}
