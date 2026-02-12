package middleware

import (
	"context"
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
