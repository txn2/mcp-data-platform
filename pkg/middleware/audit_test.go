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

	// Verify ALL 12 fields survive JSON round-trip
	if !unmarshaled.Timestamp.Equal(now) {
		t.Errorf("Timestamp = %v, want %v", unmarshaled.Timestamp, now)
	}
	if unmarshaled.RequestID != "req-123" {
		t.Errorf("RequestID = %q, want %q", unmarshaled.RequestID, "req-123")
	}
	if unmarshaled.UserID != "user123" {
		t.Errorf("UserID = %q, want %q", unmarshaled.UserID, "user123")
	}
	if unmarshaled.UserEmail != "user@test.com" {
		t.Errorf("UserEmail = %q, want %q", unmarshaled.UserEmail, "user@test.com")
	}
	if unmarshaled.Persona != "analyst" {
		t.Errorf("Persona = %q, want %q", unmarshaled.Persona, "analyst")
	}
	if unmarshaled.ToolName != "trino_query" {
		t.Errorf("ToolName = %q, want %q", unmarshaled.ToolName, "trino_query")
	}
	if unmarshaled.ToolkitKind != "trino" {
		t.Errorf("ToolkitKind = %q, want %q", unmarshaled.ToolkitKind, "trino")
	}
	if unmarshaled.ToolkitName != "main-trino" {
		t.Errorf("ToolkitName = %q, want %q", unmarshaled.ToolkitName, "main-trino")
	}
	if unmarshaled.Connection != "prod" {
		t.Errorf("Connection = %q, want %q", unmarshaled.Connection, "prod")
	}
	if unmarshaled.Parameters["query"] != "SELECT 1" {
		t.Errorf("Parameters[query] = %v, want 'SELECT 1'", unmarshaled.Parameters["query"])
	}
	if !unmarshaled.Success {
		t.Errorf("Success = false, want true")
	}
	if unmarshaled.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %q, want empty", unmarshaled.ErrorMessage)
	}
	if unmarshaled.DurationMS != 100 {
		t.Errorf("DurationMS = %d, want 100", unmarshaled.DurationMS)
	}
}

// Verify interface compliance.
var _ AuditLogger = (*NoopAuditLogger)(nil)
var _ AuditLogger = (*mockAuditLogger)(nil)
