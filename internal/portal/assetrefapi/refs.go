package assetrefapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// refView is one reference as the asset's sidebar renders it: enough of the
// resource to show a row and a picture, plus where the asset's content still
// names it.
//
// The resource metadata is shown to every reader who can open the asset, not
// only to one who could read the resource directly. That is not a widening:
// the reference already hands such a reader the file's bytes through
// ContentURL, and a name beside a picture they can already see discloses
// nothing further. A reference whose resource has been deleted carries none of
// it and is flagged Broken instead.
type refView struct {
	ResourceID string `json:"resource_id"`
	URI        string `json:"uri"`
	Position   int    `json:"position"`
	DeclaredBy string `json:"declared_by,omitempty"`

	DisplayName string `json:"display_name,omitempty"`
	Filename    string `json:"filename,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	Scope       string `json:"scope,omitempty"`
	ScopeID     string `json:"scope_id,omitempty"`

	// ContentURL is the reference's own serving URL, the same one the rewrite
	// writes into the content this reader is served. It is here so the panel
	// can show a thumbnail through the grant the reference already makes,
	// rather than through the resource route, which a reader of a shared asset
	// may not be allowed to call.
	ContentURL string `json:"content_url,omitempty"`

	// Broken marks a reference whose resource has been deleted. The row
	// survives the delete on purpose (#1474), so this is the one place the
	// owner learns their report is now serving with a picture missing.
	Broken bool `json:"broken,omitempty"`

	// Readable is whether this reader could open the resource on its own, as
	// opposed to through the asset. It decides whether the row is a link: a
	// reader of a shared asset can see a file they have no direct access to,
	// and a link to the resource's own page would only lead them to a
	// not-found.
	Readable bool `json:"readable,omitempty"`

	// Occurrences names where the asset's stored content still writes this
	// URI. Empty means the content does not name it -- either because it never
	// did, or because the content could not be read; see scanOccurrences.
	Occurrences []occurrence `json:"occurrences,omitempty"`
}

// audience states how widely the asset this reference hangs off is shared. It
// is what "adding this reference gives the file the asset's audience" means in
// the concrete, and the add confirmation names it rather than describing the
// rule in the abstract.
type audience struct {
	// Public is true when an active link share exists, which is the reading
	// that matters: a link share is readable by anyone who holds it, with no
	// account at all.
	Public bool `json:"public"`
	// SharedWithUsers is true when the asset is shared with named people.
	SharedWithUsers bool `json:"shared_with_users"`
}

// listResponse is the reference-panel payload: the references, what changing
// them would mean, and whether this reader may change them.
type listResponse struct {
	Data     []refView `json:"data"`
	Total    int       `json:"total"`
	Audience audience  `json:"audience"`
	// CanEdit is this reader's authority over the list. The panel offers add
	// and remove on it rather than re-deriving ownership in the browser, so
	// the control a reader sees and the answer the route gives cannot differ.
	CanEdit bool `json:"can_edit"`
	// Max is the per-asset reference cap, so the panel can say what the limit
	// is before a caller hits it.
	Max int `json:"max"`
	// Notice is the sentence stating what a reference gives away, the same one
	// the toolkit shows an agent at the moment it declares one.
	Notice string `json:"notice"`
	// ContentScanned reports whether the asset's stored content was read to
	// find where it writes each URI. False means the occurrence lists say
	// nothing at all: the content is binary, too large, or could not be read.
	//
	// It is a separate field rather than an inference from empty occurrences
	// because the two mean opposite things to the person removing a reference.
	// "The content does not name this" makes a removal safe; "we could not
	// look" does not, and a client that could not tell them apart would
	// withdraw a grant from a live report without a word.
	ContentScanned bool `json:"content_scanned"`
}

// addRequest names the resource to reference. The id is what a picker over the
// caller's readable resources has in hand; the URI is not accepted, because a
// person choosing from a list is not writing one.
type addRequest struct {
	ResourceID string `json:"resource_id"`
}

// listRefs returns an asset's referenced resources, to any reader who may open
// the asset.
//
// @Summary      List an asset's referenced resources
// @Description  Returns the managed resources an asset's content references, with enough of each resource to render a row, the URL the reference is served under, and where the asset's stored content still writes the URI. A reference whose resource was deleted is returned flagged as broken.
// @Tags         Portal
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  listResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/resources [get]
func (h *handler) listRefs(w http.ResponseWriter, r *http.Request) {
	user := caller(w, r)
	if user == nil {
		return
	}
	asset, ok := h.viewableAsset(w, r, user)
	if !ok {
		return
	}
	refs, ok := h.refsOf(w, r, asset.ID)
	if !ok {
		return
	}
	h.writeList(w, r, listing{
		asset:   asset,
		user:    user,
		refs:    refs,
		canEdit: h.access.CanEditAssetSilent(r.Context(), asset.ID, user),
	})
}

// addRef references one more resource from this asset.
//
// It checks the caller's own read permission on the resource, exactly as the
// declaration path checks an agent's: a person adding a reference through the
// panel makes the same grant an agent makes by naming the URI in a save, and
// must have been able to read the file to make it.
//
// Adding does not touch the asset's content. The URI has to appear in the
// markup for the picture to render, and that is the author's edit to make; the
// response carries the URI so the panel can hand it over.
//
// @Summary      Reference a resource from an asset
// @Description  Adds one managed resource to the asset's references, checked against the caller's own read permission on that resource. The asset's stored content is not changed. Returns the asset's references after the add.
// @Tags         Portal
// @Accept       json
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  listResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/resources [post]
func (h *handler) addRef(w http.ResponseWriter, r *http.Request) {
	user, asset, ok := h.editableAsset(w, r)
	if !ok {
		return
	}
	var req addRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ResourceID == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "a resource_id is required")
		return
	}
	res, ok := h.readableResource(w, r, req.ResourceID, user)
	if !ok {
		return
	}
	refs, ok := h.refsOf(w, r, asset.ID)
	if !ok {
		return
	}
	// The cap is checked against what the asset names now. It is advisory
	// against a concurrent save, exactly as the declaration path's is, because
	// nothing about a reference makes the twenty-first worth a lock.
	if len(refs) >= portaldomain.MaxAssetResourceRefs {
		httpjson.WriteError(w, http.StatusConflict, capReached())
		return
	}
	token, err := portaldomain.GenerateRefToken()
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to add the reference")
		return
	}
	ref := portaldomain.AssetResourceRef{
		AssetID:    asset.ID,
		ResourceID: res.ID,
		URI:        res.URI,
		RefToken:   token,
		DeclaredBy: user.Email,
	}
	added, err := h.cfg.Refs.Attach(r.Context(), ref)
	if err != nil {
		slog.Error("asset resource references: attach failed",
			logKeyAssetID, logsan.SanitizeForLog(asset.ID),
			logKeyError, logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to save the reference")
		return
	}
	if !added {
		httpjson.WriteError(w, http.StatusConflict, "this asset already references that file")
		return
	}
	// The same record the agent's save writes, from the other door: an
	// operator's log has to show every grant, not the half made by agents.
	assetrefs.LogGranted(asset.ID, ref)
	h.reloadAndWrite(w, r, asset, user)
}

// removeRef stops this asset referencing a resource.
//
// It is allowed even when the content still names the URI. The reference and
// the markup are two facts an author keeps in step themselves, and the panel
// warns with the lines the URI appears on before it asks; refusing here would
// leave someone unable to withdraw a grant until they had edited a document.
//
// @Summary      Remove an asset's reference to a resource
// @Description  Removes one managed resource from the asset's references. The asset's stored content is not changed, so a URI still written in the markup stops resolving. Returns the asset's references after the removal.
// @Tags         Portal
// @Produce      json
// @Param        id          path  string  true  "Asset ID"
// @Param        resourceID  path  string  true  "Resource ID"
// @Success      200  {object}  listResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/resources/{resourceID} [delete]
func (h *handler) removeRef(w http.ResponseWriter, r *http.Request) {
	user, asset, ok := h.editableAsset(w, r)
	if !ok {
		return
	}
	resourceID := r.PathValue(pathKeyResource)
	removed, err := h.cfg.Refs.Detach(r.Context(), asset.ID, resourceID)
	if err != nil {
		slog.Error("asset resource references: detach failed",
			logKeyAssetID, logsan.SanitizeForLog(asset.ID),
			logKeyError, logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to remove the reference")
		return
	}
	if !removed {
		httpjson.WriteError(w, http.StatusNotFound, "this asset does not reference that file")
		return
	}
	assetrefs.LogRevoked(asset.ID, resourceID, user.Email)
	h.reloadAndWrite(w, r, asset, user)
}

// listing is one answer's inputs, gathered so the writer takes a subject rather
// than six positional arguments.
//
// canEdit is carried rather than re-derived: a mutation reached this point
// through editableAsset, which has already established it, and
// CanEditAssetSilent re-reads the asset and its shares on every call.
type listing struct {
	asset   *portaldomain.Asset
	user    *access.User
	refs    []portaldomain.AssetResourceRef
	canEdit bool
}

// writeList answers with the asset's references. Every route ends here, so the
// three cannot answer in different shapes.
func (h *handler) writeList(w http.ResponseWriter, r *http.Request, l listing) {
	asset, user, refs := l.asset, l.user, l.refs
	views, scanned := h.views(r, asset, user, refs)
	httpjson.WriteJSON(w, http.StatusOK, listResponse{
		Data:           views,
		Total:          len(refs),
		Audience:       h.audienceOf(r, asset.ID),
		CanEdit:        l.canEdit,
		Max:            portaldomain.MaxAssetResourceRefs,
		Notice:         assetrefs.GrantNotice,
		ContentScanned: scanned,
	})
}

// reloadAndWrite reads the asset's references back after a mutation and answers
// with them.
//
// It re-reads rather than answering from the list the handler built, which is
// what keeps a portal add from reporting a state that never existed: the write
// touched one row, and a concurrent save may have changed the others.
func (h *handler) reloadAndWrite(
	w http.ResponseWriter, r *http.Request, asset *portaldomain.Asset, user *access.User,
) {
	refs, ok := h.refsOf(w, r, asset.ID)
	if !ok {
		return
	}
	h.writeList(w, r, listing{asset: asset, user: user, refs: refs, canEdit: true})
}

// views renders the reference rows: the resource behind each one, the URL it is
// served under, and where the content still names it.
//
// A resource that cannot be read from the store is rendered broken rather than
// omitted, on the same terms a deleted one is: the row is what tells the owner
// their report is serving without that file.
func (h *handler) views(
	r *http.Request, asset *portaldomain.Asset, user *access.User,
	refs []portaldomain.AssetResourceRef,
) ([]refView, bool) {
	resources := h.resourcesOf(r, refs)
	occurrences, scanned := h.scanContent(r, asset, refs)
	claims := h.cfg.Claims(user)

	out := make([]refView, 0, len(refs))
	for _, ref := range refs {
		view := refView{
			ResourceID:  ref.ResourceID,
			URI:         ref.URI,
			Position:    ref.Position,
			DeclaredBy:  ref.DeclaredBy,
			ContentURL:  assetrefs.URL("", asset.ID, ref.RefToken),
			Occurrences: occurrences[ref.ResourceID],
		}
		res := resources[ref.ResourceID]
		if res == nil {
			view.Broken = true
			out = append(out, view)
			continue
		}
		view.DisplayName = res.DisplayName
		view.Filename = res.Filename
		view.Description = res.Description
		view.Category = res.Category
		view.MIMEType = res.MIMEType
		view.SizeBytes = res.SizeBytes
		view.Scope = string(res.Scope)
		view.ScopeID = res.ScopeID
		view.Readable = resource.CanReadResource(claims, res)
		out = append(out, view)
	}
	return out, scanned
}

// resourcesOf reads every referenced resource in one call. A read failure
// yields an empty map, which renders every row broken -- the honest answer when
// the platform cannot say whether the files are still there.
func (h *handler) resourcesOf(
	r *http.Request, refs []portaldomain.AssetResourceRef,
) map[string]*resource.Resource {
	if len(refs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ResourceID)
	}
	resources, err := h.cfg.Resources.GetByIDs(r.Context(), ids)
	if err != nil {
		slog.Warn("asset resource references: reading referenced resources failed",
			logKeyError, logsan.SanitizeForLog(err.Error()))
		return nil
	}
	return resources
}

// audienceOf reports how widely the asset is shared. With no share store, or on
// a read failure, it reports neither: an unflagged asset is a smaller failure
// than a wrong flag, and the notice beside it states the rule regardless.
func (h *handler) audienceOf(r *http.Request, assetID string) audience {
	if h.cfg.Shares == nil {
		return audience{}
	}
	summaries, err := h.cfg.Shares.ListActiveShareSummaries(r.Context(), []string{assetID})
	if err != nil {
		slog.Warn("asset resource references: reading share summary failed",
			logKeyAssetID, logsan.SanitizeForLog(assetID),
			logKeyError, logsan.SanitizeForLog(err.Error()))
		return audience{}
	}
	summary := summaries[assetID]
	return audience{Public: summary.HasPublicLink, SharedWithUsers: summary.HasUserShare}
}

// refsOf reads one asset's references, writing the failure response itself.
func (h *handler) refsOf(
	w http.ResponseWriter, r *http.Request, assetID string,
) ([]portaldomain.AssetResourceRef, bool) {
	refs, err := h.cfg.Refs.ListByAsset(r.Context(), assetID)
	if err != nil {
		slog.Error("asset resource references: list failed",
			logKeyAssetID, logsan.SanitizeForLog(assetID),
			logKeyError, logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list the referenced files")
		return nil, false
	}
	return refs, true
}

// viewableAsset loads the {id} asset and refuses a reader who may not open it.
//
// Owner authority is checked before the share cascade, which admits an
// administrator as well as the owner. The panel is rendered by the same asset
// viewer on the portal and in the admin console, and an operator reviewing an
// asset has to be able to see which files it depends on -- the reason #1474
// put the console on the list of surfaces that resolve a reference at all.
//
// A missing asset and a store failure are both answered as not found, matching
// the parent's own asset read: this route is addressed by an id the caller
// already holds, and splitting the two would say more about which ids exist
// than the parent does.
func (h *handler) viewableAsset(
	w http.ResponseWriter, r *http.Request, user *access.User,
) (*portaldomain.Asset, bool) {
	id := r.PathValue(pathKeyID)
	asset, err := h.cfg.Assets.Get(r.Context(), id)
	if err != nil || asset == nil {
		httpjson.WriteError(w, http.StatusNotFound, errAssetNotFound)
		return nil, false
	}
	if asset.DeletedAt != nil {
		httpjson.WriteError(w, http.StatusGone, errAssetDeleted)
		return nil, false
	}
	if h.access.CanManage(asset.OwnerID, user) {
		return asset, true
	}
	granted, err := h.access.AssetViewGrant(r.Context(), id, asset, user)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to check share access")
		return nil, false
	}
	if !granted {
		httpjson.WriteError(w, http.StatusForbidden, errAccessDenied)
		return nil, false
	}
	return asset, true
}

// editableAsset loads the {id} asset for a caller who may change its
// references: the owner, an editor on a shared asset, or an administrator.
// Changing a reference is changing what the asset is made of, which is the same
// authority editing its content takes.
func (h *handler) editableAsset(
	w http.ResponseWriter, r *http.Request,
) (*access.User, *portaldomain.Asset, bool) {
	user := caller(w, r)
	if user == nil {
		return nil, nil, false
	}
	asset, ok := h.viewableAsset(w, r, user)
	if !ok {
		return nil, nil, false
	}
	if !h.access.CanEditAssetSilent(r.Context(), asset.ID, user) {
		httpjson.WriteError(w, http.StatusForbidden, errNotEditable)
		return nil, nil, false
	}
	return user, asset, true
}

// readableResource loads a resource the caller named and refuses one they
// cannot read. A resource that does not exist and one outside the caller's
// reach get the same answer, so being refused cannot be used to learn that a
// file is there.
func (h *handler) readableResource(
	w http.ResponseWriter, r *http.Request, id string, user *access.User,
) (*resource.Resource, bool) {
	res, err := h.cfg.Resources.Get(r.Context(), id)
	if err != nil && !resource.IsNotFound(err) {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read resource")
		return nil, false
	}
	if res == nil || !resource.CanReadResource(h.cfg.Claims(user), res) {
		httpjson.WriteError(w, http.StatusNotFound, errResourceNotFound)
		return nil, false
	}
	return res, true
}
