package libraryapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/library"
	"github.com/txn2/mcp-data-platform/internal/portal/portalpurge"
)

// The periods Unused offers, in days, and the one it reads by default.
var unusedPeriods = map[int]bool{30: true, 90: true, 180: true}

const defaultUnusedDays = 90

// statusResponse reports what a trash action did.
type statusResponse struct {
	Status string `json:"status" example:"restored"`
}

// TrashResponse is a page of the trash.
type TrashResponse struct {
	Items []library.TrashItem `json:"items"`
	Total int                 `json:"total"`
	// RetentionDays is how long a deleted item stays before it is purged.
	RetentionDays int `json:"retention_days" example:"30"`
}

// UnusedResponse is a page of unused assets.
type UnusedResponse struct {
	Items []library.UnusedItem `json:"items"`
	Total int                  `json:"total"`
	// Days is the period the page was read for.
	Days int `json:"days" example:"90"`
}

// DuplicatesResponse is the possible-duplicates groups.
type DuplicatesResponse struct {
	Groups []library.DuplicateGroup `json:"groups"`
}

// DismissRequest names the group to dismiss by its digest.
type DismissRequest struct {
	Digest string `json:"digest" example:"9f2c4e"`
}

// scope reads whose rows an owner listing covers. everyone=true is an
// administrator's alone; anyone else asking is refused.
func (a *API) scope(w http.ResponseWriter, r *http.Request) (*access.User, library.Scope, bool) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, library.Scope{}, false
	}
	everyone, _ := strconv.ParseBool(r.URL.Query().Get("everyone"))
	if everyone && !a.access.IsAdmin(user) {
		httpjson.WriteError(w, http.StatusForbidden, errEveryone)
		return nil, library.Scope{}, false
	}
	return user, library.Scope{Owner: access.AssetOwnerOf(user), Everyone: everyone}, true
}

func pageParams(r *http.Request) (limit, offset int) {
	q := r.URL.Query()
	limit, _ = strconv.Atoi(q.Get("limit"))
	offset, _ = strconv.Atoi(q.Get("offset"))
	return limit, offset
}

// trash handles GET /api/v1/portal/trash.
//
// @Summary      List my trash
// @Description  The caller's deleted assets and collections, most recently deleted first, each with the day the retention sweep purges it for good (#2509). An administrator may pass everyone=true for every owner's trash; anyone else asking is refused with 403.
// @Tags         Library
// @Produce      json
// @Param        everyone  query  bool  false  "Every owner's trash (administrators)"
// @Param        limit     query  int   false  "Entries (default 20, at most 200)"
// @Param        offset    query  int   false  "Entries to skip"
// @Success      200  {object}  TrashResponse
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/trash [get]
func (a *API) trash(w http.ResponseWriter, r *http.Request) {
	_, scope, ok := a.scope(w, r)
	if !ok {
		return
	}
	limit, offset := pageParams(r)
	page, err := a.cfg.Library.Trash(r.Context(), scope, a.cfg.Retention, limit, offset)
	if err != nil {
		fail(w, r, errRead, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, TrashResponse{
		Items: page.Items, Total: page.Total, RetentionDays: int(a.cfg.Retention / (24 * time.Hour)),
	})
}

// restore handles POST /api/v1/portal/trash/{kind}/{id}/restore.
//
// @Summary      Restore from the trash
// @Description  Takes a deleted asset or collection out of the trash with its versions, shares and collection memberships (#2509). Its owner or an administrator only; anyone else, and an item not in the trash, gets 404.
// @Tags         Library
// @Produce      json
// @Param        kind  path  string  true  "asset or collection"
// @Param        id    path  string  true  "Item id"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/trash/{kind}/{id}/restore [post]
func (a *API) restore(w http.ResponseWriter, r *http.Request) {
	kind, id, ok := a.trashed(w, r)
	if !ok {
		return
	}
	err := a.cfg.Library.Restore(r.Context(), kind, id)
	switch {
	case errors.Is(err, library.ErrNotDeleted):
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
	case err != nil:
		fail(w, r, errWrite, err)
	default:
		httpjson.WriteJSON(w, http.StatusOK, statusResponse{Status: "restored"})
	}
}

// purge handles DELETE /api/v1/portal/trash/{kind}/{id}.
//
// @Summary      Delete permanently
// @Description  Removes a deleted asset or collection for good now, with its versions, stored files, shares and comments, as the retention sweep would after the grace period (#2509). Its owner or an administrator only; anyone else, and an item not in the trash, gets 404.
// @Tags         Library
// @Produce      json
// @Param        kind  path  string  true  "asset or collection"
// @Param        id    path  string  true  "Item id"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/trash/{kind}/{id} [delete]
func (a *API) purge(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Purger == nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "this deployment purges deleted items only on its retention schedule")
		return
	}
	kind, id, ok := a.trashed(w, r)
	if !ok {
		return
	}
	purge := a.cfg.Purger.PurgeAsset
	if kind == library.KindCollection {
		purge = a.cfg.Purger.PurgeCollection
	}
	err := purge(r.Context(), id)
	switch {
	case errors.Is(err, portalpurge.ErrNotInTrash):
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
	case errors.Is(err, portalpurge.ErrObjectsKept):
		httpjson.WriteError(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		fail(w, r, errWrite, err)
	default:
		httpjson.WriteJSON(w, http.StatusOK, statusResponse{Status: "purged"})
	}
}

// trashed resolves a trash route's item and checks the caller may manage it,
// writing the refusal itself. An item that does not exist, is not deleted,
// or is not the caller's gets one answer, 404.
func (a *API) trashed(w http.ResponseWriter, r *http.Request) (library.Kind, string, bool) {
	kind, ok := pathKind(w, r)
	if !ok {
		return "", "", false
	}
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return "", "", false
	}
	id := r.PathValue("id")
	if !a.managesDeleted(r, user, kind, id) {
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
		return "", "", false
	}
	return kind, id, true
}

func (a *API) managesDeleted(r *http.Request, user *access.User, kind library.Kind, id string) bool {
	if kind == library.KindCollection {
		if a.cfg.Collections == nil {
			return false
		}
		c, err := a.cfg.Collections.Get(r.Context(), id)
		return err == nil && c != nil && c.DeletedAt != nil && a.access.CanManage(c.OwnerID, user)
	}
	asset, err := a.cfg.Assets.Get(r.Context(), id)
	return err == nil && asset != nil && asset.DeletedAt != nil && a.access.CanManageAsset(asset, user)
}

// unused handles GET /api/v1/portal/unused.
//
// @Summary      List my unused assets
// @Description  The caller's assets nobody has opened in the portal in the last days (30, 90 or 180; default 90), never opened first, with open counts and the last open but never who opened (#2509). Opens are counted from the release that started recording them. An administrator may pass everyone=true.
// @Tags         Library
// @Produce      json
// @Param        days      query  int   false  "30, 90 or 180"
// @Param        everyone  query  bool  false  "Every owner's assets (administrators)"
// @Param        limit     query  int   false  "Entries (default 20, at most 200)"
// @Param        offset    query  int   false  "Entries to skip"
// @Success      200  {object}  UnusedResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/unused [get]
func (a *API) unused(w http.ResponseWriter, r *http.Request) {
	_, scope, ok := a.scope(w, r)
	if !ok {
		return
	}
	days := defaultUnusedDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		days, _ = strconv.Atoi(raw)
	}
	if !unusedPeriods[days] {
		httpjson.WriteError(w, http.StatusBadRequest, "days must be 30, 90 or 180")
		return
	}
	limit, offset := pageParams(r)
	since := a.now().Add(-time.Duration(days) * 24 * time.Hour)
	page, err := a.cfg.Library.Unused(r.Context(), scope, since, limit, offset)
	if err != nil {
		fail(w, r, errRead, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, UnusedResponse{Items: page.Items, Total: page.Total, Days: days})
}

func (a *API) now() time.Time {
	if a.cfg.Now != nil {
		return a.cfg.Now()
	}
	return time.Now()
}

// duplicates handles GET /api/v1/portal/duplicates.
//
// @Summary      List my possible duplicates
// @Description  Groups of the caller's assets whose name, description and tags are nearly the same, by the closeness save_asset's similar_assets uses (#2509), most alike first. A group the caller dismissed is left out until one of its assets gets a new version. An administrator may pass everyone=true; each group is still one owner's.
// @Tags         Library
// @Produce      json
// @Param        everyone  query  bool  false  "Every owner's groups (administrators)"
// @Success      200  {object}  DuplicatesResponse
// @Failure      403  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/duplicates [get]
func (a *API) duplicates(w http.ResponseWriter, r *http.Request) {
	user, scope, ok := a.scope(w, r)
	if !ok {
		return
	}
	groups, err := a.cfg.Library.Duplicates(r.Context(), library.UserKey(user.UserID, user.Email), scope)
	if err != nil {
		fail(w, r, errRead, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, DuplicatesResponse{Groups: groups})
}

// dismiss handles POST /api/v1/portal/duplicates/dismissals.
//
// @Summary      Dismiss a possible-duplicates group
// @Description  Hides a group from the caller until one of its assets gets a new version or the group gains or loses a member (#2509).
// @Tags         Library
// @Accept       json
// @Param        body  body  DismissRequest  true  "The group's digest"
// @Success      204
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/duplicates/dismissals [post]
func (a *API) dismiss(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	var body DismissRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&body); err != nil || !validDigest(body.Digest) {
		httpjson.WriteError(w, http.StatusBadRequest, errBadBody)
		return
	}
	if err := a.cfg.Library.Dismiss(r.Context(), library.UserKey(user.UserID, user.Email), body.Digest); err != nil {
		fail(w, r, errWrite, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// undismiss handles DELETE /api/v1/portal/duplicates/dismissals/{digest}.
//
// @Summary      Undo a dismissal
// @Description  Shows a dismissed possible-duplicates group again.
// @Tags         Library
// @Param        digest  path  string  true  "The group's digest"
// @Success      204
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/duplicates/dismissals/{digest} [delete]
func (a *API) undismiss(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	digest := r.PathValue("digest")
	if !validDigest(digest) {
		httpjson.WriteError(w, http.StatusBadRequest, errBadBody)
		return
	}
	if err := a.cfg.Library.Undismiss(r.Context(), library.UserKey(user.UserID, user.Email), digest); err != nil {
		fail(w, r, errWrite, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// digestLen is a group digest's length: a SHA-256 in hex.
const digestLen = 64

// validDigest accepts a group digest as library.Digest writes it: 64
// lowercase hex characters.
func validDigest(d string) bool {
	if len(d) != digestLen {
		return false
	}
	for _, c := range d {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
