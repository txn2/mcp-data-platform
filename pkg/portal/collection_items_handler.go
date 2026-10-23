package portal

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// maxCollectionTargets bounds the collections the add-to-collection picker
// lists. The picker searches on the server, so a person with more collections
// than this finds the rest by name.
const maxCollectionTargets = 100

// appendItemRequest is the body of POST /collections/{id}/items.
type appendItemRequest struct {
	AssetID string `json:"asset_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	// SectionID names the section to add to. Empty adds to the last section,
	// or to a new untitled section when the collection has none.
	SectionID string `json:"section_id,omitempty" example:"sec_01HK7R9C"`
}

// collectionTargetsResponse is the body of GET /assets/{id}/collection-targets.
type collectionTargetsResponse struct {
	Data []portaldomain.CollectionTarget `json:"data"`
}

// registerCollectionItemRoutes mounts the add-to-collection routes (#2260)
// when the collection store can append a single item.
func (h *Handler) registerCollectionItemRoutes() {
	if _, ok := h.deps.CollectionStore.(portaldomain.CollectionItemAppender); !ok {
		return
	}
	h.mux.HandleFunc("POST /api/v1/portal/collections/{id}/items", h.appendCollectionItem)
	h.mux.HandleFunc("GET /api/v1/portal/assets/{id}/collection-targets", h.listCollectionTargets)
}

// appendCollectionItem handles POST /api/v1/portal/collections/{id}/items.
//
// @Summary      Add an asset to a collection
// @Description  Adds one asset at the end of one section, leaving the collection's name, description, config, other sections and item order as they were. With no section_id the asset goes to the last section, or to a new untitled section when the collection has none. The owner, an admin, or a collection Editor, who must also be able to open the asset. 409 when the asset is already in the section, the section is full, or the section is no longer in the collection.
// @Tags         Collections
// @Accept       json
// @Produce      json
// @Param        id    path  string             true  "Collection ID"
// @Param        body  body  appendItemRequest  true  "The asset, and optionally the section"
// @Success      201  {object}  portaldomain.AppendedItem
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/items [post]
func (h *Handler) appendCollectionItem(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	var req appendItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.AssetID) == "" {
		writeError(w, http.StatusBadRequest, "asset_id is required")
		return
	}
	coll, err := h.deps.CollectionStore.Get(r.Context(), r.PathValue(pathKeyID))
	if err != nil || coll.DeletedAt != nil {
		writeError(w, http.StatusNotFound, errCollectionNotFound)
		return
	}
	if !h.access.CanEditCollection(r.Context(), coll, user) {
		writeError(w, http.StatusForbidden, "only the owner or an editor can add to this collection")
		return
	}
	if status, msg := h.assetAddable(r.Context(), req.AssetID, user); status != 0 {
		writeError(w, status, msg)
		return
	}
	h.writeAppend(w, r.Context(), coll.ID, req.SectionID, req.AssetID)
}

// writeAppend appends the item and answers with what was written, mapping the
// store's refusals to their statuses.
func (h *Handler) writeAppend(w http.ResponseWriter, ctx context.Context, collectionID, sectionID, assetID string) { //nolint:revive // writer first reads as the response it fills
	appender, _ := h.deps.CollectionStore.(portaldomain.CollectionItemAppender)
	added, err := appender.AppendItem(ctx, collectionID, sectionID, assetID)
	switch {
	case errors.Is(err, portaldomain.ErrItemAlreadyInSection),
		errors.Is(err, portaldomain.ErrSectionFull),
		errors.Is(err, portaldomain.ErrSectionNotFound):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to add the asset to the collection")
	default:
		writeJSON(w, http.StatusCreated, added)
	}
}

// seedCollection makes assetID the first item of a collection just created,
// in one untitled section, and removes the collection again when that write
// fails, so New collection never leaves an empty one behind. An empty assetID
// writes nothing.
func (h *Handler) seedCollection(ctx context.Context, collectionID, assetID string) error {
	if assetID == "" {
		return nil
	}
	sections := []CollectionSection{{
		ID:    uuid.New().String(),
		Items: []CollectionItem{{ID: uuid.New().String(), AssetID: assetID}},
	}}
	if err := h.deps.CollectionStore.SetSections(ctx, collectionID, sections); err != nil {
		_ = h.deps.CollectionStore.SoftDelete(ctx, collectionID)
		return err //nolint:wrapcheck // the handler reports a fixed message
	}
	return nil
}

// assetAddable reports why the caller may not put the asset in a collection, as
// a status and message, or zero when they may: it must exist, not be deleted,
// and be one the caller can open (an administrator opens every asset).
func (h *Handler) assetAddable(ctx context.Context, assetID string, user *User) (status int, msg string) {
	asset, err := h.deps.AssetStore.Get(ctx, assetID)
	if err != nil {
		return http.StatusNotFound, errAssetNotFound
	}
	if asset.DeletedAt != nil {
		return http.StatusNotFound, errAssetDeleted
	}
	if !h.access.CanManageAsset(asset, user) && !h.access.CanViewAsset(ctx, assetID, asset, user) {
		return http.StatusForbidden, errAccessDenied
	}
	return 0, ""
}

// listCollectionTargets handles GET /api/v1/portal/assets/{id}/collection-targets.
//
// @Summary      Collections an asset can be added to
// @Description  Lists the collections the caller can edit (their own, those shared with them as Editor, and for an administrator every collection), most recently updated first, each with its sections and whether the asset is already in each. search narrows by name, description or owner address. At most 100.
// @Tags         Collections
// @Produce      json
// @Param        id      path   string  true   "Asset ID"
// @Param        search  query  string  false  "Narrow by collection name, description or owner address"
// @Success      200  {object}  collectionTargetsResponse
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/collection-targets [get]
func (h *Handler) listCollectionTargets(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	assetID := r.PathValue(pathKeyID)
	if status, msg := h.assetAddable(r.Context(), assetID, user); status != 0 {
		writeError(w, status, msg)
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	targets, err := h.editableCollections(r.Context(), user, search)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list collections")
		return
	}
	ids := make([]string, len(targets))
	for i, t := range targets {
		ids[i] = t.ID
	}
	appender, _ := h.deps.CollectionStore.(portaldomain.CollectionItemAppender)
	sections, err := appender.SectionSummaries(r.Context(), ids, assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read collection sections")
		return
	}
	for i := range targets {
		targets[i].Sections = sections[targets[i].ID]
		if targets[i].Sections == nil {
			targets[i].Sections = []portaldomain.SectionSummary{}
		}
	}
	writeJSON(w, http.StatusOK, collectionTargetsResponse{Data: targets})
}

// editableCollections gathers the collections the caller can edit, newest
// first: owned (every collection, for an administrator) and those shared with
// them at a permission that grants editing.
func (h *Handler) editableCollections(ctx context.Context, user *User, search string) ([]portaldomain.CollectionTarget, error) {
	filter := CollectionFilter{Search: search, Limit: maxCollectionTargets, SortBy: portaldomain.SortUpdatedAt, SortDir: portaldomain.SortDesc}
	if !h.access.IsAdmin(user) {
		filter.OwnerID = user.UserID
	}
	owned, _, err := h.deps.CollectionStore.List(ctx, filter)
	if err != nil {
		return nil, err //nolint:wrapcheck // the handler reports a fixed message
	}
	targets := make([]portaldomain.CollectionTarget, 0, len(owned))
	seen := make(map[string]bool, len(owned))
	for _, c := range owned {
		seen[c.ID] = true
		targets = append(targets, portaldomain.CollectionTarget{
			ID: c.ID, Name: c.Name, OwnerEmail: c.OwnerEmail, UpdatedAt: c.UpdatedAt,
			Shared: c.OwnerID != user.UserID,
		})
	}
	shared, _, err := h.deps.ShareStore.ListSharedCollectionsWithUser(ctx, user.UserID, user.Email, portaldomain.MaxLimit, 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // the handler reports a fixed message
	}
	for _, sc := range shared {
		c := sc.Collection
		if seen[c.ID] || !h.sharedEditable(ctx, c, user) || !targetMatches(c, search) {
			continue
		}
		seen[c.ID] = true
		targets = append(targets, portaldomain.CollectionTarget{
			ID: c.ID, Name: c.Name, OwnerEmail: c.OwnerEmail, UpdatedAt: c.UpdatedAt, Shared: true,
		})
	}
	slices.SortStableFunc(targets, func(a, b portaldomain.CollectionTarget) int {
		return cmp.Compare(b.UpdatedAt.UnixNano(), a.UpdatedAt.UnixNano())
	})
	if len(targets) > maxCollectionTargets {
		targets = targets[:maxCollectionTargets]
	}
	return targets, nil
}

// sharedEditable reports whether a collection shared with the caller is one
// they may edit, through the same check the write routes make, so an external
// person's Editor share (capped to Viewer) is left out.
func (h *Handler) sharedEditable(ctx context.Context, c Collection, user *User) bool {
	return access.GrantsEdit(h.access.CollectionSharePermission(ctx, c.ID, user))
}

// targetMatches is the shared half of the picker's search, matching what the
// store's search matches on the owned half: a case-insensitive substring of the
// name, the description or the owner's address, empty matching everything.
func targetMatches(c Collection, search string) bool {
	if search == "" {
		return true
	}
	q := strings.ToLower(search)
	for _, field := range []string{c.Name, c.Description, c.OwnerEmail} {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	return false
}
