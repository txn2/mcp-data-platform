package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// maxEntityRefsPerPage caps how many references a single set request may carry,
// so a malformed or hostile payload cannot fan out unbounded inserts.
const maxEntityRefsPerPage = 100

// maxEntityRefsBodyBytes bounds the refs request body so the count cap is not
// reached only after buffering an arbitrarily large payload. 100 serialized URNs
// are well under this; the cap is the memory backstop.
const maxEntityRefsBodyBytes = 64 << 10

// resolvedRefView is a page reference with its resolved display label and source,
// returned by the GET/PUT refs endpoints. References the viewer cannot access are
// omitted entirely, so a viewer never receives the id of an inaccessible entity.
type resolvedRefView struct {
	URN    string `json:"urn"`
	Type   string `json:"type"`
	Label  string `json:"label"`
	Exists bool   `json:"exists"`
	Source string `json:"source"`
}

// knowledgePageRefsResponse is the GET/PUT refs envelope.
type knowledgePageRefsResponse struct {
	Refs []resolvedRefView `json:"refs"`
}

// resolveAndFilterRefs resolves each stored reference and drops the ones the
// viewer cannot access, so the list and set endpoints never expose the id of an
// inaccessible entity (the same access model the renderer and the agent apply).
func (h *Handler) resolveAndFilterRefs(r *http.Request, user *User, refs []knowledgepage.EntityRef) []resolvedRefView {
	out := make([]resolvedRefView, 0, len(refs))
	for _, ref := range refs {
		urn := ref.URN()
		rr := h.resolveRef(r, user, urn, ref)
		if !rr.Accessible {
			continue
		}
		out = append(out, resolvedRefView{URN: urn, Type: rr.Type, Label: rr.Label, Exists: rr.Exists, Source: ref.Source})
	}
	return out
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
	user := GetUser(r.Context())
	if user == nil {
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
	writeJSON(w, http.StatusOK, knowledgePageRefsResponse{Refs: h.resolveAndFilterRefs(r, user, refs)})
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
	writeJSON(w, http.StatusOK, knowledgePageRefsResponse{Refs: h.resolveAndFilterRefs(r, user, updated)})
}

// resolveRefsRequest carries the serialized URNs the renderer wants resolved.
type resolveRefsRequest struct {
	URNs []string `json:"urns"`
}

// resolvedRef is a reference enriched with a display label and existence, so the
// renderer can show a named chip (greyed when the target is gone) instead of a
// bare URN. The frontend builds its own link from type + the URN.
type resolvedRef struct {
	URN    string `json:"urn"`
	Type   string `json:"type"`
	Label  string `json:"label"`
	Exists bool   `json:"exists"`
	// Accessible is false when the viewer may not access the target (or it is
	// unknown/missing). The renderer hides such references rather than showing a
	// confusing id, consistent with what the agent may see over MCP.
	Accessible bool `json:"accessible"`
}

// resolveRefsResponse is the resolve envelope.
type resolveRefsResponse struct {
	Refs []resolvedRef `json:"refs"`
}

// resolveKnowledgePageRefs handles POST /api/v1/portal/knowledge-pages/refs/resolve.
//
// @Summary      Resolve entity references to display labels
// @Description  Resolves a batch of serialized reference URNs (mcp:/urn:li:) to a display label, type, and whether the target still exists, so the renderer can show named chips.
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        body  body  resolveRefsRequest  true  "URNs to resolve"
// @Success      200  {object}  resolveRefsResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages/refs/resolve [post]
func (h *Handler) resolveKnowledgePageRefs(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	var req resolveRefsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEntityRefsBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	if len(req.URNs) > maxEntityRefsPerPage {
		writeError(w, http.StatusBadRequest, "too many references")
		return
	}
	out := make([]resolvedRef, 0, len(req.URNs))
	for _, s := range req.URNs {
		ref, err := knowledgepage.ParseEntityRef(s)
		if err != nil {
			out = append(out, resolvedRef{URN: s, Label: s, Exists: false, Accessible: false})
			continue
		}
		out = append(out, h.resolveRef(r, user, s, ref))
	}
	writeJSON(w, http.StatusOK, resolveRefsResponse{Refs: out})
}

// resolveRef resolves a single reference to a display label, existence, and
// accessibility. Access-gated targets (asset, collection, prompt) resolve only
// when the user may view them; otherwise they are reported inaccessible, and
// because not-found and not-permitted both yield Accessible=false the endpoint
// cannot enumerate names or existence across the share boundary. Knowledge pages
// are org-shared (title resolved for any reader; a missing page is a broken
// reference). Connection and DataHub labels derive from the URN itself and are
// shown as platform/catalog context.
func (h *Handler) resolveRef(r *http.Request, user *User, urn string, ref knowledgepage.EntityRef) resolvedRef {
	out := resolvedRef{URN: urn, Type: ref.TargetType, Label: urn, Exists: true, Accessible: true}
	switch ref.TargetType {
	case knowledgepage.RefTargetAsset:
		h.resolveAssetRef(r, user, ref.AssetID, &out)
	case knowledgepage.RefTargetCollection:
		h.resolveCollectionRef(r, user, ref.CollectionID, &out)
	case knowledgepage.RefTargetPrompt:
		h.resolvePromptRef(r, user, ref.PromptID, &out)
	case knowledgepage.RefTargetKnowledgePage:
		h.resolvePageRef(r.Context(), ref.RefPageID, &out)
	case knowledgepage.RefTargetConnection:
		out.Label = ref.ConnectionName + " (" + ref.ConnectionKind + ")"
	case knowledgepage.RefTargetDataHub:
		out.Label = datahubLabel(ref.EntityURN)
	}
	return out
}

// resolveAssetRef sets the asset's name when the user may view it; a not-found or
// not-viewable asset is marked inaccessible (uniformly, so existence is not
// revealed). A missing AssetStore means access cannot be verified, so hide it.
func (h *Handler) resolveAssetRef(r *http.Request, user *User, id string, out *resolvedRef) {
	if h.deps.AssetStore == nil {
		out.Accessible = false
		return
	}
	a, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil || a == nil || !h.userCanViewAsset(r, id, a, user) {
		out.Accessible = false
		return
	}
	out.Label = a.Name
}

// resolveCollectionRef sets the collection's name when the user may view it.
func (h *Handler) resolveCollectionRef(r *http.Request, user *User, id string, out *resolvedRef) {
	if h.deps.CollectionStore == nil {
		out.Accessible = false
		return
	}
	c, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil || c == nil || !h.userCanViewCollection(r, c, user) {
		out.Accessible = false
		return
	}
	out.Label = c.Name
}

// resolvePromptRef sets the prompt's name when the user may view it.
func (h *Handler) resolvePromptRef(r *http.Request, user *User, id string, out *resolvedRef) {
	if h.deps.PromptStore == nil {
		out.Accessible = false
		return
	}
	p, err := h.deps.PromptStore.GetByID(r.Context(), id)
	if err != nil || p == nil || !h.userCanViewPrompt(r, user, p) {
		out.Accessible = false
		return
	}
	out.Label = p.Name
}

// resolvePageRef sets a knowledge page's title; a missing page is a broken
// reference (pages are org-shared, so this is not an access concern).
func (h *Handler) resolvePageRef(ctx context.Context, id string, out *resolvedRef) {
	if p, err := h.deps.KnowledgePageStore.Get(ctx, id); err == nil {
		out.Label = p.Title
		return
	}
	out.Label, out.Exists = id, false
}

// datahubLabel extracts a readable name from a DataHub URN: the dataset name from
// urn:li:dataset:(<platform>,<name>,<env>), or the last colon-segment otherwise.
func datahubLabel(urn string) string {
	if rest, ok := strings.CutPrefix(urn, "urn:li:dataset:("); ok {
		inner := strings.TrimSuffix(rest, ")")
		parts := strings.Split(inner, ",")
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	if i := strings.LastIndex(urn, ":"); i >= 0 && i < len(urn)-1 {
		return urn[i+1:]
	}
	return urn
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

// reconcileInlineRefs replaces a page's source=inline references with those
// mentioned in its body, leaving promoted and manual references untouched. It is
// best-effort: a failure (for example an inline reference to an entity that no
// longer exists) is logged but does not fail the page write, since the body
// itself is valid. Called after every create/update that writes the body.
func (h *Handler) reconcileInlineRefs(ctx context.Context, pageID, body string) {
	refs := knowledgepage.ScanBodyRefs(body)
	if err := h.deps.KnowledgePageStore.ReplaceEntityRefsBySource(ctx, pageID, knowledgepage.RefSourceInline, refs); err != nil {
		slog.WarnContext(ctx, "reconcile inline knowledge-page references failed",
			"page_id", pageID, "error", err)
	}
}
