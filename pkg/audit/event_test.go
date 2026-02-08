package audit

import "testing"

const (
	redactedValue       = "[REDACTED]"
	eventTestDurationMS = 100
	eventTestRespChars  = 500
	eventTestRespTokens = 125
)

func TestNewEvent(t *testing.T) {
	event := NewEvent("test_tool")

	if event.ToolName != "test_tool" {
		t.Errorf("ToolName = %q, want %q", event.ToolName, "test_tool")
	}
	if event.ID == "" {
		t.Error("ID should not be empty")
	}
	if event.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestEvent_Builders(t *testing.T) {
	event := NewEvent("test_tool").
		WithUser("user123", "user@example.com").
		WithPersona("analyst").
		WithToolkit("trino", "production").
		WithConnection("prod-cluster").
		WithParameters(map[string]any{"query": "SELECT 1"}).
		WithResult(true, "", eventTestDurationMS).
		WithRequestID("req-123").
		WithResponseSize(eventTestRespChars, eventTestRespTokens)

	if event.UserID != "user123" {
		t.Errorf("UserID = %q, want %q", event.UserID, "user123")
	}
	if event.UserEmail != "user@example.com" {
		t.Errorf("UserEmail = %q, want %q", event.UserEmail, "user@example.com")
	}
	if event.Persona != "analyst" {
		t.Errorf("Persona = %q, want %q", event.Persona, "analyst")
	}
	if event.ToolkitKind != "trino" {
		t.Errorf("ToolkitKind = %q, want %q", event.ToolkitKind, "trino")
	}
	if event.ToolkitName != "production" {
		t.Errorf("ToolkitName = %q, want %q", event.ToolkitName, "production")
	}
	if event.Connection != "prod-cluster" {
		t.Errorf("Connection = %q, want %q", event.Connection, "prod-cluster")
	}
	if event.Parameters["query"] != "SELECT 1" {
		t.Error("Parameters not set correctly")
	}
	if !event.Success {
		t.Error("Success = false, want true")
	}
	if event.DurationMS != eventTestDurationMS {
		t.Errorf("DurationMS = %d, want %d", event.DurationMS, eventTestDurationMS)
	}
	if event.RequestID != "req-123" {
		t.Errorf("RequestID = %q, want %q", event.RequestID, "req-123")
	}
	if event.ResponseChars != eventTestRespChars {
		t.Errorf("ResponseChars = %d, want %d", event.ResponseChars, eventTestRespChars)
	}
	if event.ResponseTokenEstimate != eventTestRespTokens {
		t.Errorf("ResponseTokenEstimate = %d, want %d", event.ResponseTokenEstimate, eventTestRespTokens)
	}
}

func TestSanitizeParameters(t *testing.T) {
	params := map[string]any{
		"query":    "SELECT 1",
		"password": "secret123",
		"token":    "abc123",
		"limit":    eventTestDurationMS,
	}

	sanitized := SanitizeParameters(params)

	if sanitized["query"] != "SELECT 1" {
		t.Error("query should not be sanitized")
	}
	if sanitized["password"] != redactedValue {
		t.Errorf("password = %v, want %s", sanitized["password"], redactedValue)
	}
	if sanitized["token"] != redactedValue {
		t.Errorf("token = %v, want %s", sanitized["token"], redactedValue)
	}
	if sanitized["limit"] != eventTestDurationMS {
		t.Error("limit should not be sanitized")
	}
}

func TestSanitizeParameters_Nil(t *testing.T) {
	sanitized := SanitizeParameters(nil)
	if sanitized != nil {
		t.Error("SanitizeParameters(nil) should return nil")
	}
}
