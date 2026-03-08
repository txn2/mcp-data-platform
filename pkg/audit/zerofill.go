package audit

import "time"

// hoursPerDay is the number of hours in a day.
const hoursPerDay = 24

// ZeroFill expands a sparse set of timeseries buckets into a complete
// series covering [start, end] at the given resolution. Missing buckets
// are filled with zero values.
func ZeroFill(buckets []TimeseriesBucket, start, end time.Time, resolution Resolution) []TimeseriesBucket {
	interval := resolutionInterval(resolution)
	if interval == 0 {
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
