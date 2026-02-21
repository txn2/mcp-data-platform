package middleware

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// Test constants for audit tests.
const (
	testAuditUserID      = "user123"
	testAuditPersona     = "analyst"
	testAuditToolName    = "trino_query"
	testAuditToolkit     = "trino"
	testAuditDurationMS  = 100
	testAuditQualityHigh = 95.0
	testAuditRespChars   = 42
	testAuditReqChars    = 10
	testAuditContentBlks = 2
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

func TestNoopAuditLogger(t *testing.T) {
	logger := &NoopAuditLogger{}
	err := logger.Log(context.Background(), AuditEvent{
		ToolName: "test_tool",
		UserID:   testAuditUserID,
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// assertAuditEventFields is a helper that validates all fields of an AuditEvent
// after a JSON round-trip to reduce cognitive complexity of the calling test.
func assertAuditEventFields(t *testing.T, got, want AuditEvent) {
	t.Helper()
	assertAuditEventCoreFields(t, got, want)
	assertAuditEventExtFields(t, got, want)
}

func assertAuditEventCoreFields(t *testing.T, got, want AuditEvent) {
	t.Helper()
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, want.Timestamp)
	}
	if got.RequestID != want.RequestID {
		t.Errorf("RequestID = %q, want %q", got.RequestID, want.RequestID)
	}
	if got.SessionID != want.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, want.SessionID)
	}
	if got.UserID != want.UserID {
		t.Errorf("UserID = %q, want %q", got.UserID, want.UserID)
	}
	if got.UserEmail != want.UserEmail {
		t.Errorf("UserEmail = %q, want %q", got.UserEmail, want.UserEmail)
	}
	if got.Persona != want.Persona {
		t.Errorf("Persona = %q, want %q", got.Persona, want.Persona)
	}
	if got.ToolName != want.ToolName {
		t.Errorf("ToolName = %q, want %q", got.ToolName, want.ToolName)
	}
	if got.ToolkitKind != want.ToolkitKind {
		t.Errorf("ToolkitKind = %q, want %q", got.ToolkitKind, want.ToolkitKind)
	}
	if got.ToolkitName != want.ToolkitName {
		t.Errorf("ToolkitName = %q, want %q", got.ToolkitName, want.ToolkitName)
	}
	if got.Connection != want.Connection {
		t.Errorf("Connection = %q, want %q", got.Connection, want.Connection)
	}
}

func assertAuditEventExtFields(t *testing.T, got, want AuditEvent) {
	t.Helper()
	if got.Success != want.Success {
		t.Errorf("Success = %v, want %v", got.Success, want.Success)
	}
	if got.ErrorMessage != want.ErrorMessage {
		t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, want.ErrorMessage)
	}
	if got.DurationMS != want.DurationMS {
		t.Errorf("DurationMS = %d, want %d", got.DurationMS, want.DurationMS)
	}
	if got.Transport != want.Transport {
		t.Errorf("Transport = %q, want %q", got.Transport, want.Transport)
	}
	if got.Source != want.Source {
		t.Errorf("Source = %q, want %q", got.Source, want.Source)
	}
	if got.EnrichmentApplied != want.EnrichmentApplied {
		t.Errorf("EnrichmentApplied = %v, want %v", got.EnrichmentApplied, want.EnrichmentApplied)
	}
	if got.EnrichmentMode != want.EnrichmentMode {
		t.Errorf("EnrichmentMode = %q, want %q", got.EnrichmentMode, want.EnrichmentMode)
	}
	if got.Authorized != want.Authorized {
		t.Errorf("Authorized = %v, want %v", got.Authorized, want.Authorized)
	}
}

func TestAuditEvent(t *testing.T) {
	now := time.Now()
	event := AuditEvent{
		Timestamp:         now,
		RequestID:         "req-123",
		SessionID:         "session-abc",
		UserID:            testAuditUserID,
		UserEmail:         "user@test.com",
		Persona:           testAuditPersona,
		ToolName:          testAuditToolName,
		ToolkitKind:       testAuditToolkit,
		ToolkitName:       "main-trino",
		Connection:        "prod",
		Parameters:        map[string]any{"query": "SELECT 1"},
		Success:           true,
		ErrorMessage:      "",
		DurationMS:        testAuditDurationMS,
		ResponseChars:     testAuditRespChars,
		RequestChars:      testAuditReqChars,
		ContentBlocks:     testAuditContentBlks,
		Transport:         "stdio",
		Source:            "mcp",
		EnrichmentApplied: true,
		Authorized:        true,
	}

	// Verify JSON marshaling.
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var unmarshaled AuditEvent
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify ALL fields survive JSON round-trip.
	assertAuditEventFields(t, unmarshaled, event)

	// Verify parameters separately (not compared in assertAuditEventFields).
	if unmarshaled.Parameters["query"] != "SELECT 1" {
		t.Errorf("Parameters[query] = %v, want 'SELECT 1'", unmarshaled.Parameters["query"])
	}
}

// Verify interface compliance.
var (
	_ AuditLogger = (*NoopAuditLogger)(nil)
	_ AuditLogger = (*mockAuditLogger)(nil)
)
