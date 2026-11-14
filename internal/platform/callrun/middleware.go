package callrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/calltransform"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// The codes and categories a call a handler stopped answers with.
const (
	CodePolicyDenied     = "policy_denied"
	CodeApprovalRequired = "approval_required"
	CodeApprovalFiled    = "approval_filed"
	CodeProtectedColumn  = "protected_column"
	CodeTransformFailed  = "transform_failed"
	CategoryPolicyDenied = "policy_denied"
	CategoryApproval     = "approval_required"
	CategoryProtected    = "protected_column"
	CategoryTransform    = "transform_failed"
)

// inputApproval is the input request id a held call asks with.
const inputApproval = "approval_request"

// multiRoundTripVersion is the first MCP revision on which a server asks
// through an input request on the result rather than in band (SEP-2322).
const multiRoundTripVersion = "2026-07-28"

const methodToolsCall = "tools/call"

// exportTools are the tools whose rows or body reach their caller in a file:
// their transforms run on each page as it is written, not on the tool's
// result, which names the file.
var exportTools = []string{"trino_export", "api_export", "graphql_export"}

// Middleware is the tools/call layer the handlers run in: inside
// authorization and the gates, inner to the result layers, so a policy only
// ever sees a call the caller's persona may make, and the result budget, call
// reference and enrichment see what the caller gets. A call no handler applies
// to pays one lookup in memory.
func (e *Evaluator) Middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return e.serve(ctx, method, req, next)
		}
	}
}

// LateMiddleware is Middleware for an evaluator assembled after the chain is
// registered: get is read on each call, and a call before it answers runs as
// if no handler existed.
func LateMiddleware(get func() *Evaluator) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return get().serve(ctx, method, req, next)
		}
	}
}

// RuntimeMiddleware is LateMiddleware over a runtime read on each call.
func RuntimeMiddleware(get func() *Runtime) mcp.Middleware {
	return LateMiddleware(func() *Evaluator {
		if rt := get(); rt != nil {
			return rt.Evaluator
		}
		return nil
	})
}

// serve is one request through the handlers' layer.
func (e *Evaluator) serve(ctx context.Context, method string, req mcp.Request, next mcp.MethodHandler) (mcp.Result, error) {
	ctr, ok := req.(*mcp.CallToolRequest)
	if !ok || method != methodToolsCall || ctr.Params == nil || e == nil || e.Index == nil || callhandler.Within(ctx) {
		return next(ctx, method, req)
	}
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.Connection == "" || !e.Index.Any(pc.Connection) {
		return next(ctx, method, req)
	}
	return e.handle(ctx, method, ctr, pc, next)
}

// handle is one call some handler on its connection may apply to.
func (e *Evaluator) handle(ctx context.Context, method string, req *mcp.CallToolRequest, pc *middleware.PlatformContext, next mcp.MethodHandler) (mcp.Result, error) {
	ctx, queued := withNotices(ctx)
	defer e.flush(ctx, queued)
	c := e.BuildCall(ctx, pc, req.Params.Name, argsOf(req))
	if answers := req.Params.InputResponses; len(answers) > 0 {
		if r, ok := answers[inputApproval].(*mcp.ElicitResult); ok {
			return e.answered(ctx, pc, c, r), nil
		}
	}
	callID := pc.EventID
	if callID == "" {
		callID = uuid.NewString()
	}
	v := e.Decide(ctx, c, callID, false)
	pc.Handlers = append(pc.Handlers, v.Runs...)
	switch v.Outcome {
	case VerdictDenied:
		return denied(c, v), nil
	case VerdictHeld:
		return e.held(ctx, req, pc, c, v), nil
	}
	transforms := e.Index.Match(c, callhandler.KindTransform)
	if len(transforms) > 0 {
		if res := e.protect(ctx, c, transforms); res != nil {
			return res, nil
		}
		ctx = calltransform.With(ctx, &transformer{e: e, call: c, transforms: transforms, record: func(runs []audit.HandlerRun) {
			pc.Handlers = append(pc.Handlers, runs...)
		}})
	}
	res, err := next(ctx, method, req)
	if err != nil {
		return res, err
	}
	out, _ := res.(*mcp.CallToolResult)
	if out == nil {
		return res, nil
	}
	return e.after(ctx, pc, c, transforms, out), nil
}

// after is what the handlers do with a call's result: the transforms change
// it, unless the tool wrote it to a file, and a call that succeeded queues
// its watches.
func (e *Evaluator) after(ctx context.Context, pc *middleware.PlatformContext, c *callhandler.Call, transforms []callhandler.Handler, out *mcp.CallToolResult) *mcp.CallToolResult {
	if len(transforms) > 0 && !out.IsError && !slices.Contains(exportTools, c.Tool) {
		if failed := e.transformResult(ctx, pc, c, transforms, out); failed != nil {
			return failed
		}
	}
	if succeeded(out) {
		for _, id := range e.Watch(ctx, c) {
			pc.Handlers = append(pc.Handlers, audit.HandlerRun{ID: id, Kind: callhandler.KindWatch, Outcome: OutcomeQueued})
		}
	}
	return out
}

// transformResult runs the transforms over a tool's result in place, or
// returns the refusal a failed one is.
func (e *Evaluator) transformResult(ctx context.Context, pc *middleware.PlatformContext, c *callhandler.Call, transforms []callhandler.Handler, res *mcp.CallToolResult) *mcp.CallToolResult {
	got, runs, err := e.Transform(ctx, c, transforms, resultMap(res), false)
	pc.Handlers = append(pc.Handlers, runs...)
	if err != nil {
		return middleware.BuildErrorResult(middleware.NewToolError(CodeTransformFailed, CategoryTransform,
			"TRANSFORM_FAILED: "+err.Error(), "An administrator can read the failure on the handler's page."))
	}
	replaceResult(res, got)
	return nil
}

// protect refuses a covered caller's SQL that uses a protected column other
// than by selecting it as it is.
func (e *Evaluator) protect(ctx context.Context, c *callhandler.Call, transforms []callhandler.Handler) *mcp.CallToolResult {
	if c.SQL == "" {
		return nil
	}
	// A column is named with its table: catalog.schema.table.column on
	// Trino, schema.table.column on an application database. A JSON path
	// (body.card.number) read as a column names no table and matches none.
	protected := map[string]string{}
	for _, h := range transforms {
		name := e.scriptName(ctx, h)
		for _, col := range h.Protected {
			if strings.Count(col, ".") >= 2 {
				protected[strings.ToLower(col)] = name
			}
		}
	}
	if len(protected) == 0 {
		return nil
	}
	check := e.Protect
	if check == nil {
		check = func(context.Context, string, string, map[string]string) error {
			return errors.New("this deployment cannot read SQL against protected columns")
		}
	}
	if err := check(ctx, c.Connection, c.SQL, protected); err != nil {
		return middleware.BuildErrorResult(middleware.NewToolError(CodeProtectedColumn, CategoryProtected,
			"PROTECTED_COLUMN: "+err.Error(),
			"Select the protected column as it is, or leave it out; a transform covering you changes what it returns."))
	}
	return nil
}

// succeeded reports whether a call did what it was asked: no error, and an
// api call's upstream answered 2xx.
func succeeded(res *mcp.CallToolResult) bool {
	if res.IsError {
		return false
	}
	m := resultMap(res)
	if status, ok := m["status"].(float64); ok {
		return status >= http.StatusOK && status < http.StatusMultipleChoices
	}
	return true
}

// denied is the refusal a denying policy's call answers with.
func denied(c *callhandler.Call, v Verdict) *mcp.CallToolResult {
	msg := fmt.Sprintf("POLICY_DENIED: %s is refused by %s: %s", describeCall(c), v.Handler, v.Reason)
	return middleware.BuildErrorResult(middleware.NewToolError(CodePolicyDenied, CategoryPolicyDenied, msg,
		"A policy refused this call; asking again will not change its decision."))
}

// heldMessage is the sentence a held call's person is told: what holds it,
// what each policy said, and who can approve it.
func heldMessage(c *callhandler.Call, v Verdict) string {
	why := make([]string, 0, len(v.Asks))
	for _, a := range v.Asks {
		s := a.Script
		if summary := strings.TrimRight(strings.TrimSpace(a.Summary), "."); summary != "" {
			s += ": " + summary
		}
		why = append(why, s)
	}
	return fmt.Sprintf("%s is held by %s. %s can approve it.", describeCall(c), strings.Join(why, "; "), approverText(v.Approvers))
}

// anAdministrator is how a held call names the administrators who can
// approve it.
const anAdministrator = "an administrator"

func approverText(approvers []string) string {
	var out []string
	for _, a := range approvers {
		if a == approval.Administrators {
			out = append(out, anAdministrator)
		} else {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return anAdministrator
	}
	if !slices.Contains(out, anAdministrator) {
		out = append(out, anAdministrator)
	}
	return strings.Join(out, " or ")
}

// describeCall names a call as a person reads it: its connection and
// operation.
func describeCall(c *callhandler.Call) string {
	op := c.Operation
	if op == "" {
		op = c.Tool
	}
	return c.Connection + " " + op
}

// held answers a held call: an input request a 2026-07-28 client asks the
// person with, the same question asked in band of an older client that can be
// asked, and otherwise approval_required.
func (e *Evaluator) held(ctx context.Context, req *mcp.CallToolRequest, pc *middleware.PlatformContext, c *callhandler.Call, v Verdict) *mcp.CallToolResult {
	msg := heldMessage(c, v)
	caps := req.ClientCapabilities()
	if caps != nil && caps.Elicitation != nil {
		if req.ProtocolVersion() >= multiRoundTripVersion {
			return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{inputApproval: approvalPrompt(msg)}}
		}
		if req.Session != nil {
			if r, err := req.Session.Elicit(ctx, approvalPrompt(msg)); err == nil && r != nil {
				return e.fileAnswer(ctx, pc, c, v, r)
			}
		}
	}
	return middleware.BuildErrorResult(middleware.NewToolError(CodeApprovalRequired, CategoryApproval, "APPROVAL_REQUIRED: "+msg,
		"Ask the person whether to request approval. If they agree, call manage_approval with command submit, kind call, "+
			"this tool and these arguments, and their reason."))
}

// schemaType is the JSON Schema keyword naming a value's type.
const schemaType = "type"

// approvalPrompt is the question a held call asks: how many calls to ask for,
// and why.
func approvalPrompt(msg string) *mcp.ElicitParams {
	return &mcp.ElicitParams{
		Message: msg + " Ask for approval?",
		RequestedSchema: map[string]any{
			schemaType: "object",
			"properties": map[string]any{
				"calls":  map[string]any{schemaType: "integer", "minimum": 1, "default": 1, "title": "Calls to ask for"},
				"reason": map[string]any{schemaType: "string", "title": "Reason"},
			},
		},
	}
}

// answered is a retry carrying the answer to a held call's question: the
// policies are asked again, without counting, for what the request names.
func (e *Evaluator) answered(ctx context.Context, pc *middleware.PlatformContext, c *callhandler.Call, r *mcp.ElicitResult) *mcp.CallToolResult {
	v := e.Decide(ctx, c, "", true)
	return e.fileAnswer(ctx, pc, c, v, r)
}

// fileAnswer is what an answer to a held call does: accepting files the
// request and never runs the call; declining files nothing; any other answer
// is no answer.
func (e *Evaluator) fileAnswer(ctx context.Context, pc *middleware.PlatformContext, c *callhandler.Call, v Verdict, r *mcp.ElicitResult) *mcp.CallToolResult {
	switch r.Action {
	case "accept":
	case "decline":
		return refusal("Not run: you declined to ask for approval. Nothing was filed.", middleware.ErrCategoryDeclined)
	default:
		return refusal("Not run: the approval prompt was not answered. Nothing was filed.", audit.CategoryPromptUnanswered)
	}
	calls := 1
	if n, ok := r.Content["calls"].(float64); ok && n >= 1 {
		calls = int(n)
	}
	reason, _ := r.Content["reason"].(string)
	filed, err := e.Approvals.SubmitCall(ctx, approval.Actor{Email: pc.UserEmail, Persona: pc.PersonaName, Admin: pc.IsAdmin}, RequestFor(c, v, calls, reason))
	if err != nil {
		return refusal("Not run: the approval request could not be filed: "+err.Error(), CategoryApproval)
	}
	msg := fmt.Sprintf("Not run. Approval request %s is filed; %s decides it and you are told. Make the same call again once it is approved.",
		filed.ID, approverText(v.Approvers))
	return middleware.BuildErrorResult(middleware.NewToolError(CodeApprovalFiled, CategoryApproval, msg, ""))
}

// RequestFor is the request a held call files: the call as sent, the
// platform's reading of it, and what its policies asked.
func RequestFor(c *callhandler.Call, v Verdict, calls int, reason string) approval.CallRequest {
	return approval.CallRequest{
		Tool: c.Tool, Connection: c.Connection, Operation: c.Operation, Arguments: c.Arguments,
		Rendering: Rendering(c), Handlers: v.Asks, Approvers: v.Approvers, Calls: calls, Reason: reason,
	}
}

func refusal(msg, category string) *mcp.CallToolResult {
	return middleware.BuildErrorResult(middleware.NewToolError(category, category, msg, ""))
}

// Rendering is the platform's own reading of a call, which an approver reads:
// what it addresses, how many items it carries, and its body as sent.
func Rendering(c *callhandler.Call) string {
	var b strings.Builder
	switch {
	case c.Method != "":
		fmt.Fprintf(&b, "%s %s", c.Method, c.Path)
	case c.SQL != "":
		fmt.Fprintf(&b, "%s: %s", c.Tool, c.SQL)
	default:
		_, _ = b.WriteString(c.Tool)
	}
	if c.Operation != "" && c.Method != "" {
		fmt.Fprintf(&b, " (%s)", c.Operation)
	}
	if c.Body != nil {
		raw, _ := json.Marshal(c.Body)
		fmt.Fprintf(&b, "\nbody %s", raw)
	}
	return b.String()
}

// argsOf is a tool call's arguments, the session handle the platform adds
// left out.
func argsOf(req *mcp.CallToolRequest) map[string]any {
	args := map[string]any{}
	if len(req.Params.Arguments) > 0 {
		_ = json.Unmarshal(req.Params.Arguments, &args)
	}
	delete(args, "session_id")
	return args
}
