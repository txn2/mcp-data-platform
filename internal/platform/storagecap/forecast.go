package storagecap

import (
	"math"
	"time"
)

// The windows a store's growth is measured over.
const (
	shortWindow = 7 * 24 * time.Hour
	longWindow  = 30 * 24 * time.Hour
	day         = 24 * time.Hour
	// minSpan7d and minSpan30d are the shortest stretch of history each rate
	// is fitted over (#2201). A new install's first hours are setup, not
	// growth: migrations, the built-in pages and their embeddings, the first
	// catalog scan. An hour of that read as +112 MB/day, four times the whole
	// database. A day of history is the least a weekly rate is read from, and
	// a week the least a monthly one is, so the 30-day rate never repeats the
	// 7-day one over the same few hours.
	minSpan7d  = day
	minSpan30d = 7 * day
	// flatRate is the largest slope read as no growth, in bytes per day: a
	// store holding one size gives a slope of rounding error, never 0.
	flatRate = 1.0
)

// The names a forecast gives the rate it projects with.
const (
	// rate7dName is the rate measured over shortWindow.
	rate7dName = "7d"
	// rate30dName is the rate measured over longWindow.
	rate30dName = "30d"
)

// Point is one sample of a store.
type Point struct {
	At    time.Time `json:"at"`
	Used  int64     `json:"used_bytes"`
	Total *int64    `json:"total_bytes"`
}

// Forecast is how fast a store is growing and when it would fill.
type Forecast struct {
	// Rate7d and Rate30d are the growth in bytes per day fitted over the last
	// 7 and 30 days, nil where the history is too short.
	Rate7d  *float64 `json:"rate_7d_bytes_per_day"`
	Rate30d *float64 `json:"rate_30d_bytes_per_day"`
	// HistoryHours is how much history the store has, first sample to last,
	// and Rate7dMinHistoryHours and Rate30dMinHistoryHours how much each rate
	// needs, so a reader says how far a missing rate is from being measured.
	HistoryHours           float64 `json:"history_hours"`
	Rate7dMinHistoryHours  float64 `json:"rate_7d_min_history_hours"`
	Rate30dMinHistoryHours float64 `json:"rate_30d_min_history_hours"`
	// RateUsed names the rate the projection uses, "7d" or "30d": the slower
	// of the two that are growing, so a burst does not bring the date in.
	RateUsed string `json:"rate_used,omitempty"`
	// DaysLeft and ExhaustionAt are when the store fills at RateUsed, nil
	// when it is not growing or has no known capacity.
	DaysLeft     *float64   `json:"days_left"`
	ExhaustionAt *time.Time `json:"exhaustion_at"`
}

// Project fits the store's growth over its history and, with its capacity,
// when it fills. points are in time order; the last is the current reading.
func Project(points []Point, now time.Time) Forecast {
	f := Forecast{
		Rate7dMinHistoryHours:  minSpan7d.Hours(),
		Rate30dMinHistoryHours: minSpan30d.Hours(),
	}
	if len(points) > 1 {
		f.HistoryHours = points[len(points)-1].At.Sub(points[0].At).Hours()
	}
	f.Rate7d = rateOver(points, now.Add(-shortWindow), minSpan7d)
	f.Rate30d = rateOver(points, now.Add(-longWindow), minSpan30d)
	rate, used := slower(f.Rate7d, f.Rate30d)
	if rate == nil || len(points) == 0 {
		return f
	}
	f.RateUsed = used
	last := points[len(points)-1]
	if last.Total == nil {
		return f
	}
	free := float64(*last.Total - last.Used)
	days := math.Max(free, 0) / *rate
	f.DaysLeft = &days
	// A date past what a Duration holds (about 292 years) is no date: the
	// conversion would overflow into the past.
	if days*float64(day) < float64(math.MaxInt64) {
		at := last.At.Add(time.Duration(days * float64(day)))
		f.ExhaustionAt = &at
	}
	return f
}

// slower is the smaller of the growing rates and which it is; nil when
// neither grows.
func slower(r7, r30 *float64) (rate *float64, name string) {
	grows := func(r *float64) bool { return r != nil && *r > 0 }
	switch {
	case grows(r7) && grows(r30):
		if *r30 < *r7 {
			return r30, rate30dName
		}
		return r7, rate7dName
	case grows(r7):
		return r7, rate7dName
	case grows(r30):
		return r30, rate30dName
	default:
		return nil, ""
	}
}

// rateOver is the least-squares slope of used bytes against time, in bytes
// per day, over the points at or after since; nil when they span less than
// minSpan. A slope within flatRate of zero is zero.
func rateOver(points []Point, since time.Time, minSpan time.Duration) *float64 {
	var in []Point
	for _, p := range points {
		if !p.At.Before(since) {
			in = append(in, p)
		}
	}
	if len(in) < 2 || in[len(in)-1].At.Sub(in[0].At) < minSpan {
		return nil
	}
	// The sums are taken about the means. From raw sums, n*sxy - sx*sy
	// subtracts two numbers near 1e18 for a store of a few tens of megabytes,
	// and what survives the cancellation is rounding error, which read as
	// -0.15 B/day for a store that never changed size (#2201).
	origin := in[0].At
	days := func(p Point) float64 { return p.At.Sub(origin).Hours() / day.Hours() }
	n := float64(len(in))
	var mx, my float64
	for _, p := range in {
		mx += days(p)
		my += float64(p.Used)
	}
	mx /= n
	my /= n
	var sxx, sxy float64
	for _, p := range in {
		dx := days(p) - mx
		sxx += dx * dx
		sxy += dx * (float64(p.Used) - my)
	}
	if sxx == 0 {
		return nil
	}
	slope := sxy / sxx
	if math.Abs(slope) < flatRate {
		slope = 0
	}
	return &slope
}
