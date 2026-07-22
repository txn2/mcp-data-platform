package versionhttp

import (
	"context"
	"net/http"
	"slices"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// adminUsage returns the audit-derived usage rollup for every database prompt.
//
// @Summary      Prompt usage stats
// @Description  Returns run count and last-run timestamp per prompt id, aggregated from prompt_serve audit events within the audit retention window. Prompts never served are absent from the map.
// @Tags         Prompts
// @Produce      json
// @Success      200  {object}  map[string]prompt.Usage
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts/usage [get]
func (h *Handler) adminUsage(w http.ResponseWriter, r *http.Request) {
	// System rows are read-only config mirrors; their ids are excluded like
	// the admin prompt list excludes them.
	ids, err := h.promptIDs(r.Context(), prompt.ListFilter{ExcludeSource: prompt.SourceSystem})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list prompts")
		return
	}
	h.writeUsage(r.Context(), w, ids)
}

// portalUsage returns the usage rollup for the prompts visible to the caller:
// their own personal prompts, global prompts, and their persona's prompts.
//
// @Summary      Prompt usage stats (portal)
// @Description  Returns run count and last-run timestamp per prompt id for the prompts visible to the caller.
// @Tags         Prompts
// @Produce      json
// @Success      200  {object}  map[string]prompt.Usage
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/usage [get]
func (h *Handler) portalUsage(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	ids, err := h.visiblePromptIDs(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list prompts")
		return
	}
	h.writeUsage(r.Context(), w, ids)
}

// portalListVersions returns a prompt's version history to any caller who can
// view the prompt itself (#1010): version history with per-version approval
// provenance is the library's verification surface, so a reader of an
// approved shared prompt may audit what they are running. Personal prompts
// remain owner-or-admin; a personal prompt shared person-to-person via the
// portal share system does not expose its history (recipients see only the
// served version, and the UI omits the section on 403).
//
// @Summary      List prompt versions (portal)
// @Description  Returns the version history of a prompt visible to the caller: their own personal prompts, and enabled shared (global or persona-matching) prompts.
// @Tags         Prompts
// @Produce      json
// @Param        id  path  string  true  "Prompt ID"
// @Success      200  {object}  versionListResponse
// @Failure      401  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/{id}/versions [get]
func (h *Handler) portalListVersions(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	pr, ok := h.loadPrompt(w, r)
	if !ok {
		return
	}
	if !canViewPrompt(user, pr) {
		writeError(w, http.StatusForbidden, "you do not have access to this prompt's version history")
		return
	}
	versions, err := h.deps.Versions.ListVersions(r.Context(), pr.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errListVers)
		return
	}
	// Admins curate everything; a personal prompt's history is its owner's
	// (canViewPrompt already restricted personal scope to the owner). Every
	// other viewer of a shared prompt gets the served history only.
	if !user.IsAdmin && pr.Scope != prompt.ScopePersonal {
		versions = redactVersionsForViewer(versions)
	}
	if versions == nil {
		versions = []prompt.Version{}
	}
	writeJSON(w, http.StatusOK, versionListResponse{Data: versions, Total: len(versions)})
}

// redactVersionsForViewer reduces a shared prompt's history to what a
// non-privileged viewer may verify: applied snapshots in full (they were
// served), and the pending draft as a metadata stub — its existence is the
// at-a-glance re-review signal, but its content was never served and stays
// admin/author-only until approved. Rejected and superseded drafts were never
// served and are dropped entirely.
func redactVersionsForViewer(versions []prompt.Version) []prompt.Version {
	out := make([]prompt.Version, 0, len(versions))
	for _, v := range versions {
		switch v.Status {
		case prompt.VersionStatusApplied:
			out = append(out, v)
		case prompt.VersionStatusDraft:
			v.DisplayName, v.Description, v.Content = "", "", ""
			v.Arguments, v.Tags = []prompt.Argument{}, []string{}
			out = append(out, v)
		}
	}
	return out
}

// canViewPrompt mirrors the portal prompt list's visibility rule: admins see
// everything; owners see their own personal prompts; enabled global prompts
// are visible to all; enabled persona prompts are visible to members of a
// listed persona.
func canViewPrompt(user *PortalIdentity, pr *prompt.Prompt) bool {
	if user.IsAdmin {
		return true
	}
	switch pr.Scope {
	case prompt.ScopePersonal:
		return pr.OwnerEmail == user.Email
	case prompt.ScopeGlobal:
		return pr.Enabled
	case prompt.ScopePersona:
		return pr.Enabled && user.Persona != "" && slices.Contains(pr.Personas, user.Persona)
	default:
		return false
	}
}

// writeUsage writes the usage map for the given prompt ids. Without a usage
// reader (audit disabled) the map is empty: no serve events exist to count.
func (h *Handler) writeUsage(ctx context.Context, w http.ResponseWriter, ids []string) {
	usage := map[string]prompt.Usage{}
	if h.deps.Usage != nil && len(ids) > 0 {
		var err error
		usage, err = h.deps.Usage.PromptUsage(ctx, ids)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read prompt usage")
			return
		}
	}
	writeJSON(w, http.StatusOK, usage)
}

// promptIDs lists the ids of enabled and disabled prompts matching the filter.
func (h *Handler) promptIDs(ctx context.Context, filter prompt.ListFilter) ([]string, error) {
	prompts, err := h.deps.Store.List(ctx, filter)
	if err != nil {
		return nil, err //nolint:wrapcheck // callers map any store failure to one HTTP error
	}
	ids := make([]string, 0, len(prompts))
	for i := range prompts {
		ids = append(ids, prompts[i].ID)
	}
	return ids, nil
}

// visiblePromptIDs collects the ids of the prompts the portal caller can see:
// their own personal prompts, global prompts, and their persona's prompts —
// the same enabled-only scope the portal prompt list serves, so a disabled
// prompt's usage is as invisible as the prompt itself. Admins get every
// non-system prompt.
func (h *Handler) visiblePromptIDs(ctx context.Context, user *PortalIdentity) ([]string, error) {
	if user.IsAdmin {
		return h.promptIDs(ctx, prompt.ListFilter{ExcludeSource: prompt.SourceSystem})
	}
	enabled := true
	ids, err := h.promptIDs(ctx, prompt.ListFilter{Scope: prompt.ScopePersonal, OwnerEmail: user.Email, Enabled: &enabled})
	if err != nil {
		return nil, err
	}
	globals, err := h.promptIDs(ctx, prompt.ListFilter{Scope: prompt.ScopeGlobal, ExcludeSource: prompt.SourceSystem, Enabled: &enabled})
	if err != nil {
		return nil, err
	}
	ids = append(ids, globals...)
	if user.Persona != "" {
		personas, err := h.promptIDs(ctx, prompt.ListFilter{Scope: prompt.ScopePersona, Personas: []string{user.Persona}, Enabled: &enabled})
		if err != nil {
			return nil, err
		}
		ids = append(ids, personas...)
	}
	if h.deps.SharedPromptIDs != nil {
		shared, err := h.deps.SharedPromptIDs(ctx, user.UserID, user.Email)
		if err != nil {
			return nil, err
		}
		ids = append(ids, shared...)
	}
	return ids, nil
}
