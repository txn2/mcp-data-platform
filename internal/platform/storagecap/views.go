package storagecap

import "time"

// BucketView is what the platform holds in one bucket for one purpose.
type BucketView struct {
	Bucket   string     `json:"bucket"`
	Purpose  string     `json:"purpose"`
	Bytes    int64      `json:"bytes"`
	Objects  int64      `json:"objects"`
	ListedAt *time.Time `json:"listed_at"`
}

// ReconcileView is what the last reconcile found for one purpose.
type ReconcileView struct {
	Purpose      string    `json:"purpose"`
	Orphaned     int64     `json:"orphaned"`
	Dangling     int64     `json:"dangling"`
	ReconciledAt time.Time `json:"reconciled_at"`
}

// ScanView is the last full listing of the buckets.
type ScanView struct {
	DurationSeconds float64   `json:"duration_seconds"`
	Objects         int64     `json:"objects"`
	FinishedAt      time.Time `json:"finished_at"`
}

// ClusterView is the SeaweedFS cluster as last read, or why it was not.
type ClusterView struct {
	MasterURL string          `json:"master_url"`
	Capacity  ClusterCapacity `json:"capacity"`
	Error     string          `json:"error,omitempty"`
}

// SpoolView is one receiver's spool and the volume it is on.
type SpoolView struct {
	Replica   string    `json:"replica"`
	Dir       string    `json:"dir"`
	Bytes     int64     `json:"bytes"`
	MaxBytes  int64     `json:"max_bytes"`
	FsTotal   *int64    `json:"fs_total_bytes"`
	FsFree    *int64    `json:"fs_free_bytes"`
	Reporting bool      `json:"reporting"`
	UpdatedAt time.Time `json:"updated_at"`
}
