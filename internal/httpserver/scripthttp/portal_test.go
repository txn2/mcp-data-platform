package scripthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The portal callers every test in this file serves as.
var (
	owner    = &PortalIdentity{UserID: "u1", Email: "jane@example.com", Persona: "analyst"}
	stranger = &PortalIdentity{UserID: "u2", Email: "bob@example.com", Persona: "analyst"}
	admin    = &PortalIdentity{UserID: "u3", Email: "admin@example.com", Persona: "admin", IsAdmin: true}
)

// stubRuns is the run history half of the portal surface.
type stubRuns struct {
	runs []script.Run
	// lastFilter records what ListRuns was asked for, so the scoping and the
	// limit are asserted on the request rather than on the fake's answer.
	lastFilter script.RunFilter
	listErr    error
	getErr     error
	// latest is what LatestRuns returns, and latestFor records the ids it was
	// asked about: the surface must never ask about a script the caller does
	// not own.
	latest    map[string]script.Run
	latestFor []string
	latestErr error
}

func (s *stubRuns) ListRuns(_ context.Context, f script.RunFilter) ([]script.Run, error) {
	s.lastFilter = f
	return s.runs, s.listErr
}

func (s *stubRuns) GetRun(_ context.Context, id string) (*script.Run, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	for i := range s.runs {
		if s.runs[i].ID == id {
			return &s.runs[i], nil
		}
	}
	return nil, script.ErrRunNotFound
}

func (s *stubRuns) LatestRuns(_ context.Context, ids []string) (map[string]script.Run, error) {
	s.latestFor = ids
	return s.latest, s.latestErr
}

func (*stubRuns) Enqueue(context.Context, *script.Run) error { return nil }
func (*stubRuns) Claim(context.Context, string, time.Duration) (*script.Run, error) {
	return nil, script.ErrNoWork
}
func (*stubRuns) RecordOutput(context.Context, script.RunLease, script.RunOutput) error { return nil }
func (*stubRuns) Finish(context.Context, script.RunLease, script.RunResult) error       { return nil }
func (*stubRuns) Retry(context.Context, script.RunLease, string, time.Duration) error   { return nil }
func (*stubRuns) PurgeRuns(context.Context, time.Duration) (int64, error)               { return 0, nil }

// stubContracts serves the detail route's contract document.
type stubContracts struct {
	contract *script.Contract
	err      error
}

func (s *stubContracts) Contract(context.Context, string) (*script.Contract, error) {
	return s.contract, s.err
}

// portalStore returns a store holding two scripts: jane's, and one carol
// keeps. The pair is what separates the caller's own scripts from everybody
// else's, which is the whole of what the portal surface may show.
func portalStore() *stubStore {
	s := newStore()
	s.scripts = append(s.scripts, script.Script{
		ID: "script_2", Name: "carols-report",
		OwnerEmail: "carol@example.com", Enabled: true, Status: script.StatusActive,
	})
	return s
}

// portalDeps assembles the portal handler dependencies for one caller.
func portalDeps(store *stubStore, runs *stubRuns, contracts *stubContracts, user *PortalIdentity) Deps {
	deps := Deps{
		Scripts: store, Versions: store, Schedules: store,
		PortalUser: func(*http.Request) *PortalIdentity { return user },
	}
	if runs != nil {
		deps.Runs, deps.LatestRuns = runs, runs
	}
	if contracts != nil {
		deps.Contracts = contracts
	}
	return deps
}

// servePortal mounts the portal routes and runs one request against them.
func servePortal(t *testing.T, deps Deps, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(deps).RegisterPortal(mux, func(h http.Handler) http.Handler { return h })
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, strings.NewReader(""))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// decodeInto reads a typed JSON response body.
func decodeInto(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), out), rec.Body.String())
}

func TestPortalListScripts_ScopesToTheCaller(t *testing.T) {
	store := portalStore()
	rec := servePortal(t, portalDeps(store, nil, nil, owner), "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "jane@example.com", store.lastFilter.OwnerEmail)

	var body portalScriptListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 2)
	assert.True(t, body.Data[0].Owned, "jane owns her own script")
	assert.False(t, body.Data[1].Owned, "carol's script belongs to somebody else")
}

func TestPortalListScripts_AdminCarriesNoPredicate(t *testing.T) {
	store := portalStore()
	rec := servePortal(t, portalDeps(store, nil, nil, admin), "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Empty(t, store.lastFilter.OwnerEmail, "an administrator sees every script")

	var body portalScriptListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 2)
	assert.True(t, body.Data[0].Owned)
	assert.True(t, body.Data[1].Owned, "an administrator may read every script's runs")
}

func TestPortalListScripts_LastRunOnlyForOwnedScripts(t *testing.T) {
	store := portalStore()
	runs := &stubRuns{latest: map[string]script.Run{
		"script_1": {ID: "run_1", ScriptID: "script_1", Status: script.RunStatusFailed, Error: "boom"},
		"script_2": {ID: "run_2", ScriptID: "script_2", Status: script.RunStatusSucceeded},
	}}
	rec := servePortal(t, portalDeps(store, runs, nil, owner), "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, []string{"script_1"}, runs.latestFor,
		"a script the caller does not own is never even asked about")

	var body portalScriptListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 2)
	require.NotNil(t, body.Data[0].LastRun)
	assert.Equal(t, script.RunStatusFailed, body.Data[0].LastRun.Status)
	assert.Equal(t, "boom", body.Data[0].LastRun.Error)
	assert.Nil(t, body.Data[1].LastRun, "another owner's run state is not this caller's to read")
}

func TestPortalListScripts_CarriesTheCadence(t *testing.T) {
	store := portalStore()
	store.schedule = &script.Schedule{ID: "sched_1", ScriptID: "script_1", CronSpec: "0 7 * * *", Enabled: true}
	rec := servePortal(t, portalDeps(store, nil, nil, owner), "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code)

	var body portalScriptListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 2)
	require.NotNil(t, body.Data[0].Schedule)
	assert.Equal(t, "0 7 * * *", body.Data[0].Schedule.CronSpec)
	assert.Nil(t, body.Data[1].Schedule, "a script with no schedule reports none")
}

// A listing that cannot read the cadence or the last run is still the listing.
// Failing the page over either would take away the scripts as well.
func TestPortalListScripts_DegradesWhenTheExtrasFail(t *testing.T) {
	store := portalStore()
	store.scheduleErr = errors.New("schedules unavailable")
	runs := &stubRuns{latestErr: errors.New("runs unavailable")}
	rec := servePortal(t, portalDeps(store, runs, nil, owner), "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code)

	var body portalScriptListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 2)
	assert.Nil(t, body.Data[0].Schedule)
	assert.Nil(t, body.Data[0].LastRun)
}

func TestPortalListScripts_StoreFailure(t *testing.T) {
	store := portalStore()
	store.listErr = errors.New("boom")
	rec := servePortal(t, portalDeps(store, nil, nil, owner), "/api/v1/portal/scripts")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPortalRoutesRequireAuthentication(t *testing.T) {
	deps := portalDeps(portalStore(), &stubRuns{}, &stubContracts{}, nil)
	for _, path := range []string{
		"/api/v1/portal/scripts",
		"/api/v1/portal/scripts/script_1",
		"/api/v1/portal/scripts/script_1/versions",
		"/api/v1/portal/scripts/runs",
		"/api/v1/portal/scripts/script_1/runs",
		"/api/v1/portal/scripts/script_1/runs/run_1",
	} {
		rec := servePortal(t, deps, path)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, path)
	}
}

// A deployment with no portal identity accessor mounts no portal routes at
// all, rather than serving them to an unresolvable caller.
func TestPortalRoutesUnmountedWithoutAnIdentityAccessor(t *testing.T) {
	deps := portalDeps(portalStore(), &stubRuns{}, &stubContracts{}, nil)
	deps.PortalUser = nil
	rec := servePortal(t, deps, "/api/v1/portal/scripts")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalGetScript_ReturnsTheContract(t *testing.T) {
	contracts := &stubContracts{contract: &script.Contract{
		ID: "script_1", Name: "daily", OwnerEmail: "jane@example.com",
		Version: 3,
	}}
	rec := servePortal(t, portalDeps(portalStore(), nil, contracts, owner), "/api/v1/portal/scripts/script_1")
	require.Equal(t, http.StatusOK, rec.Code)

	var body portalScriptResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, "daily", body.Contract.Name)
	assert.Equal(t, 3, body.Contract.Version)
	assert.True(t, body.Owned)
}

// An administrator reads another person's script in full, which is the one way
// a caller who does not own a script reaches it.
func TestPortalGetScript_AdminReadsAnotherPersonsScript(t *testing.T) {
	contracts := &stubContracts{contract: &script.Contract{
		ID: "script_2", Name: "carols-report", OwnerEmail: "carol@example.com",
	}}
	rec := servePortal(t, portalDeps(portalStore(), nil, contracts, admin), "/api/v1/portal/scripts/script_2")
	require.Equal(t, http.StatusOK, rec.Code)

	var body portalScriptResponse
	decodeInto(t, rec, &body)
	assert.True(t, body.Owned)
}

func TestPortalGetScript_InvisibleAnswersAsMissing(t *testing.T) {
	contracts := &stubContracts{contract: &script.Contract{
		ID: "script_1", Name: "daily", OwnerEmail: "jane@example.com",
	}}
	rec := servePortal(t, portalDeps(portalStore(), nil, contracts, stranger), "/api/v1/portal/scripts/script_1")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), errScriptNot)
}

func TestPortalGetScript_NotFound(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, &stubContracts{}, owner), "/api/v1/portal/scripts/nope")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalGetScript_StoreFailure(t *testing.T) {
	contracts := &stubContracts{err: errors.New("boom")}
	rec := servePortal(t, portalDeps(portalStore(), nil, contracts, owner), "/api/v1/portal/scripts/script_1")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPortalGetScript_UnmountedWithoutAContractReader(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, nil, owner), "/api/v1/portal/scripts/script_1")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalListVersions_OwnerReadsTheSource(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, nil, owner), "/api/v1/portal/scripts/script_1/versions")
	require.Equal(t, http.StatusOK, rec.Code)

	var body versionListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 1)
	assert.Equal(t, reportSource, body.Data[0].Source)
}

// The source is the owner's and the administrator's. A caller who may see the
// script gets the same answer as one who may not: not found.
func TestPortalListVersions_RefusedForANonOwner(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, nil, stranger), "/api/v1/portal/scripts/script_2/versions")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), errScriptNot)
}

func TestPortalListVersions_AdminIsUnrestricted(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, nil, admin), "/api/v1/portal/scripts/script_1/versions")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestPortalListVersions_StoreFailure(t *testing.T) {
	store := portalStore()
	store.versionErr = errors.New("boom")
	rec := servePortal(t, portalDeps(store, nil, nil, owner), "/api/v1/portal/scripts/script_1/versions")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPortalListVersions_ScriptReadFailure(t *testing.T) {
	store := portalStore()
	store.getErr = errors.New("boom")
	rec := servePortal(t, portalDeps(store, nil, nil, owner), "/api/v1/portal/scripts/script_1/versions")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// An owner with no email cannot be matched by an unidentified caller: the
// empty-matches-empty hole the scope rule closes is closed here too.
func TestPortalListVersions_AnonymousOwnerIsNotEveryone(t *testing.T) {
	store := portalStore()
	store.scripts[0].OwnerEmail = ""
	rec := servePortal(t, portalDeps(store, nil, nil, &PortalIdentity{UserID: "u9"}),
		"/api/v1/portal/scripts/script_1/versions")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// The cross-script listing (#1405): an owner reads the runs of everything they
// own in one place, and the owned set is BOUND INTO the query rather than
// filtered out of the answer.
func TestPortalListOwnRuns_BindsTheCallersScripts(t *testing.T) {
	finished := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	runs := &stubRuns{runs: []script.Run{{
		ID: "run_1", ScriptID: "script_1", Version: 3, Status: script.RunStatusFailed,
		Trigger: script.TriggerSchedule, FinishedAt: &finished,
		Error:   "trino: table not found",
		Metrics: script.RunMetrics{DurationMS: 1840},
		Log:     "printed while working",
	}}}
	store := portalStore()
	store.scripts[0].DisplayName = "Daily Sales Report"
	rec := servePortal(t, portalDeps(store, runs, nil, owner), "/api/v1/portal/scripts/runs")
	require.Equal(t, http.StatusOK, rec.Code)

	// The script read carried the caller's visibility, and the run read carried
	// the scripts it returned.
	assert.Equal(t, "jane@example.com", store.lastFilter.OwnerEmail)
	assert.Equal(t, []string{"script_1", "script_2"}, runs.lastFilter.ScriptIDs)
	assert.Empty(t, runs.lastFilter.ScriptID, "the listing spans scripts")

	var body portalOwnRunsResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 1)
	assert.Equal(t, "script_1", body.Data[0].ScriptID)
	assert.Equal(t, "Daily Sales Report", body.Data[0].ScriptName)
	assert.Equal(t, "trino: table not found", body.Data[0].Error)
	assert.Equal(t, portalOwnRunsLimit, body.Limit)
	// The log is read one run at a time here too.
	assert.NotContains(t, rec.Body.String(), "printed while working")
}

// A script nobody has named is listed under the name it was created with.
func TestPortalListOwnRuns_FallsBackToTheScriptName(t *testing.T) {
	runs := &stubRuns{runs: []script.Run{{ID: "run_1", ScriptID: "script_1"}}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/runs")
	require.Equal(t, http.StatusOK, rec.Code)

	var body portalOwnRunsResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 1)
	assert.Equal(t, "daily", body.Data[0].ScriptName)
}

// A caller who owns no script must not fall through to an unfiltered listing:
// the empty set is bound, and it matches no run.
func TestPortalListOwnRuns_OwningNothingBindsAnEmptySet(t *testing.T) {
	store := portalStore()
	store.scripts = nil
	runs := &stubRuns{}
	rec := servePortal(t, portalDeps(store, runs, nil, owner), "/api/v1/portal/scripts/runs")
	require.Equal(t, http.StatusOK, rec.Code)

	require.NotNil(t, runs.lastFilter.ScriptIDs, "an unscoped filter would list every run")
	assert.Empty(t, runs.lastFilter.ScriptIDs)
}

// An administrator reads every run, which is the reach their script listing
// already has.
func TestPortalListOwnRuns_AdministratorIsUnscoped(t *testing.T) {
	runs := &stubRuns{}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, admin), "/api/v1/portal/scripts/runs")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Nil(t, runs.lastFilter.ScriptIDs)
	assert.Empty(t, runs.lastFilter.ScriptID)
}

// Naming one script narrows the listing to it (#1407), which is the run log a
// metric that names a script links to.
func TestPortalListOwnRuns_NarrowsToOneScript(t *testing.T) {
	runs := &stubRuns{}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, admin),
		"/api/v1/portal/scripts/runs?script_id=script_1")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "script_1", runs.lastFilter.ScriptID)
}

// The named script is ANDed with the visibility predicate rather than
// replacing it: naming somebody else's script must not read its runs.
func TestPortalListOwnRuns_ANamedScriptStaysInsideVisibility(t *testing.T) {
	runs := &stubRuns{}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner),
		"/api/v1/portal/scripts/runs?script_id=someone_elses_script")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "someone_elses_script", runs.lastFilter.ScriptID)
	assert.NotNil(t, runs.lastFilter.ScriptIDs)
	assert.NotContains(t, runs.lastFilter.ScriptIDs, "someone_elses_script")
}

// The status and the cap are the caller's to name, and the cap is the store's
// own ceiling however large a number is asked for.
func TestPortalListOwnRuns_StatusAndCap(t *testing.T) {
	runs := &stubRuns{}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner),
		"/api/v1/portal/scripts/runs?status=failed&per_page=500")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, script.RunStatusFailed, runs.lastFilter.Status)
	assert.Equal(t, portalOwnRunsLimit, runs.lastFilter.Limit)
}

func TestPortalListOwnRuns_StoreFailures(t *testing.T) {
	t.Run("the script listing", func(t *testing.T) {
		store := portalStore()
		store.listErr = errors.New("boom")
		rec := servePortal(t, portalDeps(store, &stubRuns{}, nil, owner), "/api/v1/portal/scripts/runs")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("the run listing", func(t *testing.T) {
		runs := &stubRuns{listErr: errors.New("boom")}
		rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/runs")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// The literal segment outranks the {id} wildcard, so a script whose id is
// "runs" cannot shadow the cross-script listing.
func TestPortalListOwnRuns_OutranksTheScriptWildcard(t *testing.T) {
	store := portalStore()
	store.scripts[0].ID = "runs"
	runs := &stubRuns{}
	rec := servePortal(t, portalDeps(store, runs, nil, owner), "/api/v1/portal/scripts/runs")
	require.Equal(t, http.StatusOK, rec.Code)

	var body portalOwnRunsResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, portalOwnRunsLimit, body.Limit, "the cross-script listing answered")
}

func TestPortalListRuns_ScopesToTheScript(t *testing.T) {
	finished := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	runs := &stubRuns{runs: []script.Run{{
		ID: "run_1", ScriptID: "script_1", Version: 3, Status: script.RunStatusSucceeded,
		Trigger: script.TriggerSchedule, FinishedAt: &finished,
		Metrics: script.RunMetrics{DurationMS: 1840},
		Log:     "printed while working",
		Outputs: []script.RunOutput{{Name: "daily", AssetID: "asset_1", AssetVersion: 4}},
	}}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner),
		"/api/v1/portal/scripts/script_1/runs?status=succeeded&per_page=5")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "script_1", runs.lastFilter.ScriptID)
	assert.Equal(t, script.RunStatusSucceeded, runs.lastFilter.Status)
	assert.Equal(t, 5, runs.lastFilter.Limit)

	var body portalRunListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 1)
	assert.Equal(t, int64(1840), body.Data[0].DurationMS)
	assert.Equal(t, 1, body.Data[0].OutputCount)
	// The log is read one run at a time, never fifty at once.
	assert.NotContains(t, rec.Body.String(), "printed while working")
}

func TestPortalListRuns_DefaultLimit(t *testing.T) {
	runs := &stubRuns{}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/script_1/runs")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, portalRunListLimit, runs.lastFilter.Limit)
}

func TestPortalListRuns_RefusedForANonOwner(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), &stubRuns{}, nil, stranger),
		"/api/v1/portal/scripts/script_2/runs")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalListRuns_StoreFailure(t *testing.T) {
	runs := &stubRuns{listErr: errors.New("boom")}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/script_1/runs")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPortalRunRoutesUnmountedWithoutRuns(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, nil, owner), "/api/v1/portal/scripts/script_1/runs")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalGetRun_CarriesTheLog(t *testing.T) {
	runs := &stubRuns{runs: []script.Run{{
		ID: "run_1", ScriptID: "script_1", Status: script.RunStatusSucceeded, Log: "printed while working",
	}}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner),
		"/api/v1/portal/scripts/script_1/runs/run_1")
	require.Equal(t, http.StatusOK, rec.Code)

	var run portalRunDetail
	decodeInto(t, rec, &run)
	assert.Equal(t, "printed while working", run.Log)
}

// A run id is unguessable, but unguessable is not an authorization rule: a run
// belonging to another script is not readable through a script the caller owns.
func TestPortalGetRun_RefusesARunOfAnotherScript(t *testing.T) {
	runs := &stubRuns{runs: []script.Run{{ID: "run_9", ScriptID: "script_2"}}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner),
		"/api/v1/portal/scripts/script_1/runs/run_9")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), errRunNot)
}

func TestPortalGetRun_NotFound(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), &stubRuns{}, nil, owner),
		"/api/v1/portal/scripts/script_1/runs/run_1")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalGetRun_StoreFailure(t *testing.T) {
	runs := &stubRuns{getErr: errors.New("boom")}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner),
		"/api/v1/portal/scripts/script_1/runs/run_1")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// Whoever asked for a run may read it back, whether or not they own the
// script: the result was handed to them when they requested it, so a run id
// they hold must stay followable.
func TestPortalGetRun_ReadableByWhoeverAskedForIt(t *testing.T) {
	runs := &stubRuns{runs: []script.Run{{
		ID: "run_2", ScriptID: "script_2", RequestedBy: "bob@example.com", Log: "printed while working",
	}}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, stranger),
		"/api/v1/portal/scripts/script_2/runs/run_2")
	require.Equal(t, http.StatusOK, rec.Code)

	var run portalRunDetail
	decodeInto(t, rec, &run)
	assert.Equal(t, "printed while working", run.Log)
}

func TestPortalGetRun_ScriptReadFailure(t *testing.T) {
	store := portalStore()
	store.getErr = errors.New("boom")
	rec := servePortal(t, portalDeps(store, &stubRuns{}, nil, owner),
		"/api/v1/portal/scripts/script_1/runs/run_1")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPortalGetRun_MissingScript(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), &stubRuns{}, nil, owner),
		"/api/v1/portal/scripts/nope/runs/run_1")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A caller the identity provider named but issued no email for is still a
// distinct person: the portal compares owners on the same identity the script
// tool records, so two of them are not one shared owner.
func TestPortalIdentity_EmaillessCallersAreDistinct(t *testing.T) {
	store := portalStore()
	store.scripts[0].OwnerEmail = "oidc|sarah"
	sarah := &PortalIdentity{UserID: "oidc|sarah", Persona: "analyst"}
	marcus := &PortalIdentity{UserID: "oidc|marcus", Persona: "analyst"}

	rec := servePortal(t, portalDeps(store, nil, nil, sarah), "/api/v1/portal/scripts/script_1/versions")
	assert.Equal(t, http.StatusOK, rec.Code, "the owner reads their own script")

	rec = servePortal(t, portalDeps(store, nil, nil, marcus), "/api/v1/portal/scripts/script_1/versions")
	assert.Equal(t, http.StatusNotFound, rec.Code, "another email-less caller is not the same person")

	// And the listing scopes on that identity rather than on an empty string.
	servePortal(t, portalDeps(store, nil, nil, sarah), "/api/v1/portal/scripts")
	assert.Equal(t, "oidc|sarah", store.lastFilter.OwnerEmail)
}

// A caller the platform cannot name at all owns nothing here: an empty
// identity must not match an owner the store could not establish either.
func TestPortalIdentity_UnnamedCallerOwnsNothing(t *testing.T) {
	store := portalStore()
	store.scripts[0].OwnerEmail = ""
	rec := servePortal(t, portalDeps(store, nil, nil, &PortalIdentity{Persona: "analyst"}),
		"/api/v1/portal/scripts/script_1/versions")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalGetRun_RefusedForANonOwner(t *testing.T) {
	runs := &stubRuns{runs: []script.Run{{ID: "run_2", ScriptID: "script_2"}}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, stranger),
		"/api/v1/portal/scripts/script_2/runs/run_2")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestPortalGetScript_CarriesTheLiveParameterContractForTheOwner is the pair
// the dry-run form is built from (#1364). The contract's parameters come from
// the contract document; DraftParams are read with the source, so the dry-run
// form binds against exactly the contract the code beside it was written
// against.
func TestPortalGetScript_CarriesTheLiveParameterContractForTheOwner(t *testing.T) {
	store := portalStore()
	store.scripts[1].Source = "x = 1\n"
	store.scripts[1].Params = []script.Param{{Name: "region", Type: script.ParamTypeString}}
	contracts := &stubContracts{contract: &script.Contract{
		ID: "script_2", Name: "carols-report",
		OwnerEmail: "carol@example.com",
		Params:     []script.Param{{Name: "report_date", Type: script.ParamTypeDate, Required: true}},
	}}

	rec := servePortal(t, portalDeps(store, nil, contracts, carol), "/api/v1/portal/scripts/script_2")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body portalScriptResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, "x = 1\n", body.Source)
	require.Len(t, body.DraftParams, 1)
	assert.Equal(t, "region", body.DraftParams[0].Name)
	require.Len(t, body.Contract.Params, 1)
	assert.Equal(t, "report_date", body.Contract.Params[0].Name,
		"the contract document's parameters pass through unchanged")
}

// TestPortalGetScript_WithholdsAnotherPersonsScriptEntirely keeps every half on
// the same side of the line: a caller who does not own a script learns nothing
// about it, not its contract, its code, or that it exists.
func TestPortalGetScript_WithholdsAnotherPersonsScriptEntirely(t *testing.T) {
	store := portalStore()
	store.scripts[1].Source = "x = 1\n"
	store.scripts[1].Params = []script.Param{{Name: "region", Type: script.ParamTypeString}}
	contracts := &stubContracts{contract: &script.Contract{
		ID: "script_2", Name: "carols-report",
		OwnerEmail: "carol@example.com",
	}}

	rec := servePortal(t, portalDeps(store, nil, contracts, stranger), "/api/v1/portal/scripts/script_2")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), errScriptNot)
}
