// Package graphqlapi serves the admin surface for a GraphQL connection's
// schema: reading what the platform holds, re-reading it from the
// endpoint, and — for an endpoint that disables introspection —
// accepting one an operator supplies.
//
// It is a decomposition seam of pkg/admin, which sits near its package
// size budget; the parent mounts it on the admin mux and hands it the
// live toolkit registry.
package graphqlapi

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// maxSchemaUploadBytes bounds an uploaded schema. The largest published
// GraphQL schemas are a few megabytes of SDL and rather more as an
// introspection result; 16 MiB is past both without letting an upload
// become a memory exhaustion.
const maxSchemaUploadBytes = 16 << 20

// pathKeyName is the {name} path placeholder on these routes.
const pathKeyName = "name"

// Config carries what the routes need from the parent.
type Config struct {
	// Toolkits enumerates the live toolkits, so a connection added
	// through the admin API after startup is reachable here without a
	// restart. nil disables every route in this package.
	Toolkits func() []registry.Toolkit
	// Mutable reports database config mode. False registers the read
	// route only: re-reading a schema writes it to the store, and a
	// file-configured deployment has nowhere to put it.
	Mutable bool
	// SchemaStored is told the connection kind and name whose stored
	// schema state this replica just changed, by an upload, a re-read, or
	// a re-read the endpoint refused beside a stored schema, so the parent
	// can announce it to peer replicas. Nil announces nothing.
	SchemaStored func(kind, name string)
}

// handler binds the routes to their dependencies.
type handler struct {
	cfg Config
}

// Register mounts the schema routes on the admin mux.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Toolkits == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/admin/connection-instances/graphql/{name}/schema", h.getSchema)
	if cfg.Mutable {
		mux.HandleFunc("POST /api/v1/admin/connection-instances/graphql/{name}/refresh-schema", h.refreshSchema)
	}
}

// getSchema handles GET /api/v1/admin/connection-instances/graphql/{name}/schema.
//
// @Summary      Read a GraphQL connection's schema state
// @Description  Reports which schema version the platform holds for this connection, where it came from, when it was read, how many operations it exposes, and — when the platform holds none — why.
// @Tags         Connections
// @Produce      json
// @Param        name  path  string  true  "GraphQL connection name"
// @Success      200  {object}  graphql.SchemaInfo
// @Failure      404  {object}  httpjson.ProblemDetail
// @Router       /admin/connection-instances/graphql/{name}/schema [get]
func (h *handler) getSchema(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue(pathKeyName)
	tk := h.toolkitFor(name)
	if tk == nil {
		httpjson.WriteError(w, http.StatusNotFound, "no graphql connection named "+name)
		return
	}
	info, err := tk.SchemaInfo(name)
	if err != nil {
		httpjson.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, info)
}

// refreshSchema handles POST /api/v1/admin/connection-instances/graphql/{name}/refresh-schema.
//
// One route serves both ways a schema arrives, because they are one
// operator action — "make the platform's schema current" — reached from
// one button. An empty body re-reads the endpoint by introspection; a
// body is taken as the schema itself, in SDL or as an introspection
// result, which is the path for an endpoint that disables
// introspection.
//
// @Summary      Re-read or supply a GraphQL connection's schema
// @Description  With an empty body, reads the connection's schema from its endpoint by introspection. With a body, takes the body as the schema: SDL, or a saved introspection result in either the full GraphQL response shape or the __schema object alone. Either way the schema is stored, the operation index is rebuilt on every replica, and the response reports the new state. A re-read the endpoint refuses is state too: the schema the connection holds stays in place, the refusal is recorded beside it on every replica, and the response is the same 200 with `error` filled. A body that does not parse is the caller's input and answers 400.
// @Tags         Connections
// @Accept       plain
// @Produce      json
// @Param        name  path  string  true  "GraphQL connection name"
// @Success      200  {object}  graphql.SchemaInfo
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Router       /admin/connection-instances/graphql/{name}/refresh-schema [post]
func (h *handler) refreshSchema(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue(pathKeyName)
	tk := h.toolkitFor(name)
	if tk == nil {
		httpjson.WriteError(w, http.StatusNotFound, "no graphql connection named "+name)
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSchemaUploadBytes))
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "reading the uploaded schema failed: "+err.Error())
		return
	}
	applyErr := h.apply(r.Context(), tk, name, payload)
	if applyErr != nil && !isReread(payload) {
		httpjson.WriteError(w, http.StatusBadRequest, applyErr.Error())
		return
	}
	// A connection removed while the request ran answers 404 here.
	info, err := tk.SchemaInfo(name)
	if err != nil {
		httpjson.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	// A refused re-read changed the stored state only when there was a
	// stored schema to record the refusal beside; a connection holding
	// none has nothing a peer could load.
	if h.cfg.SchemaStored != nil && (applyErr == nil || info.Hash != "") {
		h.cfg.SchemaStored(graphqlkit.Kind, name)
	}
	httpjson.WriteJSON(w, http.StatusOK, info)
}

// apply installs the schema: from the body when there is one, from the
// endpoint when there is not.
func (*handler) apply(ctx context.Context, tk *graphqlkit.Toolkit, name string, payload []byte) error {
	if isReread(payload) {
		//nolint:wrapcheck // the toolkit's message is already operator-facing
		return tk.RefreshSchema(ctx, name)
	}
	//nolint:wrapcheck // as above
	return tk.SetSchema(ctx, name, payload)
}

// isReread reports a request that asks the platform to read the schema
// from the endpoint rather than supplying one. Its failure is the
// upstream's answer, recorded on the connection and reported as state
// with a 200: a 5xx here was replaced by the CDN in front of a
// deployment, and the browser that pressed Re-read got a status code
// with no sentence (#1704). A supplied schema that does not parse is the
// caller's input, recorded nowhere, and stays a 400.
func isReread(payload []byte) bool {
	return strings.TrimSpace(string(payload)) == ""
}

// toolkitFor finds the live graphql toolkit holding a connection.
func (h *handler) toolkitFor(name string) *graphqlkit.Toolkit {
	if name == "" {
		return nil
	}
	for _, tk := range h.cfg.Toolkits() {
		gql, ok := tk.(*graphqlkit.Toolkit)
		if ok && gql.HasConnection(name) {
			return gql
		}
	}
	return nil
}
