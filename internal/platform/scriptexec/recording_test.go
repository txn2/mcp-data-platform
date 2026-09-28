package scriptexec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// recordings is an in-memory scriptrec.Store.
type recordings struct {
	saved    []scriptrec.Stored
	purged   int
	purgeErr error
}

func (r *recordings) Save(_ context.Context, rec scriptrec.Stored) error {
	r.saved = append(r.saved, rec)
	return nil
}

func (*recordings) Get(context.Context, string) (*scriptrec.Stored, error) {
	return nil, scriptrec.ErrNotFound
}

func (*recordings) Recent(context.Context, string, int) ([]scriptrec.Stored, error) {
	return nil, nil
}
func (*recordings) Keep(context.Context, string, string, []string) error { return nil }
func (r *recordings) Purge(context.Context, time.Duration) (int64, error) {
	r.purged++
	return 3, r.purgeErr
}

// A platform run records what its host calls were answered, as a run of the
// version it executed (#1939).
func TestRunner_RecordsTheRun(t *testing.T) {
	var seen middleware.PlatformContext
	sc, v, run := executableState()
	run.LockedBy, run.Attempt = "worker-a", 1
	run.Params = map[string]any{"day": "2026-09-20"}
	v.Source = `platform.query(connection="warehouse", sql="SELECT 1")`
	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	st := &recordings{}
	r := newRunner(runs, Config{Server: identityServer(t, &seen), Recordings: st})

	out := r.execute(context.Background(), run, sc, v)
	require.Equal(t, "succeeded", out.result.Status, out.result.Error)
	require.Len(t, st.saved, 1)
	got := st.saved[0]
	assert.Equal(t, run.ID, got.RunID)
	assert.Equal(t, sc.ID, got.ScriptID)
	assert.Equal(t, scriptrec.KindRun, got.Kind)
	assert.Equal(t, v.Version, got.Version)
	assert.True(t, got.Succeeded)
	rec, err := scriptrec.Decode(got.Data)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"day": "2026-09-20"}, rec.Params)
	require.Len(t, rec.Calls, 1)
	assert.Equal(t, "trino_query", rec.Calls[0].Tool)
}

// Recordings are swept with the runs, and a failed sweep of them does not
// stop the queue.
func TestWorker_SweepsRecordingsWithTheRuns(t *testing.T) {
	w, _, _ := newTestWorker(t, nil, succeeded)
	st := &recordings{}
	w.cfg.recordings = st
	drainAll(w)
	assert.Equal(t, 1, st.purged)

	w, _, exec := newTestWorker(t, nil, succeeded)
	w.cfg.recordings = &recordings{purgeErr: errors.New("boom")}
	drainAll(w)
	assert.Equal(t, 1, exec.called)
}
