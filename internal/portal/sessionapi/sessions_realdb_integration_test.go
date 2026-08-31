//go:build integration

package sessionapi_test

// Real-Postgres test for the caller-scoped session surface. The scoping is a
// predicate inside a rollup over audit_logs, and a fake store cannot prove it:
// a fake that filters on the handler's behalf passes whether or not the caller
// ever reached the SQL. What is asserted here is the acceptance criterion of
// #1319, over one real database holding two users' sessions — user A lists only
// their own, A asking for B's session is answered not-found, and an operator
// asking for that same id through the admin route gets it.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adminsessionapi "github.com/txn2/mcp-data-platform/internal/admin/sessionapi"
	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	portalsessionapi "github.com/txn2/mcp-data-platform/internal/portal/sessionapi"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	auditpg "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
)

const (
	userA        = "analyst-a"
	userB        = "analyst-b"
	sessionOfA   = "dps_realdb1319000000000000000001"
	sessionOfB   = "dps_realdb1319000000000000000002"
	callsBySideA = 3
)

// listResponse mirrors the paginated envelope both surfaces return. Declared
// here rather than imported because each surface keeps its own unexported
// response type; the shapes agreeing is part of what this asserts.
type listResponse struct {
	Data  []sessionview.Summary `json:"data"`
	Total int                   `json:"total"`
}

// twoUserFixture writes one session for each of two callers: A saved an asset,
// B did not, so every assertion below has both something to find and something
// to exclude.
func twoUserFixture(t *testing.T) *sessionview.PostgresStore {
	t.Helper()
	db := testdb.New(t)
	ctx := t.Context()

	events := auditpg.New(db, auditpg.Config{RetentionDays: 30})
	base := time.Now().Add(-time.Hour)

	log := func(sessionID, userID, email, tool, purpose string, at time.Time) {
		t.Helper()
		ev := audit.NewEvent(tool).
			WithUser(userID, email).
			WithPersona("analyst").
			WithSessionID(sessionID).
			WithPurpose(purpose).
			WithResult(true, "", 12)
		ev.Timestamp = at
		require.NoError(t, events.Log(ctx, *ev))
	}

	log(sessionOfA, userA, "a@example.com", "search", "Finding the revenue table.", base)
	log(sessionOfA, userA, "a@example.com", "trino_query", "Summing Q3 revenue by region.", base.Add(time.Minute))
	log(sessionOfA, userA, "a@example.com", "save_asset", "Saving the finished table.", base.Add(2*time.Minute))
	log(sessionOfB, userB, "b@example.com", "search", "Checking last month's signups.", base.Add(3*time.Minute))

	assets := portalstore.NewPostgresAssetStore(db, nil)
	require.NoError(t, assets.Insert(ctx, portaldomain.Asset{
		ID:          "ast_realdb_1319",
		OwnerID:     userA,
		OwnerEmail:  "a@example.com",
		Name:        "Q3 revenue by region",
		ContentType: "text/csv",
		S3Bucket:    "portal-assets",
		S3Key:       "assets/ast_realdb_1319/content.csv",
		SessionID:   sessionOfA,
	}))

	return sessionview.NewPostgresStore(db)
}

// portalGet runs one GET against the portal surface as the named caller.
func portalGet(t *testing.T, store sessionview.Store, target, userID string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	portalsessionapi.Register(mux, portalsessionapi.Config{Sessions: store})

	req := httptest.NewRequestWithContext(
		access.ContextWithUser(t.Context(), &access.User{UserID: userID, Email: userID + "@example.com"}),
		http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// adminGet runs one GET against the operator surface, which carries no caller:
// the admin mux is already behind the admin persona gate.
func adminGet(t *testing.T, store sessionview.Store, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	adminsessionapi.Register(mux, adminsessionapi.Config{Sessions: store})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestPortalSessions_RealDB_ListReturnsOnlyTheCallersOwn is the first half of
// the acceptance criterion: a user's list holds their sessions and no one
// else's, and the total counts the same set the page came from.
func TestPortalSessions_RealDB_ListReturnsOnlyTheCallersOwn(t *testing.T) {
	store := twoUserFixture(t)

	rec := portalGet(t, store, "/api/v1/portal/sessions", userA)
	require.Equal(t, http.StatusOK, rec.Code)

	var got listResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Data, 1, "A ran one session; B's is not A's to see")
	assert.Equal(t, 1, got.Total)

	s := got.Data[0]
	assert.Equal(t, sessionOfA, s.SessionID)
	assert.Equal(t, userA, s.UserID)
	assert.Equal(t, callsBySideA, s.CallCount)
	assert.Equal(t, 1, s.AssetCount, "the asset A saved is attached to A's session")

	// The same request as B returns B's session and nothing of A's, so the
	// scoping is a filter on the caller rather than on this one fixture.
	rec = portalGet(t, store, "/api/v1/portal/sessions", userB)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Data, 1)
	assert.Equal(t, sessionOfB, got.Data[0].SessionID)
}

// TestPortalSessions_RealDB_OtherUsersSessionIsNotFound is the second half: A
// naming B's session id — which exists, and which A could only have learned by
// guessing — is answered exactly as an id that was never used.
func TestPortalSessions_RealDB_OtherUsersSessionIsNotFound(t *testing.T) {
	store := twoUserFixture(t)

	other := portalGet(t, store, "/api/v1/portal/sessions/"+sessionOfB, userA)
	assert.Equal(t, http.StatusNotFound, other.Code)

	never := portalGet(t, store, "/api/v1/portal/sessions/dps_never_ran", userA)
	assert.Equal(t, http.StatusNotFound, never.Code)
	assert.Equal(t, other.Body.String(), never.Body.String(),
		"an existing session that is not mine reads the same as one that never existed")

	// A's own session, through the same route, is served in full — so the
	// not-found above is the scope refusing and not the route being broken.
	mine := portalGet(t, store, "/api/v1/portal/sessions/"+sessionOfA, userA)
	require.Equal(t, http.StatusOK, mine.Code)

	var detail sessionview.Detail
	require.NoError(t, json.Unmarshal(mine.Body.Bytes(), &detail))
	assert.Equal(t, sessionOfA, detail.SessionID)
	assert.Equal(t, callsBySideA, detail.TimelineTotal)
	require.Len(t, detail.Timeline, callsBySideA)
	assert.Equal(t, "Finding the revenue table.", detail.Timeline[0].Purpose,
		"the reason the agent stated survives to the user's own reading of it")
	require.Len(t, detail.Assets, 1)
	assert.Equal(t, "Q3 revenue by region", detail.Assets[0].Name)
}

// TestPortalSessions_RealDB_AdminReadsWhatTheUserCannot is the third half: the
// id the portal refused A is served to an operator over the same store, so the
// refusal is the portal's scope and not an absence in the read model.
func TestPortalSessions_RealDB_AdminReadsWhatTheUserCannot(t *testing.T) {
	store := twoUserFixture(t)

	require.Equal(t, http.StatusNotFound,
		portalGet(t, store, "/api/v1/portal/sessions/"+sessionOfB, userA).Code)

	rec := adminGet(t, store, "/api/v1/admin/sessions/"+sessionOfB)
	require.Equal(t, http.StatusOK, rec.Code)

	var detail sessionview.Detail
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	assert.Equal(t, sessionOfB, detail.SessionID)
	assert.Equal(t, userB, detail.UserID)

	// And the operator's list holds both, which is the shape the portal list
	// above is the narrowed view of.
	rec = adminGet(t, store, "/api/v1/admin/sessions")
	require.Equal(t, http.StatusOK, rec.Code)

	var got listResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 2, got.Total, "the operator surface is unrestricted by design")
}

// TestPortalSessions_RealDB_TimelineIsScopedToo covers the read the scoped Get
// authorizes but does not itself bound: a timeline page must not carry calls
// the caller did not make, even inside a session they did.
func TestPortalSessions_RealDB_TimelineIsScopedToo(t *testing.T) {
	store := twoUserFixture(t)

	entries, total, err := store.Timeline(t.Context(), sessionview.Scope{
		SessionID: sessionOfA,
		UserID:    userB,
	})
	require.NoError(t, err)
	assert.Empty(t, entries, "B reads none of A's calls")
	assert.Zero(t, total, "and the total is bounded the same way the page is")
}
