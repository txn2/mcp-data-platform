package middleware

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// mockAuditLogger implements AuditLogger for testing.
type mockAuditLogger struct {
	mu     sync.Mutex
	events []AuditEvent
	logErr error
}

func (m *mockAuditLogger) Log(_ context.Context, event AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return m.logErr
}

func (m *mockAuditLogger) getEvents() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditEvent{}, m.events...)
}

func TestNoopAuditLogger(t *testing.T) {
	logger := &NoopAuditLogger{}
	err := logger.Log(context.Background(), AuditEvent{
		ToolName: "test_tool",
		UserID:   "user123",
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAuditEvent(t *testing.T) {
	now := time.Now()
	event := AuditEvent{
		Timestamp:    now,
		RequestID:    "req-123",
		UserID:       "user123",
		UserEmail:    "user@test.com",
		Persona:      "analyst",
		ToolName:     "trino_query",
		ToolkitKind:  "trino",
		ToolkitName:  "main-trino",
		Connection:   "prod",
		Parameters:   map[string]any{"query": "SELECT 1"},
		Success:      true,
		ErrorMessage: "",
		DurationMS:   100,
	}

	// Verify JSON marshaling
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var unmarshaled AuditEvent
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if unmarshaled.ToolName != "trino_query" {
		t.Errorf("expected ToolName 'trino_query', got %q", unmarshaled.ToolName)
	}
	if unmarshaled.DurationMS != 100 {
		t.Errorf("expected DurationMS 100, got %d", unmarshaled.DurationMS)
	}
}

// Verify interface compliance.
var _ AuditLogger = (*NoopAuditLogger)(nil)
var _ AuditLogger = (*mockAuditLogger)(nil)
