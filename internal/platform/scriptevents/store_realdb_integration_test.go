//go:build integration

package scriptevents

// The real-schema proof for #2332: an event makes one run however often it
// is noticed, a burst past the bound is recorded as skipped, and an exclusive
// script's event runs wait their turn.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// rig is a migrated database with its script store and event run store.
type rig struct {
	scripts *scriptstore.Store
	events  *PostgresStore
}

func newRig(t *testing.T) rig {
	t.Helper()
	db := testdb.New(t)
	return rig{scripts: scriptstore.New(db), events: NewStore(db)}
}

// savedScript creates an enabled script and returns it with its version.
func (g rig) savedScript(ctx context.Context, t *testing.T, name string) (*script.Script, *script.Version) {
	t.Helper()
	sc := &script.Script{Name: name, Source: "def main():\n    print(1)\n", OwnerEmail: "jane@example.com", Enabled: true}
	require.NoError(t, g.scripts.Create(ctx, sc, script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}))
	v, err := g.scripts.GetVersion(ctx, sc.ID, sc.Version)
	require.NoError(t, err)
	live, err := g.scripts.GetByID(ctx, sc.ID)
	require.NoError(t, err)
	return live, v
}

// setExclusive saves the script's exclusive setting.
func (g rig) setExclusive(ctx context.Context, sc *script.Script, mode runstate.ExclusiveMode) error {
	live, err := g.scripts.GetByID(ctx, sc.ID)
	if err != nil {
		return err
	}
	live.Exclusive = mode
	return g.scripts.Update(ctx, live)
}

// eventRuns is one run per event id for v, from source.
func eventRuns(sc *script.Script, v *script.Version, source string, ids ...string) []*script.Run {
	runs := make([]*script.Run, 0, len(ids))
	for _, id := range ids {
		runs = append(runs, &script.Run{
			ID: "dpx_" + sc.Name + "_" + id, ScriptID: sc.ID, VersionID: v.ID, Version: v.Version,
			Trigger: runstate.TriggerEvent, RequestedBy: "jane@example.com",
			EventSource: source, EventID: id,
			Event: map[string]any{"id": id, "source": source, "payload": map[string]any{"n": id}},
		})
	}
	return runs
}

func TestRealDB_AnEventMakesOneRun(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	sc, v := g.savedScript(ctx, t, "on-sale")
	sc.EventSource = "shop"
	require.NoError(t, g.scripts.Update(ctx, sc))

	ids, err := g.events.ScriptsForSource(ctx, "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{sc.ID}, ids)
	none, err := g.events.ScriptsForSource(ctx, "other")
	require.NoError(t, err)
	assert.Empty(t, none, "a source no script names enqueues nothing")

	got, err := g.events.EnqueueEvents(ctx, eventRuns(sc, v, "shop", "ev1", "ev2"), 100)
	require.NoError(t, err)
	assert.Equal(t, EventEnqueue{Enqueued: 2}, got)

	again := eventRuns(sc, v, "shop", "ev1")
	again[0].ID = "dpx_retry"
	got, err = g.events.EnqueueEvents(ctx, again, 100)
	require.NoError(t, err)
	assert.Equal(t, EventEnqueue{Already: 1}, got, "a retried delivery of the same event makes no second run")

	run, err := g.scripts.GetRun(ctx, "dpx_on-sale_ev1")
	require.NoError(t, err)
	assert.Equal(t, runstate.TriggerEvent, run.Trigger)
	assert.Equal(t, script.RunStatusPending, run.Status)
	assert.Equal(t, "shop", run.EventSource)
	assert.Equal(t, "ev1", run.EventID)
	assert.Equal(t, map[string]any{"id": "ev1", "source": "shop", "payload": map[string]any{"n": "ev1"}}, run.Event)
	assert.Equal(t, map[string]any{}, run.StateRead)

	claimed, err := g.scripts.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	assert.Equal(t, runstate.TriggerEvent, claimed.Trigger, "an event run goes through the one queue")
}

func TestRealDB_ABurstPastTheBoundIsRecordedAsSkipped(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	sc, v := g.savedScript(ctx, t, "burst")

	ids := make([]string, 5)
	for i := range ids {
		ids[i] = fmt.Sprintf("ev%d", i)
	}
	got, err := g.events.EnqueueEvents(ctx, eventRuns(sc, v, "shop", ids...), 3)
	require.NoError(t, err)
	assert.Equal(t, EventEnqueue{Enqueued: 3, Skipped: 2}, got)

	skipped, err := g.scripts.GetRun(ctx, "dpx_burst_ev4")
	require.NoError(t, err)
	assert.Equal(t, runstate.StatusSkippedBacklog, skipped.Status)
	assert.True(t, skipped.Terminal())
	assert.NotNil(t, skipped.FinishedAt)
	assert.Contains(t, skipped.Error, "3 event runs of this script were already waiting")

	summary, err := g.events.EventSummary(ctx, sc.ID, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 3, summary.Pending)
	assert.Equal(t, 2, summary.SkippedSince)
	assert.NotNil(t, summary.LastEventRun)
	assert.NotNil(t, summary.LastSkippedAt)

	// A claimed run frees a place in the bound.
	_, err = g.scripts.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	got, err = g.events.EnqueueEvents(ctx, eventRuns(sc, v, "shop", "ev9"), 3)
	require.NoError(t, err)
	assert.Equal(t, EventEnqueue{Enqueued: 1}, got)
}

func TestRealDB_AnExclusiveScriptsEventRunsWaitTheirTurn(t *testing.T) {
	for _, mode := range []runstate.ExclusiveMode{runstate.ExclusiveRefuse, runstate.ExclusiveQueue} {
		t.Run(string(mode), func(t *testing.T) {
			g := newRig(t)
			ctx := context.Background()
			sc, v := g.savedScript(ctx, t, "one-at-a-time")
			require.NoError(t, g.setExclusive(ctx, sc, mode))

			got, err := g.events.EnqueueEvents(ctx, eventRuns(sc, v, "shop", "a", "b", "c"), 100)
			require.NoError(t, err)
			assert.Equal(t, EventEnqueue{Enqueued: 3}, got, "an event is never refused for an open run")

			first, err := g.scripts.Claim(ctx, "worker-a", time.Minute, runstate.DefaultMaxReclaims)
			require.NoError(t, err)
			_, err = g.scripts.Claim(ctx, "worker-b", time.Minute, runstate.DefaultMaxReclaims)
			require.ErrorIs(t, err, script.ErrNoWork, "the next event run waits while one is running")

			require.NoError(t, g.scripts.Finish(ctx, first.Lease(), script.RunResult{Status: script.RunStatusSucceeded}))
			next, err := g.scripts.Claim(ctx, "worker-b", time.Minute, runstate.DefaultMaxReclaims)
			require.NoError(t, err)
			assert.NotEqual(t, first.ID, next.ID)
		})
	}
}

func TestRealDB_EventRunRefusals(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	sc, v := g.savedScript(ctx, t, "refusals")

	got, err := g.events.EnqueueEvents(ctx, nil, 100)
	require.NoError(t, err)
	assert.Equal(t, EventEnqueue{}, got)

	bad := eventRuns(sc, v, "shop", "x")
	bad[0].EventID = ""
	_, err = g.events.EnqueueEvents(ctx, bad, 100)
	require.ErrorContains(t, err, "needs a run id and an event id")
}
