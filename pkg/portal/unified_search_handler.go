package portal

import (
	"context"
	"net/http"
	"slices"
	"strings"
)

// applyKnowledgeTool is the persona tool whose access gates insight review and
// canonical-knowledge writes. It is the single capability the REST path checks,
// matching the MCP path's persona tool-visibility gate so both agree on who may
// promote knowledge.
const applyKnowledgeTool = "apply_knowledge"

// SearchRouter is the unified knowledge-search federation behind the portal's
// GET /api/v1/portal/search endpoint. It is a portal-local interface (rather
// than a direct dependency on pkg/knowledge) because that package already
// imports pkg/portal for the asset and knowledge-page stores; importing it back
// would form a cycle. main wires a thin adapter over *knowledge.Router that maps
// to these types, so the REST surface returns the same grouped, scope-enforced
// result set as the MCP search tool.
type SearchRouter interface {
	Search(ctx context.Context, q SearchQuery) (SearchResult, error)
}

// SearchQuery is one unified search. Intent is natural-language text;
// EntityURNs is an exact entity-keyed lookup; at least one is set. Caller is the
// per-user scope boundary. These fields mirror knowledge.Query; the adapter in
// main maps between them.
type SearchQuery struct {
	Intent     string
	EntityURNs []string
	Status     string
	Sources    []string
	Caller     SearchCaller
	Limit      int
}

// SearchCaller is the resolved requester identity per-user providers scope on.
type SearchCaller struct {
	UserID  string
	Email   string
	Persona string
}

// SearchResult is the grouped-by-source response. It mirrors knowledge.Result.
type SearchResult struct {
	Groups         []SearchGroup
	Coverage       []SearchCoverage
	Ranking        string
	UnknownSources []string
}

// SearchGroup is one source's slice of the balanced display set.
type SearchGroup struct {
	Source string      `json:"source"`
	Hits   []SearchHit `json:"hits"`
}

// SearchHit is one matched record. The JSON shape matches the MCP search tool's
// hit so the portal and agent surfaces render identical results.
type SearchHit struct {
	Text       string   `json:"text"`
	Source     string   `json:"source"`
	Ref        string   `json:"ref"`
	Score      float64  `json:"score"`
	Status     string   `json:"status,omitempty"`
	EntityURNs []string `json:"entity_urns,omitempty"`
	Dimension  string   `json:"dimension,omitempty"`
}

// SearchCoverage reports, per source, how many records matched versus how many
// are shown, the anti-tunnel signal that breadth exists beyond the display set.
type SearchCoverage struct {
	Source  string `json:"source"`
	Matched int    `json:"matched"`
	Shown   int    `json:"shown"`
}

// registerSearchRoutes wires the unified knowledge-search endpoint. It is
// registered only when a SearchRouter is configured (at least one searchable
// source exists); a store-less deployment omits it entirely.
func (h *Handler) registerSearchRoutes() {
	if h.deps.SearchRouter == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/portal/search", h.search)
}

// searchResponse is the GET /api/v1/portal/search envelope. It serializes the
// router's grouped-by-source contract directly, so the portal renders the same
// balanced display set and coverage summary the MCP search tool returns.
type searchResponse struct {
	Groups         []SearchGroup    `json:"groups"`
	Coverage       []SearchCoverage `json:"coverage"`
	Count          int              `json:"count"`
	Ranking        string           `json:"ranking"`
	UnknownSources []string         `json:"unknown_sources,omitempty"`
}

// search handles GET /api/v1/portal/search. It is a thin REST adapter over the
// existing knowledge router: it resolves the caller identity (the per-user scope
// boundary), forwards the query, and returns the router's grouped result
// unchanged. No new ranking or allocation logic lives here.
//
// @Summary      Unified knowledge search
// @Description  One query fans across every source the caller can access (catalog, context documents, knowledge pages, memory, insights, feedback, assets, prompts, endpoints, connections), grouped by source and scope-enforced.
// @Tags         Knowledge
// @Produce      json
// @Param        q            query  string  false  "Natural-language intent"
// @Param        entity_urns  query  string  false  "Entity-keyed lookup (repeatable)"
// @Param        status       query  string  false  "Insight review status filter"
// @Param        sources      query  string  false  "Narrow to specific sources (repeatable)"
// @Param        limit        query  int     false  "Display budget across all sources"
// @Success      200  {object}  searchResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/search [get]
func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	intent := strings.TrimSpace(r.URL.Query().Get(paramQuery))
	entityURNs := queryValues(r, "entity_urns")
	if intent == "" && len(entityURNs) == 0 {
		writeError(w, http.StatusBadRequest, "query parameter 'q' or 'entity_urns' is required")
		return
	}

	res, err := h.deps.SearchRouter.Search(r.Context(), SearchQuery{
		Intent:     intent,
		EntityURNs: entityURNs,
		Status:     strings.TrimSpace(r.URL.Query().Get("status")),
		Sources:    queryValues(r, "sources"),
		Caller:     h.callerFor(user),
		Limit:      intParam(r, paramLimit, 0),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}

	groups := res.Groups
	if groups == nil {
		groups = []SearchGroup{}
	}
	coverage := res.Coverage
	if coverage == nil {
		coverage = []SearchCoverage{}
	}
	shown := 0
	for _, g := range groups {
		shown += len(g.Hits)
	}
	writeJSON(w, http.StatusOK, searchResponse{
		Groups:         groups,
		Coverage:       coverage,
		Count:          shown,
		Ranking:        res.Ranking,
		UnknownSources: res.UnknownSources,
	})
}

// callerFor builds the SearchCaller for a portal user. The persona name is
// resolved the same way GET /me resolves it, so per-user scope and persona-scoped
// content match between the REST search and the MCP search tool.
func (h *Handler) callerFor(user *User) SearchCaller {
	c := SearchCaller{UserID: user.UserID, Email: user.Email}
	if h.deps.PersonaResolver != nil {
		if info := h.deps.PersonaResolver(user.Roles); info != nil {
			c.Persona = info.Name
		}
	}
	return c
}

// userHasApplyKnowledge reports whether the user effectively holds the
// apply_knowledge capability. It grants access when the user's resolved persona
// lists the tool (the same Tools the frontend reads from GET /me and the MCP
// path gates on, so a non-admin persona granted apply_knowledge can review and
// promote), OR when the user is an admin.
//
// Admins are always treated as holding the capability for two reasons: their
// persona normally grants every registered tool, and the tool may not be
// registered at all on a given deployment (apply_knowledge is absent when
// Knowledge.Apply.Enabled is false, its default), in which case the resolved
// Tools list can never contain it. Without the admin arm, enabling capability
// gating would lock admins out of knowledge writes wherever apply is disabled,
// a regression from the prior admin-role gate. The admin arm only widens access;
// the capability still grants non-admins, which is the behavior #661 requires.
func (h *Handler) userHasApplyKnowledge(user *User) bool {
	return h.userHasTool(user, applyKnowledgeTool)
}

// userHasTool reports whether the user's resolved persona grants the named tool,
// or the user is an admin. It is the shared capability check behind the
// apply_knowledge and DataHub write authorizations; the admin arm only widens
// access (a separate write-enabled-connection check still applies to DataHub
// writes, so admin cannot mutate a read-only connection).
func (h *Handler) userHasTool(user *User, tool string) bool {
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

// queryValues returns the trimmed, non-empty values for a query parameter,
// accepting both repeated keys (?sources=a&sources=b) and comma-separated lists
// (?sources=a,b). It returns nil when none are present so the router treats the
// federation as unrestricted.
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
