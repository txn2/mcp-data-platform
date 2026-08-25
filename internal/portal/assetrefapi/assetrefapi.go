// Package assetrefapi is the portal surface over the managed resources an
// asset's content references (#1475).
//
// The mechanism in internal/portal/assetrefs gives an agent a way to declare a
// reference at save time and gives every viewing surface a way to resolve it.
// Neither is something a person can see or act on: before this, an owner could
// not tell which files their report depended on, could not add one without
// asking an agent to re-save, could not remove one, and could not find out what
// a resource was holding up before deleting it.
//
// It serves both ends of the same edge. The asset end lists, adds and removes
// the references one asset declares; the resource end answers "what is holding
// this file up?" for the person about to edit or delete it. They are one
// package because they are one fact read from two sides, and because the second
// question is the reason the first one matters: a reference carries the asset's
// audience, so a resource referenced by a publicly shared asset is readable by
// anyone holding that link.
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
	errNotEditable      = "you can only change the referenced files on an asset you own or have edit access to"

	pathKeyID       = "id"
	pathKeyResource = "resourceID"

	logKeyError   = "error"
	logKeyAssetID = "asset_id"
)

// Config carries what the reference routes need.
//
// Refs, Resources, Assets, Access and Claims are required; without any of them
// there is nothing to manage, and the routes stay unregistered rather than
// answering 503 to every caller. Shares and Blobs are optional and each costs
// exactly one thing when absent: no public-share flag on the used-by list, and
// no occurrence report on the reference list.
type Config struct {
	// Refs is the reference store, the record of which asset names which file.
	Refs portaldomain.AssetResourceRefStore
	// Resources reads the managed-resource layer. It is the declaration path's
	// own view of it, so the two cannot drift onto different notions of what a
	// referenceable file is.
	Resources assetrefs.Resources
	// Assets and Shares resolve the asset behind a reference and how widely it
	// is shared. A nil Shares leaves every asset unflagged rather than
	// unlisted: not knowing whether an asset is public is a smaller failure
	// than hiding the fact that it references the file at all.
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
	return c.Refs != nil && c.Resources != nil && c.Assets != nil &&
		c.Access != nil && c.Claims != nil
}

// handler serves the reference routes.
type handler struct {
	cfg    Config
	access *access.Checker
}

// Register mounts the reference routes on mux. A deployment with no
// managed-resource layer registers none of them, so the paths 404 as unknown
// rather than as a feature that is present and always refuses.
func Register(mux *http.ServeMux, cfg Config) {
	if !cfg.ready() {
		return
	}
	h := &handler{cfg: cfg, access: cfg.Access}
	mux.HandleFunc("GET /api/v1/portal/assets/{id}/resources", h.listRefs)
	mux.HandleFunc("POST /api/v1/portal/assets/{id}/resources", h.addRef)
	mux.HandleFunc("DELETE /api/v1/portal/assets/{id}/resources/{resourceID}", h.removeRef)
	mux.HandleFunc("GET /api/v1/portal/resources/{id}/assets", h.assetsUsingResource)
}

// caller resolves the authenticated portal user, writing the 401 itself.
func caller(w http.ResponseWriter, r *http.Request) *access.User {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
	}
	return user
}
