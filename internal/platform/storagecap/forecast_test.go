package storagecap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// linear is a store sampled every interval for days, growing perDay bytes a
// day from start, with capacity total.
func linear(days int, interval time.Duration, start, perDay, total int64) []Point {
	var out []Point
	from := now.Add(-time.Duration(days) * day)
	for at := from; !at.After(now); at = at.Add(interval) {
		used := start + int64(float64(perDay)*at.Sub(from).Hours()/24)
		out = append(out, Point{At: at, Used: used, Total: new(total)})
	}
	return out
}

// TestProjectMatchesTheLinearAnswer is #2148's acceptance: a synthetic 30-day
// history projects an exhaustion date within one sample interval of the
// linear answer.
func TestProjectMatchesTheLinearAnswer(t *testing.T) {
	const (
		interval = 15 * time.Minute
		perDay   = int64(2 << 30)
		total    = int64(1 << 40)
	)
	points := linear(30, interval, 100<<30, perDay, total)
	f := Project(points, now)
	require.NotNil(t, f.ExhaustionAt)
	last := points[len(points)-1]
	want := now.Add(time.Duration(float64(total-last.Used) / float64(perDay) * float64(day)))
	assert.WithinDuration(t, want, *f.ExhaustionAt, interval)
	require.NotNil(t, f.Rate7d)
	require.NotNil(t, f.Rate30d)
	assert.InDelta(t, float64(perDay), *f.Rate7d, float64(perDay)/1000)
	assert.InDelta(t, float64(perDay), *f.Rate30d, float64(perDay)/1000)
	require.NotNil(t, f.DaysLeft)
	assert.InDelta(t, float64(total-last.Used)/float64(perDay), *f.DaysLeft, 0.02)
}

// TestProjectUsesTheSlowerRate: a recent burst does not bring the date in;
// the projection says which rate it used.
func TestProjectUsesTheSlowerRate(t *testing.T) {
	slow := linear(30, time.Hour, 0, 1<<30, 1<<40)
	// The last week grows ten times faster.
	for i := range slow {
		if age := now.Sub(slow[i].At); age < 7*day {
			slow[i].Used += int64(float64(9<<30) * (7 - age.Hours()/24))
		}
	}
	f := Project(slow, now)
	require.NotNil(t, f.Rate7d)
	require.NotNil(t, f.Rate30d)
	assert.Less(t, *f.Rate30d, *f.Rate7d)
	assert.Equal(t, "30d", f.RateUsed)

	// Only a week of history: the 7-day rate is all there is.
	week := linear(6, time.Hour, 0, 1<<30, 1<<40)
	f = Project(week, now)
	assert.Equal(t, "7d", f.RateUsed)
}

func TestProjectEdges(t *testing.T) {
	empty := Forecast{Rate7dMinHistoryHours: 24, Rate30dMinHistoryHours: 168}
	assert.Equal(t, empty, Project(nil, now))
	single := []Point{{At: now, Used: 1, Total: new(int64(10))}}
	assert.Equal(t, empty, Project(single, now), "one sample has no rate")
	short := []Point{{At: now.Add(-time.Minute), Used: 1}, {At: now, Used: 2}}
	assert.Nil(t, Project(short, now).Rate7d, "a minute of history is too short to fit")

	shrinking := linear(10, time.Hour, 1<<40, -(1 << 30), 1<<41)
	f := Project(shrinking, now)
	require.NotNil(t, f.Rate7d)
	assert.Negative(t, *f.Rate7d)
	assert.Nil(t, f.ExhaustionAt, "a store that is not growing does not fill")
	assert.Empty(t, f.RateUsed)

	noCapacity := linear(10, time.Hour, 0, 1<<30, 1)
	for i := range noCapacity {
		noCapacity[i].Total = nil
	}
	f = Project(noCapacity, now)
	assert.NotEmpty(t, f.RateUsed)
	assert.Nil(t, f.DaysLeft, "with no capacity there is no date")

	full := linear(10, time.Hour, 1<<40, 1<<30, 1<<30)
	f = Project(full, now)
	require.NotNil(t, f.DaysLeft)
	assert.Zero(t, *f.DaysLeft, "a store past its capacity has no days left")

	same := []Point{{At: now.Add(-2 * time.Hour), Used: 5}, {At: now.Add(-2 * time.Hour), Used: 5}, {At: now, Used: 5}}
	assert.NotNil(t, rateOver(same, now.Add(-day), time.Hour))
	flat := []Point{{At: now, Used: 1}, {At: now, Used: 2}}
	assert.Nil(t, rateOver(flat, now.Add(-day), 0))
}

// TestProjectWaitsForEnoughHistory is #2201's first finding: a new install's
// setup burst over its first hour and a half is no daily rate, and the 30-day
// rate waits for a week so it never repeats the 7-day one.
func TestProjectWaitsForEnoughHistory(t *testing.T) {
	// The samples #2201 was reported from: 16 MB to 28 MB in 1h42m.
	burst := make([]Point, 0, 16)
	from := now.Add(-102 * time.Minute)
	for i := range 16 {
		at := from.Add(time.Duration(i) * 102 * time.Minute / 15)
		burst = append(burst, Point{At: at, Used: 16_075_923 + int64(i)*(28_011_667-16_075_923)/15})
	}
	f := Project(burst, now)
	assert.Nil(t, f.Rate7d, "an hour and a half is no weekly rate")
	assert.Nil(t, f.Rate30d)
	assert.Empty(t, f.RateUsed)
	assert.InDelta(t, 1.7, f.HistoryHours, 0.01)
	assert.Equal(t, 24.0, f.Rate7dMinHistoryHours)
	assert.Equal(t, 168.0, f.Rate30dMinHistoryHours)

	three := linear(3, time.Hour, 0, 1<<30, 1<<40)
	f = Project(three, now)
	require.NotNil(t, f.Rate7d, "three days is enough for the weekly rate")
	assert.Nil(t, f.Rate30d, "and not for the monthly one")
	assert.Equal(t, "7d", f.RateUsed)
	assert.InDelta(t, 72.0, f.HistoryHours, 0.01)
}

// TestProjectFlatStoreIsZero is #2201's second finding: a store holding one
// size at every sample grows at exactly zero, not at the rounding error of
// sums near 1e18.
func TestProjectFlatStoreIsZero(t *testing.T) {
	flat := linear(10, 7*time.Minute, 16_801_792, 0, 1<<40)
	f := Project(flat, now)
	require.NotNil(t, f.Rate7d)
	require.NotNil(t, f.Rate30d)
	assert.Zero(t, *f.Rate7d)
	assert.Zero(t, *f.Rate30d)
	assert.Nil(t, f.ExhaustionAt)

	// A big store gaining a little still reads its growth.
	creep := linear(10, 7*time.Minute, 1<<40, 4096, 1<<41)
	f = Project(creep, now)
	require.NotNil(t, f.Rate7d)
	assert.InDelta(t, 4096, *f.Rate7d, 64)
}

func TestSlower(t *testing.T) {
	r := func(v float64) *float64 { return &v }
	got, name := slower(r(5), r(3))
	assert.Equal(t, 3.0, *got)
	assert.Equal(t, "30d", name)
	got, name = slower(r(2), r(3))
	assert.Equal(t, 2.0, *got)
	assert.Equal(t, "7d", name)
	got, name = slower(r(-1), r(3))
	assert.Equal(t, 3.0, *got)
	assert.Equal(t, "30d", name)
	got, _ = slower(nil, r(0))
	assert.Nil(t, got)
}

// TestProjectFarOffHasNoDate: a store that would take centuries to fill has
// its days left and no date, rather than one overflowed into the past.
func TestProjectFarOffHasNoDate(t *testing.T) {
	points := linear(30, time.Hour, 0, 1<<20, 1<<50)
	f := Project(points, now)
	require.NotNil(t, f.DaysLeft)
	assert.Greater(t, *f.DaysLeft, 106752.0)
	assert.Nil(t, f.ExhaustionAt)
}
