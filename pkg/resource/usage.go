package resource

import (
	"context"
	"fmt"
	"time"
)

// Read surfaces a resource's content can be served through. Recorded on every
// read event so a curator can tell material an agent actually pulls from
// material a human downloads once and forgets.
const (
	// SurfaceMCPRead is an MCP resources/read call.
	SurfaceMCPRead = "mcp_read"
	// SurfaceFetch is a search `fetch` of an mcp:resource:<id> reference.
	SurfaceFetch = "fetch"
	// SurfaceDownload is a REST content download (the portal's Download
	// button, and any direct API client).
	SurfaceDownload = "rest_download"
)

// ReadEvent describes one served read of a resource's content.
type ReadEvent struct {
	ResourceID string
	URI        string
	// Surface is one of the Surface* constants: which door served the content.
	Surface string
	// Version is the revision served, or 0 when the head was served without
	// naming a version.
	Version   int
	UserID    string
	UserEmail string
	Persona   string
	SessionID string
	RequestID string
}

// ReadRecorder records a served read. Every surface that hands a resource's
// bytes to a caller calls it, so "who read this, through which door" has one
// answer rather than one per surface.
//
// Implementations are best-effort and must not fail the read: a recorder that
// cannot write must swallow the failure, because refusing to serve a file
// because its audit row would not persist trades a working read for a lost log
// line. Recording is off entirely when audit is disabled, which is why every
// call site tolerates a nil recorder.
type ReadRecorder interface {
	RecordRead(ctx context.Context, ev ReadEvent)
}

// Usage is the audit-derived read activity of a single resource.
type Usage struct {
	// Reads30d and Reads90d count audited reads in the trailing 30 and 90
	// days. Both are bounded by the audit retention window: a deployment
	// keeping 30 days of audit reports the same number twice.
	Reads30d int64 `json:"reads_30d" example:"42"`
	Reads90d int64 `json:"reads_90d" example:"117"`
	// BySurface30d breaks the 30-day count down by Surface* value.
	BySurface30d map[string]int64 `json:"by_surface_30d,omitempty"`
	// LastReadAt is the most recent audited read within the retention window.
	// The durable answer lives on the resource row (Resource.LastReadAt), which
	// outlives retention; this field is what the rollup itself saw.
	LastReadAt *time.Time `json:"last_read_at,omitempty"`
}

// UsageReader aggregates read events into per-resource usage. The Postgres
// audit store implements it, the same way it implements prompt usage (#1009):
// the audit log is already the durable record of who read what, so usage is a
// rollup of it rather than a second set of counters to keep in sync.
type UsageReader interface {
	ResourceUsage(ctx context.Context, resourceIDs []string) (map[string]Usage, error)
}

// ReadTracker stamps the durable last-read time on a resource row. It is
// separate from the audit rollup because it answers a different question: the
// rollup is bounded by audit retention and cannot be sorted on in SQL, while
// this column survives retention and is what the admin table orders by.
type ReadTracker interface {
	TouchRead(ctx context.Context, id string, at time.Time) error
}

// TouchRead records that a resource's content was served at at. A read of a
// resource that no longer exists is not an error: the row can be deleted
// between the content read and the stamp.
func (s *postgresStore) TouchRead(ctx context.Context, id string, at time.Time) error { //nolint:revive // interface impl
	if _, err := s.db.ExecContext(ctx,
		`UPDATE resources SET last_read_at = $1 WHERE id = $2`, at.UTC(), id); err != nil {
		return fmt.Errorf("stamping resource last read: %w", err)
	}
	return nil
}

// Verify interface compliance.
var _ ReadTracker = (*postgresStore)(nil)
