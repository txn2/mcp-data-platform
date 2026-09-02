package scriptlayer

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The schedule half of the in-memory store. It lives with these tests rather
// than in the shared fixture because only the schedule commands need it, and
// the Handle discovers it exactly as it discovers the version store: by asking
// whether the store it was given implements the contract.
func (m *memStore) SetSchedule(_ context.Context, sched *script.Schedule) error {
	if m.scheduleErr != nil {
		return m.scheduleErr
	}
	if prev, ok := m.schedules[sched.ScriptID]; ok {
		sched.ID = prev.ID
	} else {
		sched.ID = "sched_" + sched.ScriptID
	}
	stored := *sched
	m.schedules[sched.ScriptID] = &stored
	return nil
}

func (m *memStore) GetSchedule(_ context.Context, scriptID string) (*script.Schedule, error) {
	if m.scheduleErr != nil {
		return nil, m.scheduleErr
	}
	if m.scheduleReadErr != nil {
		return nil, m.scheduleReadErr
	}
	sched, ok := m.schedules[scriptID]
	if !ok {
		return nil, script.ErrScheduleNotFound
	}
	out := *sched
	return &out, nil
}

// ListSchedules models the real store's ScriptIDs scope, including the
// distinction the SQL makes: a nil slice is "no scope" and an empty one matches
// nothing. A fake that ignored the filter would let a listing test pass while
// PostgreSQL returned a different set — which is the whole point of the scope.
func (m *memStore) ListSchedules(_ context.Context, filter script.ScheduleFilter) ([]script.Schedule, error) {
	if m.scheduleErr != nil {
		return nil, m.scheduleErr
	}
	out := []script.Schedule{}
	for _, sched := range m.schedules {
		if filter.ScriptIDs != nil && !slices.Contains(filter.ScriptIDs, sched.ScriptID) {
			continue
		}
		out = append(out, *sched)
	}
	return out, nil
}

func (m *memStore) SetScheduleEnabled(_ context.Context, scriptID string, enabled bool, actor string) error {
	if m.enabledErr != nil {
		return m.enabledErr
	}
	sched, ok := m.schedules[scriptID]
	if !ok {
		return script.ErrScheduleNotFound
	}
	sched.Enabled = enabled
	sched.UpdatedBy = actor
	return nil
}

func (*memStore) DueSchedules(context.Context, time.Time, int) ([]script.Schedule, error) {
	return nil, nil
}

func (*memStore) MaterializeRun(context.Context, *script.Run) (script.Materialization, error) {
	return script.MaterializedRun, nil
}

func (*memStore) AdvanceSchedule(context.Context, script.ScheduleAdvance) (bool, error) {
	return true, nil
}

// weekdayMornings is the headline cadence: the case a plain interval schedule
// cannot express.
const weekdayMornings = "0 7 * * 1-5"

// scheduleSet issues a schedule_set for the daily script.
func scheduleSet(t *testing.T, h *Handle, ctx context.Context, input manageScriptInput) map[string]any { //nolint:revive // t first is this package's testing convention
	t.Helper()
	input.Command = cmdScheduleSet
	if input.Name == "" {
		input.Name = "daily"
	}
	res := call(t, h, ctx, input)
	if res.IsError {
		return map[string]any{"error": resultText(res)}
	}
	return resultFields(t, res)
}

func TestScheduleSet_StoresTheCadenceAndSaysWhatWillHappen(t *testing.T) {
	h, store, _ := runnableHandle(t)

	fields := scheduleSet(t, h, authorCtx(), manageScriptInput{
		Cron: weekdayMornings, Timezone: "America/Los_Angeles",
	})
	require.NotContains(t, fields, "error", fields)
	assert.Equal(t, weekdayMornings, fields["cron"])
	assert.Equal(t, "America/Los_Angeles", fields["timezone"])
	assert.Equal(t, true, fields["enabled"])
	assert.NotEmpty(t, fields["next_run_at"])
	assert.Contains(t, fields["message"], "latest saved version")
	assert.Contains(t, fields["message"], "as the script's own principal")

	sc, err := store.GetByName(context.Background(), "jane@example.com", "daily")
	require.NoError(t, err)
	stored, err := store.GetSchedule(context.Background(), sc.ID)
	require.NoError(t, err)
	assert.Equal(t, "jane@example.com", stored.CreatedBy)
	assert.True(t, stored.NextRunAt.After(time.Now()))
}

// TestScheduleSet_OnAScriptTheRunGateRefusesSaysNothingWillRunIt pins the
// honest report: the schedule is stored, and the author is told plainly that
// the run gate still refuses the script, in its own words.
func TestScheduleSet_OnAScriptTheRunGateRefusesSaysNothingWillRunIt(t *testing.T) {
	h, _, _ := runnableHandle(t)
	no := false
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Enabled: &no})
	require.False(t, res.IsError, resultText(res))

	fields := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})
	require.NotContains(t, fields, "error", fields)
	assert.Contains(t, fields["message"], "nothing will execute this script")
	assert.Contains(t, fields["message"], "the script is disabled")
}

// TestScheduleSet_BindsAgainstTheLiveContract pins that a schedule's
// parameters are checked against the live record — what its fires will bind
// against — when it is set, not silently at the first fire with nobody
// watching.
func TestScheduleSet_BindsAgainstTheLiveContract(t *testing.T) {
	h, _ := newHandle()
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: "print(1)\n",
		Params: []script.Param{{Name: "report_date", Type: script.ParamTypeDate, Required: true}},
	})
	require.False(t, res.IsError, resultText(res))

	t.Run("the fire-date token satisfies a date parameter", func(t *testing.T) {
		fields := scheduleSet(t, h, authorCtx(), manageScriptInput{
			Cron: "@daily", Args: map[string]any{"report_date": script.FireDateToken},
		})
		assert.NotContains(t, fields, "error", fields)
	})

	t.Run("a missing required parameter is refused", func(t *testing.T) {
		fields := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})
		assert.Contains(t, fields["error"], "required")
	})

	t.Run("an unknown token is refused", func(t *testing.T) {
		fields := scheduleSet(t, h, authorCtx(), manageScriptInput{
			Cron: "@daily", Args: map[string]any{"report_date": "${last_week}"},
		})
		assert.Contains(t, fields["error"], script.FireDateToken)
	})
}

func TestScheduleSet_Refusals(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		input   manageScriptInput
		wantErr string
	}{
		{"an unparseable cadence", authorCtx(), manageScriptInput{Cron: "every tuesday"}, "not a cron expression"},
		{"no cadence at all", authorCtx(), manageScriptInput{}, "needs a cron expression"},
		{"an unknown timezone", authorCtx(), manageScriptInput{Cron: "@daily", Timezone: "Mars/Olympus"}, "not a known timezone"},
		{"a sub-minute cadence", authorCtx(), manageScriptInput{Cron: "@every 5s"}, "once a minute"},
		{"somebody else's script", callerCtx("bob@example.com", "analyst"), manageScriptInput{Cron: "@daily"}, "not found"},
		{"a script that does not exist", authorCtx(), manageScriptInput{Name: "nope", Cron: "@daily"}, "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, _ := runnableHandle(t)
			fields := scheduleSet(t, h, tt.ctx, tt.input)
			assert.Contains(t, fields["error"], tt.wantErr)
		})
	}
}

// TestScheduleSet_ReplacingKeepsTheAutomation pins that editing a cadence does
// not create a second schedule: the runs already pointing at this one point at
// the same automation.
func TestScheduleSet_ReplacingKeepsTheAutomation(t *testing.T) {
	h, store, _ := runnableHandle(t)
	scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})
	scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: weekdayMornings})

	all, err := store.ListSchedules(context.Background(), script.ScheduleFilter{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, weekdayMornings, all[0].CronSpec)
}

// TestScheduleEnableDisable pins the retirement path: a schedule is turned off,
// never deleted, so the row that explains its runs stays.
func TestScheduleEnableDisable(t *testing.T) {
	h, _, _ := runnableHandle(t)
	scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})

	off := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdScheduleDisable, Name: "daily"}))
	assert.Equal(t, false, off["enabled"])
	assert.NotContains(t, off, "next_run_at",
		"an author reading a paused schedule is not told it is about to fire")

	on := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdScheduleEnable, Name: "daily"}))
	assert.Equal(t, true, on["enabled"])
	assert.NotEmpty(t, on["next_run_at"], "and resuming reports the fire again")
}

func TestScheduleEnable_WithNoScheduleSaysHowToSetOne(t *testing.T) {
	h, _, _ := runnableHandle(t)
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdScheduleEnable, Name: "daily"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), cmdScheduleSet)
}

// TestScheduleSet_ANonOwnerIsRefused pins who decides when a script runs: its
// owner, and an administrator, who is unrestricted as everywhere else. Anybody
// else is not told the script is there.
func TestScheduleSet_ANonOwnerIsRefused(t *testing.T) {
	h, _, _ := runnableHandle(t)

	refused := scheduleSet(t, h, callerCtx("bob@example.com", "analyst"), manageScriptInput{Cron: "@daily"})
	assert.Contains(t, refused["error"], "not found")

	paused := call(t, h, callerCtx("bob@example.com", "analyst"),
		manageScriptInput{Command: cmdScheduleDisable, Name: "daily"})
	require.True(t, paused.IsError)
	assert.Contains(t, resultText(paused), "not found")

	byAdmin := scheduleSet(t, h, adminCtx(), manageScriptInput{
		Cron: "@daily", OwnerEmail: "jane@example.com",
	})
	assert.NotContains(t, byAdmin, "error", byAdmin)
}

func TestScheduleList(t *testing.T) {
	h, _, _ := runnableHandle(t)
	scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: weekdayMornings})

	t.Run("across scripts", func(t *testing.T) {
		fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdScheduleList}))
		assert.EqualValues(t, 1, fields["count"])
	})

	t.Run("for one named script", func(t *testing.T) {
		fields := resultFields(t, call(t, h, authorCtx(),
			manageScriptInput{Command: cmdScheduleList, Name: "daily"}))
		assert.EqualValues(t, 1, fields["count"])
	})

	t.Run("a script with no schedule says so", func(t *testing.T) {
		res := call(t, h, authorCtx(), manageScriptInput{
			Command: cmdCreate, Name: "other", Source: "print(1)\n",
		})
		require.False(t, res.IsError, resultText(res))
		fields := resultFields(t, call(t, h, authorCtx(),
			manageScriptInput{Command: cmdScheduleList, Name: "other"}))
		assert.EqualValues(t, 0, fields["count"])
		assert.Contains(t, fields["message"], cmdScheduleSet)
	})

	t.Run("another caller sees nothing of a personal script's schedule", func(t *testing.T) {
		fields := resultFields(t, call(t, h, callerCtx("bob@example.com", "analyst"),
			manageScriptInput{Command: cmdScheduleList}))
		assert.EqualValues(t, 0, fields["count"],
			"a schedule is exactly as visible as the script it belongs to")
	})

	t.Run("an admin sees every schedule", func(t *testing.T) {
		fields := resultFields(t, call(t, h, adminCtx(), manageScriptInput{Command: cmdScheduleList}))
		assert.EqualValues(t, 1, fields["count"])
	})
}

// TestScheduleCommands_WithoutAScheduleStore pins the degraded deployment: the
// commands answer plainly instead of panicking on a nil store.
func TestScheduleCommands_WithoutAScheduleStore(t *testing.T) {
	h := New(Config{Store: &scheduleless{newMemStore()}, AdminPersona: "admin"})
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: "print(1)\n",
	})
	require.False(t, res.IsError, resultText(res))

	for _, command := range []string{cmdScheduleSet, cmdScheduleList, cmdScheduleEnable, cmdScheduleDisable} {
		res := call(t, h, authorCtx(), manageScriptInput{Command: command, Name: "daily", Cron: "@daily"})
		assert.True(t, res.IsError, command)
		assert.Contains(t, resultText(res), "cannot store schedules")
	}
}

// scheduleless is the memory store with its schedule half hidden, which is what
// a store that does not implement the contract looks like to the Handle.
type scheduleless struct{ inner *memStore }

func (s *scheduleless) Create(ctx context.Context, sc *script.Script, a script.Author) error {
	return s.inner.Create(ctx, sc, a)
}

func (s *scheduleless) GetByName(ctx context.Context, owner, name string) (*script.Script, error) {
	return s.inner.GetByName(ctx, owner, name)
}

func (s *scheduleless) Transfer(ctx context.Context, req script.TransferRequest, a script.Author) (script.Transferred, error) {
	return s.inner.Transfer(ctx, req, a)
}

func (s *scheduleless) GetByID(ctx context.Context, id string) (*script.Script, error) {
	return s.inner.GetByID(ctx, id)
}

func (s *scheduleless) Update(ctx context.Context, sc *script.Script) error {
	return s.inner.Update(ctx, sc)
}
func (s *scheduleless) Delete(ctx context.Context, id string) error { return s.inner.Delete(ctx, id) }
func (s *scheduleless) List(ctx context.Context, f script.ListFilter) ([]script.Script, error) {
	return s.inner.List(ctx, f)
}

// TestScheduleSet_StoreFailuresAreReportedNotPanicked covers the read and write
// failures the commands can meet.
func TestScheduleSet_StoreFailuresAreReportedNotPanicked(t *testing.T) {
	h, store, _ := runnableHandle(t)
	store.scheduleErr = errors.New("boom")

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdScheduleSet, Name: "daily", Cron: "@daily"})
	assert.True(t, res.IsError)

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdScheduleList})
	assert.True(t, res.IsError)

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdScheduleList, Name: "daily"})
	assert.True(t, res.IsError)
}

// TestScheduleDisable_ReportsTheChangeWhenTheReadBackFails pins that a change
// that landed is reported as landed: re-reading it is a courtesy, not the
// outcome.
func TestScheduleDisable_ReportsTheChangeWhenTheReadBackFails(t *testing.T) {
	h, store, _ := runnableHandle(t)
	scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})
	store.scheduleReadErr = errors.New("boom")

	fields := resultFields(t, call(t, h, authorCtx(),
		manageScriptInput{Command: cmdScheduleDisable, Name: "daily"}))
	assert.Equal(t, false, fields["enabled"])
	assert.Equal(t, "daily", fields["name"])
}

// TestScheduleNote_SaysWhatADisabledScheduleWillDo pins the third state an
// author can be in: the script runs, and its schedule is switched off.
func TestScheduleNote_SaysWhatADisabledScheduleWillDo(t *testing.T) {
	sc := &script.Script{Enabled: true, Status: script.StatusActive}
	assert.Contains(t, scheduleNote(sc, &script.Schedule{Enabled: false}), cmdScheduleEnable)
}

// TestScheduleEnable_AStoreFailureIsReported pins that a failed write is not
// reported as a change that landed.
func TestScheduleEnable_AStoreFailureIsReported(t *testing.T) {
	h, store, _ := runnableHandle(t)
	scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})
	store.enabledErr = errors.New("boom")

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdScheduleEnable, Name: "daily"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "failed to change the schedule")
}

// TestScheduleList_ResolvesAScriptTheListingCutOff pins that the listing does
// not silently shorten itself: a schedule whose script fell outside the bulk
// lookup is resolved individually and still checked for visibility.
func TestScheduleList_ResolvesAScriptTheListingCutOff(t *testing.T) {
	h, store, _ := runnableHandle(t)
	scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})

	// A schedule pointing at a script the bulk listing does not return.
	store.schedules["ghost"] = &script.Schedule{ID: "sched_ghost", ScriptID: "ghost", CronSpec: "@daily"}
	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdScheduleList}))
	assert.EqualValues(t, 1, fields["count"], "a schedule whose script cannot be read is not listed")

	// And one whose script exists but belongs to somebody else.
	other := &script.Script{
		ID: "script_other", Name: "theirs", OwnerEmail: "bob@example.com", Enabled: true, Status: script.StatusActive,
	}
	store.scripts[other.ID] = other
	store.schedules[other.ID] = &script.Schedule{ID: "sched_other", ScriptID: other.ID, CronSpec: "@daily"}
	fields = resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdScheduleList}))
	assert.EqualValues(t, 1, fields["count"], "and neither is one the caller may not see")

	fields = resultFields(t, call(t, h, adminCtx(), manageScriptInput{Command: cmdScheduleList}))
	assert.EqualValues(t, 2, fields["count"], "an admin sees both real scripts' schedules")
}
