package middleware

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// Test constants for audit adapter tests.
const (
	adapterTestEmail      = "user@example.com"
	adapterTestDuration   = 100
	adapterTestDurationSm = 50
	adapterTestYear       = 2024
	adapterTestDay        = 15
	adapterTestHour       = 10
	adapterTestMinute     = 30
	adapterTestRespChars  = 42
	adapterTestReqChars   = 15
)

// mockAuditStore implements auditStore for testing.
type mockAuditStore struct {
	events []audit.Event
	logErr error
}

func (m *mockAuditStore) Log(_ context.Context, event audit.Event) error {
	if m.logErr != nil {
		return m.logErr
	}
	m.events = append(m.events, event)
	return nil
}

// newAuditStoreAdapterWithStore creates an AuditLogger with a custom store (for testing).
func newAuditStoreAdapterWithStore(store auditStore) AuditLogger {
	return &auditStoreAdapter{store: store}
}

func TestNewAuditStoreAdapter(t *testing.T) {
	// Test with nil store (just verifies constructor doesn't panic)
	adapter := NewAuditStoreAdapter(nil)
	require.NotNil(t, adapter)
}

func TestAuditStoreAdapter_Log(t *testing.T) {
	store := &mockAuditStore{}
	adapter := newAuditStoreAdapterWithStore(store)

	event := AuditEvent{
		Timestamp:         time.Now(),
		RequestID:         "req-123",
		SessionID:         "session-xyz",
		UserID:            adapterTestEmail,
		UserEmail:         adapterTestEmail,
		Persona:           "analyst",
		ToolName:          "trino_query",
		ToolkitKind:       "trino",
		ToolkitName:       "production",
		Connection:        "trino://prod",
		Parameters:        map[string]any{"sql": "SELECT 1", "password": "secret"},
		Success:           true,
		ErrorMessage:      "",
		DurationMS:        adapterTestDuration,
		ResponseChars:     adapterTestRespChars,
		RequestChars:      adapterTestReqChars,
		ContentBlocks:     2,
		Transport:         "stdio",
		Source:            "mcp",
		EnrichmentApplied: true,
		Authorized:        true,
	}

	err := adapter.Log(context.Background(), event)
	require.NoError(t, err)

	// Verify event was logged
	require.Len(t, store.events, 1)
	logged := store.events[0]

	assert.Equal(t, "trino_query", logged.ToolName)
	assert.Equal(t, adapterTestEmail, logged.UserID)
	assert.Equal(t, adapterTestEmail, logged.UserEmail)
	assert.Equal(t, "analyst", logged.Persona)
	assert.Equal(t, "trino", logged.ToolkitKind)
	assert.Equal(t, "production", logged.ToolkitName)
	assert.Equal(t, "trino://prod", logged.Connection)
	assert.True(t, logged.Success)
	assert.Equal(t, int64(adapterTestDuration), logged.DurationMS)
	assert.Equal(t, "req-123", logged.RequestID)
	assert.Equal(t, "session-xyz", logged.SessionID)
	assert.Equal(t, adapterTestRespChars, logged.ResponseChars)
	assert.Equal(t, adapterTestReqChars, logged.RequestChars)
	assert.Equal(t, 2, logged.ContentBlocks)
	assert.Equal(t, "stdio", logged.Transport)
	assert.Equal(t, "mcp", logged.Source)
	assert.True(t, logged.EnrichmentApplied)
	assert.True(t, logged.Authorized)

	// Verify sensitive parameters are sanitized
	assert.Equal(t, "[REDACTED]", logged.Parameters["password"])
	assert.Equal(t, "SELECT 1", logged.Parameters["sql"])
}

func TestAuditStoreAdapter_Log_WithError(t *testing.T) {
	store := &mockAuditStore{}
	adapter := newAuditStoreAdapterWithStore(store)

	event := AuditEvent{
		Timestamp:    time.Now(),
		RequestID:    "req-456",
		UserID:       adapterTestEmail,
		ToolName:     "trino_query",
		Success:      false,
		ErrorMessage: "query failed",
		DurationMS:   adapterTestDurationSm,
	}

	err := adapter.Log(context.Background(), event)
	require.NoError(t, err)

	require.Len(t, store.events, 1)
	logged := store.events[0]

	assert.False(t, logged.Success)
	assert.Equal(t, "query failed", logged.ErrorMessage)
}

func TestAuditStoreAdapter_Close(t *testing.T) {
	store := &mockAuditStore{}
	adapter := newAuditStoreAdapterWithStore(store)

	// Close should be a no-op that returns nil
	// Type assert to concrete type to access Close method
	concreteAdapter, ok := adapter.(*auditStoreAdapter)
	require.True(t, ok)
	err := concreteAdapter.Close()
	assert.NoError(t, err)
}

// drainableAuditStore is an audit.Logger whose Log records events (after an
// optional delay) so a test can prove the adapter's Close drains the async
// writer that wraps it.
type drainableAuditStore struct {
	mu     sync.Mutex
	events []audit.Event
	delay  time.Duration
}

func (s *drainableAuditStore) Log(_ context.Context, e audit.Event) error {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (*drainableAuditStore) Query(_ context.Context, _ audit.QueryFilter) ([]audit.Event, error) {
	return nil, nil
}
func (*drainableAuditStore) Close() error { return nil }

func (s *drainableAuditStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// TestAuditStoreAdapter_CloseDrainsAsyncWriter proves the adapter's Close
// flushes a wrapping async writer: events enqueued through the adapter but not
// yet written to the slow store are all persisted by the time Close returns.
// This is the shutdown-drain path the platform relies on via its audit-logger
// Closer branch (#884).
func TestAuditStoreAdapter_CloseDrainsAsyncWriter(t *testing.T) {
	store := &drainableAuditStore{delay: time.Millisecond}
	writer := audit.NewAsyncWriter(store)
	adapter := NewAuditStoreAdapter(writer)

	const n = 20
	for range n {
		if err := adapter.Log(context.Background(), AuditEvent{ToolName: "trino_query"}); err != nil {
			t.Fatalf("Log returned error: %v", err)
		}
	}

	closer, ok := adapter.(*auditStoreAdapter)
	require.True(t, ok)
	if err := closer.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if got := store.count(); got != n {
		t.Errorf("expected %d events flushed by Close, got %d", n, got)
	}
}

// blockingDrainStore is an audit.Logger whose Log blocks forever, so the async
// writer wrapping it cannot drain within the adapter's shortened deadline.
type blockingDrainStore struct{ release chan struct{} }

func (s *blockingDrainStore) Log(_ context.Context, _ audit.Event) error {
	<-s.release
	return nil
}

func (*blockingDrainStore) Query(_ context.Context, _ audit.QueryFilter) ([]audit.Event, error) {
	return nil, nil
}
func (*blockingDrainStore) Close() error { return nil }

// TestAuditStoreAdapter_CloseDrainTimeout verifies Close surfaces a wrapped
// error when the async writer cannot drain before the deadline, so shutdown
// records the audit-loss rather than swallowing it (#884).
func TestAuditStoreAdapter_CloseDrainTimeout(t *testing.T) {
	orig := auditDrainTimeout
	auditDrainTimeout = 20 * time.Millisecond
	defer func() { auditDrainTimeout = orig }()

	store := &blockingDrainStore{release: make(chan struct{})}
	defer close(store.release)
	writer := audit.NewAsyncWriter(store)
	adapter := NewAuditStoreAdapter(writer)

	if err := adapter.Log(context.Background(), AuditEvent{ToolName: "trino_query"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	closer, ok := adapter.(*auditStoreAdapter)
	require.True(t, ok)
	err := closer.Close()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "draining audit writer")
}

func TestAuditStoreAdapter_Log_PreservesTimestamp(t *testing.T) {
	store := &mockAuditStore{}
	adapter := newAuditStoreAdapterWithStore(store)

	specificTime := time.Date(adapterTestYear, 1, adapterTestDay, adapterTestHour, adapterTestMinute, 0, 0, time.UTC)
	event := AuditEvent{
		Timestamp: specificTime,
		ToolName:  "test_tool",
	}

	err := adapter.Log(context.Background(), event)
	require.NoError(t, err)

	require.Len(t, store.events, 1)
	assert.Equal(t, specificTime, store.events[0].Timestamp)
}

// The id the tool-call middleware minted is the id of the stored row: an agent
// was already handed it, and an asset may already cite it as a source (#1320).
func TestAuditStoreAdapter_LogKeepsTheMintedID(t *testing.T) {
	store := &mockAuditStore{}
	adapter := newAuditStoreAdapterWithStore(store)

	require.NoError(t, adapter.Log(context.Background(), AuditEvent{ID: "evt-minted", ToolName: "trino_query"}))
	require.Len(t, store.events, 1)
	assert.Equal(t, "evt-minted", store.events[0].ID)
}

// An event assembled outside the tool-call path still gets an id.
func TestAuditStoreAdapter_LogMintsAnIDWhenNoneWasStamped(t *testing.T) {
	store := &mockAuditStore{}
	adapter := newAuditStoreAdapterWithStore(store)

	require.NoError(t, adapter.Log(context.Background(), AuditEvent{ToolName: "trino_query"}))
	require.Len(t, store.events, 1)
	assert.NotEmpty(t, store.events[0].ID)
}

// Flush drains the async writer without closing it, so a provenance capture
// can read the calls that just completed.
func TestAuditStoreAdapter_FlushDrainsAsyncWriter(t *testing.T) {
	store := &drainableAuditStore{delay: time.Millisecond}
	writer := audit.NewAsyncWriter(store)
	adapter := NewAuditStoreAdapter(writer)
	defer func() { _ = writer.Close(context.Background()) }()

	const n = 10
	for range n {
		require.NoError(t, adapter.Log(context.Background(), AuditEvent{ToolName: "trino_query"}))
	}

	flusher, ok := adapter.(interface{ Flush(context.Context) error })
	require.True(t, ok, "the adapter must offer the flush the capture waits on")
	require.NoError(t, flusher.Flush(context.Background()))
	assert.Equal(t, n, store.count())

	require.NoError(t, adapter.Log(context.Background(), AuditEvent{ToolName: "trino_query"}),
		"the writer is still open after a flush")
}

// A store that writes synchronously has nothing to wait for.
func TestAuditStoreAdapter_FlushWithoutABufferingStore(t *testing.T) {
	adapter := newAuditStoreAdapterWithStore(&mockAuditStore{})
	flusher, ok := adapter.(interface{ Flush(context.Context) error })
	require.True(t, ok)
	assert.NoError(t, flusher.Flush(context.Background()))
}

// A flush that cannot complete is reported to the caller, which treats it as
// degraded accuracy rather than a failed write.
func TestAuditStoreAdapter_FlushError(t *testing.T) {
	store := &blockingDrainStore{release: make(chan struct{})}
	defer close(store.release)
	writer := audit.NewAsyncWriter(store)
	adapter := NewAuditStoreAdapter(writer)

	require.NoError(t, adapter.Log(context.Background(), AuditEvent{ToolName: "trino_query"}))

	flusher, ok := adapter.(interface{ Flush(context.Context) error })
	require.True(t, ok)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := flusher.Flush(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flushing audit writer")
}
