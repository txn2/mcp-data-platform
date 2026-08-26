// Package assetrefapi is the portal surface over the things an asset's content
// references (#1475): the managed resources it names, and the assets it names
// (#1488).
//
// The mechanism in internal/portal/assetrefs gives an agent a way to declare a
// reference at save time and gives every viewing surface a way to resolve it.
// Neither is something a person can see or act on: before this, an owner could
// not tell which files their report depended on, could not add one without
// asking an agent to re-save, could not remove one, and could not find out what
// a resource was holding up before deleting it.
//
// It serves both ends of the same edge. The asset end lists, adds and removes
// the references one asset declares; the target end answers "what is holding
// this up?" for the person about to edit or delete a file or an asset. They are
// one package because they are one fact read from two sides, and because the
// second question is the reason the first one matters: a reference carries the
// referencing asset's audience, so a target referenced by a publicly shared
// asset is readable by anyone holding that link.
//
// It owns no policy of its own. Whether the caller may see or change an asset
// goes through the portal's authorization core in internal/portal/access, and
// whether they may read a resource goes through resource.CanReadResource with
// claims the parent builds -- the same two checks the declaration path applies
// to an agent.
package assetrefapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Response literals and path keys. They are spelled here rather than imported
// from pkg/portal because that package registers this one and so cannot be
// imported back; the wording is what a client reads and must match.
const (
	errAuthRequired     = "authentication required"
	errAssetNotFound    = "asset not found"
	errAssetDeleted     = "asset has been deleted"
	errResourceNotFound = "resource not found"
	errAccessDenied     = "access denied"
	errNotEditable      = "you can only change the references on an asset you own or have edit access to"

	pathKeyID     = "id"
	pathKeyKind   = "kind"
	pathKeyTarget = "targetID"

	logKeyError   = "error"
	logKeyAssetID = "asset_id"
)

// Config carries what the reference routes need.
//
// Refs, Assets, Access and Claims are required; without any of them there is
// nothing to manage, and the routes stay unregistered rather than answering 503
// to every caller. The rest are optional and each costs exactly one thing when
// absent: no resource references on a deployment with no managed-resource layer
// (asset references still work), no public-share flag on the used-by list, and
// no occurrence report on the reference list.
type Config struct {
	// Refs is the reference store, the record of which asset names what.
	Refs assetrefs.Store
	// Resources reads the managed-resource layer. It is the declaration path's
	// own view of it, so the two cannot drift onto different notions of what a
	// referenceable file is. A nil reader leaves a resource reference
	// unresolvable and a resource target unaddable, on a deployment that has no
	// managed resources to name; asset references are unaffected.
	Resources assetrefs.Resources
	// Assets and Shares resolve the asset a reference hangs off -- and, for an
	// asset reference, the asset it points at -- along with how widely each is
	// shared. A nil Shares leaves every asset unflagged rather than unlisted:
	// not knowing whether an asset is public is a smaller failure than hiding
	// the fact that it references the target at all.
	Assets portaldomain.AssetStore
	Shares portaldomain.ShareStore
	// Blobs reads an asset's stored content, which is where a reference's URI
	// is actually written. It backs the occurrence report the removal warning
	// is built from; a nil reader leaves that report empty.
	Blobs assetrefs.BlobReader
	// Access is the portal's authorization core, built by the parent so this
	// surface and the routes that stayed there answer permission questions the
	// same way.
	Access *access.Checker
	// Claims builds a caller's managed-resource permission claims, through the
	// same resource.BuildClaims every other resource surface derives them
	// with. The parent supplies it because it owns the persona resolver.
	Claims func(*access.User) resource.Claims
}

// ready reports whether cfg names everything the routes need.
func (c Config) ready() bool {
	return c.Refs != nil && c.Assets != nil && c.Access != nil && c.Claims != nil
}

// handler serves the reference routes.
type handler struct {
	cfg    Config
	access *access.Checker
}

// Register mounts the reference routes on mux. A deployment with no reference
// layer registers none of them, so the paths 404 as unknown rather than as a
// feature that is present and always refuses.
//
// The two used-by routes are one handler over the two target kinds, so "what
// is holding this up?" cannot come to mean different things depending on what
// is being held.
func Register(mux *http.ServeMux, cfg Config) {
	if !cfg.ready() {
		return
	}
	h := &handler{cfg: cfg, access: cfg.Access}
	mux.HandleFunc("GET /api/v1/portal/assets/{id}/references", h.listRefs)
	mux.HandleFunc("POST /api/v1/portal/assets/{id}/references", h.addRef)
	mux.HandleFunc("DELETE /api/v1/portal/assets/{id}/references/{kind}/{targetID}", h.removeRef)
	mux.HandleFunc("GET /api/v1/portal/resources/{id}/used-by", h.assetsUsingResource)
	mux.HandleFunc("GET /api/v1/portal/assets/{id}/used-by", h.assetsUsingAsset)
}

// caller resolves the authenticated portal user, writing the 401 itself.
func caller(w http.ResponseWriter, r *http.Request) *access.User {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
	}
	return user
}
