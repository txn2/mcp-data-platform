package audit

import (
	"errors"
	"fmt"
	"time"
)

// hoursPerDay is the number of hours in a day.
const hoursPerDay = 24

// MaxTimeseriesBuckets is the most points one timeseries answers with: a
// week at minute resolution is past it, a week by the hour or a year by the
// day is not. Every bucket of the range is materialized, so the range a
// caller names is bounded here rather than by the memory it would take.
const MaxTimeseriesBuckets = 10_000

// ErrTimeseriesRange is a timeseries range that cannot be answered: its end
// is before its start, or it holds more than MaxTimeseriesBuckets buckets at
// its resolution. A caller answers it as a bad request.
var ErrTimeseriesRange = errors.New("invalid timeseries range")

// CheckTimeseriesRange reports why [start, end] at resolution r cannot be
// answered, wrapping ErrTimeseriesRange, or nil when it can.
func CheckTimeseriesRange(start, end time.Time, r Resolution) error {
	if end.Before(start) {
		return fmt.Errorf("end_time is before start_time: %w", ErrTimeseriesRange)
	}
	interval := resolutionInterval(r)
	if interval == 0 {
		return nil
	}
	// Sub saturates rather than overflowing, so a range across centuries
	// reads as the longest duration and is refused like any too-wide one.
	if span := end.Sub(start); span/interval >= MaxTimeseriesBuckets {
		return fmt.Errorf("the range holds more than %d %s buckets; choose a coarser resolution or a shorter range: %w",
			MaxTimeseriesBuckets, r, ErrTimeseriesRange)
	}
	return nil
}

// ZeroFill expands a sparse set of timeseries buckets into a complete
// series covering [start, end] at the given resolution. Missing buckets
// are filled with zero values.
func ZeroFill(buckets []TimeseriesBucket, start, end time.Time, resolution Resolution) []TimeseriesBucket {
	interval := resolutionInterval(resolution)
	if interval == 0 || CheckTimeseriesRange(start, end, resolution) != nil {
		// A range past the bound is never expanded: the sparse buckets are
		// what the query found.
		return buckets
	}

	// Build lookup map from existing buckets (keyed by truncated time).
	existing := make(map[int64]TimeseriesBucket, len(buckets))
	for _, b := range buckets {
		existing[b.Bucket.Unix()] = b
	}

	// Generate complete series.
	truncStart := truncateTime(start, resolution)
	truncEnd := truncateTime(end, resolution)

	var result []TimeseriesBucket
	for t := truncStart; !t.After(truncEnd); t = t.Add(interval) {
		if b, ok := existing[t.Unix()]; ok {
			result = append(result, b)
		} else {
			result = append(result, TimeseriesBucket{Bucket: t})
		}
	}

	if result == nil {
		return []TimeseriesBucket{}
	}
	return result
}

// resolutionInterval maps a Resolution to its corresponding time.Duration.
func resolutionInterval(r Resolution) time.Duration {
	switch r {
	case ResolutionMinute:
		return time.Minute
	case ResolutionHour:
		return time.Hour
	case ResolutionDay:
		return hoursPerDay * time.Hour
	default:
		return 0
	}
}

// truncateTime truncates a time to the bucket boundary for the given resolution.
func truncateTime(t time.Time, r Resolution) time.Time {
	switch r {
	case ResolutionMinute:
		return t.Truncate(time.Minute)
	case ResolutionHour:
		return t.Truncate(time.Hour)
	case ResolutionDay:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	default:
		return t
	}
}
