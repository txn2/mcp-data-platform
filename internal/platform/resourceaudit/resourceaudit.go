// Package resourceaudit records what happens to a managed resource as audit
// events: reads of its content, which also stamp the durable last-read time on
// the resource row, and the move that refiles it in another library.
//
// It exists so the doors that serve a resource's bytes — MCP resources/read, a
// search `fetch` of an mcp:resource:<id> reference, the REST content download,
// and the portal's own preview of an image in the library — produce one event
// shape from one implementation. A
// per-surface copy would drift on exactly the fields a curator asks about
// ("which door was this read through, and when was it last touched"), and the
// usage rollup reads those fields back by name.
//
// The recorder is the audit-enabled switch for the feature: a deployment with
// audit disabled wires no recorder, so reads are neither audited nor counted and
// continue to serve unchanged. Recording never fails a read — a resource whose
// audit row cannot be written is still a resource the caller asked for.
package resourceaudit

import (
	"context"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Parameter keys carried on a resource_read event. The usage rollup
// (pkg/audit/postgres.ResourceUsage) reads resource_id and surface back by
// name, so these are a contract, not labels.
const (
	paramResourceID  = "resource_id"
	paramResourceURI = "resource_uri"
	paramSurface     = "surface"
	paramVersion     = "version"
)

// Parameter keys carried on a resource_move event: the file, and the library
// and address on each side of the move.
const (
	paramDisplayName = "display_name"
	paramFromScope   = "from_scope"
	paramFromScopeID = "from_scope_id"
	paramFromPath    = "from_path"
	paramFromURI     = "from_uri"
	paramToScope     = "to_scope"
	paramToScopeID   = "to_scope_id"
	paramToPath      = "to_path"
	paramToURI       = "to_uri"
)

// toolNameResourceMove is the tool_name a move is filed under. The column is
// what the audit views group by, and a move is not a tool call, so it carries
// the name of the act.
const toolNameResourceMove = "resource_move"

// logKeyError is the slog key for error values.
const logKeyError = "error"

// Recorder writes resource read events through an audit logger and stamps the
// resource's last-read column.
type Recorder struct {
	logger  middleware.AuditLogger
	tracker resource.ReadTracker
	now     func() time.Time
}

// New builds a recorder over an audit logger and an optional read tracker.
//
// logger determines the delivery discipline: pass the platform's async logger
// on the MCP paths, where a synchronous insert would sit in front of an agent's
// read, and the store-backed logger on the REST path, where the caller is a
// human clicking Download. A nil logger yields a nil Recorder, which every call
// site tolerates, so "audit disabled" needs no second switch.
//
// tracker may be nil (a store that does not implement resource.ReadTracker);
// the events are still written and only the sortable last-read column goes
// unstamped.
func New(logger middleware.AuditLogger, tracker resource.ReadTracker) *Recorder {
	if logger == nil {
		return nil
	}
	return &Recorder{logger: logger, tracker: tracker, now: func() time.Time { return time.Now().UTC() }}
}

// RecordRead writes one resource_read audit event and, for every surface but
// the portal's preview, stamps the resource's last-read time. Both failures are
// logged and swallowed: the content has
// already been served (or is about to be), and a lost audit row must not become
// a failed read.
//
// The stamp is a single UPDATE by primary key issued inline rather than from a
// goroutine: a detached write per read is the unbounded-goroutine shape a
// stalled database turns into a leak (#884), and the audit write itself is
// already where the latency budget went.
func (r *Recorder) RecordRead(ctx context.Context, ev resource.ReadEvent) {
	if r == nil {
		return
	}
	at := r.now()
	if err := r.logger.Log(ctx, readEvent(ctx, ev, at)); err != nil {
		slog.Warn("resource read audit: write failed", logKeyError, err,
			paramResourceID, ev.ResourceID) // #nosec G706 -- server-generated ID
	}
	// A preview is the portal drawing the library, not somebody using the file,
	// so it is audited and does not move the column a curator sorts dead weight
	// out by (resource.SurfacePreview).
	if r.tracker == nil || !resource.StampsLastRead(ev.Surface) {
		return
	}
	if err := r.tracker.TouchRead(ctx, ev.ResourceID, at); err != nil {
		slog.Warn("resource read audit: last-read stamp failed", logKeyError, err,
			paramResourceID, ev.ResourceID) // #nosec G706 -- server-generated ID
	}
}

// readEvent builds the audit event for a served read, preferring the identity
// on the request's PlatformContext when one is present (the MCP paths) and
// falling back to the identity the calling surface resolved (the REST path,
// which runs outside the tool-call middleware chain and has no
// PlatformContext).
func readEvent(ctx context.Context, ev resource.ReadEvent, at time.Time) middleware.AuditEvent {
	params := map[string]any{
		paramResourceID:  ev.ResourceID,
		paramResourceURI: ev.URI,
		paramSurface:     ev.Surface,
	}
	if ev.Version > 0 {
		params[paramVersion] = ev.Version
	}

	out := middleware.AuditEvent{
		Timestamp: at,
		ToolName:  ev.Surface,
		UserID:    ev.UserID,
		UserEmail: ev.UserEmail,
		Persona:   ev.Persona,
		SessionID: ev.SessionID,
		RequestID: ev.RequestID,
		// The read already passed its visibility check; without these the row
		// would persist as an unauthorized failure and read as a denial.
		Success:    true,
		Authorized: true,
		Source:     sourceForSurface(ev.Surface),
		EventKind:  string(audit.EventTypeResourceRead),
		Parameters: params,
	}
	applyPlatformContext(ctx, &out)
	return out
}

// applyPlatformContext overlays the request-scoped identity when the read ran
// inside the MCP middleware chain. Fields the surface already resolved are kept
// when the context carries nothing for them, so the REST path's identity is not
// blanked by an absent context.
func applyPlatformContext(ctx context.Context, ev *middleware.AuditEvent) {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return
	}
	if pc.UserID != "" {
		ev.UserID = pc.UserID
	}
	if pc.UserEmail != "" {
		ev.UserEmail = pc.UserEmail
	}
	if pc.PersonaName != "" {
		ev.Persona = pc.PersonaName
	}
	if pc.SessionID != "" {
		ev.SessionID = pc.SessionID
	}
	if pc.RequestID != "" {
		ev.RequestID = pc.RequestID
	}
	if pc.Transport != "" {
		ev.Transport = pc.Transport
	}
	if pc.Source != "" {
		ev.Source = pc.Source
	}
}

// sourceForSurface maps a read surface onto the coarse audit source population:
// the two agent-facing doors are MCP traffic, and a content download is a
// portal-driven REST call. The precise door is on the surface parameter; this
// only sorts the row into the population operators filter audit_logs by.
func sourceForSurface(surface string) string {
	if surface == resource.SurfaceDownload {
		return middleware.SourceAdmin
	}
	return middleware.SourceMCP
}

// RecordMove writes one resource_move audit event. A folder rename produces one
// per resource it carried, which is what makes the trail answer "what address
// does this file have now" for each of them rather than only for the folder.
//
// A failure is logged and swallowed: the resource has already been refiled, and
// a lost audit row must not become a failed move reported to somebody whose file
// did move.
//
// It shares the Recorder with reads because it shares everything that decides
// how the row is written -- the logger, the identity overlay, the delivery
// discipline -- and because "what has happened to this resource" is one trail.
func (r *Recorder) RecordMove(ctx context.Context, ev resource.MoveEvent) {
	if r == nil {
		return
	}
	if err := r.logger.Log(ctx, moveEvent(ctx, ev, r.now())); err != nil {
		slog.Warn("resource move audit: write failed", logKeyError, err,
			paramResourceID, ev.ResourceID) // #nosec G706 -- server-generated ID
	}
}

// moveEvent builds the audit event for a completed move. The identity the
// calling surface resolved is the REST caller's; applyPlatformContext overlays
// a request-scoped one where there is any, exactly as a read does.
func moveEvent(ctx context.Context, ev resource.MoveEvent, at time.Time) middleware.AuditEvent {
	out := middleware.AuditEvent{
		Timestamp: at,
		ToolName:  toolNameResourceMove,
		UserID:    ev.UserID,
		UserEmail: ev.UserEmail,
		// The move passed its permission check before the write; without these
		// the row would persist as an unauthorized failure and read as a denial.
		Success:    true,
		Authorized: true,
		Source:     middleware.SourceAdmin,
		EventKind:  string(audit.EventTypeResourceMove),
		Parameters: map[string]any{
			paramResourceID:  ev.ResourceID,
			paramDisplayName: ev.DisplayName,
			paramFromScope:   string(ev.FromScope),
			paramFromScopeID: ev.FromScopeID,
			paramFromPath:    ev.FromPath,
			paramFromURI:     ev.FromURI,
			paramToScope:     string(ev.ToScope),
			paramToScopeID:   ev.ToScopeID,
			paramToPath:      ev.ToPath,
			paramToURI:       ev.ToURI,
		},
	}
	applyPlatformContext(ctx, &out)
	return out
}

// Verify interface compliance.
var (
	_ resource.ReadRecorder = (*Recorder)(nil)
	_ resource.MoveRecorder = (*Recorder)(nil)
)
