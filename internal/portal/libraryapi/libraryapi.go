// Package libraryapi serves a person's library over the portal (#2504,
// #2509): recents and favorites, and the trash, unused assets and possible
// duplicates an owner tidies their assets from.
//
// Every route is mounted on the portal's authenticated mux (portal.Deps
// Mounts) and decides access with the portal's authorization core. Starring
// or recording an open needs the caller to open the item, and every list of
// recents or favorites is read under the reach rule, so nothing here names an
// item the caller cannot open. The trash, unused and duplicates lists are the
// caller's own rows; an administrator may ask for everyone's.
package libraryapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/library"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// Refusals the routes answer with.
const (
	errAuthRequired = "authentication required"
	errBadKind      = "kind must be asset or collection"
	errNotFound     = "not found"
	errBadBody      = "invalid request body"
	errEveryone     = "only an administrator can list everyone's"
	errRead         = "the list could not be read"
	errWrite        = "the change could not be saved"
)

// maxBody bounds a request body.
const maxBody = 4 << 10

// Library is the store the routes read and write.
type Library interface {
	RecordOpen(ctx context.Context, userID string, kind library.Kind, id string) error
	Recents(ctx context.Context, userID string, reach *portaldomain.Reach, limit int) ([]library.Item, error)
	Star(ctx context.Context, userID string, kind library.Kind, id string) error
	Unstar(ctx context.Context, userID string, kind library.Kind, id string) error
	Favorites(ctx context.Context, userID string, reach *portaldomain.Reach, limit int) ([]library.Item, error)
	Trash(ctx context.Context, scope library.Scope, retention time.Duration, limit, offset int) (library.Page[library.TrashItem], error)
	Restore(ctx context.Context, kind library.Kind, id string) error
	Unused(ctx context.Context, scope library.Scope, since time.Time, limit, offset int) (library.Page[library.UnusedItem], error)
	Duplicates(ctx context.Context, userID string, scope library.Scope) ([]library.DuplicateGroup, error)
	Dismiss(ctx context.Context, userID, digest string) error
	Undismiss(ctx context.Context, userID, digest string) error
}

// Purger removes one deleted item for good (internal/portal/portalpurge).
type Purger interface {
	PurgeAsset(ctx context.Context, id string) error
	PurgeCollection(ctx context.Context, id string) error
}

// Config is what the routes read.
type Config struct {
	Library     Library
	Assets      portaldomain.AssetStore
	Collections portaldomain.CollectionStore
	// Purger removes a trash item for good; nil leaves Delete permanently
	// to the retention sweep.
	Purger Purger
	// Retention is how long a deleted item stays in the trash
	// (portal.deleted_retention_days).
	Retention time.Duration
	// Now is the clock the unused cutoff is read from; nil is time.Now.
	Now func() time.Time
}

// API serves the library routes.
type API struct {
	cfg    Config
	access *access.Checker
}

// Mount returns the hook that registers the library routes on the portal's
// authenticated mux with its authorization core.
func Mount(cfg Config) func(*http.ServeMux, *access.Checker) {
	return func(mux *http.ServeMux, checker *access.Checker) {
		api := &API{cfg: cfg, access: checker}
		mux.HandleFunc("GET /api/v1/portal/recents", api.recents)
		mux.HandleFunc("POST /api/v1/portal/recents", api.recordOpen)
		mux.HandleFunc("GET /api/v1/portal/favorites", api.favorites)
		mux.HandleFunc("PUT /api/v1/portal/favorites/{kind}/{id}", api.star)
		mux.HandleFunc("DELETE /api/v1/portal/favorites/{kind}/{id}", api.unstar)
		mux.HandleFunc("GET /api/v1/portal/trash", api.trash)
		mux.HandleFunc("POST /api/v1/portal/trash/{kind}/{id}/restore", api.restore)
		mux.HandleFunc("DELETE /api/v1/portal/trash/{kind}/{id}", api.purge)
		mux.HandleFunc("GET /api/v1/portal/unused", api.unused)
		mux.HandleFunc("GET /api/v1/portal/duplicates", api.duplicates)
		mux.HandleFunc("POST /api/v1/portal/duplicates/dismissals", api.dismiss)
		mux.HandleFunc("DELETE /api/v1/portal/duplicates/dismissals/{digest}", api.undismiss)
	}
}

// recents handles GET /api/v1/portal/recents.
//
// @Summary      List what I opened recently
// @Description  The assets and collections the caller opened in the portal, newest first, that they can still open (#2504). An item whose share was revoked is left out, with no placeholder or count.
// @Tags         Library
// @Produce      json
// @Param        limit  query  int  false  "Entries (default 20, at most 200)"
// @Success      200  {object}  ItemsResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/recents [get]
func (a *API) recents(w http.ResponseWriter, r *http.Request) {
	listItems(w, r, a.cfg.Library.Recents)
}

// favorites handles GET /api/v1/portal/favorites.
//
// @Summary      List my favorites
// @Description  The assets and collections the caller starred, newest first, that they can still open (#2504). Favorites are private to the caller and grant nothing.
// @Tags         Library
// @Produce      json
// @Param        limit  query  int  false  "Entries (default 20, at most 200)"
// @Success      200  {object}  ItemsResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/favorites [get]
func (a *API) favorites(w http.ResponseWriter, r *http.Request) {
	listItems(w, r, a.cfg.Library.Favorites)
}

type itemsReader func(ctx context.Context, userID string, reach *portaldomain.Reach, limit int) ([]library.Item, error)

func listItems(w http.ResponseWriter, r *http.Request, read itemsReader) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	reach := portaldomain.NewReach(user.UserID, user.Email, portaldomain.CallerPersona(r.Context()), portaldomain.ScopeAll)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := read(r.Context(), library.UserKey(user.UserID, user.Email), reach, limit)
	if err != nil {
		fail(w, r, errRead, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, ItemsResponse{Items: items})
}

// recordOpen handles POST /api/v1/portal/recents.
//
// @Summary      Record that I opened an item
// @Description  The portal reports each asset and collection page a person opens, which is what Recent and the Unused counts read (#2504, #2509). An item the caller cannot open answers 404 and records nothing.
// @Tags         Library
// @Accept       json
// @Param        body  body  OpenRequest  true  "The item opened"
// @Success      204
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/recents [post]
func (a *API) recordOpen(w http.ResponseWriter, r *http.Request) {
	var body OpenRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&body); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, errBadBody)
		return
	}
	kind, ok := library.ParseKind(body.Kind)
	if !ok {
		httpjson.WriteError(w, http.StatusBadRequest, errBadKind)
		return
	}
	user, ok := a.opens(w, r, kind, body.ID)
	if !ok {
		return
	}
	if err := a.cfg.Library.RecordOpen(r.Context(), library.UserKey(user.UserID, user.Email), kind, body.ID); err != nil {
		fail(w, r, errWrite, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// star handles PUT /api/v1/portal/favorites/{kind}/{id}.
//
// @Summary      Star an item
// @Description  Adds an asset or collection the caller can open to their favorites (#2504). Starring grants nothing and is private to the caller; an item the caller cannot open answers 404.
// @Tags         Library
// @Param        kind  path  string  true  "asset or collection"
// @Param        id    path  string  true  "Item id"
// @Success      204
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/favorites/{kind}/{id} [put]
func (a *API) star(w http.ResponseWriter, r *http.Request) {
	kind, ok := pathKind(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	user, ok := a.opens(w, r, kind, id)
	if !ok {
		return
	}
	if err := a.cfg.Library.Star(r.Context(), library.UserKey(user.UserID, user.Email), kind, id); err != nil {
		fail(w, r, errWrite, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unstar handles DELETE /api/v1/portal/favorites/{kind}/{id}.
//
// @Summary      Remove a favorite
// @Description  Removes an item from the caller's favorites. Removing one that is not starred is not an error.
// @Tags         Library
// @Param        kind  path  string  true  "asset or collection"
// @Param        id    path  string  true  "Item id"
// @Success      204
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/favorites/{kind}/{id} [delete]
func (a *API) unstar(w http.ResponseWriter, r *http.Request) {
	kind, ok := pathKind(w, r)
	if !ok {
		return
	}
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	if err := a.cfg.Library.Unstar(r.Context(), library.UserKey(user.UserID, user.Email), kind, r.PathValue("id")); err != nil {
		fail(w, r, errWrite, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// opens checks the caller may open the live item, writing the refusal
// itself: 401 with no caller, 404 for an item that does not exist, is
// deleted, or the caller cannot open (one answer for all three).
func (a *API) opens(w http.ResponseWriter, r *http.Request, kind library.Kind, id string) (*access.User, bool) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, false
	}
	if id == "" || !a.canOpen(r.Context(), user, kind, id) {
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
		return nil, false
	}
	return user, true
}

func (a *API) canOpen(ctx context.Context, user *access.User, kind library.Kind, id string) bool {
	if kind == library.KindCollection {
		if a.cfg.Collections == nil {
			return false
		}
		c, err := a.cfg.Collections.Get(ctx, id)
		return err == nil && c != nil && c.DeletedAt == nil && a.access.CanViewCollection(ctx, c, user)
	}
	asset, err := a.cfg.Assets.Get(ctx, id)
	return err == nil && asset != nil && asset.DeletedAt == nil && a.access.CanViewAsset(ctx, id, asset, user)
}

func pathKind(w http.ResponseWriter, r *http.Request) (library.Kind, bool) {
	kind, ok := library.ParseKind(r.PathValue("kind"))
	if !ok {
		httpjson.WriteError(w, http.StatusBadRequest, errBadKind)
	}
	return kind, ok
}

// fail answers a store failure with msg, logging what went wrong.
func fail(w http.ResponseWriter, r *http.Request, msg string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	slog.WarnContext(r.Context(), "library route failed", "path", logsan.SanitizeForLog(r.URL.Path), "error", logsan.SanitizeForLog(err.Error()))
	httpjson.WriteError(w, http.StatusInternalServerError, msg)
}
