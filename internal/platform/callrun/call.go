package callrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/calltransform"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// apiKind is the toolkit kind whose calls name a catalog operation.
const apiKind = "api"

// BuildCall is what a handler is matched against and given for a tool call:
// its tool, connection and operation, its arguments as the caller sent them
// (a stored secret still a placeholder), its body read as the value it holds
// whether sent as an object or a string of JSON, and who calls.
func (e *Evaluator) BuildCall(ctx context.Context, pc *middleware.PlatformContext, tool string, args map[string]any) *callhandler.Call {
	c := &callhandler.Call{
		Tool: tool, Connection: pc.Connection, Operation: tool, Arguments: args,
		PathParams: map[string]string{}, Query: map[string]any{},
		User: pc.UserEmail, Persona: pc.PersonaName, Time: e.clock().UTC(),
	}
	c.Body = bodyOf(args["body"])
	if q, ok := args["query_params"].(map[string]any); ok {
		c.Query = q
	}
	if sql, ok := args["sql"].(string); ok {
		c.SQL = sql
	}
	e.applyShape(ctx, c, pc, args)
	return c
}

// applyShape reads an api call's operation, method, path and path
// parameters onto c.
func (e *Evaluator) applyShape(ctx context.Context, c *callhandler.Call, pc *middleware.PlatformContext, args map[string]any) {
	if pc.ToolkitKind != apiKind || e.Shapes == nil {
		return
	}
	s := e.Shapes(apiKind)
	if s == nil {
		return
	}
	shape := s.CallShape(ctx, pc.Connection, args)
	c.Method, c.Path = shape.Method, shape.Path
	if shape.Operation != "" {
		c.Operation = shape.Operation
	}
	if len(shape.Params) > 0 {
		c.PathParams = shape.Params
	}
}

// bodyOf is a body as the value it holds: a string of JSON is decoded, so the
// two forms the body admits are matched and counted alike.
func bodyOf(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	var decoded any
	if json.Unmarshal([]byte(s), &decoded) == nil {
		return decoded
	}
	return s
}

// Test is the test panel's answer for a call (#2436): every handler on the
// connection that would apply, in the order they run, what each would do,
// and the outcome the real call would get. The upstream is never called and
// no count or allowance is spent; a handler's own reads run.
func (e *Evaluator) Test(ctx context.Context, req callhandler.TestRequest) (map[string]any, error) {
	if req.Connection == "" {
		return nil, errors.New("name the connection the call would be made on")
	}
	if req.Tool == "" {
		return nil, errors.New("name the tool the call would be made with")
	}
	at := e.clock().UTC()
	if req.At != "" {
		t, err := time.Parse(time.RFC3339, req.At)
		if err != nil {
			return nil, fmt.Errorf("at %q is not an RFC 3339 time", req.At)
		}
		at = t.UTC()
	}
	args := maps.Clone(req.Arguments)
	if args == nil {
		args = map[string]any{}
	}
	args["connection"] = req.Connection
	pc := &middleware.PlatformContext{Connection: req.Connection, UserEmail: strings.ToLower(req.User), PersonaName: req.Persona, ToolkitKind: kindOf(req.Tool)}
	c := e.BuildCall(ctx, pc, req.Tool, args)
	c.Time = at
	v := e.Decide(ctx, c, "", true)
	runs := runsOf(v.Runs)
	for _, r := range runs {
		r["says"] = saying(v, r)
	}
	out := map[string]any{"outcome": v.Outcome, "handlers": runs, "not_applying": e.notApplying(ctx, c)}
	switch v.Outcome {
	case VerdictDenied:
		out["reason"], out["denied_by"] = v.Reason, v.Handler
	case VerdictHeld:
		out["approvers"], out["message"] = v.Approvers, heldMessage(c, v)
	}
	if v.Outcome == VerdictRuns {
		e.testAfter(ctx, c, req.Result, out)
	}
	return out, nil
}

// saying is what one policy said of a tested call: its reason for a refusal,
// its summary for a hold.
func saying(v Verdict, run map[string]any) string {
	if v.Outcome == VerdictDenied && run["script"] == v.Handler {
		return v.Reason
	}
	for _, ask := range v.Asks {
		if ask.ID == run["id"] {
			return ask.Summary
		}
	}
	return ""
}

// notApplying are the handlers on the call's connection that do not match
// it, each by its script, kind and operations, so the test panel says why a
// handler the administrator expected did not run.
func (e *Evaluator) notApplying(ctx context.Context, c *callhandler.Call) []map[string]any {
	out := []map[string]any{}
	for _, h := range e.Index.On(c.Connection) {
		if h.Matches(c) {
			continue
		}
		name := e.scriptName(ctx, h)
		out = append(out, map[string]any{"id": h.ID, "kind": h.Kind, "script": name, "operations": h.Operations})
	}
	return out
}

// testAfter adds what the transforms and watches would do to a call that
// runs.
func (e *Evaluator) testAfter(ctx context.Context, c *callhandler.Call, result any, out map[string]any) {
	transforms := e.Index.Match(c, callhandler.KindTransform)
	if len(transforms) > 0 {
		out["transforms"] = len(transforms)
		if m, ok := result.(map[string]any); ok {
			got, runs, err := e.Transform(ctx, c, transforms, m, true)
			prior, _ := out["handlers"].([]map[string]any)
			out["handlers"] = append(prior, runsOf(runs)...)
			if err != nil {
				out["transform_error"] = err.Error()
			} else {
				out["result"] = got
			}
		}
	}
	if watches := e.Index.Match(c, callhandler.KindWatch); len(watches) > 0 {
		out["watches"] = len(watches)
	}
}

// kindOf is the toolkit kind a tool belongs to, as far as the test panel
// needs it: the api tools name an operation.
func kindOf(tool string) string {
	if strings.HasPrefix(tool, "api_") {
		return apiKind
	}
	return ""
}

func runsOf(runs []audit.HandlerRun) []map[string]any {
	out := make([]map[string]any, 0, len(runs))
	for _, r := range runs {
		out = append(out, map[string]any{"id": r.ID, "kind": r.Kind, "script": r.Script, "outcome": r.Outcome, "ms": r.MS})
	}
	return out
}

// HeldRequest is the request manage_approval submit files for a call the
// person was told is held: the policies are asked again, without counting,
// so the request names what held it and the platform's reading of the call.
// A call no policy holds is refused: it runs as it is, or no approval would
// change that it is refused.
func (e *Evaluator) HeldRequest(ctx context.Context, held approval.HeldCall) (approval.CallRequest, error) {
	tool, args := held.Tool, held.Arguments
	connection, _ := args["connection"].(string)
	if connection == "" || tool == "" {
		return approval.CallRequest{}, errors.New("name the tool and give the call's arguments, its connection among them, as the call was made")
	}
	pc := &middleware.PlatformContext{Connection: connection, UserEmail: held.User, PersonaName: held.Persona, ToolkitKind: kindOf(tool)}
	c := e.BuildCall(ctx, pc, tool, maps.Clone(args))
	v := e.Decide(ctx, c, "", true)
	switch v.Outcome {
	case VerdictRuns:
		return approval.CallRequest{}, fmt.Errorf("this call is not held: no policy on %s asks for approval, so make the call", connection)
	case VerdictDenied:
		return approval.CallRequest{}, fmt.Errorf("this call is refused by %s (%s); an approval does not change a refusal", v.Handler, v.Reason)
	}
	return RequestFor(c, v, held.Calls, held.Reason), nil
}

// Statement puts a script's application database statement through the
// handlers (scriptsession.AppDBHandlers): the policies on its connection
// decide it, without anyone to ask, so a hold refuses it naming what holds
// it; a transform covering the run refuses a statement using a column it
// protects and changes the rows it returns. A statement no handler covers
// has no transformer.
func (e *Evaluator) Statement(ctx context.Context, st callhandler.Statement) (calltransform.Transformer, error) {
	if e == nil || e.Index == nil || !e.Index.Any(st.Connection) {
		return nil, nil //nolint:nilnil // no handler on the connection: no transformer, and nothing refused
	}
	ctx, queued := withNotices(ctx)
	defer e.flush(ctx, queued)
	c := &callhandler.Call{
		Tool: st.Tool, Connection: st.Connection, Operation: st.Tool, SQL: st.SQL, User: st.User, Persona: st.Persona,
		Arguments: map[string]any{"connection": st.Connection, "sql": st.SQL}, PathParams: map[string]string{},
		Query: map[string]any{}, Time: e.clock().UTC(),
	}
	v := e.Decide(ctx, c, uuid.NewString(), false)
	switch v.Outcome {
	case VerdictDenied:
		msg := fmt.Sprintf("POLICY_DENIED: %s is refused by %s: %s", describeCall(c), v.Handler, v.Reason)
		return nil, errors.New(msg)
	case VerdictHeld:
		msg := fmt.Sprintf("APPROVAL_REQUIRED: %s; a script's statement is not asked about, so it is refused", heldMessage(c, v))
		return nil, errors.New(msg)
	}
	transforms := e.Index.Match(c, callhandler.KindTransform)
	if len(transforms) == 0 {
		return nil, nil //nolint:nilnil // no transform covers the statement: no transformer, and nothing refused
	}
	if res := e.protect(ctx, c, transforms); res != nil {
		return nil, errors.New(firstText(res))
	}
	return &transformer{e: e, call: c, transforms: transforms, record: func([]audit.HandlerRun) {}}, nil
}
