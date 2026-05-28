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

	// EventTypeMCPToolCall categorizes an MCP tool invocation routed
	// through a non-apigateway toolkit (trino, datahub, s3, mcp gateway).
	// Stamped into Event.EventKind at write time so the portal Activity
	// view can exclude apigateway HTTP noise by default.
	EventTypeMCPToolCall EventType = "mcp_tool_call"

	// EventTypeAPIGatewayInvoke categorizes an HTTP API invocation
	// through the apigateway toolkit. Stamped into Event.EventKind at
	// write time so the portal Activity view can split these out from
	// the MCP-only view.
	EventTypeAPIGatewayInvoke EventType = "apigateway_invoke"
)

// toolkitKindAPIGateway is the toolkit-kind discriminator for the
// apigateway toolkit (mirrors apigateway.Kind). Kept as a literal here
// rather than importing the toolkit package so the low-level audit
// package stays decoupled from toolkit implementations.
const toolkitKindAPIGateway = "api"

// EventKindForToolkit maps a toolkit kind to its high-level event
// category. The apigateway toolkit ("api") produces HTTP invocations;
// every other toolkit kind produces MCP tool calls. Used at audit
// write time so the portal can split gateway noise from MCP activity.
func EventKindForToolkit(toolkitKind string) EventType {
	if toolkitKind == toolkitKindAPIGateway {
		return EventTypeAPIGatewayInvoke
	}
	return EventTypeMCPToolCall
}

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

// WithResponseSize adds response size metrics to the event.
func (e *Event) WithResponseSize(chars, contentBlocks int) *Event {
	e.ResponseChars = chars
	e.ContentBlocks = contentBlocks
	return e
}

// WithSessionID adds session identification to the event.
func (e *Event) WithSessionID(sessionID string) *Event {
	e.SessionID = sessionID
	return e
}

// WithRequestSize adds request size metrics to the event.
func (e *Event) WithRequestSize(chars int) *Event {
	e.RequestChars = chars
	return e
}

// WithTransport adds transport and source metadata to the event.
func (e *Event) WithTransport(transport, source string) *Event {
	e.Transport = transport
	e.Source = source
	return e
}

// WithEnrichment records whether semantic enrichment was applied.
func (e *Event) WithEnrichment(applied bool) *Event {
	e.EnrichmentApplied = applied
	return e
}

// WithAuthorized records the authorization decision.
func (e *Event) WithAuthorized(authorized bool) *Event {
	e.Authorized = authorized
	return e
}

// WithEventKind records the high-level category of the event
// (e.g. mcp_tool_call, apigateway_invoke). See Event.EventKind.
func (e *Event) WithEventKind(kind EventType) *Event {
	e.EventKind = kind
	return e
}

// WithEnrichmentTokens records estimated token counts for enrichment.
func (e *Event) WithEnrichmentTokens(full, dedup int) *Event {
	e.EnrichmentTokensFull = full
	e.EnrichmentTokensDedup = dedup
	return e
}

// WithEnrichmentMode records the enrichment mode used for this event.
func (e *Event) WithEnrichmentMode(mode string) *Event {
	e.EnrichmentMode = mode
	return e
}

// WithEnrichmentMatchKind records how enrichment matched its target:
// "urn" for exact URN equality, "semantic" for similarity-fallback,
// empty when no enrichment ran. See Event.EnrichmentMatchKind.
func (e *Event) WithEnrichmentMatchKind(kind string) *Event {
	e.EnrichmentMatchKind = kind
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
