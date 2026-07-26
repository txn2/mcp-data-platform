package middleware_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/resourceaudit"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// capturingAuditStore is an audit.Logger that keeps every event, standing in for
// the PostgreSQL audit store so the test can assert what a resource read
// actually persists.
type capturingAuditStore struct {
	mu     sync.Mutex
	events []audit.Event
}

func (s *capturingAuditStore) Log(_ context.Context, ev audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

func (*capturingAuditStore) Query(_ context.Context, _ audit.QueryFilter) ([]audit.Event, error) {
	return nil, nil
}
func (*capturingAuditStore) Close() error { return nil }

func (s *capturingAuditStore) snapshot() []audit.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.Event(nil), s.events...)
}

// stubResourceStore is the minimal ResourceListProvider the read path needs.
type stubResourceStore struct {
	resources map[string]*resource.Resource
}

func (s *stubResourceStore) List(_ context.Context, _ resource.Filter) ([]resource.Resource, int, error) {
	out := make([]resource.Resource, 0, len(s.resources))
	for _, r := range s.resources {
		out = append(out, *r)
	}
	return out, len(out), nil
}

func (s *stubResourceStore) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	if r, ok := s.resources[uri]; ok {
		return r, nil
	}
	return nil, errResourceStubNotFound
}

// errResourceStubNotFound mirrors the store's "no such resource" answer.
var errResourceStubNotFound = &stubNotFoundError{}

type stubNotFoundError struct{}

func (*stubNotFoundError) Error() string { return "not found" }

// stubBlobReader serves resource content from memory.
type stubBlobReader struct {
	objects map[string][]byte
}

func (b *stubBlobReader) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	if data, ok := b.objects[key]; ok {
		return data, "text/markdown", nil
	}
	return nil, "", errResourceStubNotFound
}

// stampedTracker records the last-read stamps the recorder applies.
type stampedTracker struct {
	mu  sync.Mutex
	ids []string
}

func (t *stampedTracker) TouchRead(_ context.Context, id string, _ time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ids = append(t.ids, id)
	return nil
}

func (t *stampedTracker) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.ids...)
}

const testResourceURI = "mcp://global/runbooks/etl.md"

// newResourceReadServer wires a real MCP server with the managed-resource
// middleware over an in-memory store, returning the server and the pieces the
// assertions read. recorder may be nil, which is what a deployment with audit
// disabled produces.
func newResourceReadServer(t *testing.T, recorder resource.ReadRecorder) *mcp.Server {
	t.Helper()
	store := &stubResourceStore{resources: map[string]*resource.Resource{
		testResourceURI: {
			ID:          "res-42",
			Scope:       resource.ScopeGlobal,
			URI:         testResourceURI,
			DisplayName: "ETL Runbook",
			MIMEType:    "text/markdown",
			S3Key:       "resources/global/global/res-42/etl.md",
		},
	}}
	blobs := &stubBlobReader{objects: map[string][]byte{
		"resources/global/global/res-42/etl.md": []byte("# ETL Runbook\n"),
	}}

	server := mcp.NewServer(&mcp.Implementation{Name: "test-platform", Version: "v0.0.1"}, nil)
	server.AddReceivingMiddleware(middleware.MCPManagedResourceMiddleware(middleware.ManagedResourceConfig{
		Store:     store,
		S3Client:  blobs,
		S3Bucket:  "resources",
		URIScheme: "mcp",
		Authenticator: &testAuthenticator{userInfo: &middleware.UserInfo{
			UserID: "user-42",
			Email:  "analyst@example.com",
			Roles:  []string{"analyst"},
		}},
		ReadRecorder: recorder,
	}))
	return server
}

// TestResourceRead_ProducesAnAuditEventThroughTheRealChain is the #1014
// acceptance criterion for the MCP surface: a resources/read over a real
// mcp.Server with the real middleware chain and an in-memory transport must
// persist one resource_read event naming the caller, the resource, and the
// surface — and must stamp the resource's last-read column. Asserting the
// recorder in isolation would not prove the middleware reaches it with an
// identity, which is the part that has to work.
func TestResourceRead_ProducesAnAuditEventThroughTheRealChain(t *testing.T) {
	auditStore := &capturingAuditStore{}
	tracker := &stampedTracker{}
	recorder := resourceaudit.New(middleware.NewAuditStoreAdapter(auditStore), tracker)

	server := newResourceReadServer(t, recorder)
	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: testResourceURI})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text != "# ETL Runbook\n" {
		t.Fatalf("contents = %+v, want the resource's text", result.Contents)
	}

	events := auditStore.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want exactly 1 for one read", len(events))
	}
	ev := events[0]
	if ev.EventKind != audit.EventTypeResourceRead {
		t.Errorf("event_kind = %q, want %q", ev.EventKind, audit.EventTypeResourceRead)
	}
	if ev.Parameters["resource_id"] != "res-42" {
		t.Errorf("resource_id = %v, want res-42", ev.Parameters["resource_id"])
	}
	if ev.Parameters["surface"] != resource.SurfaceMCPRead {
		t.Errorf("surface = %v, want %q", ev.Parameters["surface"], resource.SurfaceMCPRead)
	}
	if ev.Parameters["resource_uri"] != testResourceURI {
		t.Errorf("resource_uri = %v, want %q", ev.Parameters["resource_uri"], testResourceURI)
	}
	if ev.UserID != "user-42" || ev.UserEmail != "analyst@example.com" {
		t.Errorf("caller = %q/%q, want the authenticated user; an unattributed read answers nobody's question",
			ev.UserID, ev.UserEmail)
	}
	if ev.Timestamp.IsZero() {
		t.Error("event timestamp is zero")
	}
	if got := tracker.snapshot(); len(got) != 1 || got[0] != "res-42" {
		t.Errorf("last-read stamps = %v, want [res-42]", got)
	}
}

// TestResourceRead_WithAuditDisabledStillServes is the other half of the
// criterion: with no recorder (audit off) the same read succeeds and writes
// nothing.
func TestResourceRead_WithAuditDisabledStillServes(t *testing.T) {
	auditStore := &capturingAuditStore{}

	server := newResourceReadServer(t, nil)
	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: testResourceURI})
	if err != nil {
		t.Fatalf("ReadResource with audit disabled: %v", err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text != "# ETL Runbook\n" {
		t.Fatalf("contents = %+v, want the resource's text", result.Contents)
	}
	if got := len(auditStore.snapshot()); got != 0 {
		t.Errorf("audit events = %d, want 0 with audit disabled", got)
	}
}

// TestResourceRead_MissingBlobRecordsNothing keeps the trail honest: a read
// that could not serve content is not a read.
func TestResourceRead_MissingBlobRecordsNothing(t *testing.T) {
	auditStore := &capturingAuditStore{}
	recorder := resourceaudit.New(middleware.NewAuditStoreAdapter(auditStore), &stampedTracker{})

	server := newResourceReadServer(t, recorder)
	ctx := context.Background()
	session, err := connectClientServer(ctx, server)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = session.Close() }()

	if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "mcp://global/runbooks/absent.md"}); err == nil {
		t.Fatal("reading a resource that is not in the store succeeded")
	}
	if got := len(auditStore.snapshot()); got != 0 {
		t.Errorf("audit events = %d, want 0: a failed read is not a read", got)
	}
}
