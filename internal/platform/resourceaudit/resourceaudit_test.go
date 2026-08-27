package resourceaudit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// captureLogger records the events written through it.
type captureLogger struct {
	events []middleware.AuditEvent
	err    error
}

func (c *captureLogger) Log(_ context.Context, ev middleware.AuditEvent) error {
	c.events = append(c.events, ev)
	return c.err
}

// captureTracker records the last-read stamps applied.
type captureTracker struct {
	ids []string
	at  []time.Time
	err error
}

func (c *captureTracker) TouchRead(_ context.Context, id string, at time.Time) error {
	c.ids = append(c.ids, id)
	c.at = append(c.at, at)
	return c.err
}

func TestNew_NilLoggerDisablesRecording(t *testing.T) {
	rec := New(nil, &captureTracker{})
	if rec != nil {
		t.Fatal("New with no logger returned a recorder; audit-disabled deployments must not record")
	}
	// The nil recorder must be safe to call: every surface holds it as an
	// interface and calls it unconditionally.
	rec.RecordRead(context.Background(), resource.ReadEvent{ResourceID: "r1"})
}

func TestRecordRead_WritesTheEventAndStampsTheRow(t *testing.T) {
	logger, tracker := &captureLogger{}, &captureTracker{}
	rec := New(logger, tracker)

	rec.RecordRead(context.Background(), resource.ReadEvent{
		ResourceID: "res-1",
		URI:        "mcp://global/runbooks/etl.md",
		Surface:    resource.SurfaceMCPRead,
		Version:    3,
		UserID:     "user-1",
		UserEmail:  "analyst@example.com",
		Persona:    "analyst",
		SessionID:  "sess-1",
		RequestID:  "req-1",
	})

	if len(logger.events) != 1 {
		t.Fatalf("events written = %d, want 1", len(logger.events))
	}
	ev := logger.events[0]
	if ev.EventKind != string(audit.EventTypeResourceRead) {
		t.Errorf("event_kind = %q, want %q (the usage rollup filters on it)", ev.EventKind, audit.EventTypeResourceRead)
	}
	if ev.Parameters[paramResourceID] != "res-1" || ev.Parameters[paramSurface] != resource.SurfaceMCPRead {
		t.Errorf("parameters = %v, want resource_id and surface the rollup reads back by name", ev.Parameters)
	}
	if ev.Parameters[paramResourceURI] != "mcp://global/runbooks/etl.md" {
		t.Errorf("resource_uri = %v, want the canonical URI", ev.Parameters[paramResourceURI])
	}
	if ev.Parameters[paramVersion] != 3 {
		t.Errorf("version = %v, want 3", ev.Parameters[paramVersion])
	}
	if ev.UserID != "user-1" || ev.UserEmail != "analyst@example.com" || ev.Persona != "analyst" {
		t.Errorf("caller = %+v, want the identity the surface resolved", ev)
	}
	if ev.SessionID != "sess-1" || ev.RequestID != "req-1" {
		t.Errorf("session/request = %q/%q, want the surface's", ev.SessionID, ev.RequestID)
	}
	if !ev.Success || !ev.Authorized {
		t.Error("a served read persisted as unsuccessful or unauthorized; it would read as a denial")
	}
	if ev.ToolName != resource.SurfaceMCPRead {
		t.Errorf("tool_name = %q, want the surface", ev.ToolName)
	}
	if len(tracker.ids) != 1 || tracker.ids[0] != "res-1" {
		t.Fatalf("stamped ids = %v, want [res-1]", tracker.ids)
	}
	if !tracker.at[0].Equal(ev.Timestamp) {
		t.Errorf("stamp time %v differs from the event timestamp %v; the two answers to \"when\" must agree", tracker.at[0], ev.Timestamp)
	}
}

func TestRecordRead_OmitsVersionWhenTheHeadWasServed(t *testing.T) {
	logger := &captureLogger{}
	New(logger, nil).RecordRead(context.Background(), resource.ReadEvent{ResourceID: "r1", Surface: resource.SurfaceFetch})

	if _, present := logger.events[0].Parameters[paramVersion]; present {
		t.Error("version parameter present for a head read; it must be absent, not zero")
	}
}

func TestRecordRead_FailuresDoNotPanicOrPropagate(t *testing.T) {
	logger := &captureLogger{err: errors.New("audit store down")}
	tracker := &captureTracker{err: errors.New("db down")}
	// Both failures are swallowed — the read has already been served.
	New(logger, tracker).RecordRead(context.Background(), resource.ReadEvent{ResourceID: "r1", Surface: resource.SurfaceDownload})

	if len(logger.events) != 1 || len(tracker.ids) != 1 {
		t.Fatal("a failing writer stopped the other from being attempted")
	}
}

// A library of photographs is drawn from the resources' own bytes, there being
// no stored thumbnail for a resource, so every page view is a read of every
// image in it. Counting those as reads would clear the never-read flag and
// reorder the last-read sort by browsing, which is the signal a curator uses to
// find dead weight (#1471). The read is still audited: the bytes reached an
// identified caller.
func TestRecordRead_PreviewIsAuditedButDoesNotStampLastRead(t *testing.T) {
	logger, tracker := &captureLogger{}, &captureTracker{}
	New(logger, tracker).RecordRead(context.Background(), resource.ReadEvent{
		ResourceID: "r1", Surface: resource.SurfacePreview,
	})

	if len(logger.events) != 1 {
		t.Fatalf("audit events = %d, want 1: a preview is still a read of the bytes", len(logger.events))
	}
	if got := logger.events[0].Parameters[paramSurface]; got != resource.SurfacePreview {
		t.Errorf("surface = %v, want %q", got, resource.SurfacePreview)
	}
	if len(tracker.ids) != 0 {
		t.Errorf("last-read stamped for %v; a preview must not move the curation signal", tracker.ids)
	}
}

// The guard is on the preview surface alone, not on "anything unusual": a door
// that serves the bytes to somebody using the file stamps the column.
func TestRecordRead_EveryOtherSurfaceStampsLastRead(t *testing.T) {
	for _, surface := range []string{
		resource.SurfaceMCPRead, resource.SurfaceFetch, resource.SurfaceDownload,
	} {
		t.Run(surface, func(t *testing.T) {
			logger, tracker := &captureLogger{}, &captureTracker{}
			New(logger, tracker).RecordRead(context.Background(), resource.ReadEvent{
				ResourceID: "r1", Surface: surface,
			})
			if len(tracker.ids) != 1 {
				t.Errorf("last-read not stamped for %q", surface)
			}
		})
	}
}

func TestRecordRead_WithoutATrackerStillAudits(t *testing.T) {
	logger := &captureLogger{}
	New(logger, nil).RecordRead(context.Background(), resource.ReadEvent{ResourceID: "r1", Surface: resource.SurfaceDownload})

	if len(logger.events) != 1 {
		t.Fatal("no event written when the store cannot stamp last-read")
	}
}

func TestRecordRead_PlatformContextIdentityWins(t *testing.T) {
	logger := &captureLogger{}
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID:      "ctx-user",
		UserEmail:   "ctx@example.com",
		PersonaName: "engineer",
		SessionID:   "ctx-session",
		RequestID:   "ctx-request",
		Transport:   "http",
		Source:      middleware.SourceMCP,
	})

	New(logger, nil).RecordRead(ctx, resource.ReadEvent{
		ResourceID: "r1", Surface: resource.SurfaceMCPRead,
		UserID: "surface-user", UserEmail: "surface@example.com",
	})

	ev := logger.events[0]
	if ev.UserID != "ctx-user" || ev.UserEmail != "ctx@example.com" {
		t.Errorf("caller = %q/%q, want the request-scoped identity", ev.UserID, ev.UserEmail)
	}
	if ev.Persona != "engineer" || ev.SessionID != "ctx-session" || ev.RequestID != "ctx-request" {
		t.Errorf("context fields not carried onto the event: %+v", ev)
	}
	if ev.Transport != "http" {
		t.Errorf("transport = %q, want http", ev.Transport)
	}
}

func TestRecordRead_SurfaceIdentitySurvivesWithoutAContext(t *testing.T) {
	logger := &captureLogger{}
	New(logger, nil).RecordRead(context.Background(), resource.ReadEvent{
		ResourceID: "r1", Surface: resource.SurfaceDownload,
		UserID: "rest-user", UserEmail: "rest@example.com",
	})

	ev := logger.events[0]
	if ev.UserID != "rest-user" || ev.UserEmail != "rest@example.com" {
		t.Errorf("caller = %q/%q, want the REST surface's resolved identity", ev.UserID, ev.UserEmail)
	}
	if ev.Source != middleware.SourceAdmin {
		t.Errorf("source = %q, want %q for a portal download", ev.Source, middleware.SourceAdmin)
	}
}

func TestSourceForSurface(t *testing.T) {
	tests := map[string]string{
		resource.SurfaceMCPRead:  middleware.SourceMCP,
		resource.SurfaceFetch:    middleware.SourceMCP,
		resource.SurfaceDownload: middleware.SourceAdmin,
	}
	for surface, want := range tests {
		if got := sourceForSurface(surface); got != want {
			t.Errorf("sourceForSurface(%q) = %q, want %q", surface, got, want)
		}
	}
}

func moveFixture() resource.MoveEvent {
	return resource.MoveEvent{
		ResourceID:  "res-1",
		DisplayName: "Report",
		FromScope:   resource.ScopeUser,
		FromScopeID: "sub-1",
		FromURI:     "mcp://user/sub-1/templates/report.docx",
		ToScope:     resource.ScopePersona,
		ToScopeID:   "ops",
		ToURI:       "mcp://persona/ops/templates/report.docx",
		UserID:      "sub-1",
		UserEmail:   "me@example.com",
	}
}

func TestRecordMove_NamesWhoMovedWhatBetweenWhichLibraries(t *testing.T) {
	logger := &captureLogger{}
	New(logger, &captureTracker{}).RecordMove(context.Background(), moveFixture())

	if len(logger.events) != 1 {
		t.Fatalf("events = %d, want 1", len(logger.events))
	}
	ev := logger.events[0]
	if ev.EventKind != string(audit.EventTypeResourceMove) {
		t.Errorf("event kind = %q", ev.EventKind)
	}
	if ev.UserID != "sub-1" || ev.UserEmail != "me@example.com" {
		t.Errorf("event does not name who moved it: %+v", ev)
	}
	if !ev.Success || !ev.Authorized {
		t.Error("a completed move persisted as an unauthorized failure")
	}
	// Both sides are on the row, or the trail answers "who published this" and
	// not "what address did it used to have".
	want := map[string]any{
		paramResourceID:  "res-1",
		paramDisplayName: "Report",
		paramFromScope:   "user",
		paramFromScopeID: "sub-1",
		paramFromURI:     "mcp://user/sub-1/templates/report.docx",
		paramToScope:     "persona",
		paramToScopeID:   "ops",
		paramToURI:       "mcp://persona/ops/templates/report.docx",
	}
	for k, v := range want {
		if ev.Parameters[k] != v {
			t.Errorf("parameter %s = %v, want %v", k, ev.Parameters[k], v)
		}
	}
}

func TestRecordMove_FailureDoesNotPropagate(t *testing.T) {
	// The resource has already been refiled: a lost audit row must not become a
	// failed move reported to somebody whose file did move.
	logger := &captureLogger{err: errors.New("audit store down")}
	New(logger, &captureTracker{}).RecordMove(context.Background(), moveFixture())
	if len(logger.events) != 1 {
		t.Fatalf("the write was not attempted: events = %d", len(logger.events))
	}
}

// TestRecordMove_NilRecorderIsSafe asserts nothing: the call itself is the
// assertion, since every surface holds the recorder as an interface and calls it
// unconditionally, and a nil one must not panic.
func TestRecordMove_NilRecorderIsSafe(_ *testing.T) {
	var rec *Recorder
	rec.RecordMove(context.Background(), moveFixture())
}

func TestRecordMove_PlatformContextIdentityWins(t *testing.T) {
	logger := &captureLogger{}
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "ctx-user", UserEmail: "ctx@example.com", PersonaName: "ops",
	})
	New(logger, &captureTracker{}).RecordMove(ctx, moveFixture())

	ev := logger.events[0]
	if ev.UserID != "ctx-user" || ev.UserEmail != "ctx@example.com" || ev.Persona != "ops" {
		t.Errorf("request-scoped identity did not win: %+v", ev)
	}
}
