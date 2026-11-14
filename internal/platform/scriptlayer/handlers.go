package scriptlayer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eppreview"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer/handlercmd"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// caller is who calls, as the handler service reads them.
func (h *Handle) caller(ctx context.Context) scripthandler.Caller {
	c := scripthandler.Caller{Email: resolveEmail(ctx), Admin: h.isAdminPersona(ctx), Roles: callerAuthor(ctx).Roles}
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		c.Persona = pc.PersonaName
	}
	return c
}

// handlerResult answers a handler operation's error as a tool error, logging
// the platform's own failures and telling the caller only that the store
// failed.
func handlerResult(ctx context.Context, out map[string]any, err error) (*mcp.CallToolResult, any, error) {
	if err == nil {
		return jsonResult(out)
	}
	if scripthandler.Failed(err) {
		slog.ErrorContext(ctx, "handlers: an operation failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		return errorResult("the handler could not be read or written; try again"), nil, nil
	}
	return errorResult(err.Error()), nil, nil
}

func (h *Handle) handlersOff() *mcp.CallToolResult {
	if h.handlers == nil {
		return errorResult("this deployment cannot keep handlers")
	}
	return nil
}

// handleHandlerSet adds or changes one of a script's handlers.
func (h *Handle) handleHandlerSet(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if off := h.handlersOff(); off != nil {
		return off, nil, nil
	}
	sc, errResult := h.owned(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	req := scripthandler.SetRequest{
		Caller: h.caller(ctx), Script: sc, Kind: input.Kind, Handler: input.Handler,
		Endpoint: input.Input, TimeoutSeconds: input.TimeoutSeconds,
		Cron: input.Cron, Timezone: input.Timezone, Args: input.Args,
		Call: input.Patch(input.Connection), Approve: input.Approve,
	}
	if input.EventSource != nil {
		req.EventSource = *input.EventSource
	}
	out, err := h.handlers.Set(ctx, req)
	return handlerResult(ctx, out, err)
}

// handleHandlerList lists handlers: one script's, each described in full,
// or, naming none, the caller's scripts' (every script's for an
// administrator).
func (h *Handle) handleHandlerList(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if off := h.handlersOff(); off != nil {
		return off, nil, nil
	}
	filter, refused := handlerListFilter(input)
	if refused != nil {
		return refused, nil, nil
	}
	if input.Name != "" {
		return h.scriptHandlerList(ctx, input, filter)
	}
	rows, total, err := h.handlers.List(ctx, filter, h.caller(ctx))
	if err != nil {
		return handlerResult(ctx, nil, err)
	}
	return jsonResult(map[string]any{fieldHandlers: rows, fieldCount: len(rows), "total": total})
}

// handlerListFilter is the listing input asks for; an endpoint kind lists
// forms too, since a form is an endpoint.
func handlerListFilter(input manageScriptInput) (scripthandler.Filter, *mcp.CallToolResult) {
	filter := scripthandler.Filter{Limit: input.Limit}
	if input.Kind == "" {
		return filter, nil
	}
	if _, ok := scripthandler.KindOf(input.Kind); !ok {
		return filter, errorResult(fmt.Sprintf("unknown handler kind %q", input.Kind))
	}
	filter.Types = []string{input.Kind}
	if input.Kind == scripthandler.KindEndpoint {
		filter.Types = append(filter.Types, scripthandler.TypeForm)
	}
	return filter, nil
}

// scriptHandlerList is the handlers of the script input names, each
// described as handler_get describes it.
func (h *Handle) scriptHandlerList(ctx context.Context, input manageScriptInput, filter scripthandler.Filter) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.owned(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	filter.ScriptIDs = []string{sc.ID}
	rows, _, err := h.handlers.List(ctx, filter, scripthandler.Caller{Admin: true})
	if err != nil {
		return handlerResult(ctx, nil, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		one, err := h.handlers.Get(ctx, row.ID, h.caller(ctx))
		if err != nil {
			return handlerResult(ctx, nil, err)
		}
		one["type"] = row.Type
		items = append(items, one)
	}
	out := map[string]any{fieldName: sc.Name, fieldHandlers: items, fieldCount: len(items)}
	if len(items) == 0 {
		out[fieldMessage] = "This script has no handlers. Add one with command=handler_set and a kind."
	}
	return jsonResult(out)
}

// handleHandlerGet reads one handler.
func (h *Handle) handleHandlerGet(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if off := h.handlersOff(); off != nil {
		return off, nil, nil
	}
	out, err := h.handlers.Get(ctx, input.Handler, h.caller(ctx))
	return handlerResult(ctx, out, err)
}

// handleHandlerEnable turns a handler on.
func (h *Handle) handleHandlerEnable(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	return h.enableHandler(ctx, input, true)
}

// handleHandlerDisable turns a handler off.
func (h *Handle) handleHandlerDisable(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	return h.enableHandler(ctx, input, false)
}

func (h *Handle) enableHandler(ctx context.Context, input manageScriptInput, enabled bool) (*mcp.CallToolResult, any, error) {
	if off := h.handlersOff(); off != nil {
		return off, nil, nil
	}
	out, err := h.handlers.Enable(ctx, input.Handler, enabled, h.caller(ctx))
	return handlerResult(ctx, out, err)
}

// handleHandlerDelete removes a handler.
func (h *Handle) handleHandlerDelete(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if off := h.handlersOff(); off != nil {
		return off, nil, nil
	}
	out, err := h.handlers.Delete(ctx, input.Handler, h.caller(ctx))
	return handlerResult(ctx, out, err)
}

// handleFormPreview opens a preview of the script's form for its owner or an
// administrator (#2494); its drafts run as the caller, as run_draft does.
func (h *Handle) handleFormPreview(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if h.endpoints == nil {
		return errorResult("this deployment cannot bind scripts to endpoints"), nil, nil
	}
	sc, errResult := h.owned(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	req := epcmd.Request{
		Input: input.Input, Script: sc, Admin: h.isAdminPersona(ctx), Actor: resolveEmail(ctx), Source: input.Source,
	}
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		req.Opener = eppreview.Opener{UserID: pc.UserID, Email: pc.UserEmail, Roles: pc.Roles, AuthType: pc.AuthType}
	}
	out, err := h.endpoints.Commands.Preview(ctx, req)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	return jsonResult(out)
}

// handleHandlerTest answers handler_test: an administrator's.
func (h *Handle) handleHandlerTest(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	out, refusal := handlercmd.RunTest(ctx, h.isAdminPersona(ctx), h.callTester, input.Connection, input.CallSettings)
	if refusal != "" {
		return errorResult(refusal), nil, nil
	}
	return jsonResult(out)
}
