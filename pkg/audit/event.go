package audit

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

// EventType categorizes audit events.
type EventType string

const (
	// EventTypeToolCall is a tool invocation event.
	EventTypeToolCall EventType = "tool_call"

	// EventTypeAuth is an authentication event.
	EventTypeAuth EventType = "auth"

	// EventTypeAdmin is an administrative event.
	EventTypeAdmin EventType = "admin"
)

// NewEvent creates a new audit event.
func NewEvent(toolName string) *Event {
	return &Event{
		ID:        generateEventID(),
		Timestamp: time.Now(),
		ToolName:  toolName,
	}
}

// WithUser adds user information to the event.
func (e *Event) WithUser(userID, email string) *Event {
	e.UserID = userID
	e.UserEmail = email
	return e
}

// WithPersona adds persona information to the event.
func (e *Event) WithPersona(persona string) *Event {
	e.Persona = persona
	return e
}

// WithToolkit adds toolkit information to the event.
func (e *Event) WithToolkit(kind, name string) *Event {
	e.ToolkitKind = kind
	e.ToolkitName = name
	return e
}

// WithConnection adds connection information to the event.
func (e *Event) WithConnection(connection string) *Event {
	e.Connection = connection
	return e
}

// WithParameters adds parameters to the event.
func (e *Event) WithParameters(params map[string]any) *Event {
	e.Parameters = params
	return e
}

// WithResult adds result information to the event.
func (e *Event) WithResult(success bool, errorMsg string, durationMS int64) *Event {
	e.Success = success
	e.ErrorMessage = errorMsg
	e.DurationMS = durationMS
	return e
}

// WithRequestID adds a request ID to the event.
func (e *Event) WithRequestID(requestID string) *Event {
	e.RequestID = requestID
	return e
}

// generateEventID generates a unique event ID.
func generateEventID() string {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes)
}

// SanitizeParameters removes sensitive parameters from the event.
func SanitizeParameters(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}

	sensitiveKeys := map[string]bool{
		"password":      true,
		"secret":        true,
		"token":         true,
		"api_key":       true,
		"authorization": true,
		"credentials":   true,
	}

	sanitized := make(map[string]any)
	for k, v := range params {
		if sensitiveKeys[k] {
			sanitized[k] = "[REDACTED]"
		} else {
			sanitized[k] = v
		}
	}
	return sanitized
}
