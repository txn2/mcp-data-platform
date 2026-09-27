package fireshttp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// wednesday is a fixed instant inside a week with no DST transition in UTC,
// New York or Los Angeles: Wednesday 14 January 2026, mid-afternoon UTC.
var wednesday = time.Date(2026, 1, 14, 15, 30, 0, 0, time.UTC)

// entry is one schedule, named by its script so a row is found by name.
type entry struct {
	name, spec, tz string
	enabled        bool
}

// layout lays entries out, each schedule's script id derived from its name.
func layout(entries []entry, viewer *time.Location, now time.Time) Timeline {
	schedules := make([]script.Schedule, 0, len(entries))
	names := make(map[string]string, len(entries))
	for _, e := range entries {
		id := "id-" + e.name
		schedules = append(schedules, script.Schedule{ScriptID: id, CronSpec: e.spec, Timezone: e.tz, Enabled: e.enabled})
		names[id] = e.name
	}
	return Build(schedules, names, viewer, now)
}

func section(t *testing.T, tl Timeline, s string) window {
	t.Helper()
	for _, w := range tl.Sections {
		if w.Section == s {
			return w
		}
	}
	t.Fatalf("no %s section", s)
	return window{}
}

func rowNamed(t *testing.T, w window, name string) row {
	t.Helper()
	for _, r := range w.Rows {
		if r.ScriptName == name {
			return r
		}
	}
	t.Fatalf("%s is not in the %s section", name, w.Section)
	return row{}
}

// TestBuild_TheTicketsFourSchedules is the ticket's own criterion: each of the
// four schedules lands in the one section its rate files it under, with the
// number of marks the ticket names.
func TestBuild_TheTicketsFourSchedules(t *testing.T) {
	tl := layout([]entry{
		{"five-minutes", "*/5 * * * *", "UTC", true},
		{"hourly-35", "35 * * * *", "UTC", true},
		{"weekday-seven", "0 7 * * 1-5", "UTC", true},
		{"monthly-first", "0 6 1 * *", "UTC", true},
	}, time.UTC, wednesday)

	require.Len(t, tl.Sections, 3)
	intraday := section(t, tl, SectionIntraday)
	assert.Equal(t, 288, rowNamed(t, intraday, "five-minutes").FireCount)
	assert.Len(t, rowNamed(t, intraday, "five-minutes").Fires, 288)
	assert.Equal(t, 24, rowNamed(t, intraday, "hourly-35").FireCount)
	assert.Len(t, intraday.Rows, 2)

	multi := section(t, tl, SectionMultiDay)
	weekday := rowNamed(t, multi, "weekday-seven")
	require.Len(t, weekday.Fires, 5)
	for i, f := range weekday.Fires {
		assert.Equal(t, time.Weekday(i+1), f.Weekday(), "Monday through Friday")
		assert.Equal(t, 7, f.Hour())
	}
	assert.Len(t, multi.Rows, 1)

	long := section(t, tl, SectionLongTerm)
	monthly := rowNamed(t, long, "monthly-first")
	require.Len(t, monthly.Fires, 3, "one per month over three months")
	for _, f := range monthly.Fires {
		assert.Equal(t, 1, f.Day())
	}
	assert.Len(t, long.Rows, 1)
	assert.Empty(t, tl.Unreadable)
	assert.NotNil(t, tl.Unreadable)
}

// TestBuild_TheWindowsAreTheViewersCalendar pins where each axis starts and
// ends: the viewer's midnight, the viewer's Monday, the first of the viewer's
// month.
func TestBuild_TheWindowsAreTheViewersCalendar(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	tl := layout(nil, la, wednesday)
	assert.Equal(t, "America/Los_Angeles", tl.Timezone)

	day := section(t, tl, SectionIntraday)
	assert.Equal(t, time.Date(2026, 1, 14, 0, 0, 0, 0, la), day.From)
	assert.Equal(t, time.Date(2026, 1, 15, 0, 0, 0, 0, la), day.To)

	week := section(t, tl, SectionMultiDay)
	assert.Equal(t, time.Date(2026, 1, 12, 0, 0, 0, 0, la), week.From, "the Monday of the viewer's week")
	assert.Equal(t, time.Date(2026, 1, 19, 0, 0, 0, 0, la), week.To)

	long := section(t, tl, SectionLongTerm)
	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, la), long.From)
	assert.Equal(t, time.Date(2026, 4, 1, 0, 0, 0, 0, la), long.To)

	for _, w := range tl.Sections {
		assert.NotNil(t, w.Rows, "an empty section is [], never null")
	}
}

// TestBuild_ASundayBelongsToTheWeekThatEndsOnIt pins the Monday arithmetic
// at its edge: Go counts Sunday as 0.
func TestBuild_ASundayBelongsToTheWeekThatEndsOnIt(t *testing.T) {
	sunday := time.Date(2026, 1, 18, 12, 0, 0, 0, time.UTC)
	week := section(t, layout(nil, time.UTC, sunday), SectionMultiDay)
	assert.Equal(t, time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC), week.From)
}

// TestBuild_EachScheduleIsExpandedInItsOwnZone is the ticket's zone criterion:
// the same 7 AM expression in New York and Los Angeles lands three hours apart
// on one viewer's axis.
func TestBuild_EachScheduleIsExpandedInItsOwnZone(t *testing.T) {
	tl := layout([]entry{
		{"ny", "0 7 * * *", "America/New_York", true},
		{"la", "0 7 * * *", "America/Los_Angeles", true},
	}, time.UTC, wednesday)
	multi := section(t, tl, SectionMultiDay)
	ny, la := rowNamed(t, multi, "ny"), rowNamed(t, multi, "la")
	require.NotEmpty(t, ny.Fires)
	require.NotEmpty(t, la.Fires)
	assert.Equal(t, 3*time.Hour, la.Fires[0].Sub(ny.Fires[0]))
}

// TestBuild_APausedScheduleKeepsItsMarks pins that pausing mutes a row rather
// than removing it.
func TestBuild_APausedScheduleKeepsItsMarks(t *testing.T) {
	tl := layout([]entry{{"paused", "0 * * * *", "UTC", false}}, time.UTC, wednesday)
	r := rowNamed(t, section(t, tl, SectionIntraday), "paused")
	assert.False(t, r.Enabled)
	assert.Equal(t, 24, r.FireCount)
}

// TestBuild_ADenseRowIsCappedAndCounted pins the per-row cap: an every-minute
// schedule carries MaxFiresPerRow fires and still states all 1,440.
func TestBuild_ADenseRowIsCappedAndCounted(t *testing.T) {
	tl := layout([]entry{{"minutely", "@every 1m", "UTC", true}}, time.UTC, wednesday)
	r := rowNamed(t, section(t, tl, SectionIntraday), "minutely")
	assert.Len(t, r.Fires, MaxFiresPerRow)
	assert.Equal(t, 1440, r.FireCount)
	assert.True(t, r.Truncated)
}

// TestBuild_ARowWithNoFireInTheWindowIsAnEmptyList pins [] for a schedule
// whose section's window happens to hold none of its fires.
func TestBuild_ARowWithNoFireInTheWindowIsAnEmptyList(t *testing.T) {
	// Weekday-only every-five-minutes: intraday by rate, and silent on a
	// Saturday.
	saturday := time.Date(2026, 1, 17, 12, 0, 0, 0, time.UTC)
	tl := layout([]entry{{"weekday-5m", "*/5 * * * 1-5", "UTC", true}}, time.UTC, saturday)
	r := rowNamed(t, section(t, tl, SectionIntraday), "weekday-5m")
	assert.NotNil(t, r.Fires)
	assert.Empty(t, r.Fires)
	assert.Zero(t, r.FireCount)
}

// TestBuild_AnUnreadableScheduleIsReported pins that a schedule the parser
// refuses is named rather than silently left off.
func TestBuild_AnUnreadableScheduleIsReported(t *testing.T) {
	tl := layout([]entry{{"broken", "@every 5s", "UTC", true}}, time.UTC, wednesday)
	require.Len(t, tl.Unreadable, 1)
	assert.Equal(t, "broken", tl.Unreadable[0].ScriptName)
	assert.Contains(t, tl.Unreadable[0].Reason, "at most once a minute")
	for _, w := range tl.Sections {
		assert.Empty(t, w.Rows)
	}
}

func TestRhythmOf(t *testing.T) {
	tests := []struct {
		spec string
		want string
	}{
		{"*/15 * * * *", RhythmMinutes},
		{"@every 30m", RhythmMinutes},
		{"35 * * * *", RhythmHours},
		{"0 */6 * * *", RhythmHours},
		{"0 7 * * *", RhythmDays},
		{"0 7 * * 1-5", RhythmDays},
		{"0 9 * * 1", RhythmWeeks},
		{"0 9 1,15 * *", RhythmWeeks},
		{"0 6 1 * *", RhythmMonths},
		{"0 6 1 */3 *", RhythmMonths},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			c, err := script.ParseCron(tt.spec, "UTC")
			require.NoError(t, err)
			assert.Equal(t, tt.want, rhythmOf(c, wednesday))
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		spec string
		want string
	}{
		{"0 7,19 * * *", SectionIntraday},
		{"0 */6 * * *", SectionIntraday},
		{"0 7 * * *", SectionMultiDay},
		{"0 7,19 * * 1", SectionMultiDay},
		{"0 9 * * 1", SectionMultiDay},
		{"0 9 1,15 * *", SectionLongTerm},
		{"0 6 1 * *", SectionLongTerm},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			c, err := script.ParseCron(tt.spec, "UTC")
			require.NoError(t, err)
			assert.Equal(t, tt.want, classify(c, wednesday))
		})
	}
}

// TestBuild_RowsOrderByRhythmThenFirstFire pins the row order: the fastest
// rhythm first, then the earliest first fire, then the name, with a row that
// has no fire in the window after the ones that do.
func TestBuild_RowsOrderByRhythmThenFirstFire(t *testing.T) {
	saturday := time.Date(2026, 1, 17, 12, 0, 0, 0, time.UTC)
	tl := layout([]entry{
		{"hourly-b", "10 * * * *", "UTC", true},
		{"weekday-5m", "*/5 * * * 1-5", "UTC", true},
		{"hourly-a", "5 * * * *", "UTC", true},
		{"every-10m", "*/10 * * * *", "UTC", true},
		{"hourly-c", "5 * * * *", "UTC", true},
	}, time.UTC, saturday)
	got := make([]string, 0, 5)
	for _, r := range section(t, tl, SectionIntraday).Rows {
		got = append(got, r.ScriptName)
	}
	assert.Equal(t, []string{"every-10m", "weekday-5m", "hourly-a", "hourly-c", "hourly-b"}, got)
}
