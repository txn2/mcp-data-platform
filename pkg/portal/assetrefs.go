package portal

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefapi"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Structured-log field names, named once because the same two appear on every
// warning this file emits.
const (
	logFieldAssetID = "asset_id"
	logFieldError   = "error"
)

// refRoutePattern is the reference-serving route. It carries the asset as well
// as the token so a token pasted onto another asset's path resolves to
// nothing, and so an operator reading an access log can see which asset a
// request came through.
const refRoutePattern = "GET " + assetrefs.PathPrefix + "{id}/{ref}"

// refRateLimit scales the public viewer's per-IP budget by the reference cap,
// so one page view still costs one unit in both limiters.
//
// Without the scaling the two limiters would count incompatible things. The
// viewer limiter is sized for share page loads at 60/min with a burst of 10;
// a single report that references the cap's worth of images issues that many
// requests in one render, and the eleventh would be shed. Multiplying the
// budget by the cap keeps the operator's one knob meaningful — it is still
// "page views per minute" — while letting a page load fetch everything it
// declared.
//
// A zero field is left zero so viewerlimit applies its own default, which it
// must do before sizing the global backstop.
func refRateLimit(cfg RateLimitConfig) RateLimitConfig {
	if cfg.RequestsPerMinute > 0 {
		cfg.RequestsPerMinute *= portaldomain.MaxAssetResourceRefs
	}
	if cfg.BurstSize > 0 {
		cfg.BurstSize *= portaldomain.MaxAssetResourceRefs
	}
	return cfg
}

// registerRefRoutes mounts the reference-serving route when this deployment
// has a managed-resource layer to serve from. A deployment without one leaves
// refMux nil and serves the prefix as an unknown path, rather than as a route
// that answers 503 to every reader.
func (h *Handler) registerRefRoutes() {
	server := assetrefs.New(assetrefs.Deps{
		Refs:      h.deps.ResourceRefs,
		Resources: h.deps.ResourceReader,
		Blobs:     h.deps.ResourceBlobs,
		Bucket:    h.deps.ResourceS3Bucket,
	})
	if !server.Ready() {
		return
	}
	h.refMux = http.NewServeMux()
	h.refMux.Handle(refRoutePattern, h.refLimiter.Middleware(server))
}

// registerRefAPI mounts the portal surface a person manages references through
// (#1475): the asset's own list, adding and removing one, and the resource's
// answer to what is holding it up. The seam lives in internal/portal/
// assetrefapi; this package supplies its dependencies and its authorization
// core, so the routes there and the ones that stayed here judge the same way.
func (h *Handler) registerRefAPI() {
	assetrefapi.Register(h.mux, assetrefapi.Config{
		Refs:      h.deps.ResourceRefs,
		Resources: h.deps.ResourceReader,
		Assets:    h.deps.AssetStore,
		Shares:    h.deps.ShareStore,
		// The asset's own bytes, read to find where its content still writes a
		// reference's URI. It is the portal's blob client rather than the
		// resource layer's: the content being scanned is the asset's.
		Blobs:  h.deps.S3Client,
		Access: h.access,
		Claims: h.resourceClaims,
	})
}

// copyRefs carries a copied asset's references onto the copy, keeping only the
// ones the person making the copy could have declared themselves.
//
// A copy is a new asset with a new owner, and a reference is a grant: carrying
// one across unexamined would hand the copier -- and everyone they later share
// the copy with -- a file they were never able to open, on the strength of
// having been shown a report that used it. Re-checking each reference against
// the copier's own read permission is the same check the original declaration
// passed, applied to the person now making one.
//
// A reference that does not survive is dropped rather than refused. The copied
// content still names the resource by its mcp:// URI, so the copy renders with
// that image missing and everything else intact, which is what a reference to
// a deleted resource already does. Refusing the copy outright would deny
// someone a report over a logo.
func (h *Handler) copyRefs(ctx context.Context, sourceID, copyID string, user *User) {
	if h.deps.ResourceRefs == nil || h.deps.ResourceReader == nil || user == nil {
		return
	}
	refs, err := h.deps.ResourceRefs.ListByAsset(ctx, sourceID)
	if err != nil {
		slog.Warn("asset copy: reading source resource references failed, copy carries none",
			logFieldAssetID, logsan.SanitizeForLog(sourceID),
			logFieldError, logsan.SanitizeForLog(err.Error()))
		return
	}
	if len(refs) == 0 {
		return
	}

	// One read for the whole set: a copy is interactive, and the reference cap
	// is high enough that a query per reference would be felt.
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ResourceID)
	}
	resources, err := h.deps.ResourceReader.GetByIDs(ctx, ids)
	if err != nil {
		slog.Warn("asset copy: reading referenced resources failed, copy carries none",
			logFieldAssetID, logsan.SanitizeForLog(sourceID),
			logFieldError, logsan.SanitizeForLog(err.Error()))
		return
	}

	carried := carryableRefs(refs, resources, h.resourceClaims(user), copyID, user.Email)
	if len(carried) == 0 {
		return
	}
	if err := h.deps.ResourceRefs.Replace(ctx, copyID, carried); err != nil {
		slog.Warn("asset copy: recording carried resource references failed",
			logFieldAssetID, logsan.SanitizeForLog(copyID),
			logFieldError, logsan.SanitizeForLog(err.Error()))
	}
}

// carryableRefs selects the references the copier can read for themselves and
// re-stamps each onto the copy.
//
// Every reference gets a fresh token, never the source asset's: the two assets
// are separate grants from here on, and revoking one must not depend on the
// other. A token that cannot be minted drops that reference, on the same terms
// as one the copier cannot read.
func carryableRefs(
	refs []portaldomain.AssetResourceRef,
	resources map[string]*resource.Resource,
	claims resource.Claims,
	copyID, declaredBy string,
) []portaldomain.AssetResourceRef {
	carried := make([]portaldomain.AssetResourceRef, 0, len(refs))
	for _, ref := range refs {
		res, ok := resources[ref.ResourceID]
		if !ok || res == nil || !resource.CanReadResource(claims, res) {
			continue
		}
		token, err := portaldomain.GenerateRefToken()
		if err != nil {
			continue
		}
		carried = append(carried, portaldomain.AssetResourceRef{
			AssetID:    copyID,
			ResourceID: ref.ResourceID,
			URI:        ref.URI,
			RefToken:   token,
			Position:   len(carried),
			DeclaredBy: declaredBy,
		})
	}
	return carried
}

// resourceClaims builds the managed-resource permission claims for a portal
// user, through the same resource.BuildClaims every other surface derives them
// with. The persona comes from the resolver the composition root wired, which
// is the same one the portal's own access gate judges by.
func (h *Handler) resourceClaims(user *User) resource.Claims {
	var persona string
	if h.deps.PersonaResolver != nil {
		if info := h.deps.PersonaResolver(user.Roles); info != nil {
			persona = info.Name
		}
	}
	return resource.BuildClaims(user.UserID, user.Email, persona, user.Roles, h.access.IsAdmin(user))
}

// serveRefs rewrites an asset's stored content for serving: every mcp:// URI
// the asset declared becomes the absolute, credential-free URL its reference
// is served under.
//
// The URL it writes is absolute against the deployment's public base URL, or
// against the origin of the request being answered when none is configured --
// the same origin the share page's own links resolve to, so a page and the
// pictures in it can never point at different hosts.
//
// It is applied on every viewing path and on none of the authoring ones. An
// agent reading content before it patches must see the URIs it wrote, because
// a rewritten URL read back and patched in would replace the reference with a
// platform-internal path and the asset would lose the reference entirely.
//
// A failure to read the references leaves the content served as stored. The
// alternative — refusing the read — would take a whole report off the screen
// over an image, which is the opposite of the rule references already follow
// for a resource that has been deleted.
func (h *Handler) serveRefs(r *http.Request, assetID, contentType string, data []byte) []byte {
	if h.deps.ResourceRefs == nil || assetID == "" {
		return data
	}
	refs, err := h.deps.ResourceRefs.ListByAsset(r.Context(), assetID)
	if err != nil {
		slog.Warn("asset resource references: list failed, serving content as stored",
			logFieldAssetID, logsan.SanitizeForLog(assetID),
			logFieldError, logsan.SanitizeForLog(err.Error()))
		return data
	}
	return assetrefs.Rewrite(data, contentType,
		assetrefs.BaseURL(r, h.deps.PublicBaseURL), assetID, refs)
}
