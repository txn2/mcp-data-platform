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
			out = append(out, resolvedRef{URN: s, Label: s, Exists: false})
			continue
		}
		out = append(out, h.resolveRef(r.Context(), user, s, ref))
	}
	writeJSON(w, http.StatusOK, resolveRefsResponse{Refs: out})
}

// resolveRef resolves a single reference to a display label. Knowledge pages are
// org-shared so their title and existence are resolved for any reader. Asset and
// collection names are only revealed to their owner, and their existence is never
// revealed, so the endpoint cannot enumerate names or existence across the share
// boundary (share-aware, page-scoped resolution is a later phase). Connection,
// prompt, and DataHub labels derive from the URN itself.
func (h *Handler) resolveRef(ctx context.Context, user *User, urn string, ref knowledgepage.EntityRef) resolvedRef {
	out := resolvedRef{URN: urn, Type: ref.TargetType, Label: urn, Exists: true}
	switch ref.TargetType {
	case knowledgepage.RefTargetAsset:
		out.Label = h.resolveOwnedAssetLabel(ctx, user, ref.AssetID)
	case knowledgepage.RefTargetCollection:
		out.Label = h.resolveOwnedCollectionLabel(ctx, user, ref.CollectionID)
	case knowledgepage.RefTargetKnowledgePage:
		out.Label, out.Exists = h.resolvePageLabel(ctx, ref.RefPageID)
	case knowledgepage.RefTargetConnection:
		out.Label = ref.ConnectionName + " (" + ref.ConnectionKind + ")"
	case knowledgepage.RefTargetPrompt:
		out.Label = ref.PromptID
	case knowledgepage.RefTargetDataHub:
		out.Label = datahubLabel(ref.EntityURN)
	}
	return out
}

// resolveOwnedAssetLabel returns the asset's name only when the requesting user
// owns it; otherwise the id is returned and existence is not signaled, so the
// endpoint cannot leak names or confirm existence of assets the user cannot view.
func (h *Handler) resolveOwnedAssetLabel(ctx context.Context, user *User, id string) string {
	if h.deps.AssetStore == nil {
		return id
	}
	if a, err := h.deps.AssetStore.Get(ctx, id); err == nil && a.OwnerID == user.UserID {
		return a.Name
	}
	return id
}

// resolveOwnedCollectionLabel returns the collection's name only when the user owns it.
func (h *Handler) resolveOwnedCollectionLabel(ctx context.Context, user *User, id string) string {
	if h.deps.CollectionStore == nil {
		return id
	}
	if c, err := h.deps.CollectionStore.Get(ctx, id); err == nil && c.OwnerID == user.UserID {
		return c.Name
	}
	return id
}

// resolvePageLabel returns a knowledge page's title and existence.
func (h *Handler) resolvePageLabel(ctx context.Context, id string) (string, bool) {
	if p, err := h.deps.KnowledgePageStore.Get(ctx, id); err == nil {
		return p.Title, true
	}
	return id, false
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

func entityRefViews(refs []knowledgepage.EntityRef) []entityRefView {
	views := make([]entityRefView, 0, len(refs))
	for _, ref := range refs {
		views = append(views, entityRefView{EntityRef: ref, URN: ref.URN()})
	}
	return views
}
