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
	// minSpan is the shortest stretch of history a rate is fitted over: two
	// samples a minute apart say nothing about a month.
	minSpan = time.Hour
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
	var f Forecast
	f.Rate7d = rateOver(points, now.Add(-shortWindow))
	f.Rate30d = rateOver(points, now.Add(-longWindow))
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
// minSpan.
func rateOver(points []Point, since time.Time) *float64 {
	var in []Point
	for _, p := range points {
		if !p.At.Before(since) {
			in = append(in, p)
		}
	}
	if len(in) < 2 || in[len(in)-1].At.Sub(in[0].At) < minSpan {
		return nil
	}
	origin := in[0].At
	var sx, sy, sxx, sxy float64
	for _, p := range in {
		x := p.At.Sub(origin).Hours() / day.Hours()
		y := float64(p.Used)
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	n := float64(len(in))
	den := n*sxx - sx*sx
	if den == 0 {
		return nil
	}
	slope := (n*sxy - sx*sy) / den
	return &slope
}
