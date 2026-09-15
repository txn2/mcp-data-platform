package catalogapi

import (
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// The operator's view of what a catalog exposes. A spec is readable
// here before any connection references it, which is the difference
// between this surface and the caller-scoped one at /api/v1/apis:
// this one describes what has been loaded, that one describes what a
// persona reaches (#1478).
//
// Neither route returns the spec document. The operations list carries
// the same OperationSummary api_discover returns, and the detail
// route the same EndpointSchemaOutput api_discover returns at its operation level,
// so a page and a tool call describe one operation identically.

// errNotAGraphQLSchema is the refusal for a spec entry marked graphql
// whose content does not load as a schema.
const errNotAGraphQLSchema = "spec content does not parse as a GraphQL schema"

// operationSummaryResponse is one row in the operations listing.
type operationSummaryResponse struct {
	OperationID string   `json:"operation_id"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Summary     string   `json:"summary,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Spec        string   `json:"spec,omitempty"`
}

// operationListResponse wraps the listing so the shape can gain fields
// (paging, a parse warning) without breaking existing consumers.
type operationListResponse struct {
	Operations []operationSummaryResponse `json:"operations"`
	// BasePath is the prefix every listed path already carries. Reported
	// so a reader can tell an operator-set prefix from one the spec's own
	// servers[] declared.
	BasePath string `json:"base_path,omitempty"`
}

// listSpecOperations handles
// GET /api/v1/admin/api-catalogs/{id}/specs/{spec}/operations.
//
// @Summary      List spec operations
// @Description  Returns the operations a catalog spec parses to, in either format the spec may be in. A GraphQL spec's rows carry the operation kind as the method and the dotted operation id as the path. The spec document is not returned.
// @Tags         API Catalogs
// @Produce      json
// @Param        id    path  string  true  "Catalog ID"
// @Param        spec  path  string  true  "Spec name"
// @Success      200  {object}  operationListResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/operations [get]
func (h *handler) listSpecOperations(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpec(w, r)
	if !ok {
		return
	}
	if !spec.ServesOpenAPI() {
		h.listGraphQLOperations(w, *spec)
		return
	}
	ops, basePath, err := apigatewaykit.SpecOperations(spec.Effective(), spec.SpecName, spec.BasePath)
	if err != nil {
		// The spec is stored but does not parse. That is a real state —
		// content can be written before a parser upgrade, or fetched from
		// a URL that started serving something else — and saying so is
		// the only honest answer; an empty list would read as "this spec
		// exposes nothing".
		httpjson.WriteError(w, http.StatusUnprocessableEntity, "spec content does not parse as OpenAPI")
		return
	}
	out := operationListResponse{
		Operations: make([]operationSummaryResponse, 0, len(ops)),
		BasePath:   basePath,
	}
	for _, op := range ops {
		out.Operations = append(out.Operations, operationSummaryResponse{
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

// listGraphQLOperations is listSpecOperations for a spec entry holding a
// GraphQL schema (#1745).
//
// It reports the same rows the OpenAPI listing reports, so one page
// renders either format: the operation id is the kind-prefixed dotted id
// the connection ranks and invokes by, the method is the GraphQL
// operation kind, and the path is the route a persona's rules name it
// under. There is no base path — a GraphQL endpoint is one address.
func (*handler) listGraphQLOperations(w http.ResponseWriter, spec apicatalog.SpecEntry) {
	ops, err := graphqlkit.SchemaOperations(spec.Effective())
	if err != nil {
		httpjson.WriteError(w, http.StatusUnprocessableEntity, errNotAGraphQLSchema)
		return
	}
	out := operationListResponse{Operations: make([]operationSummaryResponse, 0, len(ops))}
	for _, op := range ops {
		out.Operations = append(out.Operations, operationSummaryResponse{
			OperationID: op.OperationID,
			Method:      op.Kind,
			Path:        op.Path,
			Summary:     op.Summary,
			Spec:        spec.SpecName,
		})
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// getSpecOperation handles
// GET /api/v1/admin/api-catalogs/{id}/specs/{spec}/operations/{operationId}.
//
// @Summary      Get spec operation
// @Description  Returns one operation's parameters, request body and per-status responses. For a GraphQL spec it returns the arguments, the input types they reference, the return shape and a document that already calls the operation. The spec document is not returned.
// @Tags         API Catalogs
// @Produce      json
// @Param        id           path  string  true  "Catalog ID"
// @Param        spec         path  string  true  "Spec name"
// @Param        operationId  path  string  true  "Operation ID"
// @Success      200  {object}  apigateway.EndpointSchemaOutput
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/operations/{operationId} [get]
func (h *handler) getSpecOperation(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpec(w, r)
	if !ok {
		return
	}
	if !spec.ServesOpenAPI() {
		h.writeGraphQLOperation(w, *spec, r.PathValue(catalogPathOperation))
		return
	}
	detail, err := apigatewaykit.SpecOperation(
		spec.Effective(), spec.SpecName, spec.BasePath, r.PathValue(catalogPathOperation))
	switch {
	case errors.Is(err, apigatewaykit.ErrOperationNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "operation not found")
		return
	case err != nil:
		httpjson.WriteError(w, http.StatusUnprocessableEntity, "spec content does not parse as OpenAPI")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, detail)
}

// loadSpec reads the stored spec both operation routes start from,
// writing the 404 / 500 response itself when it cannot.
func (h *handler) loadSpec(w http.ResponseWriter, r *http.Request) (*apicatalog.SpecEntry, bool) {
	spec, err := h.cfg.Catalogs.GetSpec(r.Context(), r.PathValue(catalogPathID), r.PathValue(catalogPathSpec))
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, errSpecNotFound)
		return nil, false
	case err != nil:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get spec")
		return nil, false
	}
	return spec, true
}

// graphQLOperationResponse is one GraphQL operation as the operations
// pane reads it.
//
// The pane renders either format, so the fields it reads for an OpenAPI
// operation carry the GraphQL equivalent: `method` is the operation kind
// the badge shows, `path` the route a persona rule names it under. What
// an OpenAPI operation puts in parameters, request body and responses has
// no equivalent here, and what does — the arguments, the input types they
// reference, the shape the operation returns, and a document that already
// calls it — travels beside them under its own names.
type graphQLOperationResponse struct {
	OperationID string `json:"operation_id"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Summary     string `json:"summary,omitempty"`
	Spec        string `json:"spec,omitempty"`
	ReturnType  string `json:"return_type,omitempty"`
	Deprecated  bool   `json:"deprecated,omitempty"`

	Arguments   []gqlschema.Argument  `json:"graphql_arguments,omitempty"`
	InputTypes  []gqlschema.InputType `json:"graphql_input_types,omitempty"`
	ReturnShape []gqlschema.FieldNode `json:"graphql_return_shape,omitempty"`
	Skeleton    string                `json:"graphql_skeleton,omitempty"`
	Variables   string                `json:"graphql_variables,omitempty"`
}

// writeGraphQLOperation is getSpecOperation for a spec entry holding a
// GraphQL schema: one operation's arguments, the input types they
// reference, its return shape and a document that already calls it —
// the same detail graphql_discover returns at its operation level
// (#1745).
func (*handler) writeGraphQLOperation(w http.ResponseWriter, spec apicatalog.SpecEntry, operationID string) {
	detail, err := graphqlkit.SchemaOperation(spec.Effective(), operationID)
	switch {
	case errors.Is(err, graphqlkit.ErrOperationNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "operation not found")
	case err != nil:
		httpjson.WriteError(w, http.StatusUnprocessableEntity, errNotAGraphQLSchema)
	default:
		httpjson.WriteJSON(w, http.StatusOK, graphQLOperationResponse{
			OperationID: detail.OperationID,
			Method:      detail.Kind,
			Path:        detail.Path,
			Summary:     detail.Summary,
			Spec:        spec.SpecName,
			ReturnType:  detail.ReturnType,
			Deprecated:  detail.Deprecated,
			Arguments:   detail.ArgumentDetails,
			InputTypes:  detail.InputTypes,
			ReturnShape: detail.ReturnShape,
			Skeleton:    detail.Skeleton,
			Variables:   detail.Variables,
		})
	}
}
