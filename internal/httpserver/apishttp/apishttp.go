// Package apishttp serves /api/v1/apis: the read surface that answers, for one
// caller, which api-kind connections they reach and what operations each one
// exposes.
//
// It is the caller-scoped half of the operation browser (#1478). The operator's
// half lives on the admin catalog routes and describes what has been LOADED; this
// one describes what a persona may CALL, which is a different set: a connection
// outside the persona's rules is absent, and so is an operation an APIRoutes deny
// rule refuses. Both come from the toolkit's own browse methods, which apply the
// same route policy api_list_endpoints applies, so this surface and that tool
// cannot disagree about what a caller reaches.
//
// Its path is /api/v1/apis rather than something under /api/v1/portal because
// its second reader is not the portal: a client driving the gateway over plain
// HTTP needs the same inventory before it can compose an invoke body. It is
// mounted with the portal's routes all the same, behind the portal's own
// authenticator, which accepts a session cookie and a bearer token or API key
// alike — so one mount serves the page and that client both.
//
// Nothing here executes an upstream call. The invoke route is the one that does.
package apishttp

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// Caller is the authenticated reader a listing is narrowed to.
type Caller struct {
	UserID string
	Email  string
	// Roles are what the route policy resolves an operation's verdict from.
	Roles []string
	// Persona is the caller's resolved persona, whose connection rules decide
	// which connections they reach. An unresolved persona reaches nothing.
	Persona string
	// IsAdmin lifts the persona boundary, which is what an administrator has
	// everywhere else in the product.
	IsAdmin bool
}

// Connection is one connection a caller reaches, as the enumerator reports it.
type Connection struct {
	Name        string
	Kind        string
	Description string
}

// OperationBrowser is the per-connection read path, implemented by
// *apigateway.Toolkit. Declared as an interface so this package can be tested
// without materializing a toolkit and its HTTP clients.
type OperationBrowser interface {
	BrowseConnection(ctx context.Context, name string) (*apigatewaykit.BrowseConnection, error)
	BrowseOperations(ctx context.Context, connection string) ([]apigatewaykit.OperationSummary, error)
	BrowseOperation(ctx context.Context, connection, operationID, spec string) (*apigatewaykit.EndpointSchemaOutput, error)
}

// Deps wires the surface to the caller resolution, the enumeration, and the
// toolkits that answer for a connection.
type Deps struct {
	// Caller resolves the authenticated reader. Nil leaves the routes
	// unmounted: a surface that cannot tell who is asking cannot narrow
	// anything to them.
	Caller func(r *http.Request) *Caller
	// Connections enumerates what a caller reaches. It is the composition
	// root's, because resolving it means walking the live toolkit registry
	// through the persona boundary and this package holds neither. It takes
	// the whole Caller rather than a narrowing of it, so the answer and the
	// identity it was drawn for cannot drift apart.
	//
	// Nil leaves the routes unmounted, for the same reason connreach yields a
	// nil Lister: a deployment that cannot enumerate its connections should
	// serve no set rather than an empty one a page renders as "you reach
	// nothing".
	Connections func(ctx context.Context, caller *Caller) []Connection
	// Locate finds the api-gateway toolkit serving one connection, or nil when
	// no live toolkit does. Nil leaves the routes unmounted.
	Locate func(connection string) OperationBrowser
	// Elevate puts the caller's identity on the context the toolkit reads, so
	// the route policy resolves the same roles it would resolve for that
	// caller's tool call. Nil means no elevation, which the policy reads as an
	// anonymous caller.
	Elevate func(ctx context.Context, c *Caller) context.Context
}

// Handler serves the routes.
type Handler struct {
	deps Deps
}

// New builds a Handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Register mounts the routes, wrapped in the supplied authentication
// middleware. Returns without mounting anything when the deployment cannot
// answer who is asking or what they reach.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if h.deps.Caller == nil || h.deps.Connections == nil || h.deps.Locate == nil {
		return
	}
	if wrap == nil {
		wrap = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("GET /api/v1/apis", wrap(h.authed(h.listConnections)))
	mux.Handle("GET /api/v1/apis/{connection}/operations", wrap(h.authed(h.listOperations)))
	mux.Handle("GET /api/v1/apis/{connection}/operations/{operationId}", wrap(h.authed(h.getOperation)))
}

// authed resolves the caller once for every route, answering 401 when the
// request carries none.
func (h *Handler) authed(fn func(w http.ResponseWriter, r *http.Request, c *Caller)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := h.deps.Caller(r)
		if caller == nil {
			httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		fn(w, r, caller)
	})
}

// specResponse is one component spec of a connection's catalog, with the count
// of operations THIS caller reaches in it.
type specResponse struct {
	Name           string `json:"name"`
	Title          string `json:"title,omitempty"`
	Description    string `json:"description,omitempty"`
	OperationCount int    `json:"operation_count"`
	BasePath       string `json:"base_path,omitempty"`
}

// connectionResponse is one api-kind connection a caller reaches.
type connectionResponse struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// BaseURL is the upstream root every operation path is joined onto. The
	// credential that reaches it is never part of this surface.
	BaseURL   string `json:"base_url,omitempty"`
	AuthMode  string `json:"auth_mode,omitempty"`
	CatalogID string `json:"catalog_id,omitempty"`
	// OperationCount counts the operations this caller reaches, not the
	// catalog's total: an operation a deny rule hides is absent from the list
	// and from the count.
	OperationCount int            `json:"operation_count"`
	Specs          []specResponse `json:"specs"`
}

// connectionListResponse is the payload of GET /api/v1/apis.
type connectionListResponse struct {
	Connections []connectionResponse `json:"connections"`
}

// listConnections handles GET /api/v1/apis.
//
// @Summary      List the API connections you reach
// @Description  Returns the api-kind connections the caller's persona reaches, each with the number of operations the route policy permits them.
// @Tags         APIs
// @Produce      json
// @Success      200  {object}  connectionListResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /apis [get]
func (h *Handler) listConnections(w http.ResponseWriter, r *http.Request, c *Caller) {
	ctx := h.elevate(r.Context(), c)
	out := connectionListResponse{Connections: []connectionResponse{}}
	for _, name := range h.reachable(r.Context(), c) {
		browser := h.deps.Locate(name)
		if browser == nil {
			// The connection is enumerated but no live toolkit serves it,
			// which happens between a config change and the reload that
			// follows it. Skipping is the honest answer: the listing is what
			// is readable right now.
			continue
		}
		detail, err := browser.BrowseConnection(ctx, name)
		if err != nil {
			continue
		}
		out.Connections = append(out.Connections, toConnectionResponse(detail))
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// operationResponse is one row in a connection's operation index.
type operationResponse struct {
	OperationID string   `json:"operation_id"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Summary     string   `json:"summary,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Spec        string   `json:"spec,omitempty"`
}

// operationListResponse is the payload of the per-connection operation index.
// The connection travels with it so a page rendering one connection has the
// upstream root and the auth mode without a second request.
type operationListResponse struct {
	Connection connectionResponse  `json:"connection"`
	Operations []operationResponse `json:"operations"`
}

// listOperations handles GET /api/v1/apis/{connection}/operations.
//
// @Summary      List a connection's operations
// @Description  Returns every operation of one api-kind connection that the caller's route policy permits, with the connection's upstream root and auth mode.
// @Tags         APIs
// @Produce      json
// @Param        connection  path  string  true  "Connection name"
// @Success      200  {object}  operationListResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /apis/{connection}/operations [get]
func (h *Handler) listOperations(w http.ResponseWriter, r *http.Request, c *Caller) {
	name := r.PathValue("connection")
	browser, ok := h.browserFor(w, r, c, name)
	if !ok {
		return
	}
	ctx := h.elevate(r.Context(), c)
	detail, err := browser.BrowseConnection(ctx, name)
	if err != nil {
		writeBrowseError(w, err)
		return
	}
	ops, err := browser.BrowseOperations(ctx, name)
	if err != nil {
		writeBrowseError(w, err)
		return
	}
	out := operationListResponse{
		Connection: toConnectionResponse(detail),
		Operations: make([]operationResponse, 0, len(ops)),
	}
	for _, op := range ops {
		out.Operations = append(out.Operations, operationResponse{
			OperationID: op.OperationID,
			Method:      op.Method,
			Path:        op.Path,
			Summary:     op.Summary,
			Tags:        op.Tags,
			Spec:        op.Spec,
		})
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// getOperation handles GET /api/v1/apis/{connection}/operations/{operationId}.
//
// An operation id synthesized from a path ("GET /things/{id}") carries slashes
// and a space, so the segment is percent-encoded by the caller and decoded by
// the mux before it reaches here.
//
// @Summary      Get one operation
// @Description  Returns one operation's parameters, request body and per-status responses, resolved exactly as api_get_endpoint_schema resolves them. An operation the route policy denies is reported as not found.
// @Tags         APIs
// @Produce      json
// @Param        connection   path   string  true   "Connection name"
// @Param        operationId  path   string  true   "Operation ID (percent-encoded)"
// @Param        spec         query  string  false  "Component spec, when one id is defined by several"
// @Success      200  {object}  apigateway.EndpointSchemaOutput
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /apis/{connection}/operations/{operationId} [get]
func (h *Handler) getOperation(w http.ResponseWriter, r *http.Request, c *Caller) {
	name := r.PathValue("connection")
	browser, ok := h.browserFor(w, r, c, name)
	if !ok {
		return
	}
	detail, err := browser.BrowseOperation(
		h.elevate(r.Context(), c), name, r.PathValue("operationId"), r.URL.Query().Get("spec"))
	if err != nil {
		writeBrowseError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, detail)
}

// browserFor resolves the toolkit serving a connection the caller reaches,
// writing the 404 itself when they do not reach it or nothing serves it.
//
// A connection outside the caller's reach is reported as not found rather than
// as forbidden: the persona boundary is what this surface is drawn from, so a
// connection outside it does not exist here.
func (h *Handler) browserFor(
	w http.ResponseWriter, r *http.Request, c *Caller, name string,
) (OperationBrowser, bool) {
	if name == "" || !h.reaches(r.Context(), c, name) {
		httpjson.WriteError(w, http.StatusNotFound, "connection not found")
		return nil, false
	}
	browser := h.deps.Locate(name)
	if browser == nil {
		httpjson.WriteError(w, http.StatusNotFound, "connection not found")
		return nil, false
	}
	return browser, true
}

// reachable lists the api-kind connection names this caller reaches. The
// enumeration covers every kind the deployment holds; this surface is about one
// of them.
func (h *Handler) reachable(ctx context.Context, c *Caller) []string {
	conns := h.deps.Connections(ctx, c)
	names := make([]string, 0, len(conns))
	for _, conn := range conns {
		if conn.Kind == apigatewaykit.Kind {
			names = append(names, conn.Name)
		}
	}
	return names
}

// reaches reports whether one named connection is in the caller's reach.
func (h *Handler) reaches(ctx context.Context, c *Caller, name string) bool {
	return slices.Contains(h.reachable(ctx, c), name)
}

// elevate puts the caller on the context the toolkit reads, so the route policy
// resolves their roles rather than an anonymous caller's.
func (h *Handler) elevate(ctx context.Context, c *Caller) context.Context {
	if h.deps.Elevate == nil {
		return ctx
	}
	return h.deps.Elevate(ctx, c)
}

// toConnectionResponse projects the toolkit's view onto the wire shape.
func toConnectionResponse(detail *apigatewaykit.BrowseConnection) connectionResponse {
	out := connectionResponse{
		Name:           detail.Name,
		Description:    detail.Description,
		BaseURL:        detail.BaseURL,
		AuthMode:       detail.AuthMode,
		CatalogID:      detail.CatalogID,
		OperationCount: detail.OperationCount,
		Specs:          make([]specResponse, 0, len(detail.Specs)),
	}
	for _, s := range detail.Specs {
		out.Specs = append(out.Specs, specResponse{
			Name:           s.Name,
			Title:          s.Title,
			Description:    s.Description,
			OperationCount: s.OperationCount,
			BasePath:       s.BasePath,
		})
	}
	return out
}

// writeBrowseError maps a toolkit browse failure to its HTTP answer. An
// ambiguous id is the one failure the caller can act on, so it says so and
// keeps its message, which names the specs to retry against.
func writeBrowseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, apigatewaykit.ErrAmbiguousOperation):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, apigatewaykit.ErrOperationNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "operation not found")
	default:
		httpjson.WriteError(w, http.StatusNotFound, "connection not found")
	}
}
