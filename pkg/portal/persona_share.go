package portal

import (
	"context"
	"net/http"
	"slices"
)

// SharePersona is one persona a share can be addressed to (#2089). External
// marks a persona for people outside the organization (#2290), which the
// share dialog shows beside its name.
type SharePersona struct {
	Name        string `json:"name" example:"finance"`
	DisplayName string `json:"display_name" example:"Finance"`
	External    bool   `json:"external,omitempty" example:"false"`
	// MemberCount is how many people the persona reaches now (#2291).
	MemberCount int `json:"member_count" example:"12"`
}

// sharePersonaList is the personas the caller may share with: the body of
// GET /api/v1/portal/share-personas.
type sharePersonaList struct {
	Personas []SharePersona `json:"personas"`
}

// sharePersonas is the personas the caller may address a share to: every
// persona, for anyone who may share (#2290). A persona's membership is decided
// when its people open the item, so naming one grants nothing to anyone
// outside it.
func (h *Handler) sharePersonas(user *User) []SharePersona {
	if h.deps.Personas == nil || user == nil {
		return make([]SharePersona, 0)
	}
	return h.deps.Personas()
}

// listSharePersonas handles GET /api/v1/portal/share-personas.
//
// @Summary      List the personas the caller may share with
// @Description  Returns every persona a share may be addressed to, each marked when it is for people outside the organization (#2089, #2290), with how many people it reaches now (#2291). A share addressed to a persona opens for everyone whose roles resolve to it when they open the item.
// @Tags         Shares
// @Produce      json
// @Success      200  {object}  sharePersonaList
// @Failure      401  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/share-personas [get]
func (h *Handler) listSharePersonas(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	personas := h.sharePersonas(user)
	if h.deps.PersonaMemberCounts != nil {
		n := h.deps.PersonaMemberCounts(r.Context())
		for i := range personas {
			personas[i].MemberCount = n[personas[i].Name]
		}
	}
	writeJSON(w, http.StatusOK, sharePersonaList{Personas: personas})
}

// allowedSharePersona refuses, with 400, a share addressed to a persona the
// caller may not name. A share naming no persona passes.
func (h *Handler) allowedSharePersona(w http.ResponseWriter, user *User, req createShareRequest) bool {
	if req.SharedWithPersona == "" {
		return true
	}
	if !slices.ContainsFunc(h.sharePersonas(user), func(p SharePersona) bool { return p.Name == req.SharedWithPersona }) {
		writeError(w, http.StatusBadRequest, "no persona of that name")
		return false
	}
	return true
}

// notifyPersonaShare tells each member of the persona a share is addressed to
// that something was shared with them, by their own notification
// preferences. The share's other fields are the email it would have carried.
func (h *Handler) notifyPersonaShare(ctx context.Context, share *Share, ev ShareEvent) {
	if h.deps.Notifier == nil || h.deps.PersonaMembers == nil || share.SharedWithPersona == "" {
		return
	}
	for _, email := range h.deps.PersonaMembers(ctx, share.SharedWithPersona) {
		addressed := *share
		addressed.SharedWithEmail = email
		h.deps.Notifier.NotifyShare(ctx, &addressed, ev)
	}
}

// notifyShareRecipients notifies a share's recipients: the person it names,
// or each member of the persona it names.
func (h *Handler) notifyShareRecipients(ctx context.Context, share *Share, ev ShareEvent) {
	if share.SharedWithPersona != "" {
		h.notifyPersonaShare(ctx, share, ev)
		return
	}
	h.notifyShare(ctx, share, ev)
}

// personaName is the persona roles resolve to now, "" for none: what a share
// addressed to a persona is matched against (#2089).
func (h *Handler) personaName(roles []string) string {
	if h.deps.PersonaResolver == nil {
		return ""
	}
	if info := h.deps.PersonaResolver(roles); info != nil {
		return info.Name
	}
	return ""
}
