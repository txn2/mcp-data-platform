package mcptask

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// Mode is how a tool's call becomes a task.
type Mode int

const (
	// Native is a tool that keeps its own record of the work and accepts the
	// call's Claim with that record's id (run_script and its run).
	Native Mode = iota + 1
	// Detached is a call executed apart from its request by the Detacher,
	// which keeps the task (the exports).
	Detached
)

// Detacher executes a tool call apart from the request that asked for it.
// Start creates the task durably, starts run on a context that outlives the
// request, and returns the task's seed state; run is the rest of the
// tools/call chain.
type Detacher interface {
	Start(ctx context.Context, toolName string, run func(ctx context.Context) (mcp.Result, error)) (*Snapshot, error)
}

// Resolver attaches the caller's PlatformContext to a tasks/* request, which
// passes none of the tools/call layers that would.
type Resolver func(ctx context.Context, req mcp.Request) context.Context

// Config is what the middleware needs.
type Config struct {
	// Tools names the tools whose calls become tasks, and how.
	Tools map[string]Mode
	// Sources resolves a Native tool's accepted id to its seed state.
	Sources *Sources
	// Detacher executes a Detached tool's calls. With none, those calls
	// are answered directly.
	Detacher Detacher
	// Resolve identifies the caller of a tasks/* request.
	Resolve Resolver
}

// Middleware answers a tools/call that declares the extension with a task,
// for the tools Config names, and identifies the caller of every tasks/*
// request. A call that does not declare the extension, and every other tool,
// passes through untouched: such a client sees exactly what it saw before.
//
// It sits inside authorization, the gates and the consent prompts, so a call
// that is refused, or that needs the person's answer first, is answered
// directly before any task exists, as the extension requires.
func Middleware(cfg Config) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case methodToolsCall:
				return cfg.toolCall(ctx, next, method, req)
			case MethodGet, MethodUpdate, MethodCancel:
				if !Declared(req) {
					return nil, missingCapability()
				}
				if cfg.Resolve != nil {
					ctx = cfg.Resolve(ctx, req)
				}
			}
			return next(ctx, method, req)
		}
	}
}

// toolCall turns one tools/call into a task when it may become one.
func (cfg Config) toolCall(ctx context.Context, next mcp.MethodHandler, method string, req mcp.Request) (mcp.Result, error) {
	call, ok := req.(*mcp.CallToolRequest)
	if !ok || call.Params == nil || !Declared(req) {
		return next(ctx, method, req)
	}
	switch cfg.Tools[call.Params.Name] {
	case Native:
		return cfg.native(ctx, next, method, req)
	case Detached:
		if cfg.Detacher == nil {
			return next(ctx, method, req)
		}
		return cfg.detached(ctx, next, method, req, call.Params.Name)
	default:
		return next(ctx, method, req)
	}
}

// native runs the tool with a Claim and answers with the task it accepted.
func (cfg Config) native(ctx context.Context, next mcp.MethodHandler, method string, req mcp.Request) (mcp.Result, error) {
	claimed, claim := withClaim(ctx)
	res, err := next(claimed, method, req)
	id := claim.accepted()
	if err != nil || id == "" {
		return res, err
	}
	snap, getErr := cfg.seed(ctx, id)
	if getErr != nil {
		// The work exists and the tool's own answer names it; a client
		// that cannot be handed the task is handed that answer.
		slog.WarnContext(ctx, "a task's seed state could not be read; answering with the tool's result",
			"task_id", id, "error", logsan.SanitizeForLog(getErr.Error()))
		return res, nil
	}
	// The call's own part ended with the tool's answer: the work it started
	// is the task, followed through tasks/get.
	Complete(ctx, res, nil)
	return NewCreateResult(snap), nil
}

// seed reads the state a Native task starts in.
func (cfg Config) seed(ctx context.Context, id string) (*Snapshot, error) {
	src := cfg.Sources.For(id)
	if src == nil {
		return nil, ErrNotFound
	}
	snap, err := src.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading task %s: %w", id, err)
	}
	return snap, nil
}

// detached hands the call to the Detacher and answers with the task.
func (cfg Config) detached(ctx context.Context, next mcp.MethodHandler, method string, req mcp.Request, name string) (mcp.Result, error) {
	snap, err := cfg.Detacher.Start(ctx, name, func(dctx context.Context) (mcp.Result, error) {
		return next(dctx, method, req)
	})
	if errors.Is(err, ErrNotDetached) {
		return next(ctx, method, req)
	}
	if err != nil {
		// No task exists, so nothing was started: the call is answered
		// directly, as a client without the extension would be.
		slog.WarnContext(ctx, "a tool call could not become a task; answering it directly",
			"tool", name, "error", logsan.SanitizeForLog(err.Error()))
		return next(ctx, method, req)
	}
	return NewCreateResult(snap), nil
}

// ErrNotDetached is what a Detacher answers for a call it declines to take on,
// one that is answered directly instead.
var ErrNotDetached = errors.New("call not detached")
