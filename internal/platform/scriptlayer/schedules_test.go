package scriptlayer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The schedule half of the in-memory store, keyed by schedule id: a script
// holds several (#2434). A replace is held to the schedule's own script, and
// a schedule of a script with runs is kept on delete, as the real store's
// foreign key keeps it.
func (m *memStore) SetSchedule(_ context.Context, sched *script.Schedule) error {
	if m.scheduleErr != nil {
		return m.scheduleErr
	}
	if sched.ID == "" {
		m.nextID++
		sched.ID, sched.CreatedAt = fmt.Sprintf("sched_%d", m.nextID), time.Unix(int64(m.nextID), 0)
	} else if prev, ok := m.schedules[sched.ID]; !ok || prev.ScriptID != sched.ScriptID {
		return script.ErrScheduleNotFound
	}
	stored := *sched
	m.schedules[sched.ID] = &stored
	return nil
}

func (m *memStore) ScheduleByID(_ context.Context, id string) (*script.Schedule, error) {
	if m.scheduleErr != nil {
		return nil, m.scheduleErr
	}
	if m.scheduleReadErr != nil {
		return nil, m.scheduleReadErr
	}
	sched, ok := m.schedules[id]
	if !ok {
		return nil, script.ErrScheduleNotFound
	}
	out := *sched
	return &out, nil
}

// ListSchedules models the real store's scopes, including the distinction
// the SQL makes: a nil ScriptIDs is "no scope" and an empty one matches
// nothing.
func (m *memStore) ListSchedules(_ context.Context, filter script.ScheduleFilter) ([]script.Schedule, error) {
	if m.scheduleErr != nil {
		return nil, m.scheduleErr
	}
	out := []script.Schedule{}
	for _, sched := range m.schedules {
		if filter.ScriptID != "" && sched.ScriptID != filter.ScriptID {
			continue
		}
		if filter.ScriptIDs != nil && !slices.Contains(filter.ScriptIDs, sched.ScriptID) {
			continue
		}
		out = append(out, *sched)
	}
	slices.SortFunc(out, func(a, b script.Schedule) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (m *memStore) EnableSchedule(_ context.Context, id string, enabled bool, actor string) error {
	if m.enabledErr != nil {
		return m.enabledErr
	}
	sched, ok := m.schedules[id]
	if !ok {
		return script.ErrScheduleNotFound
	}
	sched.Enabled = enabled
	sched.UpdatedBy = actor
	return nil
}

func (m *memStore) DeleteSchedule(_ context.Context, id string) error {
	sched, ok := m.schedules[id]
	if !ok {
		return script.ErrScheduleNotFound
	}
	if m.hasRuns[sched.ScriptID] {
		return scriptstore.ErrScheduleHasRuns
	}
	delete(m.schedules, id)
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

// scheduleHandle is a handler-keeping layer with the author's runnable
// script "daily" saved.
func scheduleHandle(t *testing.T) (*Handle, *handlerWorld) {
	t.Helper()
	h, w := handlerHandle(t)
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "daily", Source: tested(inMain("print(1)\n"))})
	require.False(t, res.IsError, resultText(res))
	return h, w
}

// scheduleSet issues a handler_set kind=schedule for the daily script.
func scheduleSet(t *testing.T, h *Handle, ctx context.Context, input manageScriptInput) map[string]any { //nolint:revive // t first is this package's testing convention
	t.Helper()
	input.Command, input.Kind = cmdHandlerSet, scripthandler.KindSchedule
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
	h, w := scheduleHandle(t)

	fields := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: weekdayMornings, Timezone: "America/Los_Angeles"})
	require.NotContains(t, fields, "error", fields)
	assert.Equal(t, weekdayMornings, fields["cron"])
	assert.Equal(t, "America/Los_Angeles", fields["timezone"])
	assert.Equal(t, true, fields["enabled"])
	assert.Equal(t, scripthandler.KindSchedule, fields["kind"])
	assert.NotEmpty(t, fields["next_run_at"])
	assert.Contains(t, fields["message"], "latest saved version")
	assert.Contains(t, fields["message"], "as the script's own principal")

	id, _ := fields["handler"].(string)
	stored, err := w.store.ScheduleByID(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "jane@example.com", stored.CreatedBy)
	assert.True(t, stored.NextRunAt.After(time.Now()))
}

// TestScheduleSet_OnAScriptTheRunGateRefusesSaysNothingWillRunIt pins the
// honest report: the schedule is stored, and the author is told plainly that
// the run gate still refuses the script, in its own words.
func TestScheduleSet_OnAScriptTheRunGateRefusesSaysNothingWillRunIt(t *testing.T) {
	h, _ := scheduleHandle(t)
	no := false
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Enabled: &no})
	require.False(t, res.IsError, resultText(res))

	fields := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})
	require.NotContains(t, fields, "error", fields)
	assert.Contains(t, fields["message"], "nothing will execute this script")
	assert.Contains(t, fields["message"], "the script is disabled")
}

// TestScheduleSet_BindsAgainstTheLiveContract pins that a schedule's
// parameters are checked against the live record when it is set, not
// silently at the first fire with nobody watching.
func TestScheduleSet_BindsAgainstTheLiveContract(t *testing.T) {
	h, _ := handlerHandle(t)
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: tested(inMain("print(1)\n")),
		Params: []script.Param{{Name: "report_date", Type: script.ParamTypeDate, Required: true}},
	})
	require.False(t, res.IsError, resultText(res))

	t.Run("the fire-date token satisfies a date parameter", func(t *testing.T) {
		fields := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily", Args: map[string]any{"report_date": script.FireDateToken}})
		assert.NotContains(t, fields, "error", fields)
	})

	t.Run("a missing required parameter is refused", func(t *testing.T) {
		fields := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@hourly"})
		assert.Contains(t, fields["error"], "required")
	})

	t.Run("an unknown token is refused", func(t *testing.T) {
		fields := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@hourly", Args: map[string]any{"report_date": "${last_week}"}})
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
			h, _ := scheduleHandle(t)
			fields := scheduleSet(t, h, tt.ctx, tt.input)
			assert.Contains(t, fields["error"], tt.wantErr)
		})
	}
}

// TestScheduleSet_AScriptHoldsSeveralCadences is the several-schedules model
// (#2434): a second cadence is a second handler, changing one by its id keeps
// it (the runs pointing at it point at the same automation), and repeating a
// cadence the script holds is refused naming the one it repeats.
func TestScheduleSet_AScriptHoldsSeveralCadences(t *testing.T) {
	h, w := scheduleHandle(t)
	daily := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})
	mornings := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: weekdayMornings})
	require.NotEqual(t, daily["handler"], mornings["handler"])

	all, err := w.store.ListSchedules(context.Background(), script.ScheduleFilter{})
	require.NoError(t, err)
	require.Len(t, all, 2)

	again := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})
	assert.Contains(t, again["error"], fmt.Sprintf("already has schedule handler %s on @daily", daily["handler"]))

	dailyID, _ := daily["handler"].(string)
	require.NotEmpty(t, dailyID)
	retimed := scheduleSet(t, h, authorCtx(), manageScriptInput{Handler: dailyID, Cron: "@hourly"})
	require.NotContains(t, retimed, "error", retimed)
	assert.Equal(t, daily["handler"], retimed["handler"])
	all, err = w.store.ListSchedules(context.Background(), script.ScheduleFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 2, "changing a cadence adds no schedule")

	listed := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdHandlerList, Name: "daily", Kind: scripthandler.KindSchedule,
	}))
	assert.EqualValues(t, 2, listed["count"])
}

// TestScheduleEnableDisable pins the retirement path: a schedule is turned
// off by its id, and resuming it reports its next fire again.
func TestScheduleEnableDisable(t *testing.T) {
	h, _ := scheduleHandle(t)
	id, _ := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})["handler"].(string)

	off := resultFields(t, call(t, h, authorCtx(), byID(cmdHandlerDisable, id)))
	assert.Equal(t, false, off["enabled"])
	assert.NotContains(t, off, "next_run_at", "an author reading a paused schedule is not told it is about to fire")

	on := resultFields(t, call(t, h, authorCtx(), byID(cmdHandlerEnable, id)))
	assert.Equal(t, true, on["enabled"])
	assert.NotEmpty(t, on["next_run_at"], "and resuming reports the fire again")
}

func TestHandlerEnable_WithNoHandlerSaysWhichToName(t *testing.T) {
	h, _ := scheduleHandle(t)
	for _, cmd := range []string{cmdHandlerGet, cmdHandlerEnable, cmdHandlerDisable, cmdHandlerDelete} {
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmd, Name: "daily"})
		assert.True(t, res.IsError, cmd)
		assert.Contains(t, resultText(res), "handler is required", cmd)
	}
}

// TestScheduleDelete: a schedule that produced runs is kept, named, and the
// caller told to disable it; one that did not is deleted.
func TestScheduleDelete(t *testing.T) {
	h, w := scheduleHandle(t)
	id, _ := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})["handler"].(string)
	scriptID := w.store.schedules[id].ScriptID

	w.store.hasRuns[scriptID] = true
	res := call(t, h, authorCtx(), byID(cmdHandlerDelete, id))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "disable it with handler_disable")
	assert.Contains(t, w.store.schedules, id)

	w.store.hasRuns[scriptID] = false
	gone := resultFields(t, call(t, h, authorCtx(), byID(cmdHandlerDelete, id)))
	assert.Equal(t, true, gone["deleted"])
	assert.NotContains(t, w.store.schedules, id)
}

// TestScheduleSet_ANonOwnerIsRefused pins who decides when a script runs: its
// owner, and an administrator, who is unrestricted as everywhere else.
func TestScheduleSet_ANonOwnerIsRefused(t *testing.T) {
	h, _ := scheduleHandle(t)

	refused := scheduleSet(t, h, callerCtx("bob@example.com", "analyst"), manageScriptInput{Cron: "@daily"})
	assert.Contains(t, refused["error"], "not found")

	byAdmin := scheduleSet(t, h, adminCtx(), manageScriptInput{Cron: "@daily", OwnerEmail: "jane@example.com"})
	require.NotContains(t, byAdmin, "error", byAdmin)
	id, _ := byAdmin["handler"].(string)

	paused := call(t, h, callerCtx("bob@example.com", "analyst"), byID(cmdHandlerDisable, id))
	require.True(t, paused.IsError)
	assert.Contains(t, resultText(paused), "is not yours")
}

func TestHandlerList(t *testing.T) {
	h, _ := scheduleHandle(t)
	scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: weekdayMornings})

	t.Run("across scripts", func(t *testing.T) {
		fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdHandlerList}))
		assert.EqualValues(t, 1, fields["count"])
		assert.EqualValues(t, 1, fields["total"])
	})

	t.Run("for one named script", func(t *testing.T) {
		fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdHandlerList, Name: "daily"}))
		assert.EqualValues(t, 1, fields["count"])
		assert.Equal(t, "daily", fields["name"])
	})

	t.Run("narrowed to a kind", func(t *testing.T) {
		fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
			Command: cmdHandlerList, Kind: scripthandler.KindWebhook,
		}))
		assert.EqualValues(t, 0, fields["count"])
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdHandlerList, Kind: "policy"})
		require.True(t, res.IsError)
		assert.Contains(t, resultText(res), `unknown handler kind "policy"`)
	})

	t.Run("a script with no handler says so", func(t *testing.T) {
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "other", Source: tested(inMain("print(1)\n"))})
		require.False(t, res.IsError, resultText(res))
		fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdHandlerList, Name: "other"}))
		assert.EqualValues(t, 0, fields["count"])
		assert.Equal(t, []any{}, fields["handlers"])
		assert.Contains(t, fields["message"], cmdHandlerSet)
	})

	t.Run("another caller sees nothing of a personal script's handlers", func(t *testing.T) {
		fields := resultFields(t, call(t, h, callerCtx("bob@example.com", "analyst"), manageScriptInput{Command: cmdHandlerList}))
		assert.EqualValues(t, 0, fields["count"], "a handler is exactly as visible as the script it belongs to")
		res := call(t, h, callerCtx("bob@example.com", "analyst"), manageScriptInput{Command: cmdHandlerList, Name: "daily"})
		assert.True(t, res.IsError)
	})

	t.Run("an admin sees every handler", func(t *testing.T) {
		fields := resultFields(t, call(t, h, adminCtx(), manageScriptInput{Command: cmdHandlerList}))
		assert.EqualValues(t, 1, fields["count"])
	})
}

// TestHandlerCommands_WithoutAHandlerStore pins the degraded deployment: the
// commands answer plainly instead of panicking on a nil service.
func TestHandlerCommands_WithoutAHandlerStore(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)
	for _, command := range []string{cmdHandlerSet, cmdHandlerList, cmdHandlerGet, cmdHandlerEnable, cmdHandlerDisable, cmdHandlerDelete} {
		res := call(t, h, authorCtx(), manageScriptInput{Command: command, Name: "daily", Cron: "@daily"})
		assert.True(t, res.IsError, command)
		assert.Contains(t, resultText(res), "this deployment cannot keep handlers", command)
	}
}

// TestHandlerCommands_StoreFailuresAreReportedNotLeaked: a store's failure is
// the platform's, told to the caller as such and never in the store's words.
func TestHandlerCommands_StoreFailuresAreReportedNotLeaked(t *testing.T) {
	h, w := scheduleHandle(t)
	id, _ := scheduleSet(t, h, authorCtx(), manageScriptInput{Cron: "@daily"})["handler"].(string)
	w.store.scheduleErr = errors.New("pq: relation script_schedules is locked by pid 4242")

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdHandlerSet, Name: "daily", Cron: "@hourly",
		Kind: scripthandler.KindSchedule,
	})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "the handler could not be read or written; try again")
	assert.NotContains(t, resultText(res), "pid 4242")

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdHandlerList, Name: "daily"})
	require.True(t, res.IsError, "a listing of one script describes each handler, which reads the store")
	assert.NotContains(t, resultText(res), "pid 4242")
	w.store.scheduleErr = nil

	w.store.enabledErr = errors.New("pq: deadlock detected")
	res = call(t, h, authorCtx(), byID(cmdHandlerDisable, id))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "could not be read or written")
	w.store.enabledErr = nil

	w.viewErr = errors.New("pq: connection refused")
	for _, input := range []manageScriptInput{{Command: cmdHandlerList}, {Command: cmdHandlerList, Name: "daily"}} {
		res = call(t, h, authorCtx(), input)
		require.True(t, res.IsError)
		assert.NotContains(t, resultText(res), "connection refused")
	}
}
