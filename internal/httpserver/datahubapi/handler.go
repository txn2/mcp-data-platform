package datahubapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// DataHub tool names used for per-persona authorization.
const (
	datahubCreateTool = "datahub_create"
	datahubUpdateTool = "datahub_update"
	datahubDeleteTool = "datahub_delete"
	datahubToolPrefix = "datahub_"
)

// Error messages.
const (
	errAuthRequired          = "authentication required"
	errInvalidRequestBody    = "invalid request body"
	errUnknownDataHubConn    = "unknown datahub connection"
	errDataHubReadOnlyConn   = "datahub connection is read-only"
	errDataHubWriteForbidden = "this operation requires the matching datahub tool grant"
	errDataHubReadForbidden  = "this connection requires datahub access on your persona"
	errDataHubURNRequired    = "urn is required"
	// errNameRequired is the 400 for a create whose name is missing, shared by
	// every named governance entity (glossary node, tag, domain).
	errNameRequired = "name is required"
)

// List/body bounds.
const (
	datahubDefaultLimit = 25
	datahubMaxLimit     = 200
	documentURNPrefix   = "urn:li:document:"

	qpLimit  = "limit"
	qpOffset = "offset"

	// fieldDomain labels the domain edit in audit records and error messages.
	fieldDomain = "domain"
)

// contextDocEntityTypes is the set of entity types a context document can attach
// to upstream (mcp-datahub context_documents.go). A create against any other type
// is rejected with a clear 4xx rather than forwarded to a 500.
var contextDocEntityTypes = map[string]bool{
	"dataset":      true,
	"glossaryTerm": true,
	"glossaryNode": true,
	"container":    true,
}

// Deps holds the handler dependencies. Bridge is required; Audit and
// PersonaResolver are optional (nil disables auditing / treats no persona as
// admin-only access).
type Deps struct {
	Bridge          Bridge
	PersonaResolver portal.PersonaResolver
	AdminRoles      []string
	Audit           audit.Logger
}

// Handler serves the portal DataHub REST endpoints.
type Handler struct {
	deps Deps
}

// NewHandler creates a DataHub REST handler.
func NewHandler(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// Register wires the DataHub REST endpoints (#718) onto the portal mux. Reads are
// gated on DataHub access on the persona; writes are gated per-persona
// (datahub_create/update/delete) and require a write-enabled connection.
func (h *Handler) Register(mux *http.ServeMux) {
	const base = "/api/v1/portal/datahub"

	mux.HandleFunc("GET "+base+"/connections", h.listConnections)

	mux.HandleFunc("GET "+base+"/{conn}/catalog/search", h.searchCatalog)
	mux.HandleFunc("GET "+base+"/{conn}/catalog/browse", h.browseCatalog)
	mux.HandleFunc("GET "+base+"/{conn}/catalog/entity", h.getCatalogEntity)
	mux.HandleFunc("GET "+base+"/{conn}/catalog/lookup/tags", h.lookupTags)
	mux.HandleFunc("GET "+base+"/{conn}/catalog/lookup/glossary-terms", h.lookupGlossaryTerms)
	mux.HandleFunc("GET "+base+"/{conn}/catalog/lookup/domains", h.lookupDomains)
	// Glossary hierarchy and editing (#1155, #1158). See glossary.go.
	h.glossaryRoutes(mux, base)
	// Governance vocabularies (#1156, #1157): define and retire a tag or a domain.
	// Only the writes are routes; every read each surface needs is an existing
	// route (the picker lookups above, the catalog search's tag/domain filters,
	// the entity description and domain writes below). See vocabulary.go.
	h.vocabularyRoutes(mux, base, "tags", tagVocabulary)
	h.vocabularyRoutes(mux, base, "domains", domainVocabulary)
	mux.HandleFunc("GET "+base+"/{conn}/catalog/entity/documents", h.getEntityDocuments)
	mux.HandleFunc("PUT "+base+"/{conn}/catalog/entity/description", h.updateCatalogDescription)
	mux.HandleFunc("PUT "+base+"/{conn}/catalog/entity/tags", h.updateCatalogTags)
	mux.HandleFunc("PUT "+base+"/{conn}/catalog/entity/owners", h.updateCatalogOwners)
	mux.HandleFunc("PUT "+base+"/{conn}/catalog/entity/glossary-terms", h.updateCatalogGlossaryTerms)
	mux.HandleFunc("PUT "+base+"/{conn}/catalog/entity/domain", h.updateCatalogDomain)

	mux.HandleFunc("GET "+base+"/{conn}/documents/search", h.searchDocuments)
	mux.HandleFunc("GET "+base+"/{conn}/documents/browse", h.browseDocuments)
	mux.HandleFunc("GET "+base+"/{conn}/documents/{id}", h.getDocument)
	mux.HandleFunc("POST "+base+"/{conn}/documents", h.createDocument)
	mux.HandleFunc("PUT "+base+"/{conn}/documents/{id}", h.updateDocument)
	mux.HandleFunc("DELETE "+base+"/{conn}/documents/{id}", h.deleteDocument)
}

// --- authorization ---

func (h *Handler) userIsAdmin(user *portal.User) bool {
	if user == nil {
		return false
	}
	for _, role := range user.Roles {
		if slices.Contains(h.deps.AdminRoles, role) {
			return true
		}
	}
	return false
}

// userHasTool reports whether the user's persona grants the named tool or the
// user is an admin.
func (h *Handler) userHasTool(user *portal.User, tool string) bool {
	if user == nil {
		return false
	}
	if h.deps.PersonaResolver != nil {
		if info := h.deps.PersonaResolver(user.Roles); info != nil && slices.Contains(info.Tools, tool) {
			return true
		}
	}
	return h.userIsAdmin(user)
}

// userHasDataHubReadAccess reports whether the persona grants any DataHub tool
// (read or write) or the user is an admin. Reads are gated on this so the portal
// never discloses more than the persona-filtered MCP surface would.
func (h *Handler) userHasDataHubReadAccess(user *portal.User) bool {
	if user == nil {
		return false
	}
	if h.deps.PersonaResolver != nil {
		if info := h.deps.PersonaResolver(user.Roles); info != nil {
			for _, t := range info.Tools {
				if strings.HasPrefix(t, datahubToolPrefix) {
					return true
				}
			}
		}
	}
	return h.userIsAdmin(user)
}

// dataHubReader resolves the read surface for the connection in the path, writing
// the error and returning ok=false when the request must stop. Reads require an
// authenticated user whose persona grants DataHub access plus a known connection.
func (h *Handler) dataHubReader(w http.ResponseWriter, r *http.Request) (Reader, bool) {
	user := portal.GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, false
	}
	if !h.userHasDataHubReadAccess(user) {
		writeError(w, http.StatusForbidden, errDataHubReadForbidden)
		return nil, false
	}
	reader, ok := h.deps.Bridge.Reader(r.PathValue("conn"))
	if !ok {
		writeError(w, http.StatusNotFound, errUnknownDataHubConn)
		return nil, false
	}
	return reader, true
}

// writeAuth carries the resolved principal, connection, and write surface for an
// authorized mutation.
type writeAuth struct {
	writer Writer
	user   *portal.User
	conn   string
}

// authorizeWrite enforces the full write gate: authenticated user, known
// connection, matching persona tool grant, and a write-enabled connection.
func (h *Handler) authorizeWrite(w http.ResponseWriter, r *http.Request, tool string) (*writeAuth, bool) {
	user := portal.GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, false
	}
	conn := r.PathValue("conn")
	if _, isConn := h.deps.Bridge.Reader(conn); !isConn {
		writeError(w, http.StatusNotFound, errUnknownDataHubConn)
		return nil, false
	}
	if !h.userHasTool(user, tool) {
		writeError(w, http.StatusForbidden, errDataHubWriteForbidden)
		return nil, false
	}
	writer, ok := h.deps.Bridge.Writer(conn)
	if !ok {
		writeError(w, http.StatusForbidden, errDataHubReadOnlyConn)
		return nil, false
	}
	return &writeAuth{writer: writer, user: user, conn: conn}, true
}

// audit records a portal DataHub mutation. Best-effort: a logging failure is
// warned and swallowed so it never fails the originating request.
func (h *Handler) audit(r *http.Request, a *writeAuth, tool string, params map[string]any, opErr error) {
	if h.deps.Audit == nil {
		return
	}
	ev := audit.NewEvent(tool)
	ev.UserID = a.user.UserID
	ev.UserEmail = a.user.Email
	if h.deps.PersonaResolver != nil {
		if info := h.deps.PersonaResolver(a.user.Roles); info != nil {
			ev.Persona = info.Name
		}
	}
	ev.ToolkitKind = "datahub"
	ev.ToolkitName = a.conn
	ev.Connection = a.conn
	ev.Parameters = params
	ev.Source = "portal"
	ev.Transport = "http"
	ev.EventKind = audit.EventTypeMCPToolCall
	ev.Authorized = true
	ev.Success = opErr == nil
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := h.deps.Audit.Log(r.Context(), *ev); err != nil {
		slog.Warn("portal datahub audit log failed", "error", err, "tool", tool, "connection", a.conn)
	}
}

// --- read handlers ---

func (h *Handler) listConnections(w http.ResponseWriter, r *http.Request) {
	user := portal.GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	// A persona without DataHub access sees no connections (so the UI hides the
	// tabs) rather than a 403.
	conns := []Connection{}
	if h.userHasDataHubReadAccess(user) {
		if c := h.deps.Bridge.Connections(); c != nil {
			conns = c
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": conns})
}

func (h *Handler) searchCatalog(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	filter := semantic.SearchFilter{
		Query:    q.Get("q"),
		Platform: q.Get("platform"),
		Domain:   q.Get("domain"),
		Owner:    q.Get("owner"),
		Tags:     queryValues(r, "tags"),
		Filters:  glossaryTermFilters(q),
		Limit:    clampLimit(q.Get(qpLimit)),
		Offset:   parseOffset(q.Get(qpOffset)),
	}
	results, err := reader.SearchTables(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusBadGateway, "catalog search failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *Handler) browseCatalog(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	filter := semantic.SearchFilter{
		Query:  "*",
		Limit:  clampLimit(r.URL.Query().Get(qpLimit)),
		Offset: parseOffset(r.URL.Query().Get(qpOffset)),
	}
	results, err := reader.SearchTables(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusBadGateway, "catalog browse failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// catalogEntityResponse is the entity-detail read: table context plus columns.
type catalogEntityResponse struct {
	URN     string                             `json:"urn"`
	Context *semantic.TableContext             `json:"context"`
	Columns map[string]*semantic.ColumnContext `json:"columns,omitempty"`
}

func (h *Handler) getCatalogEntity(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	urn := strings.TrimSpace(r.URL.Query().Get("urn"))
	if urn == "" {
		writeError(w, http.StatusBadRequest, errDataHubURNRequired)
		return
	}
	id, err := reader.ResolveURN(r.Context(), urn)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid dataset urn: "+err.Error())
		return
	}
	tableCtx, err := reader.GetTableContext(r.Context(), *id)
	if err != nil {
		writeError(w, http.StatusBadGateway, "entity read failed: "+err.Error())
		return
	}
	columns, err := reader.GetColumnsContext(r.Context(), *id)
	if err != nil {
		// Columns are supplementary; a failure there should not fail the read.
		slog.Warn("portal catalog entity: columns read failed", "urn", logsan.SanitizeForLog(urn), "error", err)
		columns = nil
	}
	writeJSON(w, http.StatusOK, catalogEntityResponse{URN: urn, Context: tableCtx, Columns: columns})
}

// getEntityDocuments returns the context documents attached to a catalog entity
// (#1158). It is the one document read the browse and search routes cannot
// express: both are corpus-wide, and neither is scoped to what a given dataset,
// glossary term, or glossary node carries.
func (h *Handler) getEntityDocuments(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	urn := strings.TrimSpace(r.URL.Query().Get("urn"))
	if urn == "" {
		writeError(w, http.StatusBadRequest, errDataHubURNRequired)
		return
	}
	docs, err := reader.GetRelatedDocuments(r.Context(), urn)
	if err != nil {
		writeError(w, http.StatusBadGateway, "entity documents read failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": orEmpty(docs)})
}

// --- catalog metadata pickers (#785) ---

// lookupTags name-searches DataHub tags for the tag picker so a user selects a
// tag by name and the UI resolves it to a urn:li:tag URN. Gated as a read.
func (h *Handler) lookupTags(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	refs, err := reader.SearchTags(r.Context(), r.URL.Query().Get("q"), clampLimit(r.URL.Query().Get(qpLimit)))
	if err != nil {
		writeError(w, http.StatusBadGateway, "tag lookup failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": refs})
}

// lookupGlossaryTerms name-searches DataHub glossary terms for the glossary picker.
func (h *Handler) lookupGlossaryTerms(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	refs, err := reader.SearchGlossaryTerms(r.Context(), r.URL.Query().Get("q"), clampLimit(r.URL.Query().Get(qpLimit)))
	if err != nil {
		writeError(w, http.StatusBadGateway, "glossary term lookup failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": refs})
}

// lookupDomains lists DataHub domains for the domain picker. DataHub has no
// name-scoped domain search, so the full list is returned and the picker filters
// client-side.
func (h *Handler) lookupDomains(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	refs, err := reader.ListDomains(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "domain lookup failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": refs})
}

// requireURNParam reads and validates the urn query parameter, writing the 400
// and returning ok=false when it is missing or not one of allowedTypes. Rejecting
// a URN of the wrong kind here keeps it from reaching DataHub and coming back as
// a 502.
func requireURNParam(w http.ResponseWriter, r *http.Request, allowedTypes []string) (string, bool) {
	urn := strings.TrimSpace(r.URL.Query().Get("urn"))
	if urn == "" {
		writeError(w, http.StatusBadRequest, errDataHubURNRequired)
		return "", false
	}
	if !isURNOfType(urn, allowedTypes) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid urn: %q must be a %s", urn, urnHint(allowedTypes)))
		return "", false
	}
	return urn, true
}

func (h *Handler) searchDocuments(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q parameter is required")
		return
	}
	docs, err := reader.SearchDocuments(r.Context(), q, clampLimit(r.URL.Query().Get(qpLimit)))
	if err != nil {
		writeError(w, http.StatusBadGateway, "document search failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": docs})
}

func (h *Handler) browseDocuments(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	docs, total, err := reader.BrowseDocuments(r.Context(),
		parseOffset(r.URL.Query().Get(qpOffset)), clampLimit(r.URL.Query().Get(qpLimit)))
	if err != nil {
		writeError(w, http.StatusBadGateway, "document browse failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": docs, "total": total})
}

func (h *Handler) getDocument(w http.ResponseWriter, r *http.Request) {
	reader, ok := h.dataHubReader(w, r)
	if !ok {
		return
	}
	doc, err := reader.GetDocument(r.Context(), documentURN(r.PathValue("id")))
	if err != nil {
		writeError(w, http.StatusBadGateway, "document read failed: "+err.Error())
		return
	}
	if doc == nil {
		writeError(w, http.StatusNotFound, "context document not found")
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// --- catalog write handlers ---

// catalogChangeRequest is the shared payload for every catalog metadata edit.
type catalogChangeRequest struct {
	URN         string        `json:"urn"`
	Description string        `json:"description,omitempty"`
	Add         []string      `json:"add,omitempty"`
	Remove      []string      `json:"remove,omitempty"`
	AddOwners   []OwnerChange `json:"add_owners,omitempty"`
	Domain      string        `json:"domain,omitempty"`
	ClearDomain bool          `json:"clear_domain,omitempty"`
}

// normalize trims surrounding whitespace from the URN and every add/remove/owner/
// domain value so validation and the forwarded write operate on the same clean
// value. Empty entries left after trimming are dropped.
func (req *catalogChangeRequest) normalize() {
	req.URN = strings.TrimSpace(req.URN)
	req.Domain = strings.TrimSpace(req.Domain)
	req.Add = trimNonEmpty(req.Add)
	req.Remove = trimNonEmpty(req.Remove)
	for i := range req.AddOwners {
		req.AddOwners[i].OwnerURN = strings.TrimSpace(req.AddOwners[i].OwnerURN)
		req.AddOwners[i].OwnershipType = strings.TrimSpace(req.AddOwners[i].OwnershipType)
	}
}

// trimNonEmpty trims each element and drops any that become empty, returning nil
// for an all-empty input so an add/remove of only blanks is a no-op, not a value.
func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if t := strings.TrimSpace(v); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// catalogChangeAuditParams records what changed so the audit trail captures the
// mutation, not just that one occurred. Description content is not logged.
func catalogChangeAuditParams(field string, req catalogChangeRequest) map[string]any {
	params := map[string]any{"urn": req.URN, "field": field}
	if len(req.Add) > 0 {
		params["add"] = req.Add
	}
	if len(req.Remove) > 0 {
		params["remove"] = req.Remove
	}
	if len(req.AddOwners) > 0 {
		params["add_owners"] = req.AddOwners
	}
	if field == fieldDomain {
		params[fieldDomain] = req.Domain
		params["clear"] = req.ClearDomain
	}
	return params
}

// applyCatalogChange runs the shared authorize -> decode -> require-URN ->
// validate -> mutate -> audit -> respond flow. validate, when non-nil, returns a
// 400 message for a well-formed but semantically invalid request.
func (h *Handler) applyCatalogChange(w http.ResponseWriter, r *http.Request, field string,
	validate func(catalogChangeRequest) string,
	op func(writer Writer, req catalogChangeRequest) error,
) {
	auth, ok := h.authorizeWrite(w, r, datahubUpdateTool)
	if !ok {
		return
	}
	var req catalogChangeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	// Normalize before validating so the value the validator checks is exactly the
	// value the writer forwards to DataHub (a whitespace-padded URN would otherwise
	// pass the trimmed validity check but be rejected by DataHub's URN parser).
	req.normalize()
	if req.URN == "" {
		writeError(w, http.StatusBadRequest, errDataHubURNRequired)
		return
	}
	if validate != nil {
		if msg := validate(req); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}
	err := op(auth.writer, req)
	h.audit(r, auth, datahubUpdateTool, catalogChangeAuditParams(field, req), err)
	if err != nil {
		writeError(w, http.StatusBadGateway, "update "+field+" failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) updateCatalogDescription(w http.ResponseWriter, r *http.Request) {
	h.applyCatalogChange(w, r, "description", nil, func(writer Writer, req catalogChangeRequest) error {
		return writer.UpdateDescription(r.Context(), req.URN, req.Description)
	})
}

func (h *Handler) updateCatalogTags(w http.ResponseWriter, r *http.Request) {
	// A malformed value (e.g. "test") is a client error: reject it with a 400 here
	// rather than forwarding it to DataHub, which would surface as a misleading 502.
	validate := func(req catalogChangeRequest) string {
		return validateURNValues("tag", tagURNTypes, req.Add, req.Remove)
	}
	h.applyCatalogChange(w, r, "tags", validate, func(writer Writer, req catalogChangeRequest) error {
		return writer.ApplyTagChanges(r.Context(), req.URN, req.Add, req.Remove)
	})
}

func (h *Handler) updateCatalogGlossaryTerms(w http.ResponseWriter, r *http.Request) {
	validate := func(req catalogChangeRequest) string {
		return validateURNValues("glossary term", glossaryURNTypes, req.Add, req.Remove)
	}
	h.applyCatalogChange(w, r, "glossary_terms", validate, func(writer Writer, req catalogChangeRequest) error {
		return writer.ApplyGlossaryTermChanges(r.Context(), req.URN, req.Add, req.Remove)
	})
}

func (h *Handler) updateCatalogOwners(w http.ResponseWriter, r *http.Request) {
	h.applyCatalogChange(w, r, "owners", validateOwnerChange, func(writer Writer, req catalogChangeRequest) error {
		return writer.ApplyOwnerChanges(r.Context(), req.URN, req.AddOwners, req.Remove)
	})
}

func (h *Handler) updateCatalogDomain(w http.ResponseWriter, r *http.Request) {
	// A set request (clear_domain=false) with an empty or malformed domain is
	// rejected with a 400 rather than silently unsetting the domain or forwarding a
	// bad value to DataHub (which would surface as a 502).
	validate := func(req catalogChangeRequest) string {
		if req.ClearDomain {
			return ""
		}
		if req.Domain == "" {
			return "domain is required unless clear_domain is set"
		}
		return validateURNValues(fieldDomain, domainURNTypes, []string{req.Domain})
	}
	h.applyCatalogChange(w, r, fieldDomain, validate, func(writer Writer, req catalogChangeRequest) error {
		if req.ClearDomain {
			return writer.UnsetDomain(r.Context(), req.URN)
		}
		return writer.SetDomain(r.Context(), req.URN, req.Domain)
	})
}

// --- context-document write handlers ---

// documentRequest is the context-document create/update payload.
type documentRequest struct {
	EntityURN string `json:"entity_urn,omitempty"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Category  string `json:"category,omitempty"`
}

func (h *Handler) createDocument(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorizeWrite(w, r, datahubCreateTool)
	if !ok {
		return
	}
	var req documentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	entityURN := strings.TrimSpace(req.EntityURN)
	if entityURN == "" {
		writeError(w, http.StatusBadRequest, "entity_urn is required to create a context document")
		return
	}
	if !contextDocEntityTypes[datahubEntityType(entityURN)] {
		writeError(w, http.StatusBadRequest,
			"context documents can only attach to dataset, glossaryTerm, glossaryNode, or container entities")
		return
	}
	doc, err := auth.writer.UpsertContextDocument(r.Context(), DocumentInput{
		EntityURN: entityURN,
		Title:     req.Title,
		Content:   req.Content,
		Category:  req.Category,
	})
	h.audit(r, auth, datahubCreateTool, map[string]any{"entity_urn": entityURN, "title": req.Title}, err)
	if err != nil {
		writeError(w, http.StatusBadGateway, "create context document failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, doc)
}

func (h *Handler) updateDocument(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorizeWrite(w, r, datahubUpdateTool)
	if !ok {
		return
	}
	id := bareDocumentID(r.PathValue("id"))
	var req documentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	doc, err := auth.writer.UpsertContextDocument(r.Context(), DocumentInput{
		ID:       id,
		Title:    req.Title,
		Content:  req.Content,
		Category: req.Category,
	})
	h.audit(r, auth, datahubUpdateTool, map[string]any{"document_id": id, "title": req.Title}, err)
	if err != nil {
		writeError(w, http.StatusBadGateway, "update context document failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h *Handler) deleteDocument(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorizeWrite(w, r, datahubDeleteTool)
	if !ok {
		return
	}
	id := bareDocumentID(r.PathValue("id"))
	err := auth.writer.DeleteContextDocument(r.Context(), id)
	h.audit(r, auth, datahubDeleteTool, map[string]any{"document_id": id}, err)
	if err != nil {
		writeError(w, http.StatusBadGateway, "delete context document failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- helpers ---

// decodeBody decodes a bounded JSON request body (context-document content is
// otherwise unbounded), writing a 400 and returning false on failure.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	limited := io.LimitReader(r.Body, portal.MaxContentUploadBytes+64<<10)
	if err := json.NewDecoder(limited).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return false
	}
	return true
}

// URN entity types accepted by each catalog metadata field's validator. Owners
// may be a user or a group, matching DataHub's ownership model.
var (
	tagURNTypes      = []string{"tag"}
	glossaryURNTypes = []string{"glossaryTerm"}
	domainURNTypes   = []string{"domain"}
	ownerURNTypes    = []string{"corpuser", "corpGroup"}

	// Glossary hierarchy reads (#1155). Children hang off a node only; a parent
	// chain exists for either kind of glossary entity.
	glossaryNodeURNTypes   = []string{"glossaryNode"}
	glossaryEntityURNTypes = []string{"glossaryTerm", "glossaryNode"}
)

// validateURNValues returns a human-readable 400 message if any value across the
// given lists is not a well-formed DataHub URN of one of the allowed entity types,
// or "" when every value is valid. It is what turns a malformed picker/free-text
// value (e.g. "test") into a 400 instead of a forwarded 502 (#785). label names
// the field in the message.
func validateURNValues(label string, allowedTypes []string, lists ...[]string) string {
	for _, list := range lists {
		for _, v := range list {
			if !isURNOfType(strings.TrimSpace(v), allowedTypes) {
				return fmt.Sprintf("invalid %s: %q must be a %s", label, v, urnHint(allowedTypes))
			}
		}
	}
	return ""
}

// validateOwnerChange validates the owner-specific payload: each added owner's URN
// and each removed owner URN must be a well-formed corpuser or corpGroup URN.
func validateOwnerChange(req catalogChangeRequest) string {
	for _, o := range req.AddOwners {
		if !isURNOfType(strings.TrimSpace(o.OwnerURN), ownerURNTypes) {
			return fmt.Sprintf("invalid owner: %q must be a %s", o.OwnerURN, urnHint(ownerURNTypes))
		}
	}
	return validateURNValues("owner", ownerURNTypes, req.Remove)
}

// isURNOfType reports whether s is a well-formed "urn:li:<type>:<id>" URN for one
// of the allowed entity types, requiring a non-empty id so "urn:li:tag:" is rejected.
func isURNOfType(s string, allowedTypes []string) bool {
	for _, t := range allowedTypes {
		prefix := "urn:li:" + t + ":"
		if rest, ok := strings.CutPrefix(s, prefix); ok && strings.TrimSpace(rest) != "" {
			return true
		}
	}
	return false
}

// urnHint renders the allowed URN forms for an error message, e.g.
// "urn:li:corpuser:<id> or urn:li:corpGroup:<id> URN".
func urnHint(allowedTypes []string) string {
	parts := make([]string, len(allowedTypes))
	for i, t := range allowedTypes {
		parts[i] = "urn:li:" + t + ":<id>"
	}
	return strings.Join(parts, " or ") + " URN"
}

// datahubEntityType extracts the entity-type segment of a DataHub URN
// (e.g. "urn:li:dataset:(...)" -> "dataset"), or "" when the URN is malformed.
func datahubEntityType(urn string) string {
	rest, ok := strings.CutPrefix(urn, "urn:li:")
	if !ok {
		return ""
	}
	if t, _, found := strings.Cut(rest, ":"); found {
		return t
	}
	return ""
}

// documentURN builds a context-document URN from its id, tolerating a prefixed id.
func documentURN(id string) string {
	if strings.HasPrefix(id, documentURNPrefix) {
		return id
	}
	return documentURNPrefix + id
}

// bareDocumentID strips the document URN prefix so update/delete accept both a
// bare id and the full urn:li:document:<id> form the reads return.
func bareDocumentID(id string) string {
	return strings.TrimPrefix(id, documentURNPrefix)
}

// clampLimit parses a limit query value, defaulting when absent/invalid and
// capping at the maximum.
func clampLimit(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return datahubDefaultLimit
	}
	if n > datahubMaxLimit {
		return datahubMaxLimit
	}
	return n
}

// parseOffset parses a non-negative offset query value, defaulting to 0.
func parseOffset(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// queryValues returns trimmed, non-empty values for a query parameter, accepting
// repeated keys and comma-separated lists.
func queryValues(r *http.Request, key string) []string {
	raw := r.URL.Query()[key]
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		for part := range strings.SplitSeq(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// problemDetail mirrors the portal's RFC 9457 error body so the DataHub endpoints
// return the same shape as the rest of the portal API.
type problemDetail struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: msg,
	})
}
