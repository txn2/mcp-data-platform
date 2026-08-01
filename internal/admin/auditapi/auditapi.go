// Package auditapi serves the /api/v1/admin/audit surface: the raw event
// list, its filter vocabulary and single-event lookup, and the aggregate
// metrics behind the admin dashboard. It is a decomposition seam of pkg/admin
// (which sat at the package size budget); the parent registers it on the
// admin mux.
//
// The two stores are separate because they are separately optional: a
// deployment can serve the event list without the aggregate rollups, and the
// parent keeps its own 409 fallback for the case where audit is enabled in
// config but no database backs it.
package auditapi

import (
	"context"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// EventQuerier reads raw audit events and their filter vocabulary.
type EventQuerier interface {
	Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error)
	Count(ctx context.Context, filter audit.QueryFilter) (int, error)
	Distinct(ctx context.Context, column string, startTime, endTime *time.Time) ([]string, error)
	DistinctPairs(ctx context.Context, col1, col2 string, startTime, endTime *time.Time) (map[string]string, error)
}

// MetricsQuerier provides the aggregate audit rollups.
type MetricsQuerier interface {
	Timeseries(ctx context.Context, filter audit.TimeseriesFilter) ([]audit.TimeseriesBucket, error)
	Breakdown(ctx context.Context, filter audit.BreakdownFilter) ([]audit.BreakdownEntry, error)
	Overview(ctx context.Context, filter audit.MetricsFilter) (*audit.Overview, error)
	Performance(ctx context.Context, filter audit.MetricsFilter) (*audit.PerformanceStats, error)
	Enrichment(ctx context.Context, filter audit.MetricsFilter) (*audit.EnrichmentStats, error)
	Discovery(ctx context.Context, filter audit.MetricsFilter) (*audit.DiscoveryStats, error)
}

// Config carries the stores these routes need. Both are independently
// optional; a nil store simply leaves its routes unregistered.
type Config struct {
	// Events reads raw audit events. nil leaves the event routes off, which
	// is the parent's cue to mount its feature-unavailable fallback.
	Events EventQuerier
	// Metrics reads the aggregate rollups. nil leaves the metrics routes
	// off.
	Metrics MetricsQuerier
}

// handler binds the routes to their dependencies.
type handler struct {
	cfg Config
}

// Register mounts the audit routes on mux. Every route is read-only, so
// unlike the other admin surfaces none of this is gated on config mode.
func Register(mux *http.ServeMux, cfg Config) {
	h := &handler{cfg: cfg}
	h.registerAuditRoutes(mux)
	h.registerAuditMetricsRoutes(mux)
}

// registerAuditRoutes mounts the raw audit event endpoints.
func (h *handler) registerAuditRoutes(mux *http.ServeMux) {
	if h.cfg.Events == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/admin/audit/events/filters", h.listAuditEventFilters)
	mux.HandleFunc("GET /api/v1/admin/audit/events", h.listAuditEvents)
	mux.HandleFunc("GET /api/v1/admin/audit/events/{id}", h.getAuditEvent)
	mux.HandleFunc("GET /api/v1/admin/audit/stats", h.getAuditStats)
}

// pathParamID is the {id} path placeholder on the single-event route.
const pathParamID = "id"
