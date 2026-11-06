package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// manage_feedback edit and delete (#2514). Both are served by the portal's own
// routes inside this process, as the caller, so who may change a message --
// only its author edits; its author, the target's owner or editor, and an
// administrator delete -- and which events can be changed at all are decided
// once, by the portal, for both surfaces.

const eventIDRequired = "thread_id and event_id are required; action=get lists a thread's events with their ids"

// handleEditMessage replaces the text of a message the caller wrote.
func (t *Toolkit) handleEditMessage(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Body) == "" {
		return toolkit.ErrorResult("body is required for the edit action"), nil, nil
	}
	raw, err := wirejson.Marshal(map[string]string{"body": input.Body})
	if err != nil {
		return toolkit.ErrorResult("encoding the message: " + err.Error()), nil, nil
	}
	return t.changeMessage(ctx, input, http.MethodPatch, raw, "edit the message")
}

// handleDeleteMessage deletes a message.
func (t *Toolkit) handleDeleteMessage(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	return t.changeMessage(ctx, input, http.MethodDelete, nil, "delete the message")
}

// changeMessage serves one edit or delete through the portal and returns its
// answer.
func (t *Toolkit) changeMessage(ctx context.Context, input manageFeedbackInput, method string, body []byte, what string) (*mcp.CallToolResult, any, error) {
	if t.threadStore == nil {
		return toolkit.ErrorResult(threadsUnavail), nil, nil
	}
	if input.ThreadID == "" || input.EventID == "" {
		return toolkit.ErrorResult(eventIDRequired), nil, nil
	}
	caller, ok := mcpcontext.GetCaller(ctx)
	if !ok || caller.UserID == "" {
		return toolkit.ErrorResult("changing a message needs a signed-in caller"), nil, nil
	}
	path := threadsRoute + "/" + url.PathEscape(input.ThreadID) + "/events/" + url.PathEscape(input.EventID)
	status, answer, err := t.servePortal(ctx, caller, method, path, body)
	if err != nil {
		return toolkit.ErrorResult("could not " + what + ": " + err.Error()), nil, nil
	}
	if status != http.StatusOK {
		return toolkit.ErrorResult(portalRefusal(what, status, answer)), nil, nil
	}
	var result map[string]any
	if err := json.Unmarshal(answer, &result); err != nil {
		return toolkit.ErrorResult("reading the portal's answer: " + err.Error()), nil, nil
	}
	return toolkit.JSONResultTyped(result)
}
