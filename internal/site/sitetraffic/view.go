package sitetraffic

import (
	"fmt"
	"math"
	"time"
)

// Totals is a set of counts as a reader reads it: requests by status class
// and the latency percentiles, in milliseconds, read from the merged buckets.
type Totals struct {
	Requests int64            `json:"requests" example:"1200"`
	Classes  map[string]int64 `json:"classes"`
	P50      float64          `json:"p50_ms" example:"12.5"`
	P95      float64          `json:"p95_ms" example:"48"`
	P99      float64          `json:"p99_ms" example:"120"`
}

// classNames name the status classes in a reader's words.
var classNames = [NumClasses]string{"2xx", "3xx", "4xx", "5xx", "other"}

// TotalsOf reads counts.
func TotalsOf(c Counts) Totals {
	t := Totals{Requests: c.Total(), Classes: make(map[string]int64, NumClasses)}
	for i, n := range c.Classes {
		t.Classes[classNames[i]] = n
	}
	t.P50, t.P95, t.P99 = round(Percentile(c.Buckets, q50)), round(Percentile(c.Buckets, q95)), round(Percentile(c.Buckets, q99))
	return t
}

// The quantiles a reader is given.
const (
	q50 = 0.50
	q95 = 0.95
	q99 = 0.99
)

// tenths rounds a latency to a tenth of a millisecond.
const tenths = 10

func round(ms float64) float64 { return math.Round(ms*tenths) / tenths }

// Range is a span a reader asks for: how far back, the step a series is
// drawn in, and the grain it reads.
type Range struct {
	Name  string
	Span  time.Duration
	Step  time.Duration
	Grain string
}

// Ranges are the spans the Traffic view offers. The last hour and day read
// minute rows (kept 24 hours); the last week and month read hour rows.
var Ranges = []Range{
	{Name: "1h", Span: time.Hour, Step: time.Minute, Grain: GrainMinute},
	{Name: "24h", Span: 24 * time.Hour, Step: 15 * time.Minute, Grain: GrainMinute},
	{Name: "7d", Span: 7 * 24 * time.Hour, Step: time.Hour, Grain: GrainHour},
	{Name: "30d", Span: 30 * 24 * time.Hour, Step: 6 * time.Hour, Grain: GrainHour},
}

// RangeOf is the range named, 24h for "".
func RangeOf(name string) (Range, error) {
	if name == "" {
		name = "24h"
	}
	for _, r := range Ranges {
		if r.Name == name {
			return r, nil
		}
	}
	return Range{}, fmt.Errorf("range %q: 1h, 24h, 7d or 30d", name)
}

// Since is where the range starts, reading whole buckets of its grain.
func (r Range) Since(now time.Time) time.Time {
	unit := time.Minute
	if r.Grain == GrainHour {
		unit = time.Hour
	}
	return now.UTC().Truncate(unit).Add(-r.Span + unit)
}
