package webhookapi

import (
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whadmin"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// sizingWarnPercent is the share of the memory limit at which the largest
// window is warned about: the compactor holds a whole window, and the rest of
// the process needs the other half.
const sizingWarnPercent = 50

// percent scales a fraction to the percent the sizing is shown in.
const percent = 100

// StatusHistory is the telemetry the webhooks page charts over the range
// (#2150).
type StatusHistory struct {
	// Statuses counts requests by the HTTP status they were answered with.
	Statuses []whstore.StatusPoint `json:"statuses"`
	// Replicas is each receiver's events received and written to object
	// storage per bucket, and its spool's depth and oldest age.
	Replicas []whstore.ReplicaMinute `json:"replicas"`
	// Ack is the acknowledgement latency per bucket with each stage's
	// average.
	Ack []whstore.AckPoint `json:"ack"`
	// Compactions is each window's compaction in the range, newest first.
	Compactions []whstore.CompactionRun `json:"compactions"`
	// Storage is each source's raw and compacted bytes now.
	Storage []whstore.SourceStorage `json:"storage"`
	Sizing  WindowSizing            `json:"sizing"`
}

// WindowSizing sets the largest window compacted in the range against this
// replica's memory limit.
type WindowSizing struct {
	// MemoryLimitBytes is the memory this replica may use, 0 when it has no
	// limit.
	MemoryLimitBytes int64 `json:"memory_limit_bytes"`
	// Largest is the window that took the most memory, null when none was
	// compacted in the range.
	Largest *LargestWindow `json:"largest"`
	// WarnAtPercent is the share of the limit Warning is raised at.
	WarnAtPercent int  `json:"warn_at_percent"`
	Warning       bool `json:"warning"`
}

// LargestWindow is the window that took the most memory to compact.
type LargestWindow struct {
	Source       string    `json:"source"`
	WindowStart  time.Time `json:"window_start"`
	DecodedBytes int64     `json:"decoded_bytes"`
	InputBytes   int64     `json:"input_bytes"`
	Events       int64     `json:"events"`
	// PercentOfLimit is DecodedBytes as a percent of the memory limit, 0
	// when there is no limit.
	PercentOfLimit float64 `json:"percent_of_limit"`
}

// historyOf renders the telemetry, every list empty rather than null.
func historyOf(ov whadmin.Overview) StatusHistory {
	t := ov.Telemetry
	out := StatusHistory{
		Statuses: nonNilSlice(t.Statuses), Replicas: nonNilSlice(t.Replicas), Ack: nonNilSlice(t.Ack),
		Compactions: nonNilSlice(t.Compactions), Storage: nonNilSlice(t.Storage),
		Sizing: WindowSizing{MemoryLimitBytes: ov.MemoryLimit, WarnAtPercent: sizingWarnPercent},
	}
	for _, c := range t.Compactions {
		if out.Sizing.Largest != nil && c.DecodedBytes <= out.Sizing.Largest.DecodedBytes {
			continue
		}
		out.Sizing.Largest = &LargestWindow{
			Source: c.Source, WindowStart: c.WindowStart, DecodedBytes: c.DecodedBytes, InputBytes: c.InputBytes, Events: c.Events,
		}
	}
	if l := out.Sizing.Largest; l != nil && ov.MemoryLimit > 0 {
		l.PercentOfLimit = percent * float64(l.DecodedBytes) / float64(ov.MemoryLimit)
		out.Sizing.Warning = l.PercentOfLimit >= sizingWarnPercent
	}
	return out
}

// nonNilSlice returns s, or an empty slice in place of nil.
func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
