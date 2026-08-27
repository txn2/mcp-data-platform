package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/platform/personastore"
)

// personaSummary is a lightweight persona representation for list responses.
//
// Roles is declared []string and ships as a JSON array, NEVER null. The
// UI iterates p.roles directly (RolesPage, PersonasPanel) so a wire-level
// null crashes the persona list. MarshalJSON enforces this invariant.
type personaSummary struct {
	Name        string   `json:"name" example:"data-engineer"`
	DisplayName string   `json:"display_name" example:"Data Engineer"`
	Description string   `json:"description,omitempty" example:"ETL pipeline development and schema management"`
	Roles       []string `json:"roles" example:"data_engineer"`
	ToolCount   int      `json:"tool_count" example:"19"`
	Source      string   `json:"source,omitempty" example:"file"` // "file", "database", or "both"
}

// MarshalJSON enforces the non-nil wire invariant for Roles.
func (p personaSummary) MarshalJSON() ([]byte, error) {
	type alias personaSummary
	v := alias(p)
	if v.Roles == nil {
		v.Roles = []string{}
	}
	return json.Marshal(v) //nolint:wrapcheck // value struct of basic types cannot fail to marshal
}

// personaContextDetail holds context override fields nested under "context" in JSON.
type personaContextDetail struct {
	DescriptionPrefix         string `json:"description_prefix,omitempty" example:"This user is a data engineer responsible for ETL pipelines."`
	DescriptionOverride       string `json:"description_override,omitempty"`
	AgentInstructionsSuffix   string `json:"agent_instructions_suffix,omitempty" example:"Focus on schema details, data lineage, and query optimization."`
	AgentInstructionsOverride string `json:"agent_instructions_override,omitempty"`
}

// personaDetail includes resolved tool lists.
//
// Roles, AllowTools, DenyTools, and Tools ship as JSON arrays, NEVER
// null. The PersonaEditor UI accesses .length / .filter on these
// directly. AllowConnections / DenyConnections have omitempty so they
// are absent when nil rather than null. MarshalJSON enforces the
// non-nil invariant for the required arrays.
type personaDetail struct {
	Name             string                `json:"name" example:"data-engineer"`
	DisplayName      string                `json:"display_name" example:"Data Engineer"`
	Description      string                `json:"description,omitempty" example:"ETL pipeline development and schema management"`
	Roles            []string              `json:"roles" example:"data_engineer"`
	Priority         int                   `json:"priority" example:"10"`
	AllowTools       []string              `json:"allow_tools" example:"trino_*,datahub_*,s3_*"`
	DenyTools        []string              `json:"deny_tools" example:"trino_execute"`
	AllowConnections []string              `json:"allow_connections,omitempty"`
	DenyConnections  []string              `json:"deny_connections,omitempty"`
	Tools            []string              `json:"tools" example:"trino_query,trino_describe_table,datahub_search"`
	Context          *personaContextDetail `json:"context,omitempty"`
	Source           string                `json:"source,omitempty" example:"file"` // "file", "database", or "both"
	// APIRoutes are the persona's per-(connection, method, path) rules for
	// api-kind connections. Ships as a JSON array, NEVER null: the persona
	// editor's API-endpoint scope maps over it directly.
	APIRoutes []persona.APIRouteRule `json:"api_routes"`
}

// MarshalJSON enforces the non-nil wire invariant for the required arrays.
func (p personaDetail) MarshalJSON() ([]byte, error) {
	type alias personaDetail
	v := alias(p)
	if v.Roles == nil {
		v.Roles = []string{}
	}
	if v.AllowTools == nil {
		v.AllowTools = []string{}
	}
	if v.DenyTools == nil {
		v.DenyTools = []string{}
	}
	if v.Tools == nil {
		v.Tools = []string{}
	}
	if v.APIRoutes == nil {
		v.APIRoutes = []persona.APIRouteRule{}
	}
	return json.Marshal(v) //nolint:wrapcheck // value struct of basic types cannot fail to marshal
}

// personaCreateRequest is the request body for creating/updating a persona.
type personaCreateRequest struct {
	Name                      string   `json:"name" example:"viewer"`
	DisplayName               string   `json:"display_name" example:"Data Viewer"`
	Description               string   `json:"description,omitempty" example:"Read-only access to DataHub"`
	Roles                     []string `json:"roles" example:"viewer"`
	AllowTools                []string `json:"allow_tools" example:"datahub_*"`
	DenyTools                 []string `json:"deny_tools,omitempty"`
	AllowConnections          []string `json:"allow_connections,omitempty"`
	DenyConnections           []string `json:"deny_connections,omitempty"`
	Priority                  int      `json:"priority,omitempty" example:"0"`
	DescriptionPrefix         string   `json:"description_prefix,omitempty"`
	DescriptionOverride       string   `json:"description_override,omitempty"`
	AgentInstructionsSuffix   string   `json:"agent_instructions_suffix,omitempty"`
	AgentInstructionsOverride string   `json:"agent_instructions_override,omitempty"`
	// APIRoutes replaces the persona's API route rules wholesale. Absent
	// leaves the persona with none, which is the same "no rule touches this
	// connection" state a persona that never had any is in.
	APIRoutes []persona.APIRouteRule `json:"api_routes,omitempty"`
}

// personaListResponse wraps a list of personas.
type personaListResponse struct {
	Personas []personaSummary `json:"personas"`
	Total    int              `json:"total" example:"6"`
}

// listPersonas handles GET /api/v1/admin/personas.
//
// @Summary      List personas
// @Description  Returns all configured personas with tool counts.
// @Tags         Personas
// @Produce      json
// @Success      200  {object}  personaListResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/personas [get]
func (h *Handler) listPersonas(w http.ResponseWriter, _ *http.Request) {
	all := h.deps.PersonaRegistry.All()
	summaries := make([]personaSummary, 0, len(all))

	for _, p := range all {
		summaries = append(summaries, personaSummary{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			Description: p.Description,
			Roles:       p.Roles,
			ToolCount:   len(h.allowedTools(p)),
			Source:      p.Source,
		})
	}

	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Name < summaries[j].Name })
	writeJSON(w, http.StatusOK, personaListResponse{Personas: summaries, Total: len(summaries)})
}

// getPersona handles GET /api/v1/admin/personas/{name}.
//
// @Summary      Get persona
// @Description  Returns a single persona with resolved tool list.
// @Tags         Personas
// @Produce      json
// @Param        name  path  string  true  "Persona name"
// @Success      200  {object}  personaDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/personas/{name} [get]
func (h *Handler) getPersona(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, ok := h.deps.PersonaRegistry.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, "persona not found")
		return
	}

	writeJSON(w, http.StatusOK, toPersonaDetail(p, h.resolveTools(p)))
}

// createPersona handles POST /api/v1/admin/personas.
//
// @Summary      Create persona
// @Description  Creates a new persona. Only available in database config mode.
// @Tags         Personas
// @Accept       json
// @Produce      json
// @Param        body  body  personaCreateRequest  true  "Persona definition"
// @Success      201  {object}  personaDetail
// @Failure      400  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/personas [post]
func (h *Handler) createPersona(w http.ResponseWriter, r *http.Request) {
	var req personaCreateRequest
	if err := decodeStrict(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}

	// Check for existing persona with same name
	if _, exists := h.deps.PersonaRegistry.Get(req.Name); exists {
		writeError(w, http.StatusConflict, "persona already exists")
		return
	}

	p := buildPersonaFromRequest(req)
	p.Source = platform.SourceDatabase
	// The registry validates these rules too, but it runs AFTER the store
	// write below — a rule Register would refuse would already be persisted.
	if err := persona.ValidateAPIRoutes(p.APIRoutes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Persist to database FIRST — if it fails, don't register in-memory.
	if h.deps.PersonaStore != nil {
		author := extractAuthor(r)
		def := personastore.DefinitionFromPersona(p, author)
		if err := h.deps.PersonaStore.Set(r.Context(), def); err != nil {
			slog.Warn("failed to persist persona", logKeyName, logsan.SanitizeForLog(p.Name), logKeyError, err)
			writeError(w, http.StatusInternalServerError, "failed to persist persona")
			return
		}
	}

	if err := h.deps.PersonaRegistry.Register(p); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register persona")
		return
	}
	h.warnIncoherentPersona(p)
	if h.deps.ReloadNotifier != nil {
		h.deps.ReloadNotifier.PublishPersonaReload()
	}

	writeJSON(w, http.StatusCreated, toPersonaDetail(p, h.resolveTools(p)))
}

// updatePersona handles PUT /api/v1/admin/personas/{name}.
//
// @Summary      Update persona
// @Description  Updates an existing persona. Only available in database config mode.
// @Tags         Personas
// @Accept       json
// @Produce      json
// @Param        name  path  string                true  "Persona name"
// @Param        body  body  personaCreateRequest  true  "Persona definition"
// @Success      200  {object}  personaDetail
// @Failure      400  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/personas/{name} [put]
func (h *Handler) updatePersona(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var req personaCreateRequest
	if err := decodeStrict(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}

	// Override name from path
	req.Name = name
	p := buildPersonaFromRequest(req)
	if h.deps.FilePersonaNames[name] {
		p.Source = platform.SourceBoth
	} else {
		p.Source = platform.SourceDatabase
	}
	if err := persona.ValidateAPIRoutes(p.APIRoutes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Persist to database FIRST — if it fails, don't update in-memory.
	if h.deps.PersonaStore != nil {
		author := extractAuthor(r)
		def := personastore.DefinitionFromPersona(p, author)
		if err := h.deps.PersonaStore.Set(r.Context(), def); err != nil {
			slog.Warn("failed to persist persona update", logKeyName, logsan.SanitizeForLog(p.Name), logKeyError, err)
			writeError(w, http.StatusInternalServerError, "failed to persist persona")
			return
		}
	}

	if err := h.deps.PersonaRegistry.Register(p); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update persona")
		return
	}
	h.warnIncoherentPersona(p)
	if h.deps.ReloadNotifier != nil {
		h.deps.ReloadNotifier.PublishPersonaReload()
	}

	writeJSON(w, http.StatusOK, toPersonaDetail(p, h.resolveTools(p)))
}

// deletePersona handles DELETE /api/v1/admin/personas/{name}.
//
// @Summary      Delete persona
// @Description  Deletes a persona. Only available in database config mode. Cannot delete the admin persona.
// @Tags         Personas
// @Produce      json
// @Param        name  path  string  true  "Persona name"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/personas/{name} [delete]
func (h *Handler) deletePersona(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	// Block deletion of the admin persona.
	if h.deps.Config != nil && name == h.deps.Config.Admin.Persona {
		writeError(w, http.StatusConflict, "cannot delete the admin persona")
		return
	}

	// Block deletion of file-only personas — they would reappear on restart.
	if h.isFileOnlyPersona(name) {
		writeError(w, http.StatusConflict,
			"this persona is defined in the config file and cannot be deleted via the admin API")
		return
	}

	if err := h.deletePersonaFromStore(r, name); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete persona from database")
		return
	}

	if err := h.deps.PersonaRegistry.Unregister(name); err != nil {
		writeError(w, http.StatusNotFound, "persona not found")
		return
	}

	// If the persona has a file fallback, re-register the file version so
	// it reverts immediately rather than disappearing until restart.
	if h.deps.FilePersonaNames[name] {
		h.revertToFilePersona(name)
	}
	if h.deps.ReloadNotifier != nil {
		h.deps.ReloadNotifier.PublishPersonaReload()
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: statusDeleted})
}

// isFileOnlyPersona returns true if the persona exists in the file config
// and has not been overridden by a database entry.
func (h *Handler) isFileOnlyPersona(name string) bool {
	if !h.deps.FilePersonaNames[name] {
		return false
	}
	existing, _ := h.deps.PersonaRegistry.Get(name)
	return existing != nil && existing.Source == platform.SourceFile
}

// deletePersonaFromStore removes a persona from the database store.
// Returns nil if no store is configured or if the persona has a file fallback
// and the DB entry was already absent (ErrPersonaNotFound).
func (h *Handler) deletePersonaFromStore(r *http.Request, name string) error {
	if h.deps.PersonaStore == nil {
		return nil
	}
	err := h.deps.PersonaStore.Delete(r.Context(), name)
	if err == nil {
		return nil
	}
	// Tolerate "not found" when a file fallback exists — the DB entry
	// may have already been removed or never existed.
	if errors.Is(err, personastore.ErrNotFound) && h.deps.FilePersonaNames[name] {
		return nil
	}
	slog.Warn("failed to delete persona from database", logKeyName, logsan.SanitizeForLog(name), logKeyError, err) // #nosec G706 -- name is sanitized
	return fmt.Errorf("deleting persona %q: %w", name, err)
}

// revertToFilePersona re-registers the file-based version of a persona after
// its database override has been deleted.
func (h *Handler) revertToFilePersona(name string) {
	if h.deps.Config == nil {
		return
	}
	def, ok := h.deps.Config.Personas.Definitions[name]
	if !ok {
		return
	}
	// PersonaDef.ToPersona is the one construction of a file persona, shared
	// with the platform's own startup load. Rebuilding the struct here is what
	// let a revert silently drop whatever the file declared that this handler
	// had not been taught about.
	p := def.ToPersona(name, platform.SourceFile)
	if err := h.deps.PersonaRegistry.Register(p); err != nil {
		slog.Warn("failed to revert persona to file version", logKeyName, logsan.SanitizeForLog(name), logKeyError, err) // #nosec G706 -- name is sanitized
		return
	}
	// The persona now in force is the file version, not the one any earlier
	// write logged about, so it gets its own coherence check.
	h.warnIncoherentPersona(p)
}

// warnIncoherentPersona logs a warning per coherence finding for a persona
// just written through the admin API (#1174), so an operator who narrows a
// persona into a shape that cannot complete its own capability finds out at
// write time rather than from an unauthorized audit row weeks later.
//
// Advisory only: the write has already succeeded and is never rolled back. A
// restricted persona may be exactly what the operator intended, and the rules
// describe an incoherent tool set, not an unauthorized one.
//
// Findings are scoped to the tools this deployment actually registered, so a
// deployment with no search toolkit never warns about withholding fetch.
func (h *Handler) warnIncoherentPersona(p *persona.Persona) {
	if h.deps.ToolkitRegistry == nil {
		return
	}
	for _, f := range persona.CheckCoherence(p, h.deps.ToolkitRegistry.AllTools()) {
		slog.Warn("persona grants a capability it cannot complete",
			"persona", logsan.SanitizeForLog(f.Persona),
			"granted", f.Granted,
			"missing", f.Missing,
			"why", f.Why,
			"remedy", logsan.SanitizeForLog(f.Remedy),
		)
	}
}

// allowedTools returns, in registration order, the tools this deployment
// registered that the persona's rules allow. It is the one evaluation of a
// persona against the live tool set, shared by the tool count on the list
// response and the resolved list on every detail response.
//
// Empty means the persona's rules match no registered tool, except with no
// toolkit registry at all, where nothing can be resolved and the count is
// reported as zero. Deps.ToolkitRegistry is always set by the composition root
// (internal/httpserver/mounts.go), so that branch is a guard, not a state a
// deployment reaches.
func (h *Handler) allowedTools(p *persona.Persona) []string {
	if h.deps.ToolkitRegistry == nil {
		return nil
	}
	filter := persona.NewToolFilter(nil)
	all := h.deps.ToolkitRegistry.AllTools()
	tools := make([]string, 0, len(all))
	for _, t := range all {
		if filter.IsAllowed(p, t) {
			tools = append(tools, t)
		}
	}
	return tools
}

// resolveTools is allowedTools sorted, as personaDetail.Tools ships it. Every
// persona response carrying a tool list is built from this one call, so a
// create or update answers with the same list a later read of that persona
// returns rather than a hard-coded empty one (#1510).
//
// Never returns nil: personaDetail.Tools ships as a JSON array.
func (h *Handler) resolveTools(p *persona.Persona) []string {
	tools := h.allowedTools(p)
	if tools == nil {
		return []string{}
	}
	sort.Strings(tools)
	return tools
}

// toPersonaDetail builds a personaDetail response from a persona and its resolved tool list.
func toPersonaDetail(p *persona.Persona, tools []string) personaDetail {
	ctx := &personaContextDetail{
		DescriptionPrefix:         p.Context.DescriptionPrefix,
		DescriptionOverride:       p.Context.DescriptionOverride,
		AgentInstructionsSuffix:   p.Context.AgentInstructionsSuffix,
		AgentInstructionsOverride: p.Context.AgentInstructionsOverride,
	}
	// Omit empty context object from JSON.
	if *ctx == (personaContextDetail{}) {
		ctx = nil
	}
	return personaDetail{
		Name:             p.Name,
		DisplayName:      p.DisplayName,
		Description:      p.Description,
		Roles:            p.Roles,
		Priority:         p.Priority,
		AllowTools:       p.Tools.Allow,
		DenyTools:        p.Tools.Deny,
		AllowConnections: p.Connections.Allow,
		DenyConnections:  p.Connections.Deny,
		Tools:            tools,
		Context:          ctx,
		Source:           p.Source,
		APIRoutes:        p.APIRoutes,
	}
}

// buildPersonaFromRequest converts a create request into a persona.
func buildPersonaFromRequest(req personaCreateRequest) *persona.Persona {
	allow := req.AllowTools
	if allow == nil {
		allow = []string{}
	}
	deny := req.DenyTools
	if deny == nil {
		deny = []string{}
	}

	allowConn := req.AllowConnections
	if allowConn == nil {
		allowConn = []string{}
	}
	denyConn := req.DenyConnections
	if denyConn == nil {
		denyConn = []string{}
	}

	return &persona.Persona{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Roles:       req.Roles,
		Tools: persona.ToolRules{
			Allow: allow,
			Deny:  deny,
		},
		Connections: persona.ConnectionRules{
			Allow: allowConn,
			Deny:  denyConn,
		},
		APIRoutes: normalizeAPIRoutes(req.APIRoutes),
		Context: persona.ContextOverrides{
			DescriptionPrefix:         req.DescriptionPrefix,
			DescriptionOverride:       req.DescriptionOverride,
			AgentInstructionsSuffix:   req.AgentInstructionsSuffix,
			AgentInstructionsOverride: req.AgentInstructionsOverride,
		},
		Priority: req.Priority,
	}
}

// extractAuthor returns the author identifier from the request context.
// Returns "unknown" and logs a warning if no user is present.
func extractAuthor(r *http.Request) string {
	if user := GetUser(r.Context()); user != nil {
		if user.Email != "" {
			return user.Email
		}
		return user.UserID
	}
	slog.Warn("no user in request context for author extraction")
	return "unknown"
}

// testPersonaAccessRequest is the body for POST /personas/{name}/test-access.
//
// Two questions share the route. With tool_name set it asks whether the
// persona may call a tool. With connection set it asks whether the persona may
// invoke method on path of that api-kind connection, which is the question the
// persona editor's API-endpoint scope asks of a rule it is about to save.
type testPersonaAccessRequest struct {
	ToolName string `json:"tool_name,omitempty" example:"trino_query"`
	// Connection selects the API route case. Empty asks the tool question.
	Connection string `json:"connection,omitempty" example:"crm-prod"`
	Method     string `json:"method,omitempty" example:"DELETE"`
	// Path is the operation path, as the connection's catalog declares it.
	Path string `json:"path,omitempty" example:"/v1/orders/{id}"`
}

// testPersonaAccessResponse mirrors persona.AccessDecision for the API.
// We re-export it here so the OpenAPI generator picks up the field
// descriptions and examples without coupling the persona package to
// swagger annotations.
type testPersonaAccessResponse struct {
	Allowed        bool                 `json:"allowed" example:"true"`
	MatchedPattern string               `json:"matched_pattern,omitempty" example:"trino_*"`
	Source         persona.AccessSource `json:"source" example:"allow"`
	// MatchedRule is the API route rule that decided the route case. Absent
	// for the tool case, and absent when no rule touched the connection —
	// which is the "default" source, an allow the connection-level check is
	// the sole gate for.
	MatchedRule *persona.APIRouteRule `json:"matched_rule,omitempty"`
}

// testPersonaAccess handles POST /api/v1/admin/personas/{name}/test-access.
//
// @Summary      Preview a persona's decision for a tool or an API route
// @Description  Evaluates the named persona's rules and returns the decision with what produced it. With tool_name set it answers the tool question and returns the matching pattern. With connection set it answers whether the persona may invoke method on path of that api-kind connection and returns the matching API route rule.
// @Tags         Personas
// @Accept       json
// @Produce      json
// @Param        name  path  string                    true  "Persona name"
// @Param        body  body  testPersonaAccessRequest  true  "Tool to evaluate"
// @Success      200   {object}  testPersonaAccessResponse
// @Failure      400   {object}  problemDetail
// @Failure      404   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/personas/{name}/test-access [post]
func (h *Handler) testPersonaAccess(w http.ResponseWriter, r *http.Request) {
	if h.deps.PersonaRegistry == nil {
		writeError(w, http.StatusServiceUnavailable, "persona registry not available")
		return
	}
	name := r.PathValue(pathKeyName)
	if name == "" {
		writeError(w, http.StatusBadRequest, "persona name is required")
		return
	}
	p, ok := h.deps.PersonaRegistry.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, "persona not found")
		return
	}

	var req testPersonaAccessRequest
	if err := decodeStrict(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Connection != "" {
		if req.ToolName != "" {
			writeError(w, http.StatusBadRequest, "provide tool_name or connection, not both")
			return
		}
		testPersonaRouteAccess(w, p, req)
		return
	}
	if req.ToolName == "" {
		writeError(w, http.StatusBadRequest, "tool_name or connection is required")
		return
	}

	decision := persona.NewToolFilter(nil).WhyAllowed(p, req.ToolName)
	writeJSON(w, http.StatusOK, testPersonaAccessResponse{
		Allowed:        decision.Allowed,
		MatchedPattern: decision.MatchedPattern,
		Source:         decision.Source,
	})
}
