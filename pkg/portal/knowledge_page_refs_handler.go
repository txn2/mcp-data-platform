package portal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// maxEntityRefsPerPage caps how many references a single set request may carry,
// so a malformed or hostile payload cannot fan out unbounded inserts.
const maxEntityRefsPerPage = 100

// maxEntityRefsBodyBytes bounds the refs request body so the count cap is not
// reached only after buffering an arbitrarily large payload. 100 serialized URNs
// are well under this; the cap is the memory backstop.
const maxEntityRefsBodyBytes = 64 << 10

// entityRefView is a page reference enriched with its serialized URN form, the
// projection the agent, the UI, and search use to address the entity.
type entityRefView struct {
	knowledgepage.EntityRef
	URN string `json:"urn"`
}

// knowledgePageRefsResponse is the GET/PUT refs envelope.
type knowledgePageRefsResponse struct {
	Refs []entityRefView `json:"refs"`
}

// setEntityRefsRequest carries the page's manual references as serialized URN
// strings (mcp:asset:<id>, urn:li:dataset:..., mcp:connection:(kind,name)).
type setEntityRefsRequest struct {
	Refs []string `json:"refs"`
}

// listKnowledgePageRefs handles GET /api/v1/portal/knowledge-pages/{id}/refs.
//
// @Summary      List a knowledge page's entity references
// @Description  Returns the entities the page references (assets, prompts, collections, connections, DataHub URNs, and other pages), each with its serialized URN.
// @Tags         Knowledge
// @Produce      json
// @Param        id  path  string  true  "Knowledge page id"
// @Success      200  {object}  knowledgePageRefsResponse
// @Failure      401  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages/{id}/refs [get]
func (h *Handler) listKnowledgePageRefs(w http.ResponseWriter, r *http.Request) {
	if GetUser(r.Context()) == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	id := r.PathValue(kpIDParam)
	if !h.knowledgePageExists(w, r, id) {
		return
	}
	refs, err := h.deps.KnowledgePageStore.ListEntityRefs(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list page references")
		return
	}
	writeJSON(w, http.StatusOK, knowledgePageRefsResponse{Refs: entityRefViews(refs)})
}

// setKnowledgePageRefs handles PUT /api/v1/portal/knowledge-pages/{id}/refs.
//
// @Summary      Set a knowledge page's manual entity references
// @Description  Replaces the page's manually-authored references with the given set (parsed from serialized URNs). References carried from promotion (source=promoted) are preserved. Requires apply_knowledge access.
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        id    path  string                true  "Knowledge page id"
// @Param        body  body  setEntityRefsRequest  true  "References to set"
// @Success      200  {object}  knowledgePageRefsResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      422  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages/{id}/refs [put]
func (h *Handler) setKnowledgePageRefs(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	if !h.userHasApplyKnowledge(user) {
		writeError(w, http.StatusForbidden, errKnowledgePageForbidden)
		return
	}
	id := r.PathValue(kpIDParam)
	if !h.knowledgePageExists(w, r, id) {
		return
	}

	var req setEntityRefsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEntityRefsBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	if len(req.Refs) > maxEntityRefsPerPage {
		writeError(w, http.StatusBadRequest, "too many references")
		return
	}

	refs, err := parseEntityRefs(req.Refs, user.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.deps.KnowledgePageStore.ReplaceEntityRefsBySource(r.Context(), id, knowledgepage.RefSourceManual, refs); err != nil {
		if errors.Is(err, knowledgepage.ErrRefTargetNotFound) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to set page references")
		return
	}

	updated, err := h.deps.KnowledgePageStore.ListEntityRefs(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list page references")
		return
	}
	writeJSON(w, http.StatusOK, knowledgePageRefsResponse{Refs: entityRefViews(updated)})
}

// knowledgePageExists verifies the page is live, writing a 404 (or 500) on
// failure. It returns true only when the page exists and is not deleted.
func (h *Handler) knowledgePageExists(w http.ResponseWriter, r *http.Request, id string) bool {
	page, err := h.deps.KnowledgePageStore.Get(r.Context(), id)
	if errors.Is(err, knowledgepage.ErrNotFound) || (err == nil && page.DeletedAt != nil) {
		writeError(w, http.StatusNotFound, errKnowledgePageNotFoundMsg)
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get knowledge page")
		return false
	}
	return true
}

// parseEntityRefs parses serialized URN strings into typed references, stamping
// each with the authoring user. A parse failure aborts with the offending error.
func parseEntityRefs(urns []string, createdBy string) ([]knowledgepage.EntityRef, error) {
	refs := make([]knowledgepage.EntityRef, 0, len(urns))
	for _, s := range urns {
		ref, err := knowledgepage.ParseEntityRef(s)
		if err != nil {
			return nil, fmt.Errorf("invalid reference: %w", err)
		}
		ref.CreatedBy = createdBy
		refs = append(refs, ref)
	}
	return refs, nil
}

func entityRefViews(refs []knowledgepage.EntityRef) []entityRefView {
	views := make([]entityRefView, 0, len(refs))
	for _, ref := range refs {
		views = append(views, entityRefView{EntityRef: ref, URN: ref.URN()})
	}
	return views
}
