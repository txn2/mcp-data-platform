// Package provenance answers, at the moment an asset is written, which calls
// produced it.
//
// It reads the audit log rather than keeping a buffer of its own. The audit log
// is already the platform's record of every call: it survives a restart, it is
// shared by every replica, and it holds what a buffer never did — the call's
// identifier, its stated purpose, how long it took, and whether it succeeded.
// A per-process map could answer none of that, and answered nothing at all for
// a session whose calls were served by another replica (issue #1320).
//
// A capture is scoped to the caller: the default window is the caller's own
// session, and an explicitly cited source is resolved only among the caller's
// own calls. Nothing here can put another person's query into an asset.
package provenance

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

const (
	// MaxCalls bounds how many calls one capture records. A capture is a
	// snapshot stored on the asset row, not a copy of the session.
	MaxCalls = 100

	// scanLimit bounds how far back through a session's calls the default
	// window looks for the previous capture. A session that ran more calls
	// than this since its last capture yields the most recent scanLimit of
	// them, with Truncated set.
	scanLimit = 500

	// maxSources bounds how many event ids a caller may cite at once.
	maxSources = MaxCalls

	// flushTimeout bounds the wait for queued audit events to reach the
	// store. Exceeding it costs accuracy on the newest call, never the write.
	flushTimeout = 2 * time.Second
)

// sourceKinds maps a toolkit kind to the kind of call it produces. A toolkit
// kind absent from this map is platform bookkeeping — saving an asset, reading
// memory, searching the catalog — and is never an asset's source.
var sourceKinds = map[string]string{
	"trino":   portal.ProvenanceKindSQL,
	"api":     portal.ProvenanceKindAPI,
	"datahub": portal.ProvenanceKindTool,
	"s3":      portal.ProvenanceKindTool,
	// "mcp" is the MCP gateway toolkit's kind: every tool it proxies from an
	// upstream server, whose names are chosen upstream. Keyed by kind for the
	// same reason the purpose gate is (see middleware's "kind:mcp" entry).
	"mcp": portal.ProvenanceKindTool,
}

// SourceToolkitKinds returns the toolkit kinds whose calls can be an asset's
// source. The call-reference middleware stamps exactly these calls with their
// own id, so what an agent can cite and what the platform captures by default
// are one rule rather than two lists that drift.
func SourceToolkitKinds() []string {
	kinds := make([]string, 0, len(sourceKinds))
	for k := range sourceKinds {
		kinds = append(kinds, k)
	}
	return kinds
}

// KindFor returns the provenance call kind for a toolkit kind, or "" when
// calls to that toolkit are not asset sources.
func KindFor(toolkitKind string) string { return sourceKinds[toolkitKind] }

// EventReader reads recorded calls back out of the audit log.
type EventReader interface {
	Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error)
}

// Flusher waits for already-enqueued audit events to reach the store. The
// platform's audit writer is asynchronous, so without this the call that most
// obviously produced the asset — the one that just finished — is the one most
// likely to be missing when the capture reads.
type Flusher interface {
	Flush(ctx context.Context) error
}

// Capturer builds an asset write's provenance capture from the audit log.
type Capturer struct {
	events EventReader
	flush  Flusher
	now    func() time.Time
}

// New builds a Capturer over the audit log. A nil reader yields a Capturer
// that records only what the caller states about itself, which is what a
// deployment with audit disabled gets. The flusher is optional: a synchronous
// audit writer has nothing to wait for.
func New(events EventReader, flush Flusher) *Capturer {
	return &Capturer{events: events, flush: flush, now: time.Now}
}

// Capture resolves the calls behind one asset write.
//
// It never fails the write: an unreadable audit log yields a capture holding
// only what the caller stated about itself, because an asset that records less
// provenance is better than an asset that could not be saved.
func (c *Capturer) Capture(ctx context.Context, req portal.ProvenanceRequest) portal.ProvenanceCapture {
	capture := portal.ProvenanceCapture{
		Tool:      req.Tool,
		SessionID: req.SessionID,
		Version:   req.Version,
	}
	if c == nil {
		capture.CapturedAt = time.Now().UTC()
		return appendOwn(capture, req.Own)
	}
	capture.CapturedAt = c.now().UTC()
	if c.events == nil {
		return appendOwn(capture, req.Own)
	}

	sources := parseSources(req.Sources)
	if len(sources) == 0 && req.SessionID == "" {
		// Nothing to read: no cited call, and no session whose calls could be
		// the default window. Waiting on the audit writer would buy nothing.
		return appendOwn(capture, req.Own)
	}
	c.waitForWrites(ctx)

	var (
		events    []audit.Event
		truncated bool
	)
	if len(sources) > 0 {
		capture.Explicit = true
		events, truncated = c.resolveCited(ctx, req, sources)
	} else {
		events, truncated = c.resolveWindow(ctx, req)
	}

	capture.Truncated = truncated
	for i := range events {
		call := callFromEvent(events[i])
		capture.Calls = append(capture.Calls, call)
		capture.EventIDs = append(capture.EventIDs, call.EventID)
	}
	return appendOwn(capture, req.Own)
}

// appendOwn adds the capturing call's own record, which no audit row can
// supply yet: the row for the call performing the write is written after it
// returns.
func appendOwn(capture portal.ProvenanceCapture, own *portal.ProvenanceCall) portal.ProvenanceCapture {
	if own == nil {
		return capture
	}
	call := *own
	if call.Timestamp.IsZero() {
		call.Timestamp = capture.CapturedAt
	}
	if call.Outcome == "" {
		call.Outcome = portal.ProvenanceOutcomeSuccess
	}
	capture.Calls = append(capture.Calls, call)
	return capture
}

// waitForWrites drains the audit writer's queue so a call that just completed
// is readable. A failure here is degraded accuracy, not a failed write.
func (c *Capturer) waitForWrites(ctx context.Context) {
	if c.flush == nil {
		return
	}
	flushCtx, cancel := context.WithTimeout(ctx, flushTimeout)
	defer cancel()
	if err := c.flush.Flush(flushCtx); err != nil {
		slog.Warn("provenance: audit flush did not complete; the newest call may be missing from this capture",
			"error", err)
	}
}

// parseSources normalizes cited sources to bare event ids, dropping empties
// and duplicates and keeping at most maxSources of them.
func parseSources(sources []string) []string {
	seen := make(map[string]struct{}, len(sources))
	ids := make([]string, 0, len(sources))
	for _, s := range sources {
		id, ok := portal.ParseCallReference(s)
		if !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		if len(ids) == maxSources {
			break
		}
	}
	return ids
}

// resolveCited loads the calls the caller named, scoped to the caller's own
// calls. An id that names nothing the caller made is dropped rather than
// reported: provenance states what is known, and a citation the platform
// cannot confirm is not knowledge.
func (c *Capturer) resolveCited(ctx context.Context, req portal.ProvenanceRequest, ids []string) (events []audit.Event, truncated bool) {
	filter := audit.QueryFilter{
		IDs:       ids,
		SortBy:    "timestamp",
		SortOrder: audit.SortAsc,
		Limit:     len(ids),
	}
	// Scope in SQL, then check again in Go: the citation is caller-supplied
	// and this is the boundary that stops one person's call from being
	// recorded as another person's source.
	if req.UserID != "" {
		filter.UserID = req.UserID
	} else {
		filter.SessionID = req.SessionID
	}
	rows, err := c.events.Query(ctx, filter)
	if err != nil {
		slog.Warn("provenance: cited sources could not be read", "error", err, "tool", req.Tool)
		return nil, false
	}
	for _, ev := range rows {
		if !ownedByCaller(ev, req) {
			continue
		}
		events = append(events, ev)
	}
	return events, len(ids) > len(events)
}

// resolveWindow returns the session's data-access calls since its previous
// capture, oldest first.
func (c *Capturer) resolveWindow(ctx context.Context, req portal.ProvenanceRequest) (events []audit.Event, truncated bool) {
	if req.SessionID == "" {
		return nil, false
	}
	rows, err := c.events.Query(ctx, audit.QueryFilter{
		SessionID: req.SessionID,
		SortBy:    "timestamp",
		SortOrder: audit.SortDesc,
		Limit:     scanLimit,
	})
	if err != nil {
		slog.Warn("provenance: session calls could not be read", "error", err, "session_id", req.SessionID)
		return nil, false
	}

	// Newest first, back to the previous capture. Everything before that
	// boundary already belongs to the asset that capture recorded.
	reachedBoundary := false
	for _, ev := range rows {
		if isCaptureBoundary(ev) {
			reachedBoundary = true
			break
		}
		if !ownedByCaller(ev, req) || KindFor(ev.ToolkitKind) == "" {
			continue
		}
		if len(events) == MaxCalls {
			truncated = true
			break
		}
		events = append(events, ev)
	}
	if !reachedBoundary && len(rows) == scanLimit {
		truncated = true
	}

	reverse(events)
	return events, truncated
}

// ownedByCaller reports whether an event was produced by the caller taking the
// capture. It is deliberately strict about the fields it has: an event with no
// user identity is matched on its session instead, and an event that matches
// neither is not the caller's to record.
func ownedByCaller(ev audit.Event, req portal.ProvenanceRequest) bool {
	if req.UserID != "" && ev.UserID != "" {
		return ev.UserID == req.UserID
	}
	return req.SessionID != "" && ev.SessionID == req.SessionID
}

// isCaptureBoundary reports whether an event is a previous capture: the point
// at which an earlier asset write took the session's calls up to then.
//
// Every export tool is a boundary (the *_export naming is the platform's
// convention for stream-to-asset tools), as is save_asset. A manage_asset call
// is a boundary only when it changed content, which is the action recorded in
// its parameters; a list or a get is not a write and must not cut the window.
//
// A write that failed is not a boundary either: it captured nothing, so the
// calls before it still belong to whatever is written next. Both of the
// judgement calls here — an unknown manage_asset action, a write that ended in
// error — resolve the same way, toward recording a source twice rather than
// dropping the calls that produced the asset.
func isCaptureBoundary(ev audit.Event) bool {
	if !ev.Success {
		return false
	}
	switch {
	case strings.HasSuffix(ev.ToolName, "_export"):
		return true
	case ev.ToolName == saveAssetTool:
		return true
	case ev.ToolName == manageAssetTool:
		return contentAction(ev.Parameters)
	default:
		return false
	}
}

// Tool names of the platform's asset write path. They are the boundaries of
// the default source window; provenanceToolNamesMatchToolkit (in the tests)
// holds them to the toolkit's own constants.
const (
	saveAssetTool   = "save_asset"
	manageAssetTool = "manage_asset"
)

// contentActions are the manage_asset actions that write new content, and so
// take a capture of their own.
var contentActions = map[string]bool{"update": true, "patch": true}

func contentAction(params map[string]any) bool {
	action, _ := params["action"].(string)
	return contentActions[action]
}

// callFromEvent renders one audit row as a captured call.
func callFromEvent(ev audit.Event) portal.ProvenanceCall {
	call := portal.ProvenanceCall{
		EventID:    ev.ID,
		Kind:       KindFor(ev.ToolkitKind),
		Tool:       ev.ToolName,
		Connection: ev.Connection,
		Purpose:    ev.Purpose,
		Outcome:    portal.ProvenanceOutcomeSuccess,
		DurationMS: ev.DurationMS,
		Timestamp:  ev.Timestamp.UTC(),
	}
	if call.Kind == "" {
		// A cited call the platform does not classify as a query or an API
		// invocation is still the caller's own call and still recorded.
		call.Kind = portal.ProvenanceKindTool
	}
	if !ev.Success {
		call.Outcome = portal.ProvenanceOutcomeError
		call.Error = ev.ErrorMessage
	}
	describe(&call, ev.Parameters)
	return call
}

// describe fills in what the call did, from the arguments the audit row kept.
// Parameter capture can be disabled or redacted by policy, so every field here
// is best-effort: a call with no arguments recorded is still a call, named by
// its tool and its connection.
func describe(call *portal.ProvenanceCall, params map[string]any) {
	if len(params) == 0 {
		return
	}
	switch call.Kind {
	case portal.ProvenanceKindSQL:
		call.Statement = stringParam(params, "sql")
	case portal.ProvenanceKindAPI:
		call.Method = stringParam(params, "method")
		call.Path = stringParam(params, "path")
		call.OperationID = stringParam(params, "operation_id")
	default:
		call.Summary = toolSummary(params)
	}
}

// toolSummary names what a catalog or storage call addressed, so the reader
// sees "the orders dataset" rather than an opaque tool name. The keys are the
// addressing arguments the platform's data toolkits share.
func toolSummary(params map[string]any) string {
	for _, key := range []string{"urn", "query", "table", "key", "bucket", "path"} {
		if v := stringParam(params, key); v != "" {
			return v
		}
	}
	return ""
}

func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

func reverse(events []audit.Event) {
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
}
