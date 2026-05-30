package platform

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/session"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// fakeConnStore returns a fixed config for every Get so the reloader's
// AddConnection branch is exercised.
type fakeConnStore struct{ cfg map[string]any }

func (fakeConnStore) List(context.Context) ([]ConnectionInstance, error) { return nil, nil }
func (f fakeConnStore) Get(_ context.Context, kind, name string) (*ConnectionInstance, error) {
	return &ConnectionInstance{Kind: kind, Name: name, Config: f.cfg}, nil
}
func (fakeConnStore) Set(context.Context, ConnectionInstance) error { return nil }
func (fakeConnStore) Delete(context.Context, string, string) error  { return nil }
func (fakeConnStore) Persistent() bool                              { return false }

// TestPlatform_ReloadWiring exercises the platform-level reload methods:
// dedicated-bus init (memory fallback, no db), the connection/catalog
// reloaders against a live api-gateway toolkit, the publish methods, and
// shutdown. It is the coverage counterpart to the bus-core unit tests.
func TestPlatform_ReloadWiring(t *testing.T) {
	reg := registry.NewRegistry()
	apiTk := apigatewaykit.New("api")
	if err := reg.Register(apiTk); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	p := &Platform{
		config:          &Config{},
		toolkitRegistry: reg,
		connectionStore: fakeConnStore{cfg: map[string]any{"base_url": "https://x"}},
	}

	p.initReloadBus() // no db -> in-memory broadcaster
	if p.reloadBus == nil || p.reloadBroadcaster == nil {
		t.Fatal("initReloadBus did not wire the bus")
	}

	// Reloaders against the live toolkit (rebuild from the fake store).
	p.reloadConnectionLocal("api", "c1")
	if !apiTk.HasConnection("c1") {
		t.Error("reloadConnectionLocal did not add the connection")
	}
	p.reloadConnectionLocal("mcp", "ignored") // wrong kind: no-op, exercises the skip
	p.reloadCatalogLocal("cat-1")             // ReloadConnectionsByCatalog on the api toolkit

	// Publish methods (memory bus; no subscriber needed for coverage).
	p.PublishConnectionReload("api", "c1")
	p.PublishCatalogReload("cat-1")

	if origin := newReplicaOrigin(); !strings.Contains(origin, "-") {
		t.Errorf("origin %q lacks the hostname-suffix shape", origin)
	}

	p.stopReloadBus() // cancel subscriber + close broadcaster

	// Publish after stop must be safe (broadcaster closed).
	p.PublishConnectionReload("api", "c1")
}

// recordingHandlers captures reload-handler invocations on buffered
// channels so tests can assert delivery (or non-delivery) with a timeout.
type recordingHandlers struct {
	conn    chan [2]string
	catalog chan string
}

func newRecordingHandlers() recordingHandlers {
	return recordingHandlers{
		conn:    make(chan [2]string, 4),
		catalog: make(chan string, 4),
	}
}

func (r recordingHandlers) handlers() reloadHandlers {
	return reloadHandlers{
		connection: func(kind, name string) { r.conn <- [2]string{kind, name} },
		catalog:    func(id string) { r.catalog <- id },
	}
}

// TestReloadBus_CrossReplica proves the core #501 fix: a reload published
// by one replica is applied by the OTHER replica, and the publishing
// replica skips its own event (it reloaded synchronously on the write
// path). Two buses share one in-memory broadcaster, which is exactly how
// the postgres broadcaster re-publishes a received NOTIFY locally, so
// this is a faithful cross-replica simulation.
func TestReloadBus_CrossReplica(t *testing.T) {
	b := session.NewMemoryBroadcaster(nil)
	defer func() { _ = b.Close() }()

	recA := newRecordingHandlers()
	recB := newRecordingHandlers()
	busA := newReloadBus(b, "replica-a", recA.handlers(), nil)
	busB := newReloadBus(b, "replica-b", recB.handlers(), nil)

	ctx := t.Context()
	go busA.run(ctx)
	go busB.run(ctx)
	// Let both subscriptions register before publishing.
	waitForSubscribers(t, b, 2)

	// Replica A handled the admin write and publishes the reload.
	busA.publishConnection(t.Context(), "api", "Test API")

	// Replica B must apply it.
	select {
	case got := <-recB.conn:
		if got != [2]string{"api", "Test API"} {
			t.Fatalf("replica B reloaded %v, want [api Test API]", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replica B never received the connection reload (the #501 bug)")
	}

	// Replica A must NOT re-apply its own event (origin skip).
	select {
	case got := <-recA.conn:
		t.Fatalf("replica A re-applied its own publish %v; should skip self-origin", got)
	case <-time.After(150 * time.Millisecond):
		// expected: no self-reload
	}
}

// TestReloadBus_DispatchRouting proves each method routes to its handler.
func TestReloadBus_DispatchRouting(t *testing.T) {
	rec := newRecordingHandlers()
	rb := newReloadBus(session.NewMemoryBroadcaster(nil), "self", rec.handlers(), nil)

	rb.dispatch(session.Event{Method: reloadMethodCatalog, Params: map[string]any{"catalog_id": "cat-1", reloadParamOrigin: "peer"}})
	rb.dispatch(session.Event{Method: reloadMethodConnection, Params: map[string]any{"kind": "mcp", "name": "up", reloadParamOrigin: "peer"}})

	if got := <-rec.catalog; got != "cat-1" {
		t.Errorf("catalog reload id=%q, want cat-1", got)
	}
	if got := <-rec.conn; got != [2]string{"mcp", "up"} {
		t.Errorf("connection reload=%v, want [mcp up]", got)
	}
}

// TestReloadBus_SkipsSelfOrigin proves self-published events are ignored.
func TestReloadBus_SkipsSelfOrigin(t *testing.T) {
	rec := newRecordingHandlers()
	rb := newReloadBus(session.NewMemoryBroadcaster(nil), "self", rec.handlers(), nil)
	rb.dispatch(session.Event{Method: reloadMethodCatalog, Params: map[string]any{"catalog_id": "x", reloadParamOrigin: "self"}})
	select {
	case <-rec.catalog:
		t.Fatal("self-origin event must be skipped")
	default:
	}
}

// TestReloadBus_NilHandlerAndUnknownMethod proves a missing handler or an
// unknown method is a safe no-op (forward compatibility).
func TestReloadBus_NilHandlerAndUnknownMethod(_ *testing.T) {
	rb := newReloadBus(session.NewMemoryBroadcaster(nil), "self", reloadHandlers{}, nil)
	rb.dispatch(session.Event{Method: reloadMethodConnection, Params: map[string]any{"kind": "api", "name": "x", reloadParamOrigin: "peer"}})
	rb.dispatch(session.Event{Method: "platform/reload/future", Params: map[string]any{reloadParamOrigin: "peer"}})
	// Reaching here without panic is the assertion.
}

// TestReloadBus_NilBusPublishSafe proves a nil/disabled bus publish is a
// no-op (single-replica deployments with no broadcaster).
func TestReloadBus_NilBusPublishSafe(t *testing.T) {
	var rb *reloadBus
	rb.publishConnection(t.Context(), "api", "x") // must not panic
	rb = newReloadBus(nil, "self", reloadHandlers{}, nil)
	rb.publishCatalog(t.Context(), "x") // nil broadcaster: must not panic
}

func waitForSubscribers(t *testing.T, b *session.MemoryBroadcaster, n int) {
	t.Helper()
	for range 100 {
		if b.SubscriberCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("subscribers did not reach %d", n)
}
