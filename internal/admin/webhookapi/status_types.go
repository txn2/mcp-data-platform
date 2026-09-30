package webhookapi

import "time"

// The shape of the overview of every source (#1979).

// StatusOverview is the status of every source in one response (#1979): each
// source's health and counts, the request series over the range, and the
// newest rejections of every source.
type StatusOverview struct {
	GeneratedAt time.Time `json:"generated_at"`
	// Range is the span the series covers: hour or day.
	Range string `json:"range" enums:"hour,day" example:"hour"`
	// From is the series' lower bound: a minute's counts are in the series
	// when the minute is at or after it.
	From time.Time `json:"from"`
	// BucketSeconds is the length of one bucket of the series.
	BucketSeconds int `json:"bucket_seconds" example:"60"`
	// SilentAfterSeconds is how long an enabled source may go without an
	// event before it is reported silent.
	SilentAfterSeconds int               `json:"silent_after_seconds" example:"86400"`
	Sources            []SourceStatus    `json:"sources"`
	Volume             []VolumePoint     `json:"volume"`
	Rejections         []SourceRejection `json:"rejections"`
}

// SourceStatus is one source's line on the overview.
type SourceStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Health is the source's overall state. When more than one applies, the
	// first of disabled, failing, silent, receiving is reported.
	Health     string `json:"health" enums:"receiving,silent,failing,disabled"`
	AuthMode   string `json:"auth_mode"`
	Connection string `json:"connection"`
	Table      string `json:"table"`
	// LastEventAt is when the source last received an event; null when it
	// never has, or not within the windows it still holds.
	LastEventAt *time.Time `json:"last_event_at"`
	// LastHour and LastDay are request counts by outcome.
	LastHour map[string]int64 `json:"last_hour"`
	LastDay  map[string]int64 `json:"last_day"`
	// Pending counts windows owed a compaction; Failing, those whose last
	// attempt failed, with the newest one's error in LastError.
	Pending   int    `json:"pending"`
	Failing   int    `json:"failing"`
	LastError string `json:"last_error,omitempty"`
}

// VolumePoint is the number of requests of one source with one outcome in the
// bucket starting at At. A bucket with no requests has no point.
type VolumePoint struct {
	At      time.Time `json:"at"`
	Source  string    `json:"source"`
	Outcome string    `json:"outcome"`
	Count   int64     `json:"count"`
}

// SourceRejection is one refused request and the source it was sent to.
type SourceRejection struct {
	Source  string    `json:"source"`
	At      time.Time `json:"at"`
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason"`
}
