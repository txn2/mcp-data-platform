package script

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// losAngeles is the zone the headline scheduling case is written in, and the
// one that proves the zone database is compiled into the binary.
func losAngeles(t *testing.T) *time.Location {
	t.Helper()
	loc, err := loadTimezone("America/Los_Angeles")
	require.NoError(t, err)
	return loc
}

func TestParseCron(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		timezone string
		wantErr  string
	}{
		{name: "five fields", spec: "0 7 * * 1-5"},
		{name: "descriptor", spec: "@daily"},
		{name: "interval descriptor", spec: "@every 30m"},
		{name: "zone", spec: "0 7 * * 1-5", timezone: "America/Los_Angeles"},
		{name: "empty is refused", wantErr: "needs a cron expression"},
		{name: "nonsense is refused", spec: "every tuesday", wantErr: "is not a cron expression"},
		{name: "unknown zone is refused", spec: "@daily", timezone: "Mars/Olympus", wantErr: "not a known timezone"},
		{name: "sub-minute is refused", spec: "@every 5s", wantErr: "at most once a minute"},
		{name: "never fires is refused", spec: "0 0 30 2 *", wantErr: "never fires"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := ParseCron(tt.spec, tt.timezone)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.False(t, c.Next(time.Now()).IsZero())
		})
	}
}

// TestParseCron_RefusesAnOverlongSpec pins the input bound rather than trusting
// the parser to reject a megabyte of text.
func TestParseCron_RefusesAnOverlongSpec(t *testing.T) {
	spec := make([]byte, MaxCronSpecLength+1)
	for i := range spec {
		spec[i] = '*'
	}
	_, err := ParseCron(string(spec), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "over the 200-character limit")
}

// TestCron_NextIsComputedInItsOwnZone is the whole reason the timezone is
// stored: 07:00 in Los Angeles is not 07:00 UTC, and a report that drifts by
// the offset is the failure a plain interval schedule has.
func TestCron_NextIsComputedInItsOwnZone(t *testing.T) {
	pacific, err := ParseCron("0 7 * * *", "America/Los_Angeles")
	require.NoError(t, err)
	utc, err := ParseCron("0 7 * * *", "UTC")
	require.NoError(t, err)

	// A January instant, so the offset is standard time (UTC-8) either way.
	base := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 7, pacific.Next(base).In(losAngeles(t)).Hour(), "seven in the morning, in Los Angeles")
	assert.Equal(t, 15, pacific.Next(base).UTC().Hour(), "which is 15:00 UTC in January")
	assert.Equal(t, 7, utc.Next(base).UTC().Hour(), "the same expression in UTC is 07:00 UTC")
}

// TestCron_NextCrossesADSTBoundaryWithoutDrifting pins that a wall-clock
// schedule stays on its wall clock: the same 07:00 expression is 15:00 UTC in
// winter and 14:00 UTC in summer, which is what a person means by "07:00".
func TestCron_NextCrossesADSTBoundaryWithoutDrifting(t *testing.T) {
	c, err := ParseCron("0 7 * * *", "America/Los_Angeles")
	require.NoError(t, err)

	winter := c.Next(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	summer := c.Next(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	assert.Equal(t, 15, winter.UTC().Hour())
	assert.Equal(t, 14, summer.UTC().Hour())
	assert.Equal(t, 7, summer.In(losAngeles(t)).Hour())
}

func TestLoadTimezone(t *testing.T) {
	loc, err := loadTimezone("")
	require.NoError(t, err)
	assert.Equal(t, time.UTC, loc, "an unnamed zone is UTC, not the host's local time")

	_, err = loadTimezone("Nowhere/Special")
	require.Error(t, err)
}

// dailyParams is the parameter contract the schedule tests bind against.
func dailyParams() []Param {
	return []Param{
		{Name: "report_date", Type: ParamTypeDate, Required: true},
		{Name: "region", Type: ParamTypeEnum, Values: []string{"us", "eu"}, Default: "us"},
	}
}

func TestBindScheduleParams(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)

	t.Run("the fire date token expands to the fire's own day", func(t *testing.T) {
		bound, err := BindScheduleParams(dailyParams(),
			map[string]any{"report_date": FireDateToken}, fire, time.UTC)
		require.NoError(t, err)
		assert.Equal(t, "2026-08-14", bound["report_date"])
		assert.Equal(t, "us", bound["region"], "an omitted parameter still takes its default")
	})

	t.Run("it expands in the schedule's zone, not UTC", func(t *testing.T) {
		// 07:00 UTC on the 14th is 00:00 on the 14th in Los Angeles, but an
		// hour earlier it is still the 13th there — the case a UTC-only
		// expansion gets wrong every night.
		lateNight := time.Date(2026, 8, 14, 5, 0, 0, 0, time.UTC)
		bound, err := BindScheduleParams(dailyParams(),
			map[string]any{"report_date": FireDateToken}, lateNight, losAngeles(t))
		require.NoError(t, err)
		assert.Equal(t, "2026-08-13", bound["report_date"])
	})

	t.Run("an embedded token expands too", func(t *testing.T) {
		bound, err := BindScheduleParams([]Param{{Name: "label", Type: ParamTypeString}},
			map[string]any{"label": "sales-" + FireDateToken}, fire, time.UTC)
		require.NoError(t, err)
		assert.Equal(t, "sales-2026-08-14", bound["label"])
	})

	t.Run("a non-string value passes through untouched", func(t *testing.T) {
		bound, err := BindScheduleParams([]Param{{Name: "limit", Type: ParamTypeInt}},
			map[string]any{"limit": 10}, fire, time.UTC)
		require.NoError(t, err)
		assert.Equal(t, int64(10), bound["limit"])
	})

	t.Run("an unknown token is refused rather than passed through", func(t *testing.T) {
		_, err := BindScheduleParams(dailyParams(),
			map[string]any{"report_date": "${yesterday}"}, fire, time.UTC)
		require.ErrorIs(t, err, ErrUnknownToken)
		assert.Contains(t, err.Error(), FireDateToken, "the error names the vocabulary")
	})

	t.Run("a binding the contract refuses is refused", func(t *testing.T) {
		_, err := BindScheduleParams(dailyParams(),
			map[string]any{"report_date": FireDateToken, "region": "apac"}, fire, time.UTC)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "apac")
	})
}

// scheduledScript is a script with the daily contract, approved or not.
func scheduledScript(approved bool) *Script {
	sc := &Script{ID: "script_1", Name: "daily-sales", Params: dailyParams(), Enabled: true}
	if approved {
		sc.ApprovedVersionID = "sver_1"
		sc.Status = StatusActive
	}
	return sc
}

func TestBuildSchedule(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	req := ScheduleRequest{
		CronSpec: "0 7 * * 1-5", Timezone: "America/Los_Angeles",
		Params: map[string]any{"report_date": FireDateToken}, Actor: "jane@example.com",
	}

	t.Run("a new schedule is enabled and has its first fire computed", func(t *testing.T) {
		sched, err := BuildSchedule(scheduledScript(true), nil, nil, req, now)
		require.NoError(t, err)
		assert.True(t, sched.Enabled)
		assert.Equal(t, "jane@example.com", sched.CreatedBy)
		assert.Equal(t, "jane@example.com", sched.UpdatedBy)
		assert.True(t, sched.NextRunAt.After(now))
		assert.Equal(t, map[string]any{"report_date": FireDateToken}, sched.Params,
			"the token is stored as written; expanding it here would pin today's date forever")
	})

	t.Run("an unnamed zone defaults to UTC", func(t *testing.T) {
		plain := req
		plain.Timezone = ""
		sched, err := BuildSchedule(scheduledScript(true), nil, nil, plain, now)
		require.NoError(t, err)
		assert.Equal(t, DefaultTimezone, sched.Timezone)
	})

	t.Run("replacing keeps the identity and the creator", func(t *testing.T) {
		prev := &Schedule{ID: "sched_1", CreatedBy: "original@example.com", Enabled: false}
		sched, err := BuildSchedule(scheduledScript(true), nil, prev, req, now)
		require.NoError(t, err)
		assert.Equal(t, "sched_1", sched.ID, "the runs that point at this schedule point at the same automation")
		assert.Equal(t, "original@example.com", sched.CreatedBy)
		assert.Equal(t, "jane@example.com", sched.UpdatedBy)
		assert.False(t, sched.Enabled, "editing the cadence does not silently re-enable a disabled schedule")
	})

	t.Run("an explicit enabled overrides what was there", func(t *testing.T) {
		on := true
		enabling := req
		enabling.Enabled = &on
		sched, err := BuildSchedule(scheduledScript(true), nil,
			&Schedule{ID: "sched_1", Enabled: false}, enabling, now)
		require.NoError(t, err)
		assert.True(t, sched.Enabled)
	})

	t.Run("it binds against the APPROVED version's contract, not the live row's", func(t *testing.T) {
		// The live row renamed the parameter in a draft; the approved code
		// still reads the old name, and the schedule feeds the approved code.
		sc := scheduledScript(true)
		sc.Params = []Param{{Name: "as_of", Type: ParamTypeDate, Required: true}}
		approved := &Version{ID: "sver_1", Params: dailyParams()}

		_, err := BuildSchedule(sc, approved, nil, req, now)
		require.NoError(t, err, "report_date satisfies the approved contract")

		draftShaped := req
		draftShaped.Params = map[string]any{"as_of": FireDateToken}
		_, err = BuildSchedule(sc, approved, nil, draftShaped, now)
		require.Error(t, err, "the draft's parameter name is not what will execute")
	})

	t.Run("with nothing approved it binds against the live record", func(t *testing.T) {
		sched, err := BuildSchedule(scheduledScript(false), nil, nil, req, now)
		require.NoError(t, err, "an author may prepare a schedule before review")
		assert.False(t, sched.NextRunAt.IsZero())
	})

	t.Run("an unparseable cadence is refused", func(t *testing.T) {
		bad := req
		bad.CronSpec = "whenever"
		_, err := BuildSchedule(scheduledScript(true), nil, nil, bad, now)
		require.Error(t, err)
	})

	t.Run("a schedule with no script is refused", func(t *testing.T) {
		_, err := BuildSchedule(&Script{Name: "x"}, nil, nil, req, now)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "needs a script")
	})
}

// TestNextFire covers the misfire policy, which is the whole reason this
// function exists rather than a call to Next.
func TestNextFire(t *testing.T) {
	hourly, err := ParseCron("0 * * * *", "UTC")
	require.NoError(t, err)
	at := func(hour int) time.Time { return time.Date(2026, 8, 14, hour, 0, 0, 0, time.UTC) }

	t.Run("nothing is due before its time", func(t *testing.T) {
		sched := &Schedule{NextRunAt: at(12)}
		fire := sched.NextFire(hourly, at(11))
		assert.False(t, fire.Due)
		assert.Zero(t, fire.Missed)
		assert.Equal(t, at(12), fire.Next)
	})

	t.Run("a schedule with no next fire is never due", func(t *testing.T) {
		assert.False(t, (&Schedule{}).NextFire(hourly, at(11)).Due, "a parked schedule stays parked")
	})

	t.Run("one fire due materializes that fire", func(t *testing.T) {
		sched := &Schedule{NextRunAt: at(12)}
		fire := sched.NextFire(hourly, at(12).Add(time.Minute))
		require.True(t, fire.Due)
		assert.Equal(t, at(12), fire.At)
		assert.Zero(t, fire.Missed)
		assert.Equal(t, at(13), fire.Next)
	})

	t.Run("a gap fires once, for the latest, and counts the rest", func(t *testing.T) {
		// Due at 12:00, noticed at 17:30 — five hours of downtime.
		sched := &Schedule{NextRunAt: at(12)}
		fire := sched.NextFire(hourly, at(17).Add(30*time.Minute))
		require.True(t, fire.Due)
		assert.Equal(t, at(17), fire.At, "the most recent fire, not the oldest: no catch-up storm")
		assert.Equal(t, 5, fire.Missed, "12:00 through 16:00 are recorded as missed")
		assert.Equal(t, at(18), fire.Next)
	})

	t.Run("a gap past the walk cap converges instead of firing something stale", func(t *testing.T) {
		everyMinute, err := ParseCron("* * * * *", "UTC")
		require.NoError(t, err)
		// maxCatchupWalk minutes is well over a year; the pass gives up on
		// this one rather than materializing a fire from the distant past.
		sched := &Schedule{NextRunAt: at(12)}
		now := at(12).Add(2 * maxCatchupWalk * time.Minute)
		fire := sched.NextFire(everyMinute, now)
		assert.False(t, fire.Due)
		assert.Equal(t, maxCatchupWalk, fire.Missed)
		assert.True(t, fire.Next.After(at(12)), "the next pass continues from where this one stopped")
	})
}

// TestRunTerminal_IncludesASkippedOverlap pins that a skipped fire is finished
// on arrival: nothing will ever claim it, so a caller waiting on one would wait
// forever if it read as live.
func TestRunTerminal_IncludesASkippedOverlap(t *testing.T) {
	assert.True(t, (&Run{Status: RunStatusSkippedOverlap}).Terminal())
	assert.False(t, (&Run{Status: RunStatusPending}).Terminal())
	assert.False(t, (&Run{Status: RunStatusRunning}).Terminal())
}

// TestScheduleDueAt pins the one field a reader is told differently from the
// stored row, and both halves of why: a paused schedule announces no fire, and
// pausing does not discard the fire it resumes on.
func TestScheduleDueAt(t *testing.T) {
	parked := time.Date(2026, 8, 18, 7, 0, 0, 0, time.UTC)
	sched := Schedule{Enabled: true, NextRunAt: parked}
	assert.Equal(t, parked, sched.DueAt())

	sched.Enabled = false
	assert.True(t, sched.DueAt().IsZero(), "a paused schedule has no next fire")
	assert.Equal(t, parked, sched.NextRunAt, "and the fire it resumes on is still there")

	sched.Enabled, sched.NextRunAt = true, time.Time{}
	assert.True(t, sched.DueAt().IsZero(), "an expression with no further fire has none either")
}

func TestScheduleMarshalJSON(t *testing.T) {
	sched := Schedule{
		ID: "sched_1", ScriptID: "script_1", CronSpec: "@daily", Timezone: "UTC",
		Enabled: true, NextRunAt: time.Date(2026, 8, 18, 7, 0, 0, 0, time.UTC),
	}

	var enabled map[string]any
	data, err := json.Marshal(sched)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &enabled))
	assert.Equal(t, "2026-08-18T07:00:00Z", enabled["next_run_at"])
	assert.Equal(t, "@daily", enabled["cron_spec"])

	sched.Enabled = false
	var paused map[string]any
	data, err = json.Marshal(&sched)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &paused))
	assert.NotContains(t, paused, "next_run_at", "a pointer marshals by the same rule as a value")
	assert.Equal(t, "@daily", paused["cron_spec"], "and every other field is unchanged")
}

// A schedule binding a connection the approved grant refuses would fail on
// every fire, unattended, and the owner setting it is the last person in a
// position to notice (#1361).
func TestBuildSchedule_ChecksConnectionBindingsAgainstTheGrant(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	sc := &Script{
		ID: "script_1", Name: "daily-sales", Enabled: true, Status: StatusActive,
		ApprovedVersionID: "sver_1",
		Params:            []Param{{Name: "source", Type: ParamTypeConnection, Required: true}},
	}
	approvedAt := now.Add(-24 * time.Hour)
	approved := &Version{
		ID: "sver_1", Version: 2, ApprovedAt: &approvedAt,
		Params: sc.Params,
		Grants: Grants{Roles: []string{"analyst"}, Connections: []string{"warehouse"}},
	}
	req := func(connection string) ScheduleRequest {
		return ScheduleRequest{
			CronSpec: "0 7 * * 1-5", Timezone: "America/Los_Angeles",
			Params: map[string]any{"source": connection}, Actor: "jane@example.com",
		}
	}

	t.Run("a granted connection saves", func(t *testing.T) {
		sched, err := BuildSchedule(sc, approved, nil, req("warehouse"), now)
		require.NoError(t, err)
		assert.Equal(t, "warehouse", sched.Params["source"])
	})

	t.Run("one the grant does not cover is refused at the form", func(t *testing.T) {
		_, err := BuildSchedule(sc, approved, nil, req("staging"), now)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "warehouse")
	})

	t.Run("nothing is checked before approval: there is no grant, and nothing fires", func(t *testing.T) {
		unapproved := *sc
		unapproved.ApprovedVersionID = ""
		_, err := BuildSchedule(&unapproved, nil, nil, req("staging"), now)
		require.NoError(t, err)
	})
}
