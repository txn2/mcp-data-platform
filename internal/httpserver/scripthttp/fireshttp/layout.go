package fireshttp

import (
	"cmp"
	"slices"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// MaxFiresPerRow caps the fires one row carries. A schedule at the one-minute
// floor fires 1,440 times in a day, and at that density the drawing is a solid
// band whatever the exact count; the row's FireCount still states it.
const MaxFiresPerRow = 500

// The three axes a schedule is drawn on, in the order the page draws them.
// They are plain strings because they are wire values and nothing else.
const (
	// SectionIntraday is the viewer's day, for schedules that fire more than
	// once a day.
	SectionIntraday = "intraday"
	// SectionMultiDay is the viewer's week, Monday to Monday, for schedules
	// that fire at most once a day and at least once a week.
	SectionMultiDay = "multi_day"
	// SectionLongTerm is three calendar months from the first of the viewer's
	// month, for schedules that fire less than once a week.
	SectionLongTerm = "long_term"
)

// A row's rhythm is what kind of schedule it is, read off the typical gap
// between its fires rather than off the expression's text, so an @every
// descriptor and a hand-written expression are classified like the forms a
// builder produces. It is what the drawing colors a row by. Fastest first.
const (
	RhythmMinutes = "minutes"
	RhythmHours   = "hours"
	RhythmDays    = "days"
	RhythmWeeks   = "weeks"
	RhythmMonths  = "months"
)

// row is one schedule's fires within its section's window.
type row struct {
	ScriptID   string `json:"script_id"`
	ScriptName string `json:"script_name"`
	CronSpec   string `json:"cron_spec" example:"*/15 * * * *"`
	Timezone   string `json:"timezone" example:"America/New_York"`
	// Enabled is false for a paused schedule. Its fires are still listed: they
	// are the ones it makes when it is resumed, and a paused job is still one
	// the reader has to account for.
	Enabled bool   `json:"enabled"`
	Rhythm  string `json:"rhythm" example:"minutes"`
	// FireCount is every fire in the window; Fires holds at most
	// MaxFiresPerRow of them, and Truncated says when it holds fewer.
	FireCount int         `json:"fire_count" example:"96"`
	Truncated bool        `json:"truncated"`
	Fires     []time.Time `json:"fires"`
}

// window is one axis and the rows drawn on it.
type window struct {
	Section string    `json:"section" example:"intraday"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Rows    []row     `json:"rows"`
}

// unreadable is a schedule that could not be expanded. The scheduler refuses
// such a schedule on write, so this is a row written before a rule tightened
// or a zone the binary cannot load; it is reported rather than dropped,
// because the page exists to account for every schedule.
type unreadable struct {
	ScriptID   string `json:"script_id"`
	ScriptName string `json:"script_name"`
	Reason     string `json:"reason"`
}

// Timeline is every schedule laid out for one viewer.
type Timeline struct {
	// Timezone is the zone the windows were cut in: the viewer's.
	Timezone   string       `json:"timezone" example:"America/Los_Angeles"`
	Sections   []window     `json:"sections"`
	Unreadable []unreadable `json:"unreadable"`
}

// Classification bounds. The rate is measured over four weeks rather than
// over the week being drawn, because a monthly schedule whose fire happens to
// land in this week fires once in it and would otherwise be filed with the
// weekly ones.
const (
	day              = 24 * time.Hour
	week             = 7 * day
	rateSample       = 4 * week
	intradayAbove    = 28 // more than once a day, on average
	multiDayAtLeast  = 4  // at least once a week, on average
	rhythmSampleGaps = 8
)

// Window lengths, in the calendar units AddDate takes so a boundary stays on a
// local midnight across a DST transition.
const (
	daysPerWeek    = 7
	longTermMonths = 3
	// mondayShift turns Go's weekday (Sunday is 0) into days since Monday.
	mondayShift = 6
)

// Build lays every schedule out for a viewer in viewer at now, each row named
// by names[ScriptID]. The three windows are cut in the viewer's zone and each
// schedule is expanded in its own, so a 7 AM New York job and a 7 AM Los
// Angeles job land three hours apart.
func Build(schedules []script.Schedule, names map[string]string, viewer *time.Location, now time.Time) Timeline {
	windows := windowsAt(now.In(viewer))
	bySection := make(map[string][]row, len(windows))
	refused := make([]unreadable, 0)

	for i := range schedules {
		s := &schedules[i]
		c, err := script.ParseCron(s.CronSpec, s.Timezone)
		if err != nil {
			refused = append(refused, unreadable{ScriptID: s.ScriptID, ScriptName: names[s.ScriptID], Reason: err.Error()})
			continue
		}
		week := windows[1]
		section := classify(c, week.From)
		w := windows[sectionIndex(section)]
		fires, count := c.FiresBetween(w.From, w.To, MaxFiresPerRow)
		bySection[section] = append(bySection[section], row{
			ScriptID: s.ScriptID, ScriptName: names[s.ScriptID],
			CronSpec: s.CronSpec, Timezone: s.Timezone, Enabled: s.Enabled,
			Rhythm:    rhythmOf(c, week.From),
			FireCount: count, Truncated: count > len(fires), Fires: fires,
		})
	}

	for i := range windows {
		rows := bySection[windows[i].Section]
		if rows == nil {
			rows = []row{}
		}
		slices.SortStableFunc(rows, compareRows)
		windows[i].Rows = rows
	}
	return Timeline{Timezone: viewer.String(), Sections: windows, Unreadable: refused}
}

// windowsAt cuts the three windows around local, a time in the viewer's zone.
// Each boundary is a local midnight built by AddDate, so a window spanning a
// DST transition is 23 or 25 hours of wall clock where it has to be.
func windowsAt(local time.Time) []window {
	loc := local.Location()
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	// Monday is the first day of the week the multi-day axis draws; Go counts
	// Sunday as 0, so Sunday is six days after the Monday it closes.
	sinceMonday := (int(local.Weekday()) + mondayShift) % daysPerWeek
	weekStart := dayStart.AddDate(0, 0, -sinceMonday)
	monthStart := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	return []window{
		{Section: SectionIntraday, From: dayStart, To: dayStart.AddDate(0, 0, 1)},
		{Section: SectionMultiDay, From: weekStart, To: weekStart.AddDate(0, 0, daysPerWeek)},
		{Section: SectionLongTerm, From: monthStart, To: monthStart.AddDate(0, longTermMonths, 0)},
	}
}

// sectionIndex is a section's position in the slice windowsAt returns.
func sectionIndex(s string) int {
	switch s {
	case SectionIntraday:
		return 0
	case SectionMultiDay:
		return 1
	default:
		return 2
	}
}

// classify files a schedule by how often it fires over four weeks from
// from: more than 28 times is more than once a day, at least four is at
// least once a week, and anything rarer is long-term.
func classify(c script.Cron, from time.Time) string {
	_, count := c.FiresBetween(from, from.Add(rateSample), 0)
	switch {
	case count > intradayAbove:
		return SectionIntraday
	case count >= multiDayAtLeast:
		return SectionMultiDay
	default:
		return SectionLongTerm
	}
}

// rhythmOf reads a schedule's rhythm from the median of the gaps between its
// next few fires after from. The median rather than the smallest gap, so a
// weekday schedule (four one-day gaps and a three-day one) reads as daily.
func rhythmOf(c script.Cron, from time.Time) string {
	gaps := make([]time.Duration, 0, rhythmSampleGaps)
	prev := c.Next(from)
	for range rhythmSampleGaps {
		if prev.IsZero() {
			break
		}
		next := c.Next(prev)
		if next.IsZero() {
			break
		}
		gaps = append(gaps, next.Sub(prev))
		prev = next
	}
	if len(gaps) == 0 {
		return RhythmMonths
	}
	slices.Sort(gaps)
	median := gaps[len(gaps)/2]
	switch {
	case median < time.Hour:
		return RhythmMinutes
	case median < day:
		return RhythmHours
	case median < week:
		return RhythmDays
	case median < rateSample:
		return RhythmWeeks
	default:
		return RhythmMonths
	}
}

// rhythmRank orders rhythms fastest first.
var rhythmRank = map[string]int{
	RhythmMinutes: 0, RhythmHours: 1, RhythmDays: 2, RhythmWeeks: 3, RhythmMonths: 4,
}

// compareRows orders a section's rows by rhythm, fastest first, so rows of one
// color sit together; then by first fire, so the reader's eye moves forward
// through the day; then by name. A row with no fire in the window sorts after
// the rows of its rhythm that have one.
func compareRows(a, b row) int {
	if c := cmp.Compare(rhythmRank[a.Rhythm], rhythmRank[b.Rhythm]); c != 0 {
		return c
	}
	if c := compareFirstFire(a.Fires, b.Fires); c != 0 {
		return c
	}
	return cmp.Compare(a.ScriptName, b.ScriptName)
}

func compareFirstFire(a, b []time.Time) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	default:
		return a[0].Compare(b[0])
	}
}
