package resindexapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
	"github.com/txn2/mcp-data-platform/internal/platform/resourceindex"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcereclass"
)

type fakeStore struct {
	counts resourceindex.PolicyCounts
	err    error
}

func (f fakeStore) Counts(context.Context) (resourceindex.PolicyCounts, error) {
	return f.counts, f.err
}

// blockingReclassifier runs until released, so a test can see a run in
// progress.
type blockingReclassifier struct {
	release chan struct{}
	result  resourcereclass.ReclassifyResult
	err     error
}

func (b *blockingReclassifier) Run(context.Context) (resourcereclass.ReclassifyResult, error) {
	<-b.release
	return b.result, b.err
}

func passthrough(h http.Handler) http.Handler { return h }

// memRuns is Runs held in memory: the one row two replicas would share.
type memRuns struct {
	mu   sync.Mutex
	last *Run
	err  error
}

func (m *memRuns) Begin(context.Context) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return time.Time{}, false, m.err
	}
	if m.last != nil && m.last.FinishedAt == nil {
		return time.Time{}, false, nil
	}
	m.last = &Run{StartedAt: time.Now().UTC()}
	return m.last.StartedAt, true, nil
}

func (m *memRuns) Finish(_ context.Context, res resourcereclass.ReclassifyResult, failure string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.last.FinishedAt, m.last.Result, m.last.Error = &now, &res, failure
	return nil
}

func (m *memRuns) Last(context.Context) (*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	if m.last == nil {
		return nil, nil //nolint:nilnil // no run yet
	}
	cp := *m.last
	return &cp, nil
}

func serve(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody))
	return rec
}

func readState(t *testing.T, mux *http.ServeMux) IndexState {
	t.Helper()
	rec := serve(mux, http.MethodGet, indexPath)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var state IndexState
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &state))
	return state
}

func TestReclassifyRunsOnceAndReportsWhenDone(t *testing.T) {
	rc := &blockingReclassifier{release: make(chan struct{}), result: resourcereclass.ReclassifyResult{Examined: 23389, Changed: 6485}}
	mux := http.NewServeMux()
	runs := &memRuns{}
	Register(mux, passthrough, Config{Store: fakeStore{counts: resourceindex.PolicyCounts{None: 6485}}, Reclassifier: rc, Runs: runs})
	// A second replica shares the runs: it reports the run the first started
	// and refuses to start another while it runs.
	other := http.NewServeMux()
	Register(other, passthrough, Config{Store: fakeStore{}, Reclassifier: rc, Runs: runs})

	assert.Nil(t, readState(t, mux).LastRun, "no run yet")
	rec := serve(mux, http.MethodPost, indexPath+"/reclassify")
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, http.StatusConflict, serve(other, http.MethodPost, indexPath+"/reclassify").Code,
		"a second run is refused on any replica while the first walks the table")
	require.NotNil(t, readState(t, other).LastRun, "every replica reports the run")

	state := readState(t, mux)
	require.NotNil(t, state.LastRun)
	assert.Nil(t, state.LastRun.FinishedAt, "still running")
	assert.Equal(t, 6485, state.Counts.None)

	close(rc.release)
	require.Eventually(t, func() bool {
		s := readState(t, mux)
		return s.LastRun != nil && s.LastRun.FinishedAt != nil
	}, 5*time.Second, 5*time.Millisecond)
	state = readState(t, mux)
	require.NotNil(t, state.LastRun.Result)
	assert.Equal(t, 6485, state.LastRun.Result.Changed)
	assert.Empty(t, state.LastRun.Error)
	assert.Equal(t, http.StatusAccepted, serve(mux, http.MethodPost, indexPath+"/reclassify").Code,
		"a finished run lets the next start")
}

func TestReclassifyReportsAFailure(t *testing.T) {
	for _, cause := range []error{errors.New("db down"), resourcereclass.ErrReclassifyRunning} {
		rc := &blockingReclassifier{release: make(chan struct{}), err: cause}
		close(rc.release)
		mux := http.NewServeMux()
		Register(mux, passthrough, Config{Store: fakeStore{}, Reclassifier: rc, Runs: &memRuns{}})
		require.Equal(t, http.StatusAccepted, serve(mux, http.MethodPost, indexPath+"/reclassify").Code)
		require.Eventually(t, func() bool {
			s := readState(t, mux)
			return s.LastRun != nil && s.LastRun.FinishedAt != nil
		}, 5*time.Second, 5*time.Millisecond)
		assert.Equal(t, cause.Error(), readState(t, mux).LastRun.Error)
	}
}

func TestCountsFailureAndMissingStore(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, passthrough, Config{Store: fakeStore{err: errors.New("db down")}})
	assert.Equal(t, http.StatusInternalServerError, serve(mux, http.MethodGet, indexPath).Code)
	assert.Equal(t, http.StatusNotFound, serve(mux, http.MethodPost, indexPath+"/reclassify").Code,
		"no reclassifier mounts no route")

	broken := http.NewServeMux()
	Register(broken, passthrough, Config{Store: fakeStore{}, Reclassifier: &blockingReclassifier{}, Runs: &memRuns{err: errors.New("db down")}})
	assert.Equal(t, http.StatusInternalServerError, serve(broken, http.MethodGet, indexPath).Code)
	assert.Equal(t, http.StatusInternalServerError, serve(broken, http.MethodPost, indexPath+"/reclassify").Code)

	empty := http.NewServeMux()
	Register(empty, passthrough, Config{})
	assert.Equal(t, http.StatusNotFound, serve(empty, http.MethodGet, indexPath).Code)
}

// TestMountServesTheCountsFromTheDatabase: Mount assembles the store and the
// reclassifier over one database and serves the counts from it.
func TestMountServesTheCountsFromTheDatabase(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("FROM resources").WillReturnRows(sqlmock.NewRows(
		[]string{"full", "metadata", "none", "explicit", "content_bytes", "folders"}).AddRow(4, 3, 2, 1, 900, 5))
	mock.ExpectQuery("FROM resource_reclassify_runs").WillReturnRows(sqlmock.NewRows(
		[]string{"started_at", "finished_at", "examined", "changed", "cleared", "error"}))

	mux := http.NewServeMux()
	Mount(mux, passthrough, db, indexpolicy.Default())
	state := readState(t, mux)
	assert.Equal(t, 2, state.Counts.None)
	assert.Equal(t, 5, state.Counts.Folders)
}

func TestPostgresRuns(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	runs := PostgresRuns{DB: db}
	ctx := context.Background()
	now := time.Now()

	mock.ExpectQuery("INSERT INTO resource_reclassify_runs").WillReturnRows(sqlmock.NewRows([]string{"started_at"}).AddRow(now))
	started, ok, err := runs.Begin(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, now.UTC(), started)

	mock.ExpectQuery("INSERT INTO resource_reclassify_runs").WillReturnRows(sqlmock.NewRows([]string{"started_at"}))
	_, ok, err = runs.Begin(ctx)
	require.NoError(t, err)
	assert.False(t, ok, "a run in progress refuses a second")

	mock.ExpectQuery("INSERT INTO resource_reclassify_runs").WillReturnError(errors.New("down"))
	_, _, err = runs.Begin(ctx)
	assert.Error(t, err)

	mock.ExpectExec("UPDATE resource_reclassify_runs").WithArgs(3, 2, 1, "").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, runs.Finish(ctx, resourcereclass.ReclassifyResult{Examined: 3, Changed: 2, Cleared: 1}, ""))
	mock.ExpectExec("UPDATE resource_reclassify_runs").WillReturnError(errors.New("down"))
	assert.Error(t, runs.Finish(ctx, resourcereclass.ReclassifyResult{}, "x"))

	cols := []string{"started_at", "finished_at", "examined", "changed", "cleared", "error"}
	mock.ExpectQuery("FROM resource_reclassify_runs").WillReturnRows(sqlmock.NewRows(cols))
	last, err := runs.Last(ctx)
	require.NoError(t, err)
	assert.Nil(t, last)
	mock.ExpectQuery("FROM resource_reclassify_runs").WillReturnRows(sqlmock.NewRows(cols).AddRow(now, now, 3, 2, 1, ""))
	last, err = runs.Last(ctx)
	require.NoError(t, err)
	require.NotNil(t, last.Result)
	assert.Equal(t, 2, last.Result.Changed)
	mock.ExpectQuery("FROM resource_reclassify_runs").WillReturnRows(sqlmock.NewRows(cols).AddRow(now, nil, 0, 0, 0, ""))
	last, err = runs.Last(ctx)
	require.NoError(t, err)
	assert.Nil(t, last.FinishedAt, "running")
	mock.ExpectQuery("FROM resource_reclassify_runs").WillReturnError(errors.New("down"))
	_, err = runs.Last(ctx)
	assert.Error(t, err)
}
