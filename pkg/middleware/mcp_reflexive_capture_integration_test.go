package middleware_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
)

// recordingStore records inserted memory records and signals each on a channel,
// so an integration test can wait for the asynchronous reflexive mint.
type recordingStore struct {
	memstore.Store
	mu      sync.Mutex
	records []memstore.Record
	ch      chan memstore.Record
}

func newRecordingStore() *recordingStore {
	return &recordingStore{Store: memstore.NewNoopStore(), ch: make(chan memstore.Record, 8)}
}

func (s *recordingStore) Insert(_ context.Context, r memstore.Record) error {
	s.mu.Lock()
	s.records = append(s.records, r)
	s.mu.Unlock()
	s.ch <- r
	return nil
}

// testCaptor is the platform-side adapter's test double: it drives the real
// memory toolkit AutoCapture, proving the middleware -> AutoCapture -> store
// chain end to end.
type testCaptor struct{ tk *memorykit.Toolkit }

func (c *testCaptor) CaptureCorrection(ctx context.Context, cc middleware.CorrectionCapture) error {
	_, err := c.tk.AutoCapture(ctx, memorykit.AutoCaptureInput{
		SinkClass:  cc.SinkClass,
		Content:    cc.Content,
		Category:   cc.Category,
		Source:     memstore.SourceAutomation,
		EntityURNs: cc.EntityURNs,
		Metadata:   cc.Metadata,
		CreatedBy:  cc.CreatedBy,
		Persona:    cc.Persona,
		UserID:     cc.UserID,
		SessionID:  cc.SessionID,
	})
	if err != nil {
		return fmt.Errorf("test captor: %w", err)
	}
	return nil
}

// reflexiveTestServer wires a real MCP server with a stub trino_query tool (that
// errors on SQL containing errTrigger), the reflexive-capture middleware, and
// the tool-call middleware, returning the recording store for assertions. When
// capturePermitted is non-nil it gates capture on the caller (the persona
// memory_capture authorization check).
func reflexiveTestServer(t *testing.T, errTrigger string, capturePermitted func(context.Context, *middleware.PlatformContext) bool) (*mcp.Server, *recordingStore, *middleware.SessionErrorTracker) {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "reflexive-test", Version: "v0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "test",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"},"connection":{"type":"string"}}}`),
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			SQL string `json:"sql"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &args)
		if errTrigger != "" && strings.Contains(args.SQL, errTrigger) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "line 1:8: Column 'custmer_id' cannot be resolved"}},
			}, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	store := newRecordingStore()
	tk, err := memorykit.New("test", store, nil)
	if err != nil {
		t.Fatalf("memorykit.New: %v", err)
	}
	tracker := middleware.NewSessionErrorTracker(time.Minute, time.Minute)

	server.AddReceivingMiddleware(middleware.MCPReflexiveCaptureMiddleware(middleware.ReflexiveCaptureConfig{
		Captor:  &testCaptor{tk: tk},
		Tracker: tracker,
		URNBuilder: func(_, catalog, schema, table string) string {
			return "urn:li:dataset:(urn:li:dataPlatform:trino," + catalog + "." + schema + "." + table + ",PROD)"
		},
		CapturePermitted: capturePermitted,
	}))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: "u1", Email: "analyst@example.com", Roles: []string{"analyst"}}},
		&fakeAuthz{persona: "analyst"},
		&fakeLookup{kind: "trino", name: "prod", conn: "primary"},
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
	))
	return server, store, tracker
}

func callTrino(ctx context.Context, t *testing.T, sess *mcp.ClientSession, sql string) {
	t.Helper()
	if _, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": sql, "connection": "primary"},
	}); err != nil {
		t.Fatalf("CallTool(%q): %v", sql, err)
	}
}

// TestReflexiveCapture_ErrorThenFix_MintsCorrection proves the full loop: a
// failing query followed by a same-table success in one session auto-mints one
// reviewed correction record, with no explicit capture tool call in the
// transcript.
func TestReflexiveCapture_ErrorThenFix_MintsCorrection(t *testing.T) {
	ctx := context.Background()
	server, store, tracker := reflexiveTestServer(t, "custmer_id", nil)
	defer tracker.Stop()

	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	// 1) A query with a misspelled column fails.
	callTrino(ctx, t, sess, "SELECT custmer_id FROM hive.sales.orders")
	// 2) The corrected query over the same table succeeds -> triggers capture.
	callTrino(ctx, t, sess, "SELECT customer_id FROM hive.sales.orders")

	var rec memstore.Record
	select {
	case rec = <-store.ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for reflexive capture insert")
	}

	if rec.Source != memstore.SourceAutomation {
		t.Errorf("Source = %q, want automation", rec.Source)
	}
	if rec.SinkClass != memstore.SinkSchemaEntity {
		t.Errorf("SinkClass = %q, want schema_entity", rec.SinkClass)
	}
	if rec.Category != memstore.CategoryCorrection {
		t.Errorf("Category = %q, want correction", rec.Category)
	}
	if rec.CreatedBy != "analyst@example.com" {
		t.Errorf("CreatedBy = %q, want analyst@example.com", rec.CreatedBy)
	}
	for _, want := range []string{"custmer_id", "customer_id", "cannot be resolved"} {
		if !strings.Contains(rec.Content, want) {
			t.Errorf("content missing %q:\n%s", want, rec.Content)
		}
	}
	wantURN := "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)"
	if len(rec.EntityURNs) != 1 || rec.EntityURNs[0] != wantURN {
		t.Errorf("EntityURNs = %v, want [%s]", rec.EntityURNs, wantURN)
	}
	if rec.Metadata[memstore.MetaKeyInsightStatus] != memstore.InsightStatusPending {
		t.Errorf("expected pending-insight overlay (reviewed class), got %v", rec.Metadata)
	}
	if rec.Metadata["reflexive_trigger"] != "query_error_fix" {
		t.Errorf("expected reflexive_trigger metadata, got %v", rec.Metadata)
	}

	// Exactly one record: the pair is consumed, so it does not re-fire.
	assertNoMoreInserts(t, store)
}

// TestReflexiveCapture_NoCaptureCases proves the pairing is conservative: a
// success over a different table, and a noise error, mint nothing.
func TestReflexiveCapture_NoCaptureCases(t *testing.T) {
	ctx := context.Background()

	t.Run("success over a different table is not a fix", func(t *testing.T) {
		server, store, tracker := reflexiveTestServer(t, "custmer_id", nil)
		defer tracker.Stop()
		sess := mustConnect(ctx, t, server)
		defer func() { _ = sess.Close() }()

		callTrino(ctx, t, sess, "SELECT custmer_id FROM hive.sales.orders")
		// A success over a DIFFERENT table does not pair with the orders failure.
		callTrino(ctx, t, sess, "SELECT id FROM hive.sales.customers")
		assertNoMoreInserts(t, store)
	})

	t.Run("plain successes with no prior failure mint nothing", func(t *testing.T) {
		// errTrigger "" so the stub only ever succeeds: with no worth-capturing
		// failure recorded, a success has nothing to pair with.
		server, store, tracker := reflexiveTestServer(t, "", nil)
		defer tracker.Stop()
		sess := mustConnect(ctx, t, server)
		defer func() { _ = sess.Close() }()

		callTrino(ctx, t, sess, "SELECT 1 FROM hive.sales.orders")
		callTrino(ctx, t, sess, "SELECT 2 FROM hive.sales.orders")
		assertNoMoreInserts(t, store)
	})

	t.Run("persona denied memory_capture mints nothing", func(t *testing.T) {
		// The caller's persona is not permitted to create memory, so even a real
		// error->fix pair must not mint a record on its behalf.
		deny := func(_ context.Context, _ *middleware.PlatformContext) bool { return false }
		server, store, tracker := reflexiveTestServer(t, "custmer_id", deny)
		defer tracker.Stop()
		sess := mustConnect(ctx, t, server)
		defer func() { _ = sess.Close() }()

		callTrino(ctx, t, sess, "SELECT custmer_id FROM hive.sales.orders")
		callTrino(ctx, t, sess, "SELECT customer_id FROM hive.sales.orders")
		assertNoMoreInserts(t, store)
	})
}

func assertNoMoreInserts(t *testing.T, store *recordingStore) {
	t.Helper()
	select {
	case rec := <-store.ch:
		t.Fatalf("unexpected extra capture: %+v", rec)
	case <-time.After(300 * time.Millisecond):
	}
}
