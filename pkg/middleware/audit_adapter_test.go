package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
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
		Timestamp:    time.Now(),
		RequestID:    "req-123",
		UserID:       "user@example.com",
		UserEmail:    "user@example.com",
		Persona:      "analyst",
		ToolName:     "trino_query",
		ToolkitKind:  "trino",
		ToolkitName:  "production",
		Connection:   "trino://prod",
		Parameters:   map[string]any{"sql": "SELECT 1", "password": "secret"},
		Success:      true,
		ErrorMessage: "",
		DurationMS:   100,
	}

	err := adapter.Log(context.Background(), event)
	require.NoError(t, err)

	// Verify event was logged
	require.Len(t, store.events, 1)
	logged := store.events[0]

	assert.Equal(t, "trino_query", logged.ToolName)
	assert.Equal(t, "user@example.com", logged.UserID)
	assert.Equal(t, "user@example.com", logged.UserEmail)
	assert.Equal(t, "analyst", logged.Persona)
	assert.Equal(t, "trino", logged.ToolkitKind)
	assert.Equal(t, "production", logged.ToolkitName)
	assert.Equal(t, "trino://prod", logged.Connection)
	assert.True(t, logged.Success)
	assert.Equal(t, int64(100), logged.DurationMS)
	assert.Equal(t, "req-123", logged.RequestID)

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
		UserID:       "user@example.com",
		ToolName:     "trino_query",
		Success:      false,
		ErrorMessage: "query failed",
		DurationMS:   50,
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

func TestAuditStoreAdapter_Log_PreservesTimestamp(t *testing.T) {
	store := &mockAuditStore{}
	adapter := newAuditStoreAdapterWithStore(store)

	specificTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	event := AuditEvent{
		Timestamp: specificTime,
		ToolName:  "test_tool",
	}

	err := adapter.Log(context.Background(), event)
	require.NoError(t, err)

	require.Len(t, store.events, 1)
	assert.Equal(t, specificTime, store.events[0].Timestamp)
}
