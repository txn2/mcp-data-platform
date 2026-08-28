package audit

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

// EventType categorizes audit events.
type EventType string

// The constants below are the complete set of values written into
// audit_logs.event_kind. They are the vocabulary the admin audit API's
// event_kind filter accepts, and TestEventKindParamNamesEveryEventType
// (internal/admin/auditapi) fails when a constant is added here without
// its @Param annotations naming it.
const (
	// EventTypeAdmin categorizes an administrative act performed against
	// somebody else's object rather than a call the actor made on their own
	// behalf. The act is recorded once it is authorized, whether it then
	// succeeded or failed; a caller refused the administrative authority in
	// the first place writes no row of this kind, so these rows are not a
	// record of who attempted one.
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

	// EventTypePromptServe categorizes a prompt being served to an agent
	// (prompts/get on a database prompt, or a resolved manage_prompt use).
	// The event's parameters carry prompt_id, prompt_name, and version, and
	// the postgres store aggregates these rows into per-prompt run counts
	// and last-run timestamps (issue #1009).
	EventTypePromptServe EventType = "prompt_serve"

	// EventTypeResourceRead categorizes a managed resource's content being
	// served: an MCP resources/read, a search `fetch` of an
	// mcp:resource:<id> reference, or a REST content download. The event's
	// parameters carry resource_id, resource_uri, and surface, and the
	// postgres store aggregates these rows into per-resource read counts,
	// per-surface breakdowns, and last-read timestamps (issue #1014).
	// Listing resources is not a read: only content served counts.
	EventTypeResourceRead EventType = "resource_read"

	// EventTypeResourceMove categorizes a managed resource being refiled in
	// another library (issue #1502): the event's parameters carry resource_id,
	// the display name, and the scope, scope id and URI on both sides of the
	// move. It is separate from resource_read because it is a write, and
	// separate from the generic admin event because the question it answers --
	// who put this file in front of which audience, and what address did it
	// used to have -- is asked of the resource, not of the administrator.
	EventTypeResourceMove EventType = "resource_move"

	// EventTypeScriptRun categorizes one execution of an approved managed
	// script: the lifecycle event the run worker writes when a run finishes,
	// carrying script, script_id, version, run_id, owner, trigger, and
	// requested_by in its parameters. It is distinct from the per-capability
	// tool-call rows the same run produces under the script:<name> principal —
	// those record what the script reached, this records that the platform
	// executed it and how it ended — and both carry the run id as their session
	// so a run and its calls join on one key (#1284).
	EventTypeScriptRun EventType = "script_run"
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
		ID:        NewEventID(),
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

// WithPurpose records the one-sentence reason the caller gave for the call
// (issue #1317). It is stored in its own column, never in Parameters: it is not
// an argument value and so is outside the parameter redaction policy.
func (e *Event) WithPurpose(purpose string) *Event {
	e.Purpose = purpose
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

// NewEventID mints the identifier of one audit event.
//
// It is exported because the id is minted before the event exists: the tool-call
// middleware stamps it on the PlatformContext at the start of a call so the
// call's own result can cite it (`call_id`) and an asset can record it as a
// provenance source, while the audit row itself is written after the handler
// returns (issue #1320). The audit adapter reuses the stamped id rather than
// minting a second one.
func NewEventID() string {
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
