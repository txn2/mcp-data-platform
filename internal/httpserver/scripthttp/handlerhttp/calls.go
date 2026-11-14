package handlerhttp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
)

// pathConnection is the path value a connection route names its connection by.
const pathConnection = "connection"

// Tester answers the test panel (callrun.Evaluator).
type Tester interface {
	Test(ctx context.Context, req callhandler.TestRequest) (map[string]any, error)
}

// registerCalls mounts the routes of the tool-call handlers (#2436): a
// handler's failures, and a connection's handlers, test panel and transform
// order. Every one is an administrator's; anyone else is answered as if it
// held nothing.
func (h *Handler) registerCalls(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/handlers/{id}/failures", wrap(http.HandlerFunc(h.failures)))
	mux.Handle("GET /api/v1/portal/connections/{connection}/handlers", wrap(http.HandlerFunc(h.connectionHandlers)))
	mux.Handle("POST /api/v1/portal/connections/{connection}/handlers/test", wrap(http.HandlerFunc(h.testCall)))
	mux.Handle("PUT /api/v1/portal/connections/{connection}/handlers/order", wrap(http.HandlerFunc(h.orderTransforms)))
}

// FailureList is a tool-call handler's newest failures.
type FailureList struct {
	Data []callhandler.Failure `json:"data"`
}

// failures lists a tool-call handler's newest failures.
//
// @Summary      List a tool-call handler's failures
// @Description  The newest failures of a policy or transform, kept seven days: the call it was given (its arguments as the caller sent them, a stored secret still its placeholder), what its script printed, the error and how the call was then treated. An administrator's only; anyone else, the script's owner included, is answered 404.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Handler ID"
// @Success      200  {object}  FailureList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/handlers/{id}/failures [get]
func (h *Handler) failures(w http.ResponseWriter, r *http.Request) {
	c, ok := h.caller(w, r)
	if !ok {
		return
	}
	list, err := h.deps.Handlers.Failures(r.Context(), r.PathValue(pathID), c)
	write(w, r, http.StatusOK, FailureList{Data: list}, err)
}

// ConnectionHandlerList is every tool-call handler on a connection.
type ConnectionHandlerList struct {
	Data []map[string]any `json:"data"`
}

// connectionHandlers lists a connection's tool-call handlers.
//
// @Summary      List a connection's tool-call handlers
// @Description  Every policy, transform and watch that applies to the connection, as its page lists them: policies, transforms in the order they run, then watches, each with its match (operations, personas, users, conditions), settings, state, the version it runs and who approved it, the version waiting for approval, and stats, its last 24 hours from the audit rows of the calls it matched (matched, denied, held, errors, queued, p50_ms, p99_ms). An administrator's only.
// @Tags         Scripts
// @Produce      json
// @Param        connection  path  string  true  "Connection name"
// @Success      200  {object}  ConnectionHandlerList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/connections/{connection}/handlers [get]
func (h *Handler) connectionHandlers(w http.ResponseWriter, r *http.Request) {
	c, ok := h.caller(w, r)
	if !ok {
		return
	}
	list, err := h.deps.Handlers.ConnectionHandlers(r.Context(), r.PathValue(pathConnection), c)
	write(w, r, http.StatusOK, ConnectionHandlerList{Data: list}, err)
}

// TestRequest is a call the test panel asks about.
type TestRequest struct {
	Tool      string         `json:"tool" example:"api_invoke_endpoint"`
	Arguments map[string]any `json:"arguments,omitempty"`
	AsUser    string         `json:"as_user,omitempty" example:"jo@example.com"`
	AsPersona string         `json:"as_persona,omitempty" example:"marketing"`
	At        string         `json:"at,omitempty" example:"2026-11-13T15:40:00Z"`
	Result    any            `json:"result,omitempty"`
}

// testCall answers the test panel.
//
// @Summary      Test a call against a connection's handlers
// @Description  What every handler on the connection would make of a call, without the upstream being called or any count or allowance spent; a handler's own reads run. Answers the outcome (runs, held or denied), each matching handler in order with what it did and how long it took, a refusal's reason, a hold's approvers and message, and, given a result, what the transforms would make of it. An administrator's only.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        connection  path  string       true  "Connection name"
// @Param        body        body  TestRequest  true  "The call"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/connections/{connection}/handlers/test [post]
func (h *Handler) testCall(w http.ResponseWriter, r *http.Request) {
	c, ok := h.caller(w, r)
	if !ok {
		return
	}
	if !c.Admin {
		httpjson.WriteError(w, http.StatusForbidden, "the test panel is an administrator's")
		return
	}
	if h.deps.Tester == nil {
		httpjson.WriteError(w, http.StatusBadRequest, "this deployment cannot test calls against handlers")
		return
	}
	var body TestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes*4)).Decode(&body); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body is not a call: "+err.Error())
		return
	}
	out, err := h.deps.Tester.Test(r.Context(), callhandler.TestRequest{
		Connection: r.PathValue(pathConnection), Tool: body.Tool, Arguments: body.Arguments,
		User: body.AsUser, Persona: body.AsPersona, At: body.At, Result: body.Result,
	})
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// OrderRequest is the order a connection's transforms run in.
type OrderRequest struct {
	IDs []string `json:"ids"`
}

// orderTransforms sets the order a connection's transforms run in.
//
// @Summary      Order a connection's transforms
// @Description  Sets the order the connection's transforms run in: ids, first to last. Answers the connection's handlers as GET /portal/connections/{connection}/handlers does. An administrator's only.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        connection  path  string        true  "Connection name"
// @Param        body        body  OrderRequest  true  "The transforms' ids, first to last"
// @Success      200  {object}  ConnectionHandlerList
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/connections/{connection}/handlers/order [put]
func (h *Handler) orderTransforms(w http.ResponseWriter, r *http.Request) {
	c, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body OrderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&body); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body is not an order: "+err.Error())
		return
	}
	if err := h.deps.Handlers.OrderTransforms(r.Context(), body.IDs, c); err != nil {
		write(w, r, http.StatusOK, nil, err)
		return
	}
	list, err := h.deps.Handlers.ConnectionHandlers(r.Context(), r.PathValue(pathConnection), c)
	write(w, r, http.StatusOK, ConnectionHandlerList{Data: list}, err)
}
