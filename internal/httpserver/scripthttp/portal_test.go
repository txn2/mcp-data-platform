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
	// canceled records CancelRun calls; cancelPrior and cancelErr answer
	// them (#1847).
	canceled    []string
	cancelPrior string
	cancelErr   error
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
func (*stubRuns) Claim(context.Context, string, time.Duration, int) (*script.Run, error) {
	return nil, script.ErrNoWork
}
func (*stubRuns) FailAbandoned(context.Context, int) ([]script.Run, error)              { return nil, nil }
func (*stubRuns) RecordOutput(context.Context, script.RunLease, script.RunOutput) error { return nil }
func (*stubRuns) Finish(context.Context, script.RunLease, script.RunResult) error       { return nil }
func (*stubRuns) Retry(context.Context, script.RunLease, string, string, time.Duration) error {
	return nil
}
func (*stubRuns) PurgeRuns(context.Context, time.Duration) (int64, error) { return 0, nil }
func (*stubRuns) RecordProgress(context.Context, script.RunLease, script.RunLive) (requested bool, by string, err error) {
	return false, "", nil
}

// CancelRun records the request and answers with the configured outcome.
func (s *stubRuns) CancelRun(_ context.Context, id, by string) (prior, now string, err error) {
	s.canceled = append(s.canceled, id+" by "+by)
	return s.cancelPrior, s.cancelPrior, s.cancelErr
}

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
		Source: carolsSource,
	})
	return s
}

// carolsSource is the saved source of carol's script.
const carolsSource = "def main():\n" +
	"    \"\"\"Exports the daily rows.\"\"\"\n" +
	"    res = platform.query(connection = \"warehouse\", sql = \"SELECT 1\")\n" +
	"    platform.export(name = \"daily\", rows = res[\"rows\"])\n"

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

// TestPortalListScripts_ListsEveryScriptByDefault pins #1994: a reader opens
// on every script, the ones built for them included, and scope=mine narrows
// to their own.
func TestPortalListScripts_ListsEveryScriptByDefault(t *testing.T) {
	store := portalStore()
	rec := servePortal(t, portalDeps(store, nil, nil, owner), "/api/v1/portal/scripts?scope=mine")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "jane@example.com", store.lastFilter.OwnerEmail)

	store = portalStore()
	rec = servePortal(t, portalDeps(store, nil, nil, owner), "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, store.lastFilter.OwnerEmail, "every script by default")

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

// TestPortalListScripts_EveryRowCarriesItsLastRun pins #1994: every row says
// how its last run went, and a row the caller does not own carries neither who
// requested it nor the progress the script reported.
func TestPortalListScripts_EveryRowCarriesItsLastRun(t *testing.T) {
	store := portalStore()
	progress := &script.RunProgress{Message: "page 3"}
	runs := &stubRuns{latest: map[string]script.Run{
		"script_1": {ID: "run_1", ScriptID: "script_1", Status: script.RunStatusFailed, Error: "boom"},
		"script_2": {
			ID: "run_2", ScriptID: "script_2", Status: script.RunStatusFailed, Error: "upstream down",
			Cause: "upstream", RequestedBy: "carol@example.com", Progress: progress,
		},
	}}
	rec := servePortal(t, portalDeps(store, runs, nil, owner), "/api/v1/portal/scripts")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.ElementsMatch(t, []string{"script_1", "script_2"}, runs.latestFor)

	var body portalScriptListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 2)
	require.NotNil(t, body.Data[0].LastRun)
	assert.Equal(t, "boom", body.Data[0].LastRun.Error)
	theirs := body.Data[1].LastRun
	require.NotNil(t, theirs, "another owner's run status is readable")
	assert.Equal(t, script.RunStatusFailed, theirs.Status)
	assert.Equal(t, "upstream down", theirs.Error)
	assert.Equal(t, "upstream", theirs.Cause)
	assert.True(t, theirs.Retryable)
	assert.Empty(t, theirs.RequestedBy, "who asked for another owner's run is withheld")
	assert.Nil(t, theirs.Progress, "the script's own progress lines are withheld")
	assert.Equal(t, 2, body.Failing, "failing counts the listed rows")
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

// TestPortalGetScript_MissingIsMissing keeps the not-found answer for the one
// thing that is actually not there. A script that EXISTS and is somebody
// else's is readable in the projection the listing already applies (#1795);
// only an id that names no script is a 404.
func TestPortalGetScript_MissingIsMissing(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, &stubContracts{}, stranger), "/api/v1/portal/scripts/script_1")
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

// TestPortalGetScript_UnmountedWithoutAContractReader holds the shape of a
// deployment that cannot compose a contract: reading one script is not served.
//
// The answer is method-not-allowed rather than not-found because the delete
// route (#1575) is mounted at this same address and is served, so the address
// exists on this deployment while GET on it does not. That is what ServeMux
// reports, and it discloses nothing: every id answers identically, whether or
// not a script bears it.
func TestPortalGetScript_UnmountedWithoutAContractReader(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, nil, owner), "/api/v1/portal/scripts/script_1")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestPortalListVersions_OwnerReadsTheSource(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, nil, owner), "/api/v1/portal/scripts/script_1/versions")
	require.Equal(t, http.StatusOK, rec.Code)

	var body versionListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 1)
	assert.Equal(t, reportSource, body.Data[0].Source)
}

// versionRoles reads the author roles each version of script_1 carried for
// who.
func versionRoles(t *testing.T, store *stubStore, who *PortalIdentity) [][]string {
	t.Helper()
	rec := servePortal(t, portalDeps(store, nil, nil, who), "/api/v1/portal/scripts/script_1/versions")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body versionListResponse
	decodeInto(t, rec, &body)
	roles := make([][]string, 0, len(body.Data))
	for _, v := range body.Data {
		roles = append(roles, v.AuthorRoles)
	}
	return roles
}

// The history is the definition over time, readable by everyone (#1866); the
// roles each version's author held are that person's, shown to the owner and
// an administrator only.
func TestPortalListVersions_ReadableByEveryoneWithoutTheAuthorsRoles(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, nil, stranger), "/api/v1/portal/scripts/script_1/versions")
	require.Equal(t, http.StatusOK, rec.Code)
	var body versionListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 1)
	assert.Equal(t, reportSource, body.Data[0].Source, "the source is the definition")
	assert.Empty(t, body.Data[0].AuthorRoles, "the roles are the author's")

	assert.Equal(t, [][]string{{"analyst"}}, versionRoles(t, portalStore(), owner))
	assert.Equal(t, [][]string{{"analyst"}}, versionRoles(t, portalStore(), admin))

	rec = servePortal(t, portalDeps(portalStore(), nil, nil, stranger), "/api/v1/portal/scripts/nope/versions")
	assert.Equal(t, http.StatusNotFound, rec.Code)
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
// empty-matches-empty hole the scope rule closes is closed here too, so the
// caller reads the history as a stranger does.
func TestPortalListVersions_AnonymousOwnerIsNotEveryone(t *testing.T) {
	store := portalStore()
	store.scripts[0].OwnerEmail = ""
	assert.Equal(t, [][]string{nil}, versionRoles(t, store, &PortalIdentity{UserID: "u9"}))
}

// The cross-script listing (#1405) narrowed to the caller's own: the owned
// set is BOUND INTO the query rather than filtered out of the answer.
func TestPortalListOwnRuns_ScopeMineBindsTheCallersScripts(t *testing.T) {
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
	rec := servePortal(t, portalDeps(store, runs, nil, owner), "/api/v1/portal/scripts/runs?scope=mine")
	require.Equal(t, http.StatusOK, rec.Code)

	// The script read carried the caller's scope, and the run read carried
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

// A caller who owns no script and asks for their own must not fall through
// to an unfiltered listing: the empty set is bound, and it matches no run.
func TestPortalListOwnRuns_OwningNothingBindsAnEmptySet(t *testing.T) {
	store := portalStore()
	store.scripts = nil
	runs := &stubRuns{}
	rec := servePortal(t, portalDeps(store, runs, nil, owner), "/api/v1/portal/scripts/runs?scope=mine")
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

// The named script is ANDed with the scope rather than replacing it: under
// scope=mine, naming somebody else's script reads none of its runs.
func TestPortalListOwnRuns_ANamedScriptStaysInsideTheScope(t *testing.T) {
	runs := &stubRuns{}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner),
		"/api/v1/portal/scripts/runs?scope=mine&script_id=someone_elses_script")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "someone_elses_script", runs.lastFilter.ScriptID)
	assert.NotNil(t, runs.lastFilter.ScriptIDs)
	assert.NotContains(t, runs.lastFilter.ScriptIDs, "someone_elses_script")
}

// TestPortalListOwnRuns_EveryReaderSeesEveryScriptsRuns pins #1994: the Runs
// tab spans every script by default. A run of a script the caller does not
// own says how it went and not who asked for it, unless the caller did.
func TestPortalListOwnRuns_EveryReaderSeesEveryScriptsRuns(t *testing.T) {
	runs := &stubRuns{runs: []script.Run{
		{
			ID: "run_c", ScriptID: "script_2", Status: script.RunStatusFailed, Error: "boom", RequestedBy: "carol@example.com",
			Progress: &script.RunProgress{Message: "page 3"},
		},
		{ID: "run_j", ScriptID: "script_2", Status: script.RunStatusSucceeded, RequestedBy: "jane@example.com"},
	}}
	store := portalStore()
	rec := servePortal(t, portalDeps(store, runs, nil, owner), "/api/v1/portal/scripts/runs")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Empty(t, store.lastFilter.OwnerEmail)
	assert.Nil(t, runs.lastFilter.ScriptIDs, "every script's runs")

	var body portalOwnRunsResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 2)
	assert.Equal(t, "boom", body.Data[0].Error)
	assert.Empty(t, body.Data[0].RequestedBy, "who asked for another owner's run is withheld")
	assert.Nil(t, body.Data[0].Progress)
	assert.Equal(t, "jane@example.com", body.Data[1].RequestedBy, "a run the caller requested is theirs to read in full")
	assert.NotContains(t, rec.Body.String(), "page 3")
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

// TestPortalListRuns_ReadableByEveryReader pins #1994: a script's run
// history is readable by everyone signed in, without who requested a run
// they did not request.
func TestPortalListRuns_ReadableByEveryReader(t *testing.T) {
	runs := &stubRuns{runs: []script.Run{{
		ID: "run_2", ScriptID: "script_2", Status: script.RunStatusFailed,
		Error: "boom", RequestedBy: "carol@example.com",
	}}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, stranger), "/api/v1/portal/scripts/script_2/runs")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "script_2", runs.lastFilter.ScriptID)

	var body portalRunListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 1)
	assert.Equal(t, "boom", body.Data[0].Error)
	assert.Empty(t, body.Data[0].RequestedBy)

	rec = servePortal(t, portalDeps(portalStore(), runs, nil, stranger), "/api/v1/portal/scripts/nope/runs")
	assert.Equal(t, http.StatusNotFound, rec.Code, "a script that does not exist has no history")
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

	assert.Equal(t, [][]string{{"analyst"}}, versionRoles(t, store, sarah), "the owner reads their own script as its owner")
	assert.Equal(t, [][]string{nil}, versionRoles(t, store, marcus), "another email-less caller is not the same person")

	// And the listing scopes on that identity rather than on an empty string.
	servePortal(t, portalDeps(store, nil, nil, sarah), "/api/v1/portal/scripts?scope=mine")
	assert.Equal(t, "oidc|sarah", store.lastFilter.OwnerEmail)
}

// A caller the platform cannot name at all owns nothing here: an empty
// identity must not match an owner the store could not establish either.
func TestPortalIdentity_UnnamedCallerOwnsNothing(t *testing.T) {
	store := portalStore()
	store.scripts[0].OwnerEmail = ""
	assert.Equal(t, [][]string{nil}, versionRoles(t, store, &PortalIdentity{Persona: "analyst"}))
}

// TestPortalGetRun_AReaderGetsHowItWentAndNothingItWasGiven pins #1994: a
// reader who neither owns the script nor requested the run reads its status,
// timing, cause and error, and not its parameters, log, outputs, state or
// result.
func TestPortalGetRun_AReaderGetsHowItWentAndNothingItWasGiven(t *testing.T) {
	runs := &stubRuns{runs: []script.Run{{
		ID: "run_2", ScriptID: "script_2", Status: script.RunStatusFailed, Error: "Trino could not be reached",
		Cause: "upstream", RequestedBy: "carol@example.com", Log: "printed while working",
		Params: map[string]any{"region": "west"}, StateRead: map[string]any{"through": "2026-09-01"},
		Outputs: []script.RunOutput{{Name: "daily"}}, Result: json.RawMessage(`{"rows":3}`),
		Metrics: script.RunMetrics{DurationMS: 1840},
	}}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, stranger),
		"/api/v1/portal/scripts/script_2/runs/run_2")
	require.Equal(t, http.StatusOK, rec.Code)

	var run portalRunDetail
	decodeInto(t, rec, &run)
	assert.True(t, run.Withheld)
	assert.Equal(t, script.RunStatusFailed, run.Status)
	assert.Equal(t, "Trino could not be reached", run.Error)
	assert.True(t, run.Retryable)
	assert.Equal(t, int64(1840), run.DurationMS)
	assert.Empty(t, run.RequestedBy)
	for _, withheld := range []string{"printed while working", "west", "through", "daily", `"rows"`} {
		assert.NotContains(t, rec.Body.String(), withheld)
	}
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

// TestPortalGetScript_ShowsAnotherPersonsCode draws the line where #1866 put
// it: a caller who does not own a script reads its definition -- whose it is,
// what it says about itself, its code and the parameters its code declares --
// and owned is false, which is what closes every action on the page.
func TestPortalGetScript_ShowsAnotherPersonsCode(t *testing.T) {
	store := portalStore()
	store.scripts[1].Source = "x = 1\n"
	store.scripts[1].Params = []script.Param{{Name: "region", Type: script.ParamTypeString}}
	contracts := &stubContracts{contract: &script.Contract{
		ID: "script_2", Name: "carols-report",
		OwnerEmail: "carol@example.com",
		LastRun:    &script.ContractRun{Version: 1, Outputs: []script.ContractOutput{{Name: "carols-private-report"}}},
		State:      &script.ContractState{Reads: true, Saves: true, Revision: 4},
	}}

	rec := servePortal(t, portalDeps(store, nil, contracts, stranger), "/api/v1/portal/scripts/script_2")

	require.Equal(t, http.StatusOK, rec.Code)
	var seen portalScriptResponse
	decodeInto(t, rec, &seen)
	assert.Equal(t, "carols-report", seen.Contract.Name)
	assert.Equal(t, "carol@example.com", seen.Contract.OwnerEmail)
	assert.False(t, seen.Owned)
	assert.Equal(t, "x = 1\n", seen.Source, "the code is the definition")
	require.Len(t, seen.DraftParams, 1)
	assert.Equal(t, "region", seen.DraftParams[0].Name)
	// A run's outputs name assets that may not be shared with this reader, and
	// the state is acting-side reading (#2027).
	assert.NotContains(t, rec.Body.String(), "carols-private-report")
	assert.Nil(t, seen.Contract.LastRun)
	assert.True(t, seen.Contract.RunsWithheld)
	require.NotNil(t, seen.Contract.State)
	assert.Zero(t, seen.Contract.State.Revision)
	assert.True(t, seen.Contract.State.Reads, "what the source does with state is the definition")

	rec = servePortal(t, portalDeps(store, nil, contracts, admin), "/api/v1/portal/scripts/script_2")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "carols-private-report", "an administrator reads it whole")
}

// TestActsOnScript is the reader of what acting on a script shows: its owner
// and an administrator, and not another person or an unidentified caller.
func TestActsOnScript(t *testing.T) {
	store := portalStore()
	id := store.scripts[0].ID
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	owner := &PortalIdentity{UserID: "u1", Email: store.scripts[0].OwnerEmail}
	for who, want := range map[*PortalIdentity]bool{owner: true, admin: true, stranger: false} {
		h := New(portalDeps(store, nil, nil, who))
		assert.Equal(t, want, h.ActsOnScript(req, who, id), who.Email)
	}
	h := New(portalDeps(store, nil, nil, admin))
	assert.False(t, h.ActsOnScript(req, nil, id), "an unidentified caller acts on nothing")
	assert.False(t, h.ActsOnScript(req, admin, "missing"), "a script that cannot be read answers false")
}
