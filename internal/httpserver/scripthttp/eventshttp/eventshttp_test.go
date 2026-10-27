package eventshttp

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents/sourcerule"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

type rig struct {
	saved   *script.Script
	checked []string
	sumErr  error
	since   time.Time
}

func (g *rig) mux() *http.ServeMux {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	New(Deps{
		Edit: func(w http.ResponseWriter, r *http.Request, mutate func(*script.Script)) (*script.Script, bool) {
			if r.Header.Get("X-Owner") != "yes" {
				http.Error(w, "not yours", http.StatusForbidden)
				return nil, false
			}
			g.saved = &script.Script{ID: r.PathValue("id")}
			mutate(g.saved)
			return g.saved, true
		},
		Owned: func(w http.ResponseWriter, r *http.Request) (string, string, bool) {
			if r.Header.Get("X-Owner") != "yes" {
				http.Error(w, "not yours", http.StatusForbidden)
				return "", "", false
			}
			return r.PathValue("id"), "jane@example.com", true
		},
		Caller: func(r *http.Request) (string, bool) { return "jane@example.com", r.Header.Get("X-Admin") == "yes" },
		Config: Config{
			Check: func(_ context.Context, source, email string, admin bool) error {
				g.checked = append(g.checked, fmt.Sprintf("%s %s %v", source, email, admin))
				switch source {
				case "shop":
					if !admin {
						return fmt.Errorf("only an administrator: %w", sourcerule.ErrSourceRefused)
					}
				case "broken":
					return errors.New("db down")
				}
				return nil
			},
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

// #2332: the owner sets the webhook source a script runs for, within the
// rule; an administrator names any source.
func TestSetSource(t *testing.T) {
	g := &rig{}
	w := g.do(t, http.MethodPut, "/api/v1/portal/scripts/s1/event-source", `{"source":"form-contact"}`, "X-Owner", "yes")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "form-contact", g.saved.EventSource)
	assert.Contains(t, w.Body.String(), "Each event source form-contact stores runs this script.")
	assert.Equal(t, []string{"form-contact jane@example.com false"}, g.checked)

	w = g.do(t, http.MethodPut, "/api/v1/portal/scripts/s1/event-source", `{"source":"shop"}`, "X-Owner", "yes")
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = g.do(t, http.MethodPut, "/api/v1/portal/scripts/s1/event-source", `{"source":"shop"}`, "X-Owner", "yes", "X-Admin", "yes")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "shop", g.saved.EventSource)

	w = g.do(t, http.MethodPut, "/api/v1/portal/scripts/s1/event-source", `{"source":""}`, "X-Owner", "yes")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "runs for no webhook source")

	for _, body := range []string{`{}`, `not json`, `{"source":1}`} {
		assert.Equal(t, http.StatusBadRequest, g.do(t, http.MethodPut, "/api/v1/portal/scripts/s1/event-source", body, "X-Owner", "yes").Code, body)
	}
	assert.Equal(t, http.StatusInternalServerError, g.do(t, http.MethodPut, "/api/v1/portal/scripts/s1/event-source", `{"source":"broken"}`, "X-Owner", "yes").Code)
	assert.Equal(t, http.StatusForbidden, g.do(t, http.MethodPut, "/api/v1/portal/scripts/s1/event-source", `{"source":"form-contact"}`).Code, "the edit funnel refuses a caller who does not own it")
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
