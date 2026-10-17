package whstore

import "time"

// StatusPoint is a number of requests one source was answered with one HTTP
// status in one bucket.
type StatusPoint struct {
	At     time.Time `json:"at"`
	Source string    `json:"source"`
	Status int       `json:"http_status"`
	Count  int64     `json:"count"`
}

// AckPoint is one source's acknowledgement latency in one bucket, with the
// average time each stage took.
type AckPoint struct {
	At     time.Time `json:"at"`
	Source string    `json:"source"`
	Count  int64     `json:"count"`
	P50MS  int64     `json:"p50_ms"`
	P99MS  int64     `json:"p99_ms"`
	MaxMS  int64     `json:"max_ms"`
	// BufferMS, MarkMS and PutMS are the average delivery's wait in each
	// stage; with a spool, put is the spool's sync.
	BufferMS float64 `json:"buffer_ms"`
	MarkMS   float64 `json:"mark_ms"`
	PutMS    float64 `json:"put_ms"`
}

// SourceStorage is one source's objects: the raw segments not yet released
// and the compacted files of the windows not expired.
type SourceStorage struct {
	Source           string `json:"source"`
	RawBytes         int64  `json:"raw_bytes"`
	RawSegments      int64  `json:"raw_segments"`
	CompactedBytes   int64  `json:"compacted_bytes"`
	CompactedWindows int64  `json:"compacted_windows"`
}

// Telemetry is the history the webhooks page charts over a span (#2150).
type Telemetry struct {
	Statuses    []StatusPoint   `json:"statuses"`
	Replicas    []ReplicaMinute `json:"replicas"`
	Ack         []AckPoint      `json:"ack"`
	Compactions []CompactionRun `json:"compactions"`
	Storage     []SourceStorage `json:"storage"`
}
