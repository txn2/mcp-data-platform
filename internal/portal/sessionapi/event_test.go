package sessionapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// One row of the reader's own session timeline, opened (#1797). The timeline
// entry carries no parameters and no error text, and the call catalog is not
// the answer -- a call record is written only for the sql, api and graphql
// kinds -- so the drill-down reads the audit event itself, scoped to the
// caller.

// fakeEvents is an audit reader that records the filter the handler built,
// which is where the scoping lives. It does NOT apply the scope itself: a fake
// that filtered on the handler's behalf would pass for a handler that dropped
// the caller's id.
type fakeEvents struct {
	events    []audit.Event
	err       error
	gotFilter audit.QueryFilter
}

func (f *fakeEvents) Query(_ context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	f.gotFilter = filter
	return f.events, f.err
}

// serveEvent issues a request for one event, with the caller in context when
// userID is set.
func serveEvent(t *testing.T, store Store, events Events, target, userID string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, Config{Sessions: store, Events: events})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	if userID != "" {
		req = req.WithContext(access.ContextWithUser(req.Context(), &access.User{
			UserID: userID,
			Email:  userID + "@example.com",
		}))
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func TestGetEvent_NilEventsLeavesTheRouteOff(t *testing.T) {
	rec := serveEvent(t, &fakeStore{}, nil, "/api/v1/portal/events/evt-1", callerID)
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"a deployment with no audit reader registers no drill-down")
}

func TestGetEvent_RequiresAuthentication(t *testing.T) {
	rec := serveEvent(t, &fakeStore{}, &fakeEvents{}, "/api/v1/portal/events/evt-1", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestGetEvent_ScopesTheReadToTheCaller is the whole security of this route:
// the caller's id is assigned AFTER the id is taken from the path, so the
// scope is not something a caller can spell.
func TestGetEvent_ScopesTheReadToTheCaller(t *testing.T) {
	events := &fakeEvents{events: []audit.Event{{ID: "evt-1", ToolName: "search"}}}

	rec := serveEvent(t, &fakeStore{}, events, "/api/v1/portal/events/evt-1", callerID)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "evt-1", events.gotFilter.ID)
	assert.Equal(t, callerID, events.gotFilter.UserID, "the read carries the caller's id")
	assert.Equal(t, 1, events.gotFilter.Limit)

	var got audit.Event
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "evt-1", got.ID)
	assert.Equal(t, "search", got.ToolName)
}

// TestGetEvent_SomebodyElsesIsNotFound holds the answer to the one question
// this route must never answer differently: an id that was never issued and an
// id belonging to another caller both produce nothing, so the difference
// cannot be used to learn that somebody else's call exists.
func TestGetEvent_SomebodyElsesIsNotFound(t *testing.T) {
	rec := serveEvent(t, &fakeStore{}, &fakeEvents{}, "/api/v1/portal/events/evt-theirs", callerID)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotEqual(t, http.StatusForbidden, rec.Code,
		"a refusal would confirm the event exists")
	assert.Contains(t, rec.Body.String(), "no such call")
}

func TestGetEvent_ReportsAFailedRead(t *testing.T) {
	events := &fakeEvents{err: errors.New("boom")}

	rec := serveEvent(t, &fakeStore{}, events, "/api/v1/portal/events/evt-1", callerID)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "boom", "the store's error is not the caller's")
}
