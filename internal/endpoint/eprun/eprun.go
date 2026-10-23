// Package eprun runs a bound script inline on each request to its endpoint
// (#2172): handle(req) is called on the replica that received the request,
// in an in-memory MCP session presenting the roles its version's author held
// at the save, as a scheduled run presents them. Every platform call the
// handler makes is authorized, rate limited and audited as a script's. A
// request writes one audit row and its metrics, a failed one is kept for the
// script's owner to read, and no script_runs row is written.
//
// The compiled module is kept per script version on each replica. Before a
// request runs, one read of when its binding and its script were last written
// (epbinding.Stamp) says whether what the replica holds is current, so the
// first request after a save, on any replica, runs the new version.
package eprun

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/mailbarrier"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/internal/scripttoken"
	"github.com/txn2/mcp-data-platform/internal/toolwrite"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// SourceEndpoint is the audit source of the one row a request writes. The
// tool-call rows its handler's calls produce are the script source's, which
// is what every rule a script's calls answer to keys on.
const SourceEndpoint = "endpoint"

// maxCached bounds the endpoints a replica keeps compiled; past it the cache
// is emptied and refilled by the requests that follow.
const maxCached = 1024

// MaxConcurrent is how many requests a replica runs handlers for at once,
// across every endpoint. A request past it is answered 503 at once rather
// than held: a burst inside every endpoint's rate limit could otherwise hold
// as many executions as it sends, each with its own memory budget.
const MaxConcurrent = 32

// maxErrorLen bounds the error a failed request is kept with.
const maxErrorLen = 8 << 10

// clientLabel names the in-memory session a request's calls go through.
const clientLabel = "script-endpoint"

// Scripts reads the script a binding binds and its saved versions.
type Scripts interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
	GetVersion(ctx context.Context, scriptID string, version int) (*script.Version, error)
}

// Runner runs bound scripts.
type Runner struct {
	Bindings epbinding.Store
	Scripts  Scripts
	// Server is the assembled MCP server a request's session opens against.
	Server  *mcp.Server
	Audit   middleware.AuditLogger
	Metrics *observability.Metrics
	// Subjects resolves the subject the version's author authenticates as,
	// for where a request's managed resources land; nil leaves it empty.
	Subjects interface {
		ForRun(ctx context.Context, address string) string
	}
	// Limits are a platform run's ceilings; a binding's timeout and memory
	// budget replace those two.
	Limits       scriptrun.PlatformLimits
	Destinations []script.Destination
	// Libraries is where the libraries a version loads are read from, given
	// the version; nil refuses every load.
	Libraries func(ctx context.Context, v *script.Version) (scriptlib.Source, error)
	// Tokens answers platform.mint_token and platform.verify_token, bound to
	// the endpoint a request reached (#2175); nil refuses them.
	Tokens *scripttoken.Issuer

	mu    sync.Mutex
	cache map[string]*Target
	// slots holds one token per handler running on this replica.
	slots     chan struct{}
	slotsOnce sync.Once
	// unit is what a binding's timeout_seconds counts; zero is a second.
	unit time.Duration
}

// Target is one endpoint as a replica holds it: the binding, the script and
// version it runs, and the module compiled for that version.
type Target struct {
	Binding *epbinding.Binding
	Script  *script.Script
	Version *script.Version
	program *scriptdialect.Program
	stamp   epbinding.Stamp
}

// Resolve is the endpoint a name serves, current as of this call.
// epbinding.ErrNotFound when no binding has the name.
func (r *Runner) Resolve(ctx context.Context, name string) (*Target, error) {
	stamp, err := r.Bindings.Stamp(ctx, name)
	if err != nil {
		return nil, err //nolint:wrapcheck // ErrNotFound is matched by the caller as it is
	}
	r.mu.Lock()
	held := r.cache[name]
	r.mu.Unlock()
	if held != nil && held.stamp == stamp {
		return held, nil
	}
	t, err := r.load(ctx, name, stamp, held)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.cache == nil || len(r.cache) >= maxCached {
		r.cache = map[string]*Target{}
	}
	r.cache[name] = t
	r.mu.Unlock()
	return t, nil
}

// load reads the endpoint afresh, keeping the compiled module of held when
// the version is the same one.
func (r *Runner) load(ctx context.Context, name string, stamp epbinding.Stamp, held *Target) (*Target, error) {
	b, err := r.Bindings.GetByName(ctx, name)
	if err != nil {
		return nil, err //nolint:wrapcheck // ErrNotFound is matched by the caller as it is
	}
	sc, err := r.Scripts.GetByID(ctx, b.ScriptID)
	if err != nil {
		return nil, fmt.Errorf("reading the endpoint's script: %w", err)
	}
	if sc == nil {
		return nil, epbinding.ErrNotFound
	}
	v, err := r.Scripts.GetVersion(ctx, sc.ID, sc.Version)
	if err != nil {
		return nil, fmt.Errorf("reading the endpoint's script version: %w", err)
	}
	if v == nil {
		return nil, fmt.Errorf("the endpoint's script has no version %d", sc.Version)
	}
	t := &Target{Binding: b, Script: sc, Version: v, program: &scriptdialect.Program{}, stamp: stamp}
	if held != nil && held.Version.ID == v.ID {
		t.program = held.program
	}
	return t, nil
}

// Serve runs t's handler for req and returns its response. It never fails:
// a request the script could not answer is answered with the status that
// says why, and recorded.
func (r *Runner) Serve(ctx context.Context, t *Target, req Request) Response {
	r.slotsOnce.Do(func() { r.slots = make(chan struct{}, MaxConcurrent) })
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		resp := errorResponse(http.StatusServiceUnavailable, "the platform is running as many endpoint requests as it takes; send it again")
		resp.Header.Set("Retry-After", "1")
		r.Metrics.RecordEndpointRequest(ctx, t.Binding.Name, observability.HTTPStatusClass(resp.Status), 0)
		return resp
	}
	started := time.Now()
	var resp Response
	var failure error
	sessionID, err := pkgsession.GenerateScriptSessionID()
	switch {
	case err != nil:
		resp, failure = errorResponse(http.StatusInternalServerError, "the endpoint could not start"), err
	case script.RefuseRun(t.Script) != nil:
		resp, failure = errorResponse(http.StatusServiceUnavailable, "this endpoint is not available"), script.RefuseRun(t.Script)
	default:
		resp, failure = r.run(ctx, t, req, sessionID)
	}
	o := outcome{sessionID: sessionID, status: resp.Status, failure: failure, elapsed: time.Since(started)}
	if failure != nil {
		r.recordFailure(ctx, t, req.Method, o)
	}
	r.recordAudit(ctx, t, req, o)
	r.Metrics.RecordEndpointRequest(ctx, t.Binding.Name, observability.HTTPStatusClass(o.status), o.elapsed)
	return resp
}

// run executes the handler in the request's own session.
func (r *Runner) run(ctx context.Context, t *Target, req Request, sessionID string) (Response, error) {
	caller, cleanup, err := r.connect(ctx, t, sessionID)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "the endpoint could not start"), err
	}
	defer cleanup()
	reqValue, err := requestValue(req)
	if err != nil {
		return errorResponse(http.StatusBadRequest, "the request could not be read"), err
	}
	opts := scriptrun.RunLimits(r.Limits)
	unit := r.unit
	if unit == 0 {
		unit = time.Second
	}
	opts.Timeout = time.Duration(t.Binding.TimeoutSeconds) * unit
	opts.MaxMemoryBytes = t.Binding.MaxMemoryBytes
	opts.Source, opts.Name, opts.RunID = t.Version.Source, t.Script.Name, sessionID
	opts.ClampStatementTimeout = t.Version.PredatesStatementCap
	opts.FireTime = time.Now().UTC()
	opts.Caller, opts.Destinations, opts.Exporter = caller, r.Destinations, refuseExports{}
	opts.Caller, opts.Exporter = mailbarrier.Guard(t.Script.MailActions, toolwrite.Classifier{}, opts.Caller, opts.Exporter)
	opts.Tokens = r.Tokens.Bind(scripttoken.Binding{Endpoint: t.Binding.Name}, t.Version.AuthorRoles)
	if r.Libraries != nil {
		if opts.Libraries, err = r.Libraries(ctx, t.Version); err != nil {
			return errorResponse(http.StatusInternalServerError, "the endpoint could not start"), err
		}
	}
	var returned starlark.Value
	opts.Handler = &scriptdialect.Handler{
		Name: scriptdialect.HandlerName, Args: starlark.Tuple{reqValue}, Program: t.program,
		Returned: func(v starlark.Value) error { returned = v; return nil },
	}
	if _, err := scriptrun.Run(ctx, opts); err != nil {
		return failedRun(err), err
	}
	resp, err := responseFrom(returned)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "the endpoint answered with something that is not a response"), err
	}
	return resp, nil
}

// failedRun is the response to a handler that did not finish: past its time
// limit 504, past its memory budget 503, anything else 500. A public caller
// is told which of those happened and nothing about the script.
func failedRun(err error) Response {
	switch {
	case errors.Is(err, scriptrun.ErrTimeout):
		return errorResponse(http.StatusGatewayTimeout, "the endpoint did not answer in time")
	case errors.Is(err, scriptguard.ErrMemoryBudget):
		return errorResponse(http.StatusServiceUnavailable, "the endpoint ran out of memory")
	}
	return errorResponse(http.StatusInternalServerError, "the endpoint failed")
}

// connect opens the request's in-memory session as the script principal,
// presenting the version author's roles, exactly as a scheduled run's.
func (r *Runner) connect(ctx context.Context, t *Target, sessionID string) (scriptrun.Caller, func(), error) {
	subject := ""
	if r.Subjects != nil {
		subject = r.Subjects.ForRun(ctx, t.Version.Author)
	}
	serverCtx := middleware.WithSource(ctx, middleware.SourceScript)
	serverCtx = pkgsession.WithAwareSessionID(serverCtx, sessionID)
	serverCtx = producedby.With(serverCtx, producedby.Producer{Kind: producedby.KindScript, ID: t.Script.ID, Label: t.Script.Name})
	serverCtx = middleware.WithPreAuthenticatedUser(serverCtx, &middleware.UserInfo{
		UserID: t.Script.Principal(), Email: t.Script.OwnerEmail, Roles: t.Version.AuthorRoles,
		AuthType: middleware.AuthTypeScript, OnBehalfOf: t.Version.Author, OnBehalfOfSub: subject,
	})
	caller, cleanup, err := scriptsession.Connect(serverCtx, r.Server, clientLabel)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the request's session: %w", err)
	}
	return caller, cleanup, nil
}

// outcome is how one request ended.
type outcome struct {
	sessionID string
	status    int
	failure   error
	elapsed   time.Duration
}

func (r *Runner) recordFailure(ctx context.Context, t *Target, method string, o outcome) {
	msg := o.failure.Error()
	if len(msg) > maxErrorLen {
		msg = msg[:maxErrorLen]
	}
	if err := r.Bindings.RecordFailure(ctx, epbinding.Failure{
		EndpointID: t.Binding.ID, Version: t.Version.Version, Method: method, Status: o.status,
		Error: msg, DurationMS: o.elapsed.Milliseconds(),
	}); err != nil {
		slog.WarnContext(ctx, "endpoint: recording a failed request failed",
			"endpoint", t.Binding.Name, "error", logsan.SanitizeForLog(err.Error()))
	}
}

// recordAudit writes the request's one audit row. The handler's own platform
// calls each have theirs under the script principal and the same session id.
func (r *Runner) recordAudit(ctx context.Context, t *Target, req Request, o outcome) {
	if r.Audit == nil {
		return
	}
	params := map[string]any{
		"endpoint": t.Binding.Name, "script": t.Script.Name, "script_id": t.Script.ID,
		"version": t.Version.Version, "method": req.Method, "status": o.status, "auth_mode": t.Binding.AuthMode,
	}
	if req.Caller != nil {
		params["caller"] = req.Caller.Email
		params["caller_id"] = req.Caller.UserID
	}
	event := middleware.AuditEvent{
		Timestamp: time.Now().UTC(), SessionID: o.sessionID, UserID: t.Script.Principal(), UserEmail: t.Script.OwnerEmail,
		ToolName: "endpoint:" + t.Binding.Name, Success: o.failure == nil, Authorized: true, Source: SourceEndpoint,
		EventKind: string(audit.EventTypeEndpointRequest), DurationMS: o.elapsed.Milliseconds(), Parameters: params,
		Transport: "http",
	}
	if o.failure != nil {
		event.ErrorMessage = o.failure.Error()
	}
	if err := r.Audit.Log(ctx, event); err != nil {
		slog.WarnContext(ctx, "endpoint: recording the request audit event failed",
			"endpoint", t.Binding.Name, "error", logsan.SanitizeForLog(err.Error()))
	}
}

// refuseExports is the output writer a request runs with: a request answers
// with what handle returns, so platform.export and platform.publish_data fail
// rather than preview, which would read as written.
type refuseExports struct{}

var errNoExport = errors.New("platform.export and platform.publish_data are not available to an endpoint: " +
	"a request answers with what handle(req) returns")

// Export refuses the output.
func (refuseExports) Export(context.Context, scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	return nil, errNoExport
}

// PublishData refuses the refresh.
func (refuseExports) PublishData(context.Context, scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	return nil, errNoExport
}
