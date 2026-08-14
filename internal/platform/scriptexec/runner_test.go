package scriptexec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// recordingAudit collects the events the runner writes.
type recordingAudit struct {
	mu     sync.Mutex
	events []middleware.AuditEvent
	err    error
}

func (a *recordingAudit) Log(_ context.Context, e middleware.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return a.err
}

func (a *recordingAudit) all() []middleware.AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]middleware.AuditEvent(nil), a.events...)
}

// identityServer is a server whose one tool reports the identity the caller
// authenticated as, which is how a test sees what a run actually presented.
func identityServer(t *testing.T, seen *middleware.PlatformContext) *mcp.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "identity", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "trino_query"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ queryArgs) (*mcp.CallToolResult, any, error) {
			if pc := middleware.GetPlatformContext(ctx); pc != nil {
				*seen = *pc
			}
			return &mcp.CallToolResult{}, map[string]any{
				"columns": []any{}, "rows": []any{}, "row_count": 0,
			}, nil
		})
	// The identity the session carries is established by the tool-call
	// middleware from the pre-authenticated user the runner injects, exactly as
	// it is in production.
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&refusingAuthenticator{}, &allowAuthz{}, nil,
		middleware.ToolCallConfig{Transport: "http", AdminPersona: "admin"},
	))
	return server
}

// queryArgs is the argument shape the trino query tool accepts, which the SDK
// validates against: a stand-in that declared fewer fields would reject the
// host binding's real call.
type queryArgs struct {
	SQL        string `json:"sql"`
	Connection string `json:"connection,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// refusingAuthenticator fails every call it is asked to authenticate, so a run
// that reached the server WITHOUT injecting its principal is refused rather
// than quietly running as somebody.
type refusingAuthenticator struct{}

func (*refusingAuthenticator) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return nil, errors.New("no credentials")
}

type allowAuthz struct{}

func (*allowAuthz) IsAuthorized(context.Context, string, []string, string, string) (authorized bool, persona, reason string) {
	return true, "analyst", ""
}

// TestRunner_ExecutesAsTheScriptPrincipal is the identity property: the run
// authenticates as script:<name>, carrying the roles the approval bound and the
// run id as its session, with the owner's address alongside for attribution.
func TestRunner_ExecutesAsTheScriptPrincipal(t *testing.T) {
	var seen middleware.PlatformContext
	sc, v, run := executableState()
	run.LockedBy, run.Attempt = "worker-a", 1
	v.Source = `platform.query(connection="warehouse", sql="SELECT 1")`
	v.Grants.Connections = []string{"warehouse"}

	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	run.LockedBy, run.Attempt = "worker-a", 1

	audit := &recordingAudit{}
	r := newRunner(runs, Config{Server: identityServer(t, &seen), Audit: audit})
	out := r.execute(context.Background(), run, sc, v)

	require.Equal(t, script.RunStatusSucceeded, out.result.Status, out.result.Error)
	assert.Equal(t, "script:daily", seen.UserID)
	assert.Equal(t, "jane@example.com", seen.UserEmail)
	assert.Equal(t, []string{"analyst"}, seen.Roles, "the roles the approval bound, not the requester's")
	assert.Equal(t, middleware.AuthTypeScript, seen.AuthType)
	assert.Equal(t, middleware.SourceScript, seen.Source)
	assert.Equal(t, run.ID, seen.SessionID, "one run is one session")

	events := audit.all()
	require.Len(t, events, 1)
	assert.Equal(t, "script_run", events[0].EventKind)
	assert.Equal(t, "script:daily", events[0].UserID)
	assert.Equal(t, run.ID, events[0].SessionID)
	assert.Equal(t, 3, events[0].Parameters["version"])
	assert.True(t, events[0].Success)
}

// TestRunner_PinsTheFireTimeToWhatTheRunWasCreatedFor pins that a delayed run
// computes the report it was asked for, not one shifted by the delay. The run
// here is one an infrastructure retry has already pushed out: its due time has
// moved and its fire time has not, and the script must see the fire time.
func TestRunner_PinsTheFireTimeToWhatTheRunWasCreatedFor(t *testing.T) {
	var seen middleware.PlatformContext
	sc, v, run := executableState()
	run.LockedBy, run.Attempt = "worker-a", 1
	run.FireTime = time.Date(2026, 8, 12, 7, 0, 0, 0, time.UTC)
	run.ScheduledFor = time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	v.Source = `print(run.fire_time)`

	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	run.LockedBy, run.Attempt = "worker-a", 1

	r := newRunner(runs, Config{Server: identityServer(t, &seen)})
	out := r.execute(context.Background(), run, sc, v)
	require.Equal(t, script.RunStatusSucceeded, out.result.Status, out.result.Error)
	assert.Contains(t, out.result.Log, "2026-08-12T07:00:00Z",
		"the pinned fire time, not the due time a retry moved")
}

// TestRunner_SessionFailureIsRetryable pins the retry boundary: a session that
// could not be opened says nothing about the script.
func TestRunner_SessionFailureIsRetryable(t *testing.T) {
	sc, v, run := executableState()
	r := newRunner(&fakeRuns{}, Config{}) // no server
	out := r.execute(context.Background(), run, sc, v)

	assert.True(t, out.retryable)
	assert.Equal(t, script.RunStatusFailed, out.result.Status)
	assert.Contains(t, out.result.Error, "unavailable")
}

// TestAttemptFrom_EveryInterpreterOutcomeIsTerminal is the other half of that
// boundary: once the interpreter has run, the run is decided, because it may
// already have queried or written.
func TestAttemptFrom_EveryInterpreterOutcomeIsTerminal(t *testing.T) {
	result := &scriptrun.Result{
		Log: "line", LogTruncated: true, Steps: 42, Duration: 1500 * time.Millisecond,
		Queries: 2, Exports: []scriptrun.ExportRecord{{Name: "daily"}},
	}

	ok := attemptFrom(result, nil)
	assert.False(t, ok.retryable)
	assert.Equal(t, script.RunStatusSucceeded, ok.result.Status)
	assert.Equal(t, script.RunMetrics{Steps: 42, DurationMS: 1500, Queries: 2, Exports: 1}, ok.result.Metrics)
	assert.True(t, ok.result.LogTruncated)

	failed := attemptFrom(result, errors.New("Traceback: boom"))
	assert.False(t, failed.retryable, "a script failure reproduces exactly; retrying it changes nothing")
	assert.Equal(t, script.RunStatusFailed, failed.result.Status)
	assert.Contains(t, failed.result.Error, "boom")
	assert.Equal(t, "line", failed.result.Log, "a failed run still reports what it printed")

	nilResult := attemptFrom(nil, errors.New("boom"))
	assert.Equal(t, script.RunStatusFailed, nilResult.result.Status)
}

// TestRunner_WithoutPortalDepsFailsAnExportingRun covers the deployment that
// can run scripts but cannot persist their output. The run FAILS: a scheduled
// report recorded as succeeded, with no asset behind it, is the one outcome
// nobody could act on.
func TestRunner_WithoutPortalDepsFailsAnExportingRun(t *testing.T) {
	var seen middleware.PlatformContext
	sc, v, run := executableState()
	run.LockedBy, run.Attempt = "worker-a", 1
	v.Source = `platform.export(name="daily", rows=[{"a": 1}])`
	v.Grants.Capabilities = script.Capabilities
	v.Grants.Destinations = script.Destinations

	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	run.LockedBy, run.Attempt = "worker-a", 1

	r := newRunner(runs, Config{Server: identityServer(t, &seen)})

	out := r.execute(context.Background(), run, sc, v)
	require.Equal(t, script.RunStatusFailed, out.result.Status)
	assert.Contains(t, out.result.Error, "cannot persist script outputs")
	assert.False(t, out.retryable, "a deployment missing its object storage is not a transient fault")
	assert.Empty(t, runs.outputs)
}

// TestRunner_AuditIsOffThePath pins that a failing audit logger does not fail a
// run that otherwise succeeded.
func TestRunner_AuditIsOffThePath(t *testing.T) {
	var seen middleware.PlatformContext
	sc, v, run := executableState()
	run.LockedBy, run.Attempt = "worker-a", 1
	v.Source = `print("hello")`

	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	run.LockedBy, run.Attempt = "worker-a", 1

	audit := &recordingAudit{err: errors.New("audit store down")}
	r := newRunner(runs, Config{Server: identityServer(t, &seen), Audit: audit})
	out := r.execute(context.Background(), run, sc, v)

	assert.Equal(t, script.RunStatusSucceeded, out.result.Status)
	assert.Len(t, audit.all(), 1)
}

// TestRunner_NoAuditLoggerIsFine covers the deployment with audit disabled.
func TestRunner_NoAuditLoggerIsFine(_ *testing.T) {
	sc, v, run := executableState()
	r := newRunner(&fakeRuns{}, Config{})
	r.recordAudit(context.Background(), run, sc, v, script.RunResult{Status: script.RunStatusSucceeded})
}

// TestNew_RequiresSomewhereToKeepRuns covers the wiring contract: no storage
// means no feature, reported as a nil handle every method tolerates.
func TestNew_RequiresSomewhereToKeepRuns(t *testing.T) {
	assert.Nil(t, New(Config{}))
	assert.Nil(t, New(Config{Runs: &fakeRuns{}}), "a queue with no scripts to run is not a feature")

	var nilHandle *Handle
	assert.Nil(t, nilHandle.Runs())
	nilHandle.Notify()
	require.NoError(t, nilHandle.Start(context.Background()))
	require.NoError(t, nilHandle.Stop(context.Background()))
}

// TestNew_UsesSuppliedStoresAndStartsCleanly covers the assembled handle.
func TestNew_UsesSuppliedStoresAndStartsCleanly(t *testing.T) {
	runs := &fakeRuns{}
	sc, v, _ := executableState()
	h := New(Config{Runs: runs, Scripts: &fakeScripts{script: sc}, Versions: &fakeVersions{version: v}})
	require.NotNil(t, h)
	assert.Equal(t, script.RunStore(runs), h.Runs())

	require.NoError(t, h.Start(context.Background()))
	h.Notify()
	require.NoError(t, h.Stop(context.Background()))
}

// TestWorkerOff_ServesTheQueueWithoutEverClaiming covers the serving half of
// the split deployment. Two things have to hold at once, and the contrast with
// the same handle built worker-on is what proves the first: nothing on this
// replica claims, even when told directly that work arrived, and run_script
// still has the queue it enqueues onto and follows.
func TestWorkerOff_ServesTheQueueWithoutEverClaiming(t *testing.T) {
	build := func(disabled bool, dsn string) (*Handle, *fakeRuns) {
		runs := &fakeRuns{}
		sc, v, run := executableState()
		require.NoError(t, runs.Enqueue(context.Background(), run))
		h := New(Config{
			Runs: runs, Scripts: &fakeScripts{script: sc}, Versions: &fakeVersions{version: v},
			DSN: dsn, WorkerDisabled: disabled,
		})
		require.NotNil(t, h)
		return h, runs
	}

	off, offRuns := build(true, "postgres://unreachable.invalid/db")
	assert.Equal(t, script.RunStore(offRuns), off.Runs(),
		"the serving half still enqueues and follows runs")
	assert.Nil(t, off.listener,
		"waking a replica that will not claim buys nothing but a database connection")
	require.NoError(t, off.Start(context.Background()))
	off.Notify()
	assert.Never(t, func() bool { return offRuns.claimCount() > 0 }, 200*time.Millisecond, 10*time.Millisecond)
	require.NoError(t, off.Stop(context.Background()))

	on, onRuns := build(false, "")
	require.NoError(t, on.Start(context.Background()))
	on.Notify()
	assert.Eventually(t, func() bool { return onRuns.claimCount() > 0 }, 2*time.Second, 10*time.Millisecond,
		"the same wiring with the worker on claims at once, so the silence above is the switch")
	require.NoError(t, on.Stop(context.Background()))
}

// TestNew_BuildsPostgresStoresFromTheDB covers the production wiring path,
// where only a pool is supplied.
func TestNew_BuildsPostgresStoresFromTheDB(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h := New(Config{DB: db})
	require.NotNil(t, h)
	assert.NotNil(t, h.Runs())
	require.NoError(t, h.Stop(context.Background()))
}

// failingListener stands in for a LISTEN adapter that cannot reach the
// database.
type failingListener struct{ stopped bool }

func (*failingListener) Start(context.Context) error { return errors.New("no listen privilege") }
func (f *failingListener) Stop()                     { f.stopped = true }

// TestStart_DegradesWhenTheListenerCannotConnect pins that the wakeup is an
// optimization: a LISTEN connection that cannot be established leaves the
// worker polling rather than failing startup.
func TestStart_DegradesWhenTheListenerCannotConnect(t *testing.T) {
	runs := &fakeRuns{}
	sc, v, _ := executableState()
	h := New(Config{Runs: runs, Scripts: &fakeScripts{script: sc}, Versions: &fakeVersions{version: v}})
	require.NotNil(t, h)
	h.listener = &failingListener{}

	require.NoError(t, h.Start(context.Background()))
	assert.Nil(t, h.listener, "a listener that cannot start is dropped, not retried forever")
	require.NoError(t, h.Stop(context.Background()))
}

// TestStopClosesTheListener covers the shutdown half of that wiring.
func TestStopClosesTheListener(t *testing.T) {
	runs := &fakeRuns{}
	sc, v, _ := executableState()
	h := New(Config{Runs: runs, Scripts: &fakeScripts{script: sc}, Versions: &fakeVersions{version: v}})
	require.NotNil(t, h)
	listener := &workingListener{}
	h.listener = listener

	require.NoError(t, h.Start(context.Background()))
	require.NoError(t, h.Stop(context.Background()))
	assert.True(t, listener.stopped)
}

// workingListener is a LISTEN adapter that starts cleanly.
type workingListener struct{ stopped bool }

func (*workingListener) Start(context.Context) error { return nil }
func (l *workingListener) Stop()                     { l.stopped = true }

func TestOrDefaultRetention(t *testing.T) {
	assert.Equal(t, DefaultRunRetention, orDefaultRetention(0))
	assert.Equal(t, DefaultRunRetention, orDefaultRetention(-time.Hour))
	assert.Equal(t, time.Hour, orDefaultRetention(time.Hour))
}
