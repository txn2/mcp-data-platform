package provenance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

const (
	testSession = "dps_session"
	testUser    = "user-1"
)

var testNow = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

// fakeReader answers Query from a fixed event list, recording the filters it
// was asked for so a test can assert how the capture scoped its read.
type fakeReader struct {
	events  []audit.Event
	filters []audit.QueryFilter
	err     error
}

func (f *fakeReader) Query(_ context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	f.filters = append(f.filters, filter)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]audit.Event, 0, len(f.events))
	for _, ev := range f.events {
		if !matches(ev, filter) {
			continue
		}
		out = append(out, ev)
	}
	if filter.SortOrder == audit.SortDesc {
		reverse(out)
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// matches mirrors the store's filter semantics for the fields the capture uses.
func matches(ev audit.Event, filter audit.QueryFilter) bool {
	if filter.SessionID != "" && ev.SessionID != filter.SessionID {
		return false
	}
	if filter.UserID != "" && ev.UserID != filter.UserID {
		return false
	}
	if len(filter.IDs) == 0 {
		return true
	}
	return slices.Contains(filter.IDs, ev.ID)
}

// event builds a recorded call, oldest first by index.
func event(id, tool, kind string, index int, opts ...func(*audit.Event)) audit.Event {
	ev := audit.Event{
		ID:          id,
		Timestamp:   testNow.Add(time.Duration(index) * time.Minute),
		SessionID:   testSession,
		UserID:      testUser,
		ToolName:    tool,
		ToolkitKind: kind,
		Success:     true,
		DurationMS:  int64(10 * (index + 1)),
	}
	for _, opt := range opts {
		opt(&ev)
	}
	return ev
}

func withParams(params map[string]any) func(*audit.Event) {
	return func(ev *audit.Event) { ev.Parameters = params }
}

func failed(msg string) func(*audit.Event) {
	return func(ev *audit.Event) {
		ev.Success = false
		ev.ErrorMessage = msg
	}
}

func newTestCapturer(reader EventReader, flush Flusher) *Capturer {
	c := New(reader, flush)
	c.now = func() time.Time { return testNow.Add(time.Hour) }
	return c
}

func saveRequest() portal.ProvenanceRequest {
	return portal.ProvenanceRequest{Tool: "save_asset", SessionID: testSession, UserID: testUser, Version: 1}
}

// The default window is every data call the session made, oldest first, with
// the platform's own bookkeeping calls left out.
func TestCaptureDefaultWindow(t *testing.T) {
	reader := &fakeReader{events: []audit.Event{
		event("e1", "trino_query", "trino", 0, withParams(map[string]any{"sql": "SELECT 1"})),
		event("e2", "search", "search", 1),
		event("e3", "api_invoke_endpoint", "api", 2, withParams(map[string]any{
			"method": "GET", "path": "/v1/orders", "operation_id": "listOrders",
		})),
	}}
	capture := newTestCapturer(reader, nil).Capture(context.Background(), saveRequest())

	require.Len(t, capture.Calls, 2, "the search call is discovery, not a source")
	assert.Equal(t, []string{"e1", "e3"}, capture.EventIDs)
	assert.Equal(t, portal.ProvenanceKindSQL, capture.Calls[0].Kind)
	assert.Equal(t, "SELECT 1", capture.Calls[0].Statement)
	assert.Equal(t, portal.ProvenanceKindAPI, capture.Calls[1].Kind)
	assert.Equal(t, "GET", capture.Calls[1].Method)
	assert.Equal(t, "/v1/orders", capture.Calls[1].Path)
	assert.Equal(t, "listOrders", capture.Calls[1].OperationID)
	assert.Equal(t, "save_asset", capture.Tool)
	assert.Equal(t, testSession, capture.SessionID)
	assert.Equal(t, 1, capture.Version)
	assert.False(t, capture.Explicit)
	assert.False(t, capture.Truncated)
	assert.Equal(t, testNow.Add(time.Hour), capture.CapturedAt)
}

// A failed call is part of how the answer was reached and is captured with the
// outcome it had.
func TestCaptureRecordsFailedCalls(t *testing.T) {
	reader := &fakeReader{events: []audit.Event{
		event("e1", "trino_query", "trino", 0, failed("SYNTAX_ERROR: line 1:8")),
	}}
	capture := newTestCapturer(reader, nil).Capture(context.Background(), saveRequest())

	require.Len(t, capture.Calls, 1)
	assert.Equal(t, portal.ProvenanceOutcomeError, capture.Calls[0].Outcome)
	assert.Equal(t, "SYNTAX_ERROR: line 1:8", capture.Calls[0].Error)
}

// The window starts after the previous capture: a second save records only the
// calls made since the first.
func TestCaptureWindowStopsAtPreviousCapture(t *testing.T) {
	reader := &fakeReader{events: []audit.Event{
		event("e1", "trino_query", "trino", 0),
		event("e2", "save_asset", "portal", 1),
		event("e3", "trino_query", "trino", 2),
	}}
	capture := newTestCapturer(reader, nil).Capture(context.Background(), saveRequest())

	assert.Equal(t, []string{"e3"}, capture.EventIDs)
}

func TestCaptureWindowBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		boundary audit.Event
		want     []string
	}{
		{
			name:     "export tool",
			boundary: event("b", "trino_export", "trino", 1),
			want:     []string{"e3"},
		},
		{
			name:     "api export tool",
			boundary: event("b", "api_export", "api", 1),
			want:     []string{"e3"},
		},
		{
			name: "manage_asset content update",
			boundary: event("b", "manage_asset", "portal", 1,
				withParams(map[string]any{"action": "update"})),
			want: []string{"e3"},
		},
		{
			name: "manage_asset patch",
			boundary: event("b", "manage_asset", "portal", 1,
				withParams(map[string]any{"action": "patch"})),
			want: []string{"e3"},
		},
		{
			name: "manage_asset list is not a write",
			boundary: event("b", "manage_asset", "portal", 1,
				withParams(map[string]any{"action": "list"})),
			want: []string{"e1", "e3"},
		},
		{
			name:     "manage_asset with no recorded arguments",
			boundary: event("b", "manage_asset", "portal", 1),
			want:     []string{"e1", "e3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeReader{events: []audit.Event{
				event("e1", "trino_query", "trino", 0),
				tt.boundary,
				event("e3", "trino_query", "trino", 2),
			}}
			capture := newTestCapturer(reader, nil).Capture(context.Background(), saveRequest())
			assert.Equal(t, tt.want, capture.EventIDs)
		})
	}
}

// A write that failed captured nothing, so the calls before it still belong to
// whatever is written next.
func TestCaptureFailedWriteIsNotABoundary(t *testing.T) {
	failedSave := event("b", "save_asset", "portal", 1, failed("content storage not configured"))
	reader := &fakeReader{events: []audit.Event{
		event("e1", "trino_query", "trino", 0),
		failedSave,
		event("e3", "trino_query", "trino", 2),
	}}
	capture := newTestCapturer(reader, nil).Capture(context.Background(), saveRequest())

	assert.Equal(t, []string{"e1", "e3"}, capture.EventIDs)
}

// With nothing cited and no session, there is no window to read and no reason
// to wait on the audit writer.
func TestCaptureWithoutSessionDoesNotFlush(t *testing.T) {
	flush := &stubFlusher{}
	capture := newTestCapturer(&fakeReader{}, flush).Capture(context.Background(),
		portal.ProvenanceRequest{Tool: "save_asset", UserID: testUser})

	assert.Empty(t, capture.Calls)
	assert.Zero(t, flush.called)
}

// Cited sources replace the window and are recorded in the order they ran.
func TestCaptureExplicitSources(t *testing.T) {
	reader := &fakeReader{events: []audit.Event{
		event("e1", "trino_query", "trino", 0),
		event("e2", "trino_query", "trino", 1),
		event("e3", "api_invoke_endpoint", "api", 2),
	}}
	req := saveRequest()
	req.Sources = []string{"mcp:call:e3", "e1", "e1", "  "}

	capture := newTestCapturer(reader, nil).Capture(context.Background(), req)

	assert.True(t, capture.Explicit)
	assert.Equal(t, []string{"e1", "e3"}, capture.EventIDs, "cited calls are recorded in call order")
	require.NotEmpty(t, reader.filters)
	assert.Equal(t, []string{"e3", "e1"}, reader.filters[0].IDs, "duplicates and blanks are dropped before the read")
	assert.Equal(t, testUser, reader.filters[0].UserID, "a citation is scoped to the caller's own calls")
}

// A cited id that names nothing the caller ran is dropped, and the capture says
// it holds less than was asked for.
func TestCaptureExplicitSourceUnresolved(t *testing.T) {
	reader := &fakeReader{events: []audit.Event{event("e1", "trino_query", "trino", 0)}}
	req := saveRequest()
	req.Sources = []string{"e1", "does-not-exist"}

	capture := newTestCapturer(reader, nil).Capture(context.Background(), req)

	assert.Equal(t, []string{"e1"}, capture.EventIDs)
	assert.True(t, capture.Truncated)
}

// The scoping is the boundary that stops one person's query becoming another
// person's provenance, so it is checked again after the store answers.
func TestCaptureRefusesAnotherCallersEvent(t *testing.T) {
	other := event("e9", "trino_query", "trino", 0)
	other.UserID = "someone-else"
	other.SessionID = "dps_other"
	reader := &fakeReader{events: []audit.Event{other}}
	// A reader that ignores the scoping filter stands in for any store-side
	// mistake; the capture must still refuse the row.
	reader.err = nil

	req := saveRequest()
	req.Sources = []string{"e9"}
	capturer := newTestCapturer(&unscopedReader{fakeReader: reader}, nil)

	capture := capturer.Capture(context.Background(), req)
	assert.Empty(t, capture.Calls)
}

// unscopedReader answers every query with its whole event list, ignoring the
// scoping filter.
type unscopedReader struct{ *fakeReader }

func (u *unscopedReader) Query(_ context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	u.filters = append(u.filters, filter)
	return u.events, nil
}

// A caller with no user identity (auth disabled) is scoped by its session.
func TestCaptureScopesBySessionWithoutUser(t *testing.T) {
	reader := &fakeReader{events: []audit.Event{event("e1", "trino_query", "trino", 0)}}
	req := portal.ProvenanceRequest{Tool: "save_asset", SessionID: testSession, Sources: []string{"e1"}}

	capture := newTestCapturer(reader, nil).Capture(context.Background(), req)

	require.Len(t, capture.Calls, 1)
	assert.Equal(t, testSession, reader.filters[0].SessionID)
	assert.Empty(t, reader.filters[0].UserID)
}

// A call with no session at all has no window to read: the platform records
// nothing rather than reading another session's calls.
func TestCaptureWithoutSession(t *testing.T) {
	reader := &fakeReader{events: []audit.Event{event("e1", "trino_query", "trino", 0)}}
	capture := newTestCapturer(reader, nil).Capture(context.Background(),
		portal.ProvenanceRequest{Tool: "save_asset", UserID: testUser})

	assert.Empty(t, capture.Calls)
	assert.Empty(t, reader.filters)
}

// A caller cannot cite an unbounded list: the capture is a snapshot on an
// asset row, not a copy of the audit log.
func TestParseSourcesCapsCitations(t *testing.T) {
	sources := make([]string, 0, maxSources+10)
	for i := range maxSources + 10 {
		sources = append(sources, fmt.Sprintf("e%d", i))
	}
	assert.Len(t, parseSources(sources), maxSources)
}

func TestCaptureTruncatesAtMaxCalls(t *testing.T) {
	events := make([]audit.Event, 0, MaxCalls+5)
	for i := range MaxCalls + 5 {
		events = append(events, event(fmt.Sprintf("e%d", i), "trino_query", "trino", i))
	}
	capture := newTestCapturer(&fakeReader{events: events}, nil).Capture(context.Background(), saveRequest())

	assert.Len(t, capture.Calls, MaxCalls)
	assert.True(t, capture.Truncated)
	assert.Equal(t, "e104", capture.Calls[len(capture.Calls)-1].EventID,
		"the newest calls are the ones kept")
}

// A session that ran more calls than one read returns, with no capture in
// sight, says so rather than presenting a partial window as complete.
func TestCaptureTruncatesAtScanLimit(t *testing.T) {
	events := make([]audit.Event, 0, scanLimit)
	for i := range scanLimit {
		kind := "search"
		if i >= scanLimit-2 {
			kind = "trino"
		}
		events = append(events, event(fmt.Sprintf("e%d", i), "tool", kind, i))
	}
	capture := newTestCapturer(&fakeReader{events: events}, nil).Capture(context.Background(), saveRequest())

	assert.Len(t, capture.Calls, 2)
	assert.True(t, capture.Truncated)
}

// The capturing call states what it just did; its own audit row does not exist
// yet, so nothing else can.
func TestCaptureAppendsOwnCall(t *testing.T) {
	reader := &fakeReader{events: []audit.Event{event("e1", "trino_query", "trino", 0)}}
	req := saveRequest()
	req.Own = &portal.ProvenanceCall{Kind: portal.ProvenanceKindSQL, Tool: "trino_export", Statement: "SELECT 2"}

	capture := newTestCapturer(reader, nil).Capture(context.Background(), req)

	require.Len(t, capture.Calls, 2)
	assert.Equal(t, "trino_export", capture.Calls[1].Tool)
	assert.Equal(t, portal.ProvenanceOutcomeSuccess, capture.Calls[1].Outcome)
	assert.Equal(t, capture.CapturedAt, capture.Calls[1].Timestamp)
	assert.Empty(t, capture.EventIDs[1:], "the capturing call has no event id yet")
}

func TestCaptureOwnCallKeepsItsOwnOutcome(t *testing.T) {
	req := saveRequest()
	stamped := testNow.Add(-time.Minute)
	req.Own = &portal.ProvenanceCall{
		Kind: portal.ProvenanceKindAPI, Tool: "api_export",
		Outcome: portal.ProvenanceOutcomeError, Error: "upstream returned 503", Timestamp: stamped,
	}
	capture := newTestCapturer(&fakeReader{}, nil).Capture(context.Background(), req)

	require.Len(t, capture.Calls, 1)
	assert.Equal(t, portal.ProvenanceOutcomeError, capture.Calls[0].Outcome)
	assert.Equal(t, stamped, capture.Calls[0].Timestamp)
}

// An unreadable audit log degrades the record; it never fails the write.
func TestCaptureSurvivesAnUnreadableLog(t *testing.T) {
	own := &portal.ProvenanceCall{Kind: portal.ProvenanceKindSQL, Tool: "trino_export"}
	reader := &fakeReader{err: errors.New("connection refused")}

	req := saveRequest()
	req.Own = own
	capture := newTestCapturer(reader, nil).Capture(context.Background(), req)
	assert.Len(t, capture.Calls, 1, "the write's own account of itself survives")

	req.Sources = []string{"e1"}
	cited := newTestCapturer(reader, nil).Capture(context.Background(), req)
	assert.Len(t, cited.Calls, 1)
}

func TestCaptureWithoutAuditStore(t *testing.T) {
	capture := New(nil, nil).Capture(context.Background(), saveRequest())
	assert.Empty(t, capture.Calls)
	assert.Equal(t, "save_asset", capture.Tool)
	assert.False(t, capture.CapturedAt.IsZero())
}

func TestCaptureNilCapturer(t *testing.T) {
	var c *Capturer
	req := saveRequest()
	req.Own = &portal.ProvenanceCall{Tool: "trino_export"}
	capture := c.Capture(context.Background(), req)
	assert.Len(t, capture.Calls, 1)
	assert.False(t, capture.CapturedAt.IsZero())
}

// stubFlusher records that the capture waited for queued events, and can fail.
type stubFlusher struct {
	called int
	err    error
}

func (s *stubFlusher) Flush(context.Context) error {
	s.called++
	return s.err
}

// The newest call is the one most likely still queued, so the capture waits
// for the audit writer before it reads.
func TestCaptureFlushesBeforeReading(t *testing.T) {
	flush := &stubFlusher{}
	reader := &fakeReader{events: []audit.Event{event("e1", "trino_query", "trino", 0)}}

	capture := newTestCapturer(reader, flush).Capture(context.Background(), saveRequest())

	assert.Equal(t, 1, flush.called)
	assert.Len(t, capture.Calls, 1)
}

func TestCaptureToleratesAFailedFlush(t *testing.T) {
	flush := &stubFlusher{err: errors.New("queue full")}
	reader := &fakeReader{events: []audit.Event{event("e1", "trino_query", "trino", 0)}}

	capture := newTestCapturer(reader, flush).Capture(context.Background(), saveRequest())

	assert.Len(t, capture.Calls, 1, "a stale read is still a read")
}

func TestKindFor(t *testing.T) {
	assert.Equal(t, portal.ProvenanceKindSQL, KindFor("trino"))
	assert.Equal(t, portal.ProvenanceKindAPI, KindFor("api"))
	assert.Equal(t, portal.ProvenanceKindTool, KindFor("datahub"))
	assert.Equal(t, portal.ProvenanceKindTool, KindFor("s3"))
	assert.Equal(t, portal.ProvenanceKindTool, KindFor("mcp"))
	assert.Empty(t, KindFor("portal"), "saving an asset is not an asset's source")
	assert.Empty(t, KindFor("memory"))
}

func TestSourceToolkitKindsCoversEveryMappedKind(t *testing.T) {
	kinds := SourceToolkitKinds()
	assert.Len(t, kinds, len(sourceKinds))
	for _, k := range kinds {
		assert.NotEmpty(t, KindFor(k))
	}
}

// A catalog or storage call is named by what it addressed, so the panel can
// show more than a tool name.
func TestCaptureSummarizesToolCalls(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"urn", map[string]any{"urn": "urn:li:dataset:(x)"}, "urn:li:dataset:(x)"},
		{"query", map[string]any{"query": "orders"}, "orders"},
		{"table", map[string]any{"table": "orders"}, "orders"},
		{"object", map[string]any{"bucket": "reports", "key": "q3.csv"}, "q3.csv"},
		{"nothing addressable", map[string]any{"limit": 10}, ""},
		{"no arguments recorded", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeReader{events: []audit.Event{
				event("e1", "datahub_get_entity", "datahub", 0, withParams(tt.params)),
			}}
			capture := newTestCapturer(reader, nil).Capture(context.Background(), saveRequest())
			require.Len(t, capture.Calls, 1)
			assert.Equal(t, portal.ProvenanceKindTool, capture.Calls[0].Kind)
			assert.Equal(t, tt.want, capture.Calls[0].Summary)
		})
	}
}

// A cited call the platform does not classify is still the caller's own call.
func TestCaptureCitedCallOfAnUnclassifiedKind(t *testing.T) {
	reader := &fakeReader{events: []audit.Event{event("e1", "memory_manage", "memory", 0)}}
	req := saveRequest()
	req.Sources = []string{"e1"}

	capture := newTestCapturer(reader, nil).Capture(context.Background(), req)

	require.Len(t, capture.Calls, 1)
	assert.Equal(t, portal.ProvenanceKindTool, capture.Calls[0].Kind)
}

// The capture carries the purpose the caller stated for each call (#1317), so
// an asset says why its sources ran and not only what they were.
func TestCaptureCarriesPurposeAndDuration(t *testing.T) {
	ev := event("e1", "trino_query", "trino", 0)
	ev.Purpose = "Sizing Q3 revenue for the board deck."
	ev.Connection = "warehouse"
	reader := &fakeReader{events: []audit.Event{ev}}

	capture := newTestCapturer(reader, nil).Capture(context.Background(), saveRequest())

	require.Len(t, capture.Calls, 1)
	assert.Equal(t, "Sizing Q3 revenue for the board deck.", capture.Calls[0].Purpose)
	assert.Equal(t, "warehouse", capture.Calls[0].Connection)
	assert.Equal(t, int64(10), capture.Calls[0].DurationMS)
	assert.Equal(t, ev.Timestamp, capture.Calls[0].Timestamp)
}

// The boundary tool names are the toolkit's own, not a second list that can
// drift from it.
func TestBoundaryToolNamesMatchTheToolkit(t *testing.T) {
	assert.Equal(t, portalkit.SaveToolName, saveAssetTool)
	assert.Equal(t, portalkit.ManageToolName, manageAssetTool)
}

// The source kinds are the toolkits' own Kind() values. The two that are not
// their package name are the ones a hand-written map gets wrong: the API
// gateway answers "api" and the MCP gateway answers "mcp".
func TestSourceKindsMatchTheToolkits(t *testing.T) {
	assert.Equal(t, portal.ProvenanceKindAPI, KindFor(apigatewaykit.Kind))
	assert.Equal(t, portal.ProvenanceKindTool, KindFor(gatewaykit.Kind))
	assert.Empty(t, KindFor(portalkit.New(portalkit.Config{}).Kind()),
		"the toolkit that saves assets is not a source of them")
}
