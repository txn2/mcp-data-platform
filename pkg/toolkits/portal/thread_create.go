package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/handlertransport"
	"github.com/txn2/mcp-data-platform/internal/selfcall"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Opening a feedback thread (#2371).
//
// manage_feedback action=create opens a thread through the portal's own route,
// POST /api/v1/portal/threads, served inside this process as the caller the
// tool-call middleware authenticated (internal/selfcall, #2086). The portal's
// authenticator takes that caller as it takes a token, so the thread is opened
// by the same persona gate, target check (view access to the asset, collection,
// prompt or knowledge page; any signed-in person on the standalone channel),
// visibility rule, mention filter and notification a thread opened in the
// portal goes through, with no second copy of any of them here. The author is
// the caller. A process that serves no HTTP API cannot open one.

const (
	// threadsRoute is the portal route a thread is opened through and read
	// back from.
	threadsRoute = "/api/v1/portal/threads"
	// selfOrigin is the origin of a request served inside this process; the
	// handler routes on the path alone.
	selfOrigin = "http://platform.internal"

	threadTargetKnowledgePage = "knowledge_page"

	createScopeErr = "specify target_type=standalone or exactly one of asset_id, collection_id, prompt_id, knowledge_page_id"
)

// createThreadPayload is the body POST /api/v1/portal/threads takes.
type createThreadPayload struct {
	Kind               string          `json:"kind"`
	TargetType         string          `json:"target_type"`
	AssetID            string          `json:"asset_id,omitempty"`
	CollectionID       string          `json:"collection_id,omitempty"`
	PromptID           string          `json:"prompt_id,omitempty"`
	KnowledgePageID    string          `json:"knowledge_page_id,omitempty"`
	Anchor             json.RawMessage `json:"anchor,omitempty"`
	TargetVersion      int             `json:"target_version,omitempty"`
	Title              string          `json:"title,omitempty"`
	RequiresResolution bool            `json:"requires_resolution"`
	Body               string          `json:"body"`
	Visibility         string          `json:"visibility,omitempty"`
}

// SetSelfHandler installs the platform's own HTTP handler, through which
// action=create opens a thread (selfcall.Install calls it once the server has
// assembled the handler).
func (t *Toolkit) SetSelfHandler(h http.Handler) { t.self.Set(h) }

func (t *Toolkit) handleCreateThread(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	if t.threadStore == nil {
		return toolkit.ErrorResult(threadsUnavail), nil, nil
	}
	caller, ok := mcpcontext.GetCaller(ctx)
	if !ok || caller.UserID == "" {
		return toolkit.ErrorResult("opening a feedback thread needs a signed-in caller"), nil, nil
	}
	if strings.TrimSpace(input.Body) == "" {
		return toolkit.ErrorResult("body is required for the create action"), nil, nil
	}
	payload, errText := createPayload(input)
	if errText != "" {
		return toolkit.ErrorResult(errText), nil, nil
	}
	raw, err := wirejson.Marshal(payload)
	if err != nil {
		return toolkit.ErrorResult("encoding the thread: " + err.Error()), nil, nil
	}
	status, body, err := t.servePortal(ctx, caller, http.MethodPost, threadsRoute, raw)
	if err != nil {
		return toolkit.ErrorResult("opening the thread: " + err.Error()), nil, nil
	}
	if status != http.StatusCreated {
		return toolkit.ErrorResult(portalRefusal("open the thread", status, body)), nil, nil
	}
	var thread portal.Thread
	if err := json.Unmarshal(body, &thread); err != nil {
		return toolkit.ErrorResult("reading the opened thread: " + err.Error()), nil, nil
	}
	return toolkit.JSONResultTyped(thread)
}

// createPayload builds the portal request from the tool input, or returns
// what is wrong with the input. The target is named by exactly one object id
// or target_type=standalone; target_type may also name the id's own kind.
func createPayload(input manageFeedbackInput) (payload createThreadPayload, errText string) {
	targetType, ok := createTarget(input)
	if !ok {
		return createThreadPayload{}, createScopeErr
	}
	anchor, err := anchorJSON(input.Anchor)
	if err != nil {
		return createThreadPayload{}, err.Error()
	}
	kind := input.Kind
	if kind == "" {
		kind = portal.ThreadKindComment
	}
	return createThreadPayload{
		Kind:               kind,
		TargetType:         targetType,
		AssetID:            input.AssetID,
		CollectionID:       input.CollectionID,
		PromptID:           input.PromptID,
		KnowledgePageID:    input.KnowledgePageID,
		Anchor:             anchor,
		TargetVersion:      input.TargetVersion,
		Title:              input.Title,
		RequiresResolution: input.RequiresResolution != nil && *input.RequiresResolution,
		Body:               input.Body,
		Visibility:         input.Visibility,
	}, ""
}

// createTarget resolves the target a create names.
func createTarget(input manageFeedbackInput) (string, bool) {
	ids := map[string]string{
		threadTargetAsset:         input.AssetID,
		threadTargetCollection:    input.CollectionID,
		threadTargetPrompt:        input.PromptID,
		threadTargetKnowledgePage: input.KnowledgePageID,
	}
	named := ""
	for kind, id := range ids {
		if id == "" {
			continue
		}
		if named != "" {
			return "", false
		}
		named = kind
	}
	switch {
	case input.TargetType == threadTargetStandalone:
		return threadTargetStandalone, named == ""
	case named == "":
		return "", false
	case input.TargetType != "" && input.TargetType != named:
		return "", false
	}
	return named, true
}

// anchorJSON reads the anchor a create carries: a JSON object, or a string
// holding one, as the schema admits both.
func anchorJSON(anchor any) (json.RawMessage, error) {
	switch a := anchor.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(a) == "" {
			return nil, nil
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(a), &obj); err != nil {
			return nil, fmt.Errorf("anchor must be a JSON object, or a string holding one: %w", err)
		}
		return json.RawMessage(a), nil
	case map[string]any:
		raw, err := json.Marshal(a)
		if err != nil {
			return nil, fmt.Errorf("encoding the anchor: %w", err)
		}
		return raw, nil
	}
	return nil, fmt.Errorf("anchor must be a JSON object, or a string holding one, not %T", anchor)
}

// servePortal serves one request to the platform's own HTTP API inside this
// process, as caller, and returns the status and body it was answered with.
func (t *Toolkit) servePortal(ctx context.Context, caller mcpcontext.Caller, method, path string, body []byte) (status int, answer []byte, err error) {
	var reader io.Reader = http.NoBody
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(selfcall.With(ctx, caller), method, selfOrigin+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("building the request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := handlertransport.New(&t.self).RoundTrip(req)
	if err != nil {
		return 0, nil, fmt.Errorf("serving the request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxPortalAnswer))
	if err != nil {
		return 0, nil, fmt.Errorf("reading the answer: %w", err)
	}
	return resp.StatusCode, out, nil
}

// maxPortalAnswer bounds what is read of the portal's answer: a thread, or
// a problem document.
const maxPortalAnswer = 1 << 20

// maxRefusalQuote bounds how much of an answer that is not a problem document
// a refusal quotes.
const maxRefusalQuote = 300

// portalRefusal is the error a tool reports for a portal answer other than
// the one it wanted: the portal's own detail and its status.
func portalRefusal(what string, status int, body []byte) string {
	var problem struct {
		Detail string `json:"detail"`
		Title  string `json:"title"`
	}
	reason := strings.TrimSpace(string(body))
	if len(reason) > maxRefusalQuote {
		reason = reason[:maxRefusalQuote] + "..."
	}
	if json.Unmarshal(body, &problem) == nil && (problem.Detail != "" || problem.Title != "") {
		reason = problem.Detail
		if reason == "" {
			reason = problem.Title
		}
	}
	return fmt.Sprintf("the portal refused to %s: %s (HTTP %d)", what, reason, status)
}

// portalReaches reports whether the portal lets caller read the thread: its
// own view check, run inside this process. It is how the author of a thread
// the tool's owner-or-editor rule does not reach is let read and reply to it,
// on exactly the terms the portal sets (#2371).
func (t *Toolkit) portalReaches(ctx context.Context, threadID string) bool {
	caller, ok := mcpcontext.GetCaller(ctx)
	if !ok || caller.UserID == "" || threadID == "" {
		return false
	}
	status, _, err := t.servePortal(ctx, caller, http.MethodGet, threadsRoute+"/"+url.PathEscape(threadID), nil)
	return err == nil && status == http.StatusOK
}
