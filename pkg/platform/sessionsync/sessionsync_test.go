package sessionsync

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

// TestBuildStore_Database proves the database branch: a *sql.DB yields a
// postgres store and forces stateless mode. The cleanup routine is stopped by
// Close so no query ever fires against the mock.
//
// The postgres LISTEN-failure fallback in buildClientBroadcaster /
// buildReloadBroadcaster is deliberately not unit-tested: pq.NewListener
// retries a refused connection with backoff rather than failing fast, so an
// unreachable DSN hangs instead of returning an error (the pkg/session/postgres
// package documents the same limitation for its listener-driven goroutine). That
// fallback is exercised only by the integration/live-DB path.
func TestBuildStore_Database(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()

	store, forced, err := buildStore(db, Config{Store: storeKindDatabase, TTL: time.Minute, CleanupInterval: time.Minute}, nil)
	if err != nil {
		t.Fatalf("buildStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	if store == nil {
		t.Fatal("database store not built")
	}
	if !forced {
		t.Error("database store must force stateless mode")
	}
}

// --- Assembly (New / StartCache / Close) tests -----------------------------

// TestNew_MemoryStore proves the zero-config path: no database selects the
// memory store (no stateless forcing) and a memory broadcaster (non-nil, so the
// Broadcaster() contract holds), and wires the reload bus.
func TestNew_MemoryStore(t *testing.T) {
	h, err := New(nil, Config{Store: storeKindMemory, TTL: time.Minute, CleanupInterval: time.Minute}, nil, ReloadHandlers{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = h.Close() }()

	if h.SessionStore() == nil {
		t.Error("memory store not assembled")
	}
	if h.Broadcaster() == nil {
		t.Error("broadcaster must be non-nil after New")
	}
	if h.StatelessForced() {
		t.Error("memory store must not force stateless mode")
	}
	if h.SessionCache() != nil {
		t.Error("cache must be nil until StartCache is called")
	}
}

// TestNew_EmptyStoreDefaultsToMemory proves the "" store selects memory.
func TestNew_EmptyStoreDefaultsToMemory(t *testing.T) {
	h, err := New(nil, Config{TTL: time.Minute, CleanupInterval: time.Minute}, nil, ReloadHandlers{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = h.Close() }()
	if h.SessionStore() == nil {
		t.Error("empty store did not default to memory")
	}
}

// TestNew_DatabaseStoreWithoutDBErrors proves the precondition: database store
// selected but no *sql.DB is a construction error, not a silent fallback.
func TestNew_DatabaseStoreWithoutDBErrors(t *testing.T) {
	_, err := New(nil, Config{Store: storeKindDatabase}, nil, ReloadHandlers{})
	if err == nil {
		t.Fatal("expected error when database store selected without a db")
	}
	if !strings.Contains(err.Error(), "no database configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNew_UnknownStoreErrors proves an unrecognized store value fails fast.
func TestNew_UnknownStoreErrors(t *testing.T) {
	_, err := New(nil, Config{Store: "bogus"}, nil, ReloadHandlers{})
	if err == nil {
		t.Fatal("expected error for unknown session store")
	}
	if !strings.Contains(err.Error(), "unknown session store") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNew_InjectedStoreOverride proves the admin SessionStore override path:
// the injected store is used verbatim (no stateless forcing) yet a broadcaster
// and reload bus are still wired.
func TestNew_InjectedStoreOverride(t *testing.T) {
	injected := session.NewMemoryStore(time.Minute)
	h, err := New(nil, Config{Store: storeKindDatabase}, injected, ReloadHandlers{})
	if err != nil {
		t.Fatalf("New with injected store must not error even for database store: %v", err)
	}
	defer func() { _ = h.Close() }()

	if h.SessionStore() != injected {
		t.Error("injected store was not used verbatim")
	}
	if h.StatelessForced() {
		t.Error("injected store must not force stateless mode")
	}
	if h.Broadcaster() == nil {
		t.Error("broadcaster must still be wired on the override path")
	}
}

// TestStartCache proves the cache is built and returned, memoized across calls,
// and exposed via SessionCache.
func TestStartCache(t *testing.T) {
	h, err := New(nil, Config{TTL: time.Minute, CleanupInterval: time.Minute}, nil, ReloadHandlers{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = h.Close() }()

	cache := h.StartCache(time.Minute, 30*time.Minute)
	if cache == nil {
		t.Fatal("StartCache returned nil")
	}
	if h.SessionCache() != cache {
		t.Error("SessionCache did not return the started cache")
	}
	if again := h.StartCache(time.Minute, 30*time.Minute); again != cache {
		t.Error("StartCache must memoize (a second call must not leak a new cache goroutine)")
	}
}

// TestClose_NilSafe proves Close and the accessors are nil-safe.
func TestClose_NilSafe(t *testing.T) {
	var h *Handle
	if err := h.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
	if h.SessionStore() != nil || h.Broadcaster() != nil || h.SessionCache() != nil {
		t.Error("nil-handle accessors must return nil")
	}
	if h.StatelessForced() {
		t.Error("nil-handle StatelessForced must be false")
	}
	// Publish delegators must be no-ops on a nil handle.
	h.PublishConnectionReload(context.Background(), "api", "x")
	h.PublishCatalogReload(context.Background(), "cat")
	h.PublishPersonaReload(context.Background())
	h.PublishAPIKeyReload(context.Background())
}

// recordingStore wraps a memory store and records Close so shutdown ordering
// (cache stop -> store close -> broadcaster close) can be observed.
type recordingStore struct {
	session.Store
	closed *bool
}

func (r recordingStore) Close() error {
	*r.closed = true
	if err := r.Store.Close(); err != nil {
		return fmt.Errorf("recordingStore: %w", err)
	}
	return nil
}

// TestClose_TearsDownStoreAndCache proves Close stops the cache and closes the
// session store (and is safe to call twice).
func TestClose_TearsDownStoreAndCache(t *testing.T) {
	closed := false
	injected := recordingStore{Store: session.NewMemoryStore(time.Minute), closed: &closed}
	h, err := New(nil, Config{}, injected, ReloadHandlers{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.StartCache(time.Minute, 30*time.Minute)

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !closed {
		t.Error("Close did not close the session store")
	}
	if err := h.Close(); err != nil {
		t.Errorf("second Close must be safe: %v", err)
	}
}

// TestHandle_PublishDelegatorsSafe proves the handle's publish delegators route
// through the reload bus without panicking (the cross-replica dispatch itself is
// covered by the bus-core tests below).
func TestHandle_PublishDelegatorsSafe(t *testing.T) {
	h, err := New(nil, Config{TTL: time.Minute, CleanupInterval: time.Minute}, nil, ReloadHandlers{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = h.Close() }()
	ctx := context.Background()
	h.PublishConnectionReload(ctx, "api", "c1")
	h.PublishCatalogReload(ctx, "cat-1")
	h.PublishPersonaReload(ctx)
	h.PublishAPIKeyReload(ctx)
}

// --- Reload bus core tests -------------------------------------------------

// recordingHandlers captures reload-handler invocations on buffered
// channels so tests can assert delivery (or non-delivery) with a timeout.
type recordingHandlers struct {
	conn    chan [2]string
	catalog chan string
	persona chan struct{}
	apiKey  chan struct{}
}

func newRecordingHandlers() recordingHandlers {
	return recordingHandlers{
		conn:    make(chan [2]string, 4),
		catalog: make(chan string, 4),
		persona: make(chan struct{}, 4),
		apiKey:  make(chan struct{}, 4),
	}
}

func (r recordingHandlers) handlers() ReloadHandlers {
	return ReloadHandlers{
		Connection: func(kind, name string) { r.conn <- [2]string{kind, name} },
		Catalog:    func(id string) { r.catalog <- id },
		Persona:    func() { r.persona <- struct{}{} },
		APIKey:     func() { r.apiKey <- struct{}{} },
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
	rb.dispatch(session.Event{Method: reloadMethodPersona, Params: map[string]any{reloadParamOrigin: "peer"}})
	rb.dispatch(session.Event{Method: reloadMethodAPIKey, Params: map[string]any{reloadParamOrigin: "peer"}})

	if got := <-rec.catalog; got != "cat-1" {
		t.Errorf("catalog reload id=%q, want cat-1", got)
	}
	if got := <-rec.conn; got != [2]string{"mcp", "up"} {
		t.Errorf("connection reload=%v, want [mcp up]", got)
	}
	if _, ok := <-rec.persona; !ok {
		t.Error("persona reload not dispatched")
	}
	if _, ok := <-rec.apiKey; !ok {
		t.Error("apikey reload not dispatched")
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
	rb := newReloadBus(session.NewMemoryBroadcaster(nil), "self", ReloadHandlers{}, nil)
	rb.dispatch(session.Event{Method: reloadMethodConnection, Params: map[string]any{"kind": "api", "name": "x", reloadParamOrigin: "peer"}})
	rb.dispatch(session.Event{Method: "platform/reload/future", Params: map[string]any{reloadParamOrigin: "peer"}})
	// Reaching here without panic is the assertion.
}

// TestReloadBus_NilBusPublishSafe proves a nil/disabled bus publish is a
// no-op (single-replica deployments with no broadcaster).
func TestReloadBus_NilBusPublishSafe(t *testing.T) {
	var rb *reloadBus
	rb.publishConnection(t.Context(), "api", "x") // must not panic
	rb = newReloadBus(nil, "self", ReloadHandlers{}, nil)
	rb.publishCatalog(t.Context(), "x") // nil broadcaster: must not panic
	rb.publishPersona(t.Context())
	rb.publishAPIKey(t.Context())
}

// TestNewReplicaOrigin proves the origin id carries a hostname-suffix shape so
// two replicas sharing a hostname still differ.
func TestNewReplicaOrigin(t *testing.T) {
	o1 := newReplicaOrigin()
	o2 := newReplicaOrigin()
	if !strings.Contains(o1, "-") {
		t.Errorf("origin %q lacks the hostname-suffix shape", o1)
	}
	if o1 == o2 {
		t.Errorf("two origins collided: %q", o1)
	}
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
