package webhookapi

import (
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

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
	// Spools is every receiver's local spool, by replica: empty where no
	// receiver spools (#2121).
	Spools []ReplicaSpool `json:"spools"`
}

// ReplicaSpool is one receiver's local spool as it last reported it (#2121):
// the events it acknowledged and has not yet drained to object storage, and
// how its drain is going.
type ReplicaSpool struct {
	Replica string `json:"replica"`
	Dir     string `json:"dir"`
	Bytes   int64  `json:"bytes"`
	Events  int64  `json:"events"`
	Files   int    `json:"files"`
	// OldestAt is when the oldest event waiting was received; null when the
	// spool is empty.
	OldestAt *time.Time `json:"oldest_at"`
	// MaxBytes and MaxAgeSeconds are the bounds past which the receiver
	// answers 503 with Retry-After.
	MaxBytes      int64 `json:"max_bytes"`
	MaxAgeSeconds int64 `json:"max_age_seconds"`
	// DrainedEvents and DrainFailures count since the receiver started.
	DrainedEvents int64      `json:"drained_events"`
	DrainFailures int64      `json:"drain_failures"`
	LastDrainAt   *time.Time `json:"last_drain_at"`
	// LastError is the newest drain failure, cleared by a drain that
	// succeeds.
	LastError string    `json:"last_error"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Reporting is false for a receiver that has not reported for two
	// minutes: it stopped, and what it holds waits for it to start again.
	Reporting bool `json:"reporting"`
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
	// AckLastHour and AckLastDay are how long the deliveries answered 202
	// waited for that answer.
	AckLastHour whstore.AckLatency `json:"ack_last_hour"`
	AckLastDay  whstore.AckLatency `json:"ack_last_day"`
	// SegmentsOpenWindow is how many segments were recorded against the
	// source's open window, and SegmentsPerWindowP95 the 95th percentile of
	// that count over the windows that started in the last day (#2132). Each
	// is an object a reader of the window reads until the window is
	// compacted; a count can exceed the segments written by one per replica.
	SegmentsOpenWindow   int64 `json:"segments_open_window" example:"412"`
	SegmentsPerWindowP95 int64 `json:"segments_per_window_p95" example:"3650"`
}

// VolumePoint is the number of requests of one source with one outcome in the
// bucket starting at At. A bucket with no requests has no point.
type VolumePoint struct {
	At      time.Time `json:"at"`
	Source  string    `json:"source"`
	Outcome string    `json:"outcome"`
	Count   int64     `json:"count"`
}

// SourceRejection is a run of refused requests of one source with one outcome
// and reason (#2001): the latest at At, the first at FirstAt, Count in all.
type SourceRejection struct {
	Source  string    `json:"source"`
	At      time.Time `json:"at"`
	FirstAt time.Time `json:"first_at"`
	Count   int64     `json:"count"`
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason"`
	// RequestHash is the SHA-256 of the refused request's body and EventIDs
	// the ids of its first 20 events, when its events were read; empty for a
	// request refused before that.
	RequestHash string   `json:"request_hash"`
	EventIDs    []string `json:"event_ids"`
}
