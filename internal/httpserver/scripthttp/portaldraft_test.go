package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Checking an edit before saving it (#1364). Validate reports and executes
// nothing; a dry run executes as the caller and persists nothing.
// The assertions here are about what identity the run carried, what it was
// bound against, and that the account kept of it names the source that ran.

const (
	validatePath = "/api/v1/portal/scripts/script_2/validate"
	dryRunPath   = "/api/v1/portal/scripts/script_2/dry-run"
)

// draftSource is valid Starlark reaching for one connection and one export, so
// a validate report has something to report.
const draftSource = "res = platform.query(connection=\"warehouse\", sql=\"SELECT 1\")\n" +
	"platform.export(name=\"daily\", rows=res[\"rows\"])\n"

// fakeRunner is the draft runner the route calls. It records the request so a
// test asserts on the identity and the source the run carried rather than on a
// status code that would pass either way.
type fakeRunner struct {
	got     scriptdraft.Request
	outcome *scriptdraft.Outcome
	err     error
}

func (f *fakeRunner) Run(_ context.Context, req scriptdraft.Request) (*scriptdraft.Outcome, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return f.outcome, nil
}

// recordingDryRuns is the account store, which the route writes to and the
// version detail reads from.
type recordingDryRuns struct {
	recorded  *script.DryRun
	latest    *script.DryRun
	askedFor  []byte
	recordErr error
	latestErr error
}

func (r *recordingDryRuns) RecordDryRun(_ context.Context, d *script.DryRun) error {
	if r.recordErr != nil {
		return r.recordErr
	}
	r.recorded = d
	return nil
}

func (r *recordingDryRuns) LatestDryRun(_ context.Context, _ string, digest []byte) (*script.DryRun, error) {
	r.askedFor = digest
	return r.latest, r.latestErr
}

// okOutcome is a draft that ran and produced one preview.
func okOutcome() *scriptdraft.Outcome {
	return &scriptdraft.Outcome{
		RunID: "run_draft_1",
		Result: &scriptrun.Result{
			Log: "done", Steps: 42, Duration: 250 * time.Millisecond, Queries: 1,
			Exports: []scriptrun.ExportRecord{{
				Name: "daily", Destination: "portal", Format: "csv",
				RowCount: 12, Bytes: 300, Preview: true,
			}},
		},
	}
}

// draftDeps assembles the portal deps with a draft runner and an account store.
func draftDeps(store *stubStore, user *PortalIdentity) (Deps, *fakeRunner, *recordingDryRuns) {
	runner := &fakeRunner{outcome: okOutcome()}
	accounts := &recordingDryRuns{}
	deps := portalDeps(store, nil, nil, user)
	deps.Drafts = runner
	deps.DryRuns = accounts
	return deps, runner, accounts
}

// draftBody is a source-and-parameters request body.
func draftBody(source string) string {
	return `{"source":` + strconv.Quote(source) + `}`
}

func TestPortalValidateSource_ReportsWhatTheEditReaches(t *testing.T) {
	deps, _, _ := draftDeps(portalStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, validatePath, draftBody(draftSource))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body validateResponse
	decodeInto(t, rec, &body)
	assert.True(t, body.OK)
	assert.Contains(t, body.Connections, "warehouse")
	assert.Contains(t, body.Capabilities, scriptrun.CapabilityQuery)
	assert.Contains(t, body.Capabilities, scriptrun.CapabilityExport)
	assert.Empty(t, body.Note, "nothing about this source is computed")
}

// TestPortalValidateSource_ReportsSourceThatDoesNotParse is the answer an author
// gets instead of a save they would have had to undo.
func TestPortalValidateSource_ReportsSourceThatDoesNotParse(t *testing.T) {
	deps, _, _ := draftDeps(portalStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, validatePath,
		draftBody("def f(:\n"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body validateResponse
	decodeInto(t, rec, &body)
	assert.False(t, body.OK)
	assert.NotEmpty(t, body.Findings, "a refusal with no finding tells the author nothing")
}

// TestPortalValidateSource_SaysWhenAListIsIncomplete keeps the report honest: a
// computed connection is not in the list, and a reader must be told that.
func TestPortalValidateSource_SaysWhenAListIsIncomplete(t *testing.T) {
	deps, _, _ := draftDeps(portalStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, validatePath,
		draftBody("c = run.params[\"source\"]\nplatform.query(connection=c, sql=\"SELECT 1\")\n"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body validateResponse
	decodeInto(t, rec, &body)
	assert.True(t, body.DynamicConnections)
	assert.Contains(t, body.Note, "incomplete")
}

// TestPortalValidateSource_FallsBackToTheSavedSource lets an author check code
// they have not edited, which is what opening somebody else's script and
// pressing the button does.
func TestPortalValidateSource_FallsBackToTheSavedSource(t *testing.T) {
	store := portalStore()
	store.scripts[1].Source = draftSource
	deps, _, _ := draftDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, validatePath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body validateResponse
	decodeInto(t, rec, &body)
	assert.Contains(t, body.Connections, "warehouse")
}

func TestPortalValidateSource_RefusesACallerWhoDoesNotOwnIt(t *testing.T) {
	deps, _, _ := draftDeps(portalStore(), stranger)
	rec := servePortalRequest(t, deps, http.MethodPost, validatePath, draftBody(draftSource))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestPortalDryRunSource_RunsAsTheCallerAndPersistsNothing is the property the
// whole route rests on: no new authority, because the run is the caller's own
// session.
func TestPortalDryRunSource_RunsAsTheCallerAndPersistsNothing(t *testing.T) {
	deps, runner, _ := draftDeps(portalStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, carol.UserID, runner.got.Identity.UserID)
	assert.Equal(t, carol.Email, runner.got.Identity.Email)
	assert.Equal(t, carol.Roles, runner.got.Identity.Roles)
	assert.Equal(t, carol.AuthType, runner.got.Identity.AuthType,
		"the session must present the authentication the request arrived with")
	assert.Equal(t, draftSource, runner.got.Source)

	var body dryRunResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, script.RunStatusSucceeded, body.Status)
	assert.Equal(t, "run_draft_1", body.RunID)
	assert.Equal(t, uint64(42), body.Metrics.Steps)
	assert.Equal(t, int64(250), body.Metrics.DurationMS)
	require.Len(t, body.Outputs, 1)
	assert.Equal(t, "daily", body.Outputs[0].Name)
	assert.Equal(t, 12, body.Outputs[0].RowCount)
	assert.Contains(t, body.Message, "Nothing was persisted")
}

// TestPortalDryRunSource_ReportsAFailedRunWithItsLog keeps the reason to have
// run it: a failure answers with everything a success answers with.
func TestPortalDryRunSource_ReportsAFailedRunWithItsLog(t *testing.T) {
	deps, runner, accounts := draftDeps(portalStore(), carol)
	runner.outcome = &scriptdraft.Outcome{
		RunID:  "run_draft_2",
		Result: &scriptrun.Result{Log: "half way", LogTruncated: true},
		Err:    errors.New("no such column: regoin"),
	}
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body dryRunResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, script.RunStatusFailed, body.Status)
	assert.Contains(t, body.Error, "regoin")
	assert.Equal(t, "half way", body.Log)
	assert.True(t, body.LogTruncated)
	assert.Contains(t, body.Message, "deterministic")

	require.NotNil(t, accounts.recorded, "a failed dry run is still an account of one")
	assert.Equal(t, script.RunStatusFailed, accounts.recorded.Status)
}

// TestPortalDryRunSource_RecordsTheAccountAgainstTheSourceThatRan is what makes
// the reviewer's lookup work: the account is keyed by the code, so it attaches
// to whichever version later carries it.
func TestPortalDryRunSource_RecordsTheAccountAgainstTheSourceThatRan(t *testing.T) {
	deps, _, accounts := draftDeps(portalStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, accounts.recorded)
	assert.Equal(t, script.SourceDigest(draftSource), accounts.recorded.SourceSHA256)
	assert.Equal(t, "script_2", accounts.recorded.ScriptID)
	assert.Equal(t, "run_draft_1", accounts.recorded.ID)
	assert.Equal(t, "carol@example.com", accounts.recorded.RequestedBy)
	require.Len(t, accounts.recorded.Outputs, 1)
	assert.Equal(t, 300, accounts.recorded.Outputs[0].Bytes)
}

// TestPortalDryRunSource_AnswersWhenTheAccountCannotBeKept keeps the priority
// right: the run happened and its result is what the author asked for.
func TestPortalDryRunSource_AnswersWhenTheAccountCannotBeKept(t *testing.T) {
	deps, _, accounts := draftDeps(portalStore(), carol)
	accounts.recordErr = errors.New("the account store is unavailable")
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, accounts.recorded)
}

// TestPortalDryRunSource_BindsAgainstTheLiveContract pins which contract a
// draft binds against: the live record's, which is the contract the code
// being run was written against.
func TestPortalDryRunSource_BindsAgainstTheLiveContract(t *testing.T) {
	store := portalStore()
	store.scripts[1].Params = []script.Param{
		{Name: "region", Type: script.ParamTypeString, Required: true},
	}
	deps, runner, _ := draftDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath,
		`{"source":`+strconv.Quote(draftSource)+`,"params":{"region":"west"}}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "west", runner.got.Params["region"])
}

func TestPortalDryRunSource_RefusesAValueTheContractRejects(t *testing.T) {
	store := portalStore()
	store.scripts[1].Params = []script.Param{
		{Name: "day", Type: script.ParamTypeDate, Required: true},
	}
	deps, runner, _ := draftDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath,
		`{"source":`+strconv.Quote(draftSource)+`,"params":{"day":"yesterday"}}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, runner.got.Source, "nothing should have been executed")
}

// TestPortalDryRunSource_RefusesSourceThatDoesNotParse stops the interpreter
// being asked a question the parser already answered.
func TestPortalDryRunSource_RefusesSourceThatDoesNotParse(t *testing.T) {
	deps, runner, _ := draftDeps(portalStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody("def f(:\n"))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "was not run")
	assert.Contains(t, rec.Body.String(), "want ')'", "the refusal carries the parser's own finding")
	assert.Empty(t, runner.got.Source)
}

// TestPortalDryRunSource_RefusesADisabledScript is the same refusal the tool
// surface gives, in the same words: "disabled" must disable the draft path
// too, or the word means less than it says.
func TestPortalDryRunSource_RefusesADisabledScript(t *testing.T) {
	store := portalStore()
	store.scripts[1].Enabled = false
	deps, runner, _ := draftDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "disabled")
	assert.Empty(t, runner.got.Source)
}

func TestPortalDryRunSource_ReportsARunnerThatCouldNotStart(t *testing.T) {
	deps, runner, _ := draftDeps(portalStore(), carol)
	runner.err = errors.New("script execution is unavailable on this deployment")
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "unavailable")
}

func TestPortalDryRunSource_RefusesACallerWhoDoesNotOwnIt(t *testing.T) {
	deps, runner, _ := draftDeps(portalStore(), stranger)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, runner.got.Source)
}

func TestPortalDryRunSource_RefusesAnUnreadableBody(t *testing.T) {
	deps, runner, _ := draftDeps(portalStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, `{"source":`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, runner.got.Source)
}

// TestPortalDryRun_IsUnmountedWithoutARunner leaves validate available: parsing
// needs nothing but the source, and a deployment that cannot execute a draft
// can still tell an author whether their edit compiles.
func TestPortalDryRun_IsUnmountedWithoutARunner(t *testing.T) {
	deps := portalDeps(portalStore(), nil, nil, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "about:blank")

	rec = servePortalRequest(t, deps, http.MethodPost, validatePath, draftBody(draftSource))
	assert.Equal(t, http.StatusOK, rec.Code, "validate must survive a runnerless deployment")
}

// TestGetVersion_CarriesTheDryRunAccount is the reader's half of #1364: the
// account is looked up by the version's own source, and its ABSENCE is the
// answer when nobody ran it.
func TestGetVersion_CarriesTheDryRunAccount(t *testing.T) {
	store := newStore()
	accounts := &recordingDryRuns{latest: &script.DryRun{
		ID: "run_draft_9", Status: script.RunStatusSucceeded, RequestedBy: "jane@example.com",
	}}
	rec := serveVersionReview(t, store, accounts)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body versionDetailResponse
	decodeInto(t, rec, &body)
	require.NotNil(t, body.DryRun)
	assert.Equal(t, "run_draft_9", body.DryRun.ID)
	assert.Equal(t, script.SourceDigest(store.version.Source), accounts.askedFor,
		"the account must be matched against THIS version's source")
}

func TestGetVersion_ReportsNoAccountWhenNobodyRanIt(t *testing.T) {
	rec := serveVersionReview(t, newStore(), &recordingDryRuns{})

	require.Equal(t, http.StatusOK, rec.Code)
	var body versionDetailResponse
	decodeInto(t, rec, &body)
	assert.Nil(t, body.DryRun)
}

// TestGetVersion_SurvivesAnUnreadableAccount keeps the decision surface up: the
// account is a decoration on a decision the rest of the payload supports.
func TestGetVersion_SurvivesAnUnreadableAccount(t *testing.T) {
	accounts := &recordingDryRuns{latestErr: errors.New("unavailable")}
	rec := serveVersionReview(t, newStore(), accounts)

	require.Equal(t, http.StatusOK, rec.Code)
	var body versionDetailResponse
	decodeInto(t, rec, &body)
	assert.Nil(t, body.DryRun)
}

// serveVersionReview runs the admin version detail route with an account store.
func serveVersionReview(t *testing.T, store *stubStore, accounts script.DryRunStore) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(Deps{
		Scripts: store, Versions: store, Schedules: store, DryRuns: accounts,
		AdminEmail: func(*http.Request) string { return "admin@example.com" },
	}).RegisterAdmin(mux, "/api/v1/admin", func(h http.Handler) http.Handler { return h })
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/admin/scripts/script_1/versions/1", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestIncompleteNote covers every shape of the gap statement. The note exists
// because a list that silently omitted a computed name would be a false
// statement, so which gap is named has to be right in all four cases.
func TestIncompleteNote(t *testing.T) {
	tests := []struct {
		name   string
		report scriptrun.Report
		want   string
	}{
		{name: "nothing computed", report: scriptrun.Report{}, want: ""},
		{
			name:   "a computed connection",
			report: scriptrun.Report{DynamicConnections: true},
			want:   "the connection list is incomplete",
		},
		{
			name:   "a computed destination",
			report: scriptrun.Report{DynamicDestinations: true},
			want:   "the destination list is incomplete",
		},
		{
			name:   "both, each gap named rather than collapsed into one",
			report: scriptrun.Report{DynamicConnections: true, DynamicDestinations: true},
			want:   "the connection list is incomplete; and at least one platform.export",
		},
		{
			name:   "a computed refresh target",
			report: scriptrun.Report{DynamicRefreshTargets: true},
			want:   "the refresh-target list is incomplete",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := incompleteNote(tt.report)
			if tt.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tt.want)
		})
	}
}

// TestDraftOutcome_CarriesAListRatherThanNull keeps the payload's shape stable
// for a run that produced nothing: a client rendering outputs should iterate an
// empty list, not guard against null.
func TestDraftOutcome_CarriesAListRatherThanNull(t *testing.T) {
	out := draftOutcome(&scriptdraft.Outcome{RunID: "run_x"})

	assert.NotNil(t, out.Outputs)
	assert.Empty(t, out.Outputs)
	assert.Equal(t, script.RunStatusSucceeded, out.Status)
}

// TestPortalDryRunSource_AnswersBusyAsRetryableRatherThanBroken keeps the two
// apart: a replica already running as many drafts as it will is a "try again",
// and reporting it as a platform failure would send an author to look for a
// fault that is not there.
func TestPortalDryRunSource_AnswersBusyAsRetryableRatherThanBroken(t *testing.T) {
	deps, runner, _ := draftDeps(portalStore(), carol)
	runner.err = scriptdraft.ErrBusy
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "try again")
}

// bucketExportSource names a bucket destination, which only a deployment that
// declares one can serve.
const bucketExportSource = "platform.export(\"top-stores\", [], \"csv\", destination=\"drop\", key=\"top.csv\")\n"

// TestPortalValidateSource_RefusesAnUndeclaredDestination is #1415 on the
// editor: an author pressing Validate has to learn that this deployment cannot
// serve the destination the script names, rather than finding out from a failed
// run after the queries have executed.
func TestPortalValidateSource_RefusesAnUndeclaredDestination(t *testing.T) {
	deps, _, _ := draftDeps(portalStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, validatePath, draftBody(bucketExportSource))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body validateResponse
	decodeInto(t, rec, &body)
	assert.False(t, body.OK)
	require.Len(t, body.Findings, 1)
	assert.Contains(t, body.Findings[0].Message, `destination "drop" is not configured`)
}

// TestPortalValidateSource_AcceptsADeclaredDestination is the same source on the
// deployment that declares it: the check must not be stricter than the run.
func TestPortalValidateSource_AcceptsADeclaredDestination(t *testing.T) {
	deps, _, _ := draftDeps(portalStore(), carol)
	deps.Destinations = []script.Destination{{
		Name: "drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports",
	}}
	rec := servePortalRequest(t, deps, http.MethodPost, validatePath, draftBody(bucketExportSource))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body validateResponse
	decodeInto(t, rec, &body)
	assert.True(t, body.OK)
	assert.Contains(t, body.Destinations, "drop")
}

// TestPortalDryRunSource_RefusesAnUndeclaredDestination puts the same answer in
// front of the interpreter, so nothing runs on the way to a known refusal.
func TestPortalDryRunSource_RefusesAnUndeclaredDestination(t *testing.T) {
	deps, runner, _ := draftDeps(portalStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(bucketExportSource))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `destination \"drop\" is not configured`)
	assert.Empty(t, runner.got.Source, "nothing should have been executed")
}

// TestPortalEditSource_AcceptsAnUndeclaredDestination states the deliberate
// asymmetry: the declared set is configuration that changes under a stored
// script, so refusing the SAVE would take away the edit that fixes it. Only the
// surfaces answering "would this run" check it.
func TestPortalEditSource_AcceptsAnUndeclaredDestination(t *testing.T) {
	assert.Empty(t, refuseSource(bucketExportSource),
		"a save reads the source, not the deployment's destination configuration")
	assert.NotEmpty(t, refuseDraftSource(bucketExportSource, nil))
}
