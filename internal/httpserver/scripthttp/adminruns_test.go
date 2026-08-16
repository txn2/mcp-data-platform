package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// adminRunStore serves the operator listing and records what it was asked for:
// a listing across scripts is defined by the filter it sends, not by the rows a
// fake chooses to return.
type adminRunStore struct {
	*stubStore
	runs       []script.Run
	lastFilter script.RunFilter
	listErr    error
}

func (a *adminRunStore) ListRuns(_ context.Context, f script.RunFilter) ([]script.Run, error) {
	a.lastFilter = f
	return a.runs, a.listErr
}

func (*adminRunStore) GetRun(context.Context, string) (*script.Run, error) {
	return nil, script.ErrRunNotFound
}
func (*adminRunStore) Enqueue(context.Context, *script.Run) error { return nil }
func (*adminRunStore) Claim(context.Context, string, time.Duration) (*script.Run, error) {
	return nil, script.ErrNoWork
}

func (*adminRunStore) RecordOutput(context.Context, script.RunLease, script.RunOutput) error {
	return nil
}
func (*adminRunStore) Finish(context.Context, script.RunLease, script.RunResult) error { return nil }
func (*adminRunStore) Retry(context.Context, script.RunLease, string, time.Duration) error {
	return nil
}
func (*adminRunStore) PurgeRuns(context.Context, time.Duration) (int64, error) { return 0, nil }

func adminRunDeps(store *adminRunStore) Deps {
	return Deps{
		Scripts: store.stubStore, Versions: store.stubStore, Approvals: store.stubStore,
		Reviews: store.stubStore, Rejections: store.stubStore, Schedules: store.stubStore,
		Runs:       store,
		AdminEmail: func(*http.Request) string { return "admin@example.com" },
	}
}

func serveAdmin(t *testing.T, deps Deps, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(deps).RegisterAdmin(mux, "/api/v1/admin", func(h http.Handler) http.Handler { return h })
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestListRuns_AcrossEveryScript pins the one thing that makes this listing
// different from the per-script one: it names no script.
func TestListRuns_AcrossEveryScript(t *testing.T) {
	store := &adminRunStore{stubStore: newStore(), runs: []script.Run{
		{ID: "dpx_1", ScriptID: "script_1", Status: script.RunStatusSucceeded, Trigger: script.TriggerSchedule},
		{ID: "dpx_2", ScriptID: "script_9", Status: script.RunStatusFailed, Error: "boom"},
	}}
	rec := serveAdmin(t, adminRunDeps(store), "/api/v1/admin/scripts/runs")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Empty(t, store.lastFilter.ScriptID, "the operator listing is not scoped to one script")
	assert.Equal(t, adminRunListLimit, store.lastFilter.Limit)

	var body adminRunListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 2)
	assert.Equal(t, "script_1", body.Data[0].ScriptID, "a cross-script listing names the script")
	assert.Equal(t, "boom", body.Data[1].Error, "a failure says why in the listing")
}

func TestListRuns_ScopesByStatusAndLimit(t *testing.T) {
	store := &adminRunStore{stubStore: newStore()}
	rec := serveAdmin(t, adminRunDeps(store), "/api/v1/admin/scripts/runs?status=failed&per_page=10")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, script.RunStatusFailed, store.lastFilter.Status)
	assert.Equal(t, 10, store.lastFilter.Limit)
}

// The store clamps a listing to its own ceiling, so a caller asking for more
// must be cut here rather than believed: a request for 500 that silently
// returned 50 would read as the whole history.
func TestListRuns_CannotAskPastTheStoreCeiling(t *testing.T) {
	store := &adminRunStore{stubStore: newStore()}
	rec := serveAdmin(t, adminRunDeps(store), "/api/v1/admin/scripts/runs?per_page=500")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, adminRunListLimit, store.lastFilter.Limit)
}

// A listing that filled its cap has older runs behind it, and the payload says
// which cap it was read under so a page can state that rather than implying it
// showed everything.
func TestListRuns_ReportsTheCapItWasReadUnder(t *testing.T) {
	store := &adminRunStore{stubStore: newStore(), runs: []script.Run{{ID: "dpx_1"}}}
	rec := serveAdmin(t, adminRunDeps(store), "/api/v1/admin/scripts/runs")
	require.Equal(t, http.StatusOK, rec.Code)

	var body adminRunListResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, adminRunListLimit, body.Limit)
	assert.Equal(t, 1, body.Total)
}

func TestListRuns_StoreFailure(t *testing.T) {
	store := &adminRunStore{stubStore: newStore(), listErr: errors.New("boom")}
	rec := serveAdmin(t, adminRunDeps(store), "/api/v1/admin/scripts/runs")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "boom")
}

// A script whose id is "runs" must not shadow the listing: the literal segment
// outranks the wildcard, and this is what proves the route still resolves.
func TestListRuns_IsNotShadowedByAScriptNamedRuns(t *testing.T) {
	store := &adminRunStore{stubStore: newStore()}
	store.scripts = append(store.scripts, script.Script{ID: "runs", Name: "runs"})
	rec := serveAdmin(t, adminRunDeps(store), "/api/v1/admin/scripts/runs")
	require.Equal(t, http.StatusOK, rec.Code)
	var body adminRunListResponse
	decodeInto(t, rec, &body)
	assert.Empty(t, body.Data)
}

// A deployment that keeps no runs has no listing rather than a failing one.
func TestListRuns_UnmountedWithoutARunStore(t *testing.T) {
	store := newStore()
	deps := Deps{
		Scripts: store, Versions: store, Approvals: store, Reviews: store, Rejections: store,
		AdminEmail: func(*http.Request) string { return "admin@example.com" },
	}
	rec := serveAdmin(t, deps, "/api/v1/admin/scripts/runs")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
