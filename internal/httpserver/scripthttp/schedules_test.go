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

// The schedule half of the stub store. A schedule is keyed on its script,
// because that is the real constraint: one script, one schedule.
func (s *stubStore) SetSchedule(_ context.Context, sched *script.Schedule) error {
	if s.scheduleWriteErr != nil {
		return s.scheduleWriteErr
	}
	sched.ID = "sched_1"
	stored := *sched
	s.schedule = &stored
	return nil
}

func (s *stubStore) GetSchedule(context.Context, string) (*script.Schedule, error) {
	if s.scheduleErr != nil {
		return nil, s.scheduleErr
	}
	if s.schedule == nil {
		return nil, script.ErrScheduleNotFound
	}
	return s.schedule, nil
}

func (s *stubStore) ListSchedules(context.Context, script.ScheduleFilter) ([]script.Schedule, error) {
	if s.scheduleErr != nil {
		return nil, s.scheduleErr
	}
	if s.schedule == nil {
		return []script.Schedule{}, nil
	}
	return []script.Schedule{*s.schedule}, nil
}

func (s *stubStore) SetScheduleEnabled(_ context.Context, _ string, enabled bool, actor string) error {
	if s.scheduleWriteErr != nil {
		return s.scheduleWriteErr
	}
	if s.schedule == nil {
		return script.ErrScheduleNotFound
	}
	s.schedule.Enabled, s.schedule.UpdatedBy = enabled, actor
	return nil
}

func (*stubStore) DueSchedules(context.Context, time.Time, int) ([]script.Schedule, error) {
	return nil, nil
}

func (*stubStore) MaterializeRun(context.Context, *script.Run) (script.Materialization, error) {
	return script.MaterializedRun, nil
}

func (*stubStore) AdvanceSchedule(context.Context, script.ScheduleAdvance) (bool, error) {
	return true, nil
}

// schedulePath is the route under test.
const schedulePath = "/api/v1/admin/scripts/script_1/schedule"

// approvedStore returns a store whose script is executable and whose approved
// version declares one required date parameter.
func approvedStore() *stubStore {
	store := newStore()
	store.scripts[0].ApprovedVersionID = "sver_1"
	store.scripts[0].Status = script.StatusActive
	store.version.Params = []script.Param{{Name: "report_date", Type: script.ParamTypeDate, Required: true}}
	return store
}

func TestSetSchedule(t *testing.T) {
	store := approvedStore()
	rec := serve(t, store, http.MethodPut, schedulePath,
		`{"cron":"0 7 * * 1-5","timezone":"America/Los_Angeles","params":{"report_date":"${fire_date}"}}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := decode(t, rec)
	assert.Equal(t, "0 7 * * 1-5", body["cron_spec"])
	assert.Equal(t, "America/Los_Angeles", body["timezone"])
	assert.Equal(t, true, body["enabled"])
	assert.NotEmpty(t, body["next_run_at"])

	require.NotNil(t, store.schedule)
	assert.Equal(t, "admin@example.com", store.schedule.CreatedBy)
	assert.Equal(t, map[string]any{"report_date": script.FireDateToken}, store.schedule.Params,
		"the token is stored unexpanded; it means the day of the fire, not today")
}

// TestSetSchedule_ValidatesAgainstTheApprovedContract pins that a schedule
// which could never bind is refused when it is set, rather than failing every
// night with nobody watching.
func TestSetSchedule_ValidatesAgainstTheApprovedContract(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"a cadence that does not parse", `{"cron":"every tuesday"}`, "not a cron expression"},
		{"no cadence", `{"cron":""}`, "needs a cron expression"},
		{"an unknown zone", `{"cron":"@daily","timezone":"Mars/Olympus"}`, "not a known timezone"},
		{"a sub-minute cadence", `{"cron":"@every 10s"}`, "once a minute"},
		{"a missing required parameter", `{"cron":"@daily"}`, "required"},
		{"a parameter the contract does not declare", `{"cron":"@daily","params":{"nope":"x"}}`, "no parameter"},
		{"an unknown token", `{"cron":"@daily","params":{"report_date":"${last_month}"}}`, "unknown schedule token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, approvedStore(), http.MethodPut, schedulePath, tt.body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tt.wantErr)
		})
	}
}

func TestSetSchedule_Failures(t *testing.T) {
	t.Run("an unreadable body", func(t *testing.T) {
		rec := serve(t, approvedStore(), http.MethodPut, schedulePath, "{not json")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("an unknown script", func(t *testing.T) {
		rec := serve(t, approvedStore(), http.MethodPut,
			"/api/v1/admin/scripts/nope/schedule", `{"cron":"@daily"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("an unreadable approved version", func(t *testing.T) {
		store := approvedStore()
		store.versionErr = errors.New("boom")
		rec := serve(t, store, http.MethodPut, schedulePath, `{"cron":"@daily"}`)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("an unreadable current schedule", func(t *testing.T) {
		store := approvedStore()
		store.scheduleErr = errors.New("boom")
		rec := serve(t, store, http.MethodPut, schedulePath, `{"cron":"@daily"}`)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("the write failed", func(t *testing.T) {
		store := approvedStore()
		store.scheduleWriteErr = errors.New("boom")
		rec := serve(t, store, http.MethodPut, schedulePath,
			`{"cron":"@daily","params":{"report_date":"${fire_date}"}}`)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// TestSetSchedule_OnAnUnapprovedScriptBindsAgainstTheLiveRecord pins that an
// author may prepare a schedule before review; it simply will not fire until a
// version is approved.
func TestSetSchedule_OnAnUnapprovedScriptBindsAgainstTheLiveRecord(t *testing.T) {
	rec := serve(t, newStore(), http.MethodPut, schedulePath, `{"cron":"@daily"}`)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestSetSchedule_KeepsTheDisabledState pins that editing the cadence of a
// paused automation does not quietly restart it.
func TestSetSchedule_KeepsTheDisabledState(t *testing.T) {
	store := approvedStore()
	store.schedule = &script.Schedule{ID: "sched_1", ScriptID: "script_1", Enabled: false, CreatedBy: "jane@example.com"}

	rec := serve(t, store, http.MethodPut, schedulePath,
		`{"cron":"@daily","params":{"report_date":"${fire_date}"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, false, decode(t, rec)["enabled"])
	assert.Equal(t, "jane@example.com", store.schedule.CreatedBy, "the automation keeps its author")

	enabled := `{"cron":"@daily","enabled":true,"params":{"report_date":"${fire_date}"}}`
	rec = serve(t, store, http.MethodPut, schedulePath, enabled)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, decode(t, rec)["enabled"])
}

func TestGetSchedule(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		store := approvedStore()
		store.schedule = &script.Schedule{ID: "sched_1", ScriptID: "script_1", CronSpec: "@daily", Enabled: true}
		rec := serve(t, store, http.MethodGet, schedulePath, "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "@daily", decode(t, rec)["cron_spec"])
	})

	t.Run("none is a 404", func(t *testing.T) {
		rec := serve(t, approvedStore(), http.MethodGet, schedulePath, "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("an unknown script is a 404", func(t *testing.T) {
		rec := serve(t, approvedStore(), http.MethodGet, "/api/v1/admin/scripts/nope/schedule", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("a store failure is a 500", func(t *testing.T) {
		store := approvedStore()
		store.scheduleErr = errors.New("boom")
		rec := serve(t, store, http.MethodGet, schedulePath, "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestListSchedules(t *testing.T) {
	store := approvedStore()
	store.schedule = &script.Schedule{ID: "sched_1", ScriptID: "script_1", CronSpec: "@daily"}
	rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts/schedules", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(1), decode(t, rec)["total"])

	store.scheduleErr = errors.New("boom")
	rec = serve(t, store, http.MethodGet, "/api/v1/admin/scripts/schedules", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestScheduleRoutesAreUnmountedWithoutAStore pins the degraded deployment: a
// route that cannot keep a schedule is absent rather than failing per request.
func TestScheduleRoutesAreUnmountedWithoutAStore(t *testing.T) {
	store := approvedStore()
	mux := http.NewServeMux()
	New(Deps{
		Scripts: store, Versions: store, Approvals: store,
		AdminEmail: func(*http.Request) string { return "admin@example.com" },
	}).RegisterAdmin(mux, "/api/v1/admin", func(h http.Handler) http.Handler { return h })

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, schedulePath},
		{http.MethodPut, schedulePath},
		{http.MethodPost, schedulePath + "/enable"},
		{http.MethodPost, schedulePath + "/disable"},
		{http.MethodGet, "/api/v1/admin/scripts/schedules"},
	} {
		req := httptest.NewRequestWithContext(context.Background(), route.method, route.path, http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code, route.path)
	}

	// The review routes are still there: approving does not depend on
	// scheduling.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/scripts", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestEnableDisableSchedule pins the pause path, and the reason it is its own
// route: turning a schedule off must not re-base the fire it resumes on, which
// is exactly what sending the cadence back through PUT would do.
func TestEnableDisableSchedule(t *testing.T) {
	parked := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	store := approvedStore()
	store.schedule = &script.Schedule{
		ID: "sched_1", ScriptID: "script_1", CronSpec: "@daily", Timezone: "UTC",
		Enabled: true, NextRunAt: parked, CreatedBy: "jane@example.com",
	}

	rec := serve(t, store, http.MethodPost, schedulePath+"/disable", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	paused := decode(t, rec)
	assert.Equal(t, false, paused["enabled"])
	assert.NotContains(t, paused, "next_run_at",
		"a paused schedule announces no fire; the stored due time is what it resumes on, not a prediction")
	assert.True(t, store.schedule.NextRunAt.Equal(parked), "pausing does not move the next fire")
	assert.Equal(t, "admin@example.com", store.schedule.UpdatedBy)

	rec = serve(t, store, http.MethodPost, schedulePath+"/enable", "")
	require.Equal(t, http.StatusOK, rec.Code)
	resumed := decode(t, rec)
	assert.Equal(t, true, resumed["enabled"])
	assert.Equal(t, parked.Format(time.RFC3339), resumed["next_run_at"],
		"and resuming reports the fire again")
	assert.True(t, store.schedule.NextRunAt.Equal(parked),
		"and resuming picks up the fire it was parked on, which the misfire policy then collapses")
}

// TestGetSchedule_PausedReportsNoNextRun covers the read route as well as the
// pause route: the rule belongs to the schedule, so every surface that serves
// one states it, not only the route that turned it off.
func TestGetSchedule_PausedReportsNoNextRun(t *testing.T) {
	store := approvedStore()
	store.schedule = &script.Schedule{
		ID: "sched_1", ScriptID: "script_1", CronSpec: "@daily", Timezone: "UTC",
		Enabled: false, NextRunAt: time.Now().Add(time.Hour).UTC(),
	}

	rec := serve(t, store, http.MethodGet, schedulePath, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, decode(t, rec), "next_run_at")

	rec = serve(t, store, http.MethodGet, "/api/v1/admin/scripts/schedules", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	listed, ok := decode(t, rec)["data"].([]any)
	require.True(t, ok)
	require.Len(t, listed, 1)
	assert.NotContains(t, listed[0], "next_run_at")
}

func TestEnableSchedule_Failures(t *testing.T) {
	t.Run("no schedule is a 404", func(t *testing.T) {
		rec := serve(t, approvedStore(), http.MethodPost, schedulePath+"/enable", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("an unknown script is a 404", func(t *testing.T) {
		rec := serve(t, approvedStore(), http.MethodPost, "/api/v1/admin/scripts/nope/schedule/disable", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("a store failure is a 500", func(t *testing.T) {
		store := approvedStore()
		store.schedule = &script.Schedule{ID: "sched_1", ScriptID: "script_1"}
		store.scheduleWriteErr = errors.New("boom")
		rec := serve(t, store, http.MethodPost, schedulePath+"/enable", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
