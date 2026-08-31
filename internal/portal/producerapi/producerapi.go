// Package producerapi is the portal surface over what produced a file (#1569):
// the scripts, sessions and people that created or modified a portal asset or a
// managed resource.
//
// The relation itself lives in internal/producedby, written by the asset and
// resource write funnels. Before this surface a person could see an asset's
// provenance -- which data calls its content was built from -- and nothing at
// all about who wrote it. The practical questions had no answer: what does this
// hourly script actually touch, who else writes to this file, if I retire this
// script what goes stale.
//
// It serves the target end of the relation: given a file, what has written it.
// The producer end -- given a script, what has it written -- is served beside
// the script's other routes, where the authority to see a script is already
// resolved.
//
// It owns no policy of its own. Whether the caller may see an asset goes
// through the portal's authorization core in internal/portal/access, and
// whether they may read a resource goes through resource.CanReadResource with
// claims the parent builds: the same two checks every other read of those
// files makes.
package producerapi

import (
	"context"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Response literals and path keys. They are spelled here rather than imported
// from pkg/portal because that package registers this one and so cannot be
// imported back.
const (
	errAuthRequired     = "authentication required"
	errAssetNotFound    = "asset not found"
	errAssetDeleted     = "asset has been deleted"
	errResourceNotFound = "resource not found"
	errAccessDenied     = "access denied"

	pathKeyID = "id"

	logKeyError = "error"
)

// ScriptNames resolves script ids to the names those scripts carry now.
//
// An id missing from the result is a script that no longer exists, which is the
// whole reason this exists: the relation deliberately keeps no foreign key to
// scripts, so a deleted script leaves its rows behind and a surface has to be
// able to say that the producer is gone rather than fail on it.
type ScriptNames interface {
	Names(ctx context.Context, ids []string) (map[string]string, error)
}

// Config carries what the producer routes need.
//
// Producers, Assets, Access and Claims are required; without them there is
// nothing to report and the routes stay unregistered rather than answering 503
// to every caller. Resources is optional and costs exactly the resource route
// when absent. ScriptNames is optional and costs only the liveness of a script
// producer: without it every producer is reported as still existing, which is
// what it was before this shipped.
type Config struct {
	// Producers is the record of what wrote what.
	Producers producedby.Store
	// Assets resolves the asset being asked about, and Shares how widely it is
	// shared, which is how a reader who is not the owner is admitted.
	Assets portaldomain.AssetStore
	// Resources reads the managed-resource layer. Nil on a deployment with no
	// managed resources, which leaves the resource route unregistered.
	Resources ResourceReader
	// Access is the portal's authorization core, built by the parent so this
	// surface and the routes that stayed there answer permission questions the
	// same way.
	Access *access.Checker
	// Claims builds a caller's managed-resource permission claims through the
	// same resource.BuildClaims every other resource surface derives them with.
	Claims func(*access.User) resource.Claims
	// Scripts resolves whether a script producer still exists, and what it is
	// called now. Nil reports every producer as existing.
	Scripts ScriptNames
}

// ResourceReader is the slice of the managed-resource store this surface needs:
// one record by id, to check the caller may read it before naming what wrote it.
type ResourceReader interface {
	Get(ctx context.Context, id string) (*resource.Resource, error)
}

// ready reports whether cfg names everything the routes need.
func (c Config) ready() bool {
	return c.Producers != nil && c.Assets != nil && c.Access != nil && c.Claims != nil
}

// handler serves the producer routes.
type handler struct {
	cfg    Config
	access *access.Checker
}

// Register mounts the producer routes on mux. A deployment that records no
// producers registers none of them, so the paths 404 as unknown rather than as
// a feature that is present and always answers empty.
func Register(mux *http.ServeMux, cfg Config) {
	if !cfg.ready() {
		return
	}
	h := &handler{cfg: cfg, access: cfg.Access}
	mux.HandleFunc("GET /api/v1/portal/assets/{id}/producers", h.assetProducers)
	if cfg.Resources != nil {
		mux.HandleFunc("GET /api/v1/portal/resources/{id}/producers", h.resourceProducers)
	}
}

// caller resolves the authenticated portal user, writing the 401 itself.
func caller(w http.ResponseWriter, r *http.Request) *access.User {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
	}
	return user
}

// viewableAsset loads the {id} asset for a caller who may open it -- the same
// gate the asset's own page passes, because what wrote a file is part of the
// file, not public knowledge about it.
func (h *handler) viewableAsset(w http.ResponseWriter, r *http.Request, user *access.User) (*portaldomain.Asset, bool) {
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
	if h.access.CanManageAsset(asset, user) {
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

// readableResource loads the {id} resource for a caller who may read it. A
// resource they cannot read is reported as absent rather than as forbidden,
// which is how every other resource read answers.
func (h *handler) readableResource(w http.ResponseWriter, r *http.Request, user *access.User) (*resource.Resource, bool) {
	res, err := h.cfg.Resources.Get(r.Context(), r.PathValue(pathKeyID))
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
