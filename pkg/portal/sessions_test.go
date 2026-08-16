package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
)

// What is asserted here is the wiring, not the routes' own behavior (which is
// covered in internal/portal/sessionapi): that a SessionViewer handed to the
// portal reaches the mux behind the portal's authentication, and that with no
// viewer wired the surface is absent rather than broken.

// stubSessionViewer answers a single session and records the filter it was
// asked for, so the test can see the caller the portal handler scoped to.
type stubSessionViewer struct {
	gotFilter sessionview.Filter
}

func (s *stubSessionViewer) List(_ context.Context, filter sessionview.Filter) ([]sessionview.Summary, error) {
	s.gotFilter = filter
	return []sessionview.Summary{{SessionID: "dps_abc", Kind: sessionview.KindAgent}}, nil
}

func (*stubSessionViewer) Count(context.Context, sessionview.Filter) (int, error) {
	return 1, nil
}

func (*stubSessionViewer) Get(context.Context, sessionview.Scope) (*sessionview.Summary, error) {
	return &sessionview.Summary{SessionID: "dps_abc"}, nil
}

func (*stubSessionViewer) Timeline(context.Context, sessionview.Scope) ([]sessionview.TimelineEntry, int, error) {
	return nil, 0, nil
}

func (*stubSessionViewer) Assets(context.Context, string) ([]sessionview.AssetRef, error) {
	return nil, nil
}

func (*stubSessionViewer) Insights(context.Context, string) ([]sessionview.InsightRef, error) {
	return nil, nil
}

func TestSessionRoutes_ServeTheCallersOwnSessions(t *testing.T) {
	viewer := &stubSessionViewer{}
	h := NewHandler(
		Deps{SessionViewer: viewer},
		testAuthMiddleware(&User{UserID: "user-a", Email: "a@example.com"}),
	)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v1/portal/sessions", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "user-a", viewer.gotFilter.UserID,
		"the authenticated caller reaches the read model")

	var got struct {
		Data []sessionview.Summary `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Data, 1)
	assert.Equal(t, "dps_abc", got.Data[0].SessionID)
}

// Without a database there is no audit history to derive a session from, and
// the surface is absent rather than present and empty.
func TestSessionRoutes_AbsentWithNoViewer(t *testing.T) {
	h := NewHandler(Deps{}, testAuthMiddleware(&User{UserID: "user-a"}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v1/portal/sessions", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
