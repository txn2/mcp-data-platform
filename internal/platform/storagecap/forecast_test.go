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
	assert.Equal(t, Forecast{}, Project(nil, now))
	single := []Point{{At: now, Used: 1, Total: new(int64(10))}}
	assert.Equal(t, Forecast{}, Project(single, now), "one sample has no rate")
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
	assert.NotNil(t, rateOver(same, now.Add(-day)))
	flat := []Point{{At: now, Used: 1}, {At: now, Used: 2}}
	assert.Nil(t, rateOver(flat, now.Add(-day)))
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
