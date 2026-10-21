// Package catalogapi is the portal's REST surface over the platform catalog
// (#1921): finding and browsing datasets, reading a dataset with its columns,
// curation, lineage and history, editing that curation, and the glossary, tags,
// domains and data products it is curated with.
//
// Reading the catalog takes the persona's fetch grant and writing it the
// apply_knowledge grant, the two tools that read and curate it over MCP, so
// the portal never shows or changes more than the persona-filtered MCP surface
// would. A dataset, its columns and its schema document belong to a
// connection, and are read and written only by a caller whose persona reaches
// that connection. An administrator reaches everything.
package catalogapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// The tools whose grants open the catalog to a persona.
const (
	readTool  = "fetch"
	writeTool = "apply_knowledge"
)

// prefix is where the routes are mounted.
const prefix = "/api/v1/portal/catalog"

// maxBodyBytes bounds a request body; a context document is the largest
// value an aspect carries.
const maxBodyBytes = 1 << 20

// Connection is one connection the catalog holds datasets for.
type Connection struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Store is the catalog the handler reads and writes, with the connections'
// schema documents. catalogstore.Store implements it.
type Store interface {
	catalog.Store
	GetDocument(ctx context.Context, ref catalog.Ref) (*catalog.SchemaDocument, error)
}

// Deps holds what the handler reads and writes through.
type Deps struct {
	Store Store
	// Connections lists the connections the catalog holds datasets for.
	Connections func() []Connection
	// PersonaResolver resolves a caller's roles to the persona's tools and
	// connection reach; nil leaves the catalog to administrators.
	PersonaResolver portal.PersonaResolver
	// AdminRoles are the admin persona's roles, read only when IsAdmin is nil.
	AdminRoles []string
	// IsAdmin is the platform's one administrator rule (#2228).
	IsAdmin func(roles []string) bool
	// Audit records each write; nil records none.
	Audit audit.Logger
}

// Handler serves the portal catalog routes.
type Handler struct {
	deps Deps
}

// New returns the handler.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// Register mounts the routes on the portal mux, behind the portal's own
// authentication.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+prefix+"/connections", h.listConnections)
	mux.HandleFunc("GET "+prefix+"/datasets", h.searchDatasets)
	mux.HandleFunc("GET "+prefix+"/browse", h.browse)
	mux.HandleFunc("GET "+prefix+"/dataset", h.getDataset)
	mux.HandleFunc("GET "+prefix+"/schema-document", h.getSchemaDocument)
	mux.HandleFunc("GET "+prefix+"/history", h.history)
	mux.HandleFunc("PUT "+prefix+"/aspect", h.writeAspect)
	mux.HandleFunc("POST "+prefix+"/rollback", h.rollback)
	mux.HandleFunc("GET "+prefix+"/definitions", h.listDefinitions)
	mux.HandleFunc("POST "+prefix+"/definitions", h.createDefinition)
	mux.HandleFunc("GET "+prefix+"/definition", h.getDefinition)
	mux.HandleFunc("DELETE "+prefix+"/definition", h.deleteDefinition)
}

// caller is the signed-in reader of one request and what their persona grants.
type caller struct {
	user  *portal.User
	info  *portal.PersonaInfo
	admin bool
}

// canWrite reports whether the caller may curate the catalog.
func (c *caller) canWrite() bool {
	return c.admin || (c.info != nil && slices.Contains(c.info.Tools, writeTool))
}

// reaches reports whether the caller may see a catalog reference: one naming
// no connection always, one naming a connection when the persona reaches it.
func (c *caller) reaches(reference string) bool {
	if c.admin {
		return true
	}
	if c.info == nil {
		return false
	}
	return c.info.ReachesCatalogRef == nil || c.info.ReachesCatalogRef(reference)
}

// reader resolves the caller of a read, writing 401 or 403 and returning
// false when the request stops here.
func (h *Handler) reader(w http.ResponseWriter, r *http.Request) (*caller, bool) {
	user := portal.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
		return nil, false
	}
	admin := access.HasAnyRole(user.Roles, h.deps.AdminRoles)
	if h.deps.IsAdmin != nil {
		admin = h.deps.IsAdmin(user.Roles)
	}
	c := &caller{user: user, admin: admin}
	if h.deps.PersonaResolver != nil {
		c.info = h.deps.PersonaResolver(user.Roles)
	}
	if !c.admin && (c.info == nil || !slices.Contains(c.info.Tools, readTool)) {
		httpjson.WriteError(w, http.StatusForbidden, "reading the catalog takes the fetch tool on your persona")
		return nil, false
	}
	return c, true
}

// writer resolves the caller of a write, refusing one whose persona does not
// curate the catalog.
func (h *Handler) writer(w http.ResponseWriter, r *http.Request) (*caller, bool) {
	c, ok := h.reader(w, r)
	if !ok {
		return nil, false
	}
	if !c.canWrite() {
		httpjson.WriteError(w, http.StatusForbidden, "editing the catalog takes the apply_knowledge tool on your persona")
		return nil, false
	}
	return c, true
}

// ConnectionList is the connections the caller may browse, and whether the
// caller may edit the catalog.
type ConnectionList struct {
	Connections []Connection `json:"connections"`
	Writable    bool         `json:"writable"`
}

// listConnections handles GET /api/v1/portal/catalog/connections.
//
// @Summary      List catalog connections
// @Description  Returns the connections the platform catalog holds datasets for that the caller's persona reaches, and whether the caller may edit the catalog (the apply_knowledge tool on their persona).
// @Tags         Data Catalog
// @Produce      json
// @Success      200  {object}  ConnectionList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/catalog/connections [get]
func (h *Handler) listConnections(w http.ResponseWriter, r *http.Request) {
	c, ok := h.reader(w, r)
	if !ok {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, ConnectionList{Connections: h.reachable(c), Writable: c.canWrite()})
}

// reachable lists the catalog's connections the caller reaches.
func (h *Handler) reachable(c *caller) []Connection {
	out := make([]Connection, 0)
	if h.deps.Connections == nil {
		return out
	}
	for _, conn := range h.deps.Connections() {
		if c.reaches(probeRef(conn)) {
			out = append(out, conn)
		}
	}
	return out
}

// probeRef is a dataset reference on a connection, which the persona's reach
// is asked about to learn whether it reaches the connection.
func probeRef(c Connection) string {
	return catalog.DatasetRef(c.Kind, c.Name, "-").String()
}

// parseRef reads the ref query parameter, writing 400 when it is not a
// catalog reference.
func parseRef(w http.ResponseWriter, r *http.Request) (catalog.Ref, bool) {
	ref, err := catalog.ParseRef(r.URL.Query().Get("ref"))
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "ref must be an mcp: catalog reference: "+err.Error())
		return catalog.Ref{}, false
	}
	return ref, true
}

// writeStoreError maps a store error onto its status. A refusal the reader
// can act on (a definition still in use, a rollback a later write superseded,
// an edit made against a stale read) is a 409 saying what to do.
func writeStoreError(w http.ResponseWriter, err error, what string) {
	var inUse *catalog.InUseError
	var conflict *catalog.ConflictError
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, what+" not found")
	case errors.As(err, &inUse):
		httpjson.WriteError(w, http.StatusConflict, inUse.Error())
	case errors.As(err, &conflict):
		httpjson.WriteError(w, http.StatusConflict, conflict.Error())
	case errors.Is(err, catalog.ErrStale):
		httpjson.WriteError(w, http.StatusConflict, "this was changed since you read it; reload to see the change, then edit again")
	case errors.Is(err, catalog.ErrInvalidRef), errors.Is(err, catalog.ErrInvalidAspect),
		errors.Is(err, catalog.ErrAspectNotAllowed), errors.Is(err, catalog.ErrInvalidDefinition):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		slog.Error("portal catalog: store error", "what", what, "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "reading or writing the catalog failed")
	}
}

// decodeBody reads a JSON body into v, writing 400 when it does not decode.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// audit records one catalog write as the apply_knowledge call it stands for,
// made from the portal. A failure to record is logged, never the request's.
func (h *Handler) audit(ctx context.Context, c *caller, action string, params map[string]any, opErr error) {
	if h.deps.Audit == nil {
		return
	}
	ev := audit.NewEvent(writeTool)
	ev.UserID, ev.UserEmail = c.user.UserID, c.user.Email
	if c.info != nil {
		ev.Persona = c.info.Name
	}
	ev.ToolkitKind = "knowledge"
	params["action"] = action
	ev.Parameters = params
	ev.Source, ev.Transport = "portal", "http"
	ev.EventKind = audit.EventTypeMCPToolCall
	ev.Authorized, ev.Success = true, opErr == nil
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := h.deps.Audit.Log(ctx, *ev); err != nil {
		slog.WarnContext(ctx, "portal catalog: audit log failed", "error", logsan.SanitizeForLog(err.Error()), "action", action)
	}
}

// splitPath reads a dotted path parameter in reference form into segments.
func splitPath(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	path, err := catalog.ParsePath(s)
	if err != nil {
		return nil, fmt.Errorf("parsing path %q: %w", s, err)
	}
	return path, nil
}
