package assetrefapi

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// referencingAsset is one asset that names a resource, as the resource's detail
// view lists it.
type referencingAsset struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	OwnerEmail string `json:"owner_email,omitempty"`
	// Public marks an asset carrying an active link share. It is the flag the
	// whole list exists for: a reference carries the asset's audience, so a
	// resource referenced by a publicly shared asset is readable by anyone who
	// holds that link.
	Public bool `json:"public"`
}

// usedByResponse lists the assets referencing a resource that the reader may
// see.
type usedByResponse struct {
	Data  []referencingAsset `json:"data"`
	Total int                `json:"total"`
	// Hidden counts the referencing assets this reader may not open. They are
	// named nowhere, but they are counted: someone deciding whether to delete a
	// file has to know the list they are looking at is not the whole of what
	// would break.
	Hidden int `json:"hidden"`
	// Truncated says the answer was cut at the bound rather than being the
	// whole of what references this file. It is reported rather than left
	// implicit for the reason Hidden is: a short list read as a complete one
	// is the mistake this surface exists to prevent.
	Truncated bool `json:"truncated,omitempty"`
}

// assetsUsingResource answers "what is holding this file up?" for the resource
// detail view, so the cost of editing or deleting it is visible beforehand.
//
// The list is scoped to the reader like every other read: an asset they cannot
// open is counted and not named. The count is the deliberate part -- a resource
// referenced by someone else's private report is still referenced, and a
// delete that silently broke it would be exactly the outcome this list exists
// to prevent.
//
// @Summary      List assets referencing a resource
// @Description  Returns the assets whose content references a managed resource and which the caller may open, flagging any that carry an active public link share. Referencing assets the caller cannot open are counted but not named.
// @Tags         Resources
// @Produce      json
// @Param        id  path  string  true  "Resource ID"
// @Success      200  {object}  usedByResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/resources/{id}/assets [get]
func (h *handler) assetsUsingResource(w http.ResponseWriter, r *http.Request) {
	user := caller(w, r)
	if user == nil {
		return
	}
	res, ok := h.readableResource(w, r, r.PathValue(pathKeyID), user)
	if !ok {
		return
	}
	// One more than the bound, so an answer that was cut can say so.
	refs, err := h.cfg.Refs.ListByResource(r.Context(), res.ID, maxReferencingAssets+1)
	if err != nil {
		slog.Error("asset resource references: listing referencing assets failed",
			logKeyError, logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list the assets using this file")
		return
	}
	truncated := len(refs) > maxReferencingAssets
	if truncated {
		refs = refs[:maxReferencingAssets]
	}
	visible, hidden, err := h.visibleAssets(r, refs, user)
	if err != nil {
		slog.Error("asset resource references: reading referencing assets failed",
			logKeyError, logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list the assets using this file")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, usedByResponse{
		Data:      visible,
		Total:     len(visible),
		Hidden:    hidden,
		Truncated: truncated,
	})
}

// visibleAssets narrows a reference list to the assets this reader may open,
// counting the rest. An asset that no longer exists is neither listed nor
// counted: the reference outlived its asset only in the window before the row
// cascaded away, and reporting it would tell the reader a file is holding up
// something that is gone.
//
// A failure to read the assets is returned rather than absorbed. Every other
// read on this surface degrades, because a degraded answer there is still a
// visible one -- a row flagged broken, an unflagged audience. Here an empty
// list would read as "nothing uses this file", which is the one wrong answer
// the section exists to prevent someone acting on.
func (h *handler) visibleAssets(
	r *http.Request, refs []portaldomain.AssetResourceRef, user *access.User,
) ([]referencingAsset, int, error) {
	if len(refs) == 0 {
		return []referencingAsset{}, 0, nil
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.AssetID)
	}
	assets, err := h.cfg.Assets.GetByIDs(r.Context(), ids)
	if err != nil {
		return nil, 0, fmt.Errorf("reading referencing assets: %w", err)
	}

	out := make([]referencingAsset, 0, len(refs))
	visibleIDs := make([]string, 0, len(refs))
	hidden := 0
	for _, ref := range refs {
		asset := assets[ref.AssetID]
		if asset == nil {
			continue
		}
		// Owner authority first, which admits an administrator as well as the
		// owner, for the reason viewableAsset states: an operator asking what a
		// delete would break has to get the whole answer, not the part of it
		// they happen to hold a share on.
		if !h.access.CanManage(asset.OwnerID, user) &&
			!h.access.CanViewAsset(r.Context(), ref.AssetID, asset, user) {
			hidden++
			continue
		}
		visibleIDs = append(visibleIDs, asset.ID)
		out = append(out, referencingAsset{
			ID:         asset.ID,
			Name:       asset.Name,
			OwnerEmail: asset.OwnerEmail,
		})
	}
	h.flagPublic(r, out, visibleIDs)
	return out, hidden, nil
}

// flagPublic stamps the public-link flag onto the listed assets in one share
// read. With no share store, or on a failure, the flag is left off: an
// unflagged asset understates the audience, which the section's own wording
// covers, where a wrong flag would be read as a fact.
func (h *handler) flagPublic(r *http.Request, listed []referencingAsset, ids []string) {
	if h.cfg.Shares == nil || len(ids) == 0 {
		return
	}
	summaries, err := h.cfg.Shares.ListActiveShareSummaries(r.Context(), ids)
	if err != nil {
		slog.Warn("asset resource references: reading share summaries failed",
			logKeyError, logsan.SanitizeForLog(err.Error()))
		return
	}
	for i := range listed {
		listed[i].Public = summaries[listed[i].ID].HasPublicLink
	}
}
