// Package apprtool is the manage_approval MCP tool (#2437, #2494): submitting
// a request, listing requests and deciding one are commands of one tool, over
// the service the portal's Approvals page uses.
package apprtool

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ToolName is the tool's name.
const ToolName = "manage_approval"

// The commands.
const (
	CmdSubmit = "submit"
	CmdList   = "list"
	CmdGet    = "get"
	CmdDecide = "decide"
)

// Commands is every command, in the order the schema lists them.
var Commands = []string{CmdSubmit, CmdList, CmdGet, CmdDecide}

// Reads is the commands that change nothing.
var Reads = []string{CmdList, CmdGet}

// Input is the tool's arguments.
type Input struct {
	Command  string `json:"command"`
	Script   string `json:"script,omitempty"`
	Version  int    `json:"version,omitempty"`
	Purpose  string `json:"purpose,omitempty"`
	Reason   string `json:"reason,omitempty"`
	ID       string `json:"id,omitempty"`
	Decision string `json:"decision,omitempty"`
	Comment  string `json:"comment,omitempty"`
	Status   string `json:"status,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// Tool is the registered tool.
type Tool struct {
	Service *approval.Service
	// Persona resolves the persona a caller's roles resolve to, recorded on
	// a request; nil records none.
	Persona func(roles []string) string
}

// Register adds manage_approval to the server.
func (t *Tool) Register(server *mcp.Server) {
	if t == nil || t.Service == nil || server == nil {
		return
	}
	toolkit.AddTool(server, &mcp.Tool{
		Name:        ToolName,
		Title:       "Manage Approvals",
		Description: description,
		InputSchema: schema(),
		Annotations: toolkit.WriteAnnotations(false),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
		return t.Handle(ctx, in)
	})
}

const description = "Approvals: a request for an administrator's decision. A form (a managed script defining " +
	"form_spec and form_submit) is served to callers with no session, auth_mode public or bot_check, only at a " +
	"version an administrator approved: submit asks for one version (script, version; purpose form_public, the " +
	"default) at any time, well before going public, and each administrator is notified once. The request fixes " +
	"the version by the hash of its source and says what changed since the version approved before. An " +
	"administrator reads it with get, previews the version's form with manage_script form_preview " +
	"preview_version=<n>, and decides it (decide id, decision approve or deny, comment); the requester is " +
	"notified. Once approved, the form's owner sets auth_mode public or bot_check on its endpoint handler with manage_script handler_set. " +
	"A version saved after the approval is served to signed-in people and not publicly until it is approved too. " +
	"Administrators decide every request, their own included (recorded as self-approved); nobody else decides " +
	"one. list answers your own requests, or for an administrator every request (status filters it)."

// JSON Schema words the input schema is written in.
const (
	keyType   = "type"
	keyDesc   = "description"
	keyEnum   = "enum"
	typString = "string"
)

func schema() map[string]any {
	str := func(desc string) map[string]any { return map[string]any{keyType: typString, keyDesc: desc} }
	num := func(desc string) map[string]any { return map[string]any{keyType: "integer", keyDesc: desc} }
	return map[string]any{
		keyType:    "object",
		"required": []string{"command"},
		"properties": map[string]any{
			"command": map[string]any{keyType: typString, keyEnum: Commands, keyDesc: "The operation."},
			"script":  str("submit: the script, by id or by the name of one of your scripts."),
			"version": num("submit: the version to approve; the latest when omitted."),
			"purpose": map[string]any{
				keyType: typString, keyEnum: []string{approval.PurposeFormPublic},
				keyDesc: "submit: what the approval allows; form_public serves the form to callers with no session.",
			},
			"reason": str("submit: why, for the administrator deciding it."),
			"id":     str("get, decide: the request's id."),
			"decision": map[string]any{
				keyType: typString, keyEnum: []string{approval.DecisionApprove, approval.DecisionDeny},
				keyDesc: "decide: approve or deny.",
			},
			"comment": str("decide: a note the requester reads."),
			"status": map[string]any{
				keyType: typString,
				keyEnum: []string{approval.StatusPending, approval.StatusApproved, approval.StatusDenied},
				keyDesc: "list: only requests with this status.",
			},
			"limit": num("list: how many requests, newest first (default 100)."),
		},
	}
}

// actorOf is who is calling.
func (t *Tool) actorOf(ctx context.Context) approval.Actor {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return approval.Actor{}
	}
	a := approval.Actor{Email: pc.UserEmail, Admin: pc.IsAdmin}
	if t.Persona != nil {
		a.Persona = t.Persona(pc.Roles)
	}
	return a
}

// insideRun reports whether a managed-script run made the call.
func insideRun(ctx context.Context) bool {
	pc := middleware.GetPlatformContext(ctx)
	return pc != nil && pc.Source == middleware.SourceScript
}

// Handle runs one command.
func (t *Tool) Handle(ctx context.Context, in Input) (*mcp.CallToolResult, any, error) {
	if !slices.Contains(Reads, in.Command) && insideRun(ctx) {
		return errorResult(ToolName + " " + in.Command + " cannot be called from inside a script run: a run never " +
			"asks for or decides an approval. Do it from your own session.")
	}
	v, err := t.run(ctx, t.actorOf(ctx), in)
	if err != nil {
		return errorResult(message(err))
	}
	return jsonResult(v)
}

func (t *Tool) run(ctx context.Context, actor approval.Actor, in Input) (any, error) {
	s := t.Service
	switch in.Command {
	case CmdSubmit:
		r, err := s.Submit(ctx, actor, approval.Submit{Script: in.Script, Version: in.Version, Purpose: in.Purpose, Reason: in.Reason})
		if err != nil {
			return nil, err //nolint:wrapcheck // the caller's message
		}
		return map[string]any{"request": r, "message": fmt.Sprintf(
			"Requested approval of version %d of %s. An administrator decides it; you are notified then.", r.Version, r.ScriptName)}, nil
	case CmdList:
		rs, err := s.List(ctx, actor, approval.Filter{Status: in.Status, Limit: in.Limit})
		if err != nil {
			return nil, err //nolint:wrapcheck // the caller's message
		}
		return listed{Requests: rs, Count: len(rs)}, nil
	case CmdGet:
		return s.Get(ctx, actor, in.ID) //nolint:wrapcheck // the caller's message
	case CmdDecide:
		return s.Decide(ctx, actor, in.ID, in.Decision, in.Comment) //nolint:wrapcheck // the caller's message
	}
	return nil, &approval.RefusedError{Msg: fmt.Sprintf("unknown command %q: one of %v", in.Command, Commands)}
}

// listed is what list answers.
type listed struct {
	Requests []approval.Request `json:"requests"`
	Count    int                `json:"count"`
}

// message is the caller-facing text of an error.
func message(err error) string {
	switch {
	case errors.Is(err, approval.ErrNotFound):
		return "no such request"
	case errors.Is(err, approval.ErrDecided):
		return "the request was already decided"
	}
	return err.Error()
}

func errorResult(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := wirejson.Marshal(v)
	if err != nil {
		return errorResult("failed to encode the result: " + err.Error())
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
}
