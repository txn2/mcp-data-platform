package portal

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/contentviewer"
	"github.com/txn2/mcp-data-platform/internal/portal/publicviewer"
	"github.com/txn2/mcp-data-platform/internal/portal/sharecache"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// resolvePublicBaseURL returns the absolute URL prefix the public viewer
// should use for canonical (og:url) and asset (og:image) links. When the
// operator has set portal.public_base_url that wins; otherwise we derive
// scheme+host from the inbound request — social-media crawlers always
// follow the share URL, so the request's Host header reflects how end
// users will reach the page. Returns empty when neither is available
// (e.g. unit-test requests with no Host); callers should treat empty as
// "skip absolute-URL OG tags".
//
// X-Forwarded-Proto is honored only when r.TLS is nil (a reverse proxy is
// plausibly in front). When the server is the TLS terminator itself, an
// attacker-supplied X-Forwarded-Proto must not be allowed to override the
// real scheme. Multi-proxy chains may produce comma-separated values
// (e.g. "https, http"); we take the first token, which is the originating
// client's scheme. Only http/https are accepted — any other value falls
// back to the default to keep og:url URLs well-formed and prevent a
// misbehaving proxy (or a request without a trusted-proxy boundary) from
// emitting a non-HTTP scheme.
func resolvePublicBaseURL(r *http.Request, configBaseURL string) string {
	if s := strings.TrimRight(configBaseURL, "/"); s != "" {
		return s
	}
	if r == nil || r.Host == "" {
		return ""
	}
	if r.TLS != nil {
		return schemeHTTPS + "://" + r.Host
	}
	return forwardedScheme(r) + "://" + r.Host
}

// schemeHTTP and schemeHTTPS are the only two values that may appear in
// the resolved scheme — used both as the default and as the validation
// allow-list for X-Forwarded-Proto.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// forwardedScheme returns "http" by default, upgrading to "https" only when
// X-Forwarded-Proto explicitly says so. The header may carry a comma-
// separated chain through multiple proxies (e.g. "https, http"); we use
// the leftmost token, which is the originating client's scheme. Anything
// other than "http"/"https" falls back to the default to keep og:url
// well-formed even if a misbehaving proxy injects an arbitrary value.
func forwardedScheme(r *http.Request) string {
	forwarded := r.Header.Get("X-Forwarded-Proto")
	if forwarded == "" {
		return schemeHTTP
	}
	if i := strings.IndexByte(forwarded, ','); i >= 0 {
		forwarded = forwarded[:i]
	}
	forwarded = strings.TrimSpace(forwarded)
	if forwarded == schemeHTTP || forwarded == schemeHTTPS {
		return forwarded
	}
	return schemeHTTP
}

// publicAssetOGImage returns the absolute URL of the OG card image for a
// single-asset share, or empty if no suitable image exists. Preference
// order: image-typed asset content → asset thumbnail → empty (template
// then falls back to the brand logo). Empty baseURL disables absolute
// URL emission entirely.
func publicAssetOGImage(asset *Asset, token, baseURL string) string {
	if baseURL == "" || asset == nil {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(asset.ContentType), "image/") {
		return baseURL + publicViewPathPrefix + token + "/content"
	}
	if asset.ThumbnailS3Key != "" {
		return baseURL + publicViewPathPrefix + token + "/thumbnail"
	}
	return ""
}

// publicCollectionOGImage returns the absolute URL of the OG card image
// for a collection share. Preference: collection's own thumbnail → first
// item with a thumbnail → empty. Empty baseURL disables absolute URL
// emission entirely.
func publicCollectionOGImage(coll *Collection, assets map[string]*Asset, token, baseURL string) string {
	if baseURL == "" || coll == nil {
		return ""
	}
	if coll.ThumbnailS3Key != "" {
		return baseURL + publicViewPathPrefix + token + "/collection-thumbnail"
	}
	for _, sec := range coll.Sections {
		for _, item := range sec.Items {
			if a, ok := assets[item.AssetID]; ok && a != nil && a.ThumbnailS3Key != "" {
				return baseURL + publicViewPathPrefix + token + "/items/" + a.ID + "/thumbnail"
			}
		}
	}
	return ""
}

// incrementAccessTimeout bounds the background goroutine that increments
// share access counters after the HTTP response has been sent.
const incrementAccessTimeout = 5 * time.Second

// pathKeyAssetID is the path parameter name for asset IDs in collection share URLs.
const pathKeyAssetID = "assetId"

// pathKeyToken is the path parameter name for share tokens in public viewer URLs.
const pathKeyToken = "token"

// publicViewPathPrefix is the URL prefix for public share endpoints.
const publicViewPathPrefix = "/portal/view/"

// portalAppPath is the authenticated portal SPA root, where a signed-in viewer
// can open the asset and leave feedback.
const portalAppPath = "/portal/"

func (h *Handler) publicView(w http.ResponseWriter, r *http.Request) {
	share := shareFromRequest(r)

	// Branch: collection share vs asset share.
	if share.CollectionID != "" {
		h.publicCollectionView(w, r, share)
		return
	}

	pad, err := h.fetchPublicAsset(r, share.AssetID)
	if err != nil {
		writePublicError(w, err)
		return
	}

	// Increment access count asynchronously. Use a detached context because
	// the request context is canceled after the handler returns.
	go func() { // #nosec G118 -- intentionally detached: request ctx is canceled after handler returns
		ctx, cancel := context.WithTimeout(context.Background(), incrementAccessTimeout)
		defer cancel()
		if incErr := h.deps.ShareStore.IncrementAccess(ctx, share.ID); incErr != nil {
			slog.Warn("public view: failed to increment access", "error", incErr, "share_id", share.ID) // #nosec G706 -- structured log, not user-facing
		}
	}()

	// A signed-in viewer arriving through a public link gets a derived viewer
	// share so the asset shows up in their portal and they can leave feedback.
	h.maybeAutoPromoteViewer(r, promoteTarget{targetTypeAsset, share.AssetID, pad.Asset.OwnerID, share.CreatedBy})

	h.renderAssetViewer(w, r, pad, share)
}

// renderAssetViewer renders the public_viewer.html template for an asset.
// Used by both single-asset public shares and collection item views.
// When the request includes ?embedded=1, chrome (header, notice, info modal)
// is suppressed so the viewer can be loaded in an iframe without double headers.
// publicAssetData holds asset content and metadata for the public viewer.
type publicAssetData struct {
	Asset    *Asset
	Content  []byte
	TooLarge bool
	// ServeFromURL marks a binary asset the page must load from the raw
	// content endpoint rather than from embedded bytes.
	ServeFromURL bool
}

func (h *Handler) renderAssetViewer(w http.ResponseWriter, r *http.Request, pad publicAssetData, share *Share) { //nolint:revive // clear param naming
	asset := pad.Asset

	// Build download URL for the public viewer.
	// Single-asset shares: /portal/view/{token}/content
	// Collection items: /portal/view/{token}/items/{assetId}/content
	downloadURL := fmt.Sprintf("/portal/view/%s/content", share.Token)

	contentData := map[string]any{
		"contentType":  asset.ContentType,
		"content":      string(pad.Content),
		colName:        asset.Name,
		colDescription: asset.Description,
		colTags:        asset.Tags,
		"sizeBytes":    asset.SizeBytes,
		"tooLarge":     pad.TooLarge,
		"downloadURL":  downloadURL,
		// contentURL is the same endpoint as downloadURL, named for the role it
		// plays for binary families: the <img>/<audio>/<video>/<iframe> source
		// the viewer renders from instead of embedded bytes. It supports byte
		// ranges, so media seek works without fetching the whole object.
		"contentURL":   downloadURL,
		"serveFromURL": pad.ServeFromURL,
		"createdAt":    asset.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":    asset.UpdatedAt.UTC().Format(time.RFC3339),
	}
	contentJSON, _ := json.Marshal(contentData) // #nosec G104 -- simple map marshaling cannot fail

	csp := publicviewer.AssetCSP()
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set(headerContentType, "text/html; charset=utf-8")

	brandName := h.deps.BrandName
	if brandName == "" {
		brandName = "MCP Data Platform"
	}
	brandLogo := h.deps.BrandLogoSVG
	if brandLogo == "" {
		brandLogo = publicviewer.DefaultLogoSVG
	}

	var expiresAtISO string
	if share.ExpiresAt != nil {
		expiresAtISO = share.ExpiresAt.UTC().Format(time.RFC3339)
	}

	// OG/Twitter metadata. Empty baseURL → ShareURL/OGImageURL stay empty,
	// and the template gates each meta tag on its corresponding field, so
	// requests without a resolvable base URL still render valid HTML — they
	// just don't emit OG tags (which require absolute URLs anyway).
	baseURL := resolvePublicBaseURL(r, h.deps.PublicBaseURL)
	var shareURL string
	if baseURL != "" {
		shareURL = baseURL + publicViewPathPrefix + share.Token
	}

	_ = publicviewer.AssetTemplate.Execute(w, map[string]any{
		"Name":               asset.Name,
		"ContentType":        asset.ContentType,
		"Description":        asset.Description,
		"Tags":               asset.Tags,
		"CreatedAtISO":       asset.CreatedAt.UTC().Format(time.RFC3339),
		"UpdatedAtISO":       asset.UpdatedAt.UTC().Format(time.RFC3339),
		"ContentJSON":        template.JS(contentJSON), // #nosec G203 -- json.Marshal escapes <, >, & as \uXXXX; safe inside <script type="application/json">
		"ContentViewerURL":   contentviewer.EntryURL(),
		"ContentViewerCSS":   template.CSS(contentviewer.CSS), // #nosec G203 -- build artifact embedded at compile time, not user input
		"BrandName":          brandName,
		"BrandLogoSVG":       template.HTML(brandLogo), // #nosec G203 -- operator-provided SVG from config, not user input
		"BrandURL":           h.deps.BrandURL,
		"ImplementorName":    h.deps.ImplementorName,
		"ImplementorLogoSVG": template.HTML(h.deps.ImplementorLogoSVG), // #nosec G203 -- operator-provided SVG from config
		"ImplementorURL":     h.deps.ImplementorURL,
		"Version":            asset.CurrentVersion,
		"ExpiresAtISO":       expiresAtISO,
		"HideExpiration":     share.HideExpiration,
		"NoticeText":         share.NoticeText,
		"Embedded":           r.URL.Query().Get("embedded") == "1",
		"ShareURL":           shareURL,
		"OGImageURL":         publicAssetOGImage(asset, share.Token, baseURL),
		"SignedIn":           h.resolvePublicViewer(r) != nil,
		"SignInURL":          signInToLeaveFeedbackURL(r),
		"PortalURL":          portalAppPath,
		"IsGuest":            isGuestRequest(r),
	})
}

// resolvePublicViewer returns the authenticated user behind a public request,
// or nil if the request carries no valid session (anonymous viewer) or no
// authenticator is configured. It never writes an HTTP error — a public share
// stays viewable while signed out.
//
// Behind publicShareGate the answer is already in the request context; the
// authenticator is only consulted for requests that did not pass the gate.
func (h *Handler) resolvePublicViewer(r *http.Request) *User {
	if g, ok := r.Context().Value(gateCtxKey{}).(gateResult); ok {
		return g.Viewer
	}
	if h.deps.Authenticator == nil {
		return nil
	}
	user, err := h.deps.Authenticator.Authenticate(r)
	if err != nil || user == nil {
		return nil
	}
	return user
}

// signInToLeaveFeedbackURL builds the login URL that returns the viewer to the
// current public page after authenticating, so the auto-promote runs on return.
func signInToLeaveFeedbackURL(r *http.Request) string {
	return "/portal/auth/login?return_to=" + url.QueryEscape(r.URL.Path)
}

// promoteTarget identifies the object a public-link viewer is being promoted
// onto, plus the owner (who needs no share) and the original public-link
// creator (recorded as the derived share's created_by).
type promoteTarget struct {
	targetType string
	targetID   string
	ownerID    string
	createdBy  string
}

// maybeAutoPromoteViewer auto-promotes the request's signed-in viewer (if any)
// to a derived viewer share for the target. No-op for anonymous viewers, so the
// public viewer stays anonymous-viewable.
func (h *Handler) maybeAutoPromoteViewer(r *http.Request, t promoteTarget) {
	viewer := h.resolvePublicViewer(r)
	if viewer == nil {
		return
	}
	h.autoPromoteViewer(r.Context(), t, viewer)
}

// autoPromoteViewer grants a signed-in public-link viewer a derived viewer
// share for the target so it appears in their portal and they can leave
// feedback. It is idempotent (skips when any active share already exists) and
// never downgrades — an existing editor is left untouched. The object owner
// needs no share. Best-effort: failures are logged, never surfaced.
func (h *Handler) autoPromoteViewer(ctx context.Context, t promoteTarget, user *User) {
	if user == nil || h.deps.ShareStore == nil || t.targetID == "" {
		return
	}
	if t.ownerID == user.UserID {
		return // owner already has full access
	}

	existing, err := h.deps.ShareStore.GetActiveShareForTarget(ctx, t.targetType, t.targetID, user.UserID, user.Email)
	if err != nil {
		slog.Warn("auto-promote: lookup failed", logKeyError, err, "target", t.targetID) // #nosec G706 -- structured log
		return
	}
	if existing != nil {
		return // never downgrade an existing share
	}

	share, ok := derivedViewerShare(t, user)
	if !ok {
		return
	}
	if err := h.deps.ShareStore.Insert(ctx, share); err != nil {
		slog.Warn("auto-promote: insert failed", logKeyError, err, "target", t.targetID) // #nosec G706 -- structured log
	}
}

// derivedViewerShare builds a viewer share (origin=public_link_login) for the
// given asset or collection target, or returns ok=false if a token can't be
// generated or the target type is unsupported.
func derivedViewerShare(t promoteTarget, user *User) (Share, bool) {
	token, err := GenerateShareToken()
	if err != nil {
		slog.Warn("auto-promote: token generation failed", logKeyError, err) // #nosec G706 -- structured log
		return Share{}, false
	}
	share := Share{
		ID:               uuid.New().String(),
		Token:            token,
		CreatedBy:        t.createdBy,
		SharedWithUserID: user.UserID,
		SharedWithEmail:  user.Email,
		Permission:       PermissionViewer,
		AccessMode:       AccessModeRestricted,
		Origin:           OriginPublicLinkLogin,
	}
	switch t.targetType {
	case targetTypeAsset:
		share.AssetID = t.targetID
	case targetTypeCollection:
		share.CollectionID = t.targetID
	default:
		return Share{}, false
	}
	return share, true
}

// publicAssetContent serves the raw content for a single-asset public share.
// Always fetches from S3 regardless of size — this is a download endpoint.
func (h *Handler) publicAssetContent(w http.ResponseWriter, r *http.Request) {
	share := shareFromRequest(r)
	if share.AssetID == "" {
		http.Error(w, "Not an asset share.", http.StatusBadRequest)
		return
	}

	asset, data, fetchErr := h.fetchAssetContent(r, share.AssetID)
	if fetchErr != nil {
		writePublicError(w, fetchErr)
		return
	}

	blobserve.Serve(w, r, blobserve.Options{
		Name:        asset.Name,
		ContentType: asset.ContentType,
		ModTime:     asset.UpdatedAt,
		Data:        data,
	})
}

// publicAssetError categorizes errors from fetchPublicAsset.
type publicAssetError struct {
	Message string
	Status  int
}

func (e *publicAssetError) Error() string { return e.Message }

// largeAssetPreviewThreshold is the maximum asset size that the public viewer
// will load inline. Assets larger than this show a download prompt instead.
const largeAssetPreviewThreshold int64 = 2 * 1024 * 1024 // 2 MB

// fetchAssetContent retrieves an asset and always fetches its S3 content.
// Used by download/raw-content endpoints that must serve full content regardless of size.
func (h *Handler) fetchAssetContent(r *http.Request, assetID string) (*Asset, []byte, error) {
	asset, err := h.deps.AssetStore.Get(r.Context(), assetID)
	if err != nil {
		return nil, nil, &publicAssetError{Message: "Asset not found.", Status: http.StatusNotFound}
	}
	if asset.DeletedAt != nil {
		return nil, nil, &publicAssetError{Message: "This asset has been deleted.", Status: http.StatusGone}
	}
	if h.deps.S3Client == nil {
		return nil, nil, &publicAssetError{Message: "Content storage not configured.", Status: http.StatusServiceUnavailable}
	}
	data, _, err := h.deps.S3Client.GetObject(r.Context(), asset.S3Bucket, asset.S3Key)
	if err != nil {
		slog.Error("public view: failed to fetch content", "error", err, "asset_id", asset.ID) // #nosec G706 -- structured log
		return nil, nil, &publicAssetError{Message: "Failed to retrieve content.", Status: http.StatusInternalServerError}
	}
	return asset, data, nil
}

// fetchPublicAsset retrieves an asset and, when the viewer can use it, its S3
// content. Content is not fetched — and TooLarge is set — for assets over
// largeAssetPreviewThreshold. Content is also not fetched for binary families:
// the page embeds text content as a JSON string, which cannot carry arbitrary
// bytes, so images, audio, video and PDFs render from the raw content endpoint
// instead and there is nothing for the page to hold.
func (h *Handler) fetchPublicAsset(r *http.Request, assetID string) (publicAssetData, error) {
	asset, err := h.deps.AssetStore.Get(r.Context(), assetID)
	if err != nil {
		return publicAssetData{}, &publicAssetError{Message: "Asset not found.", Status: http.StatusNotFound}
	}
	if asset.DeletedAt != nil {
		return publicAssetData{}, &publicAssetError{Message: "This asset has been deleted.", Status: http.StatusGone}
	}

	if h.deps.S3Client == nil {
		return publicAssetData{}, &publicAssetError{Message: "Content storage not configured.", Status: http.StatusServiceUnavailable}
	}

	// Skip content fetch for large assets — they'll show a download prompt instead.
	if asset.SizeBytes > largeAssetPreviewThreshold {
		return publicAssetData{Asset: asset, TooLarge: true}, nil
	}

	if !contenttype.IsTextual(asset.ContentType) {
		return publicAssetData{Asset: asset, ServeFromURL: true}, nil
	}

	data, _, err := h.deps.S3Client.GetObject(r.Context(), asset.S3Bucket, asset.S3Key)
	if err != nil {
		slog.Error("public view: failed to fetch content", "error", err, "asset_id", asset.ID) // #nosec G706 -- structured log, not user-facing
		return publicAssetData{}, &publicAssetError{Message: "Failed to retrieve content.", Status: http.StatusInternalServerError}
	}

	return publicAssetData{Asset: asset, Content: data}, nil
}

// writePublicError writes the appropriate HTTP error for a publicAssetError.
func writePublicError(w http.ResponseWriter, err error) {
	var pae *publicAssetError
	if errors.As(err, &pae) {
		http.Error(w, pae.Message, pae.Status)
		return
	}
	http.Error(w, "Internal server error.", http.StatusInternalServerError)
}

// publicCollectionView renders a public collection page.
// The collection template is a placeholder until Phase 7 builds the real template.
func (h *Handler) publicCollectionView(w http.ResponseWriter, r *http.Request, share *Share) {
	if h.deps.CollectionStore == nil {
		http.Error(w, "Collections not configured.", http.StatusServiceUnavailable)
		return
	}

	coll, err := h.deps.CollectionStore.Get(r.Context(), share.CollectionID)
	if err != nil {
		http.Error(w, "Collection not found.", http.StatusNotFound)
		return
	}
	if coll.DeletedAt != nil {
		http.Error(w, "This collection has been deleted.", http.StatusGone)
		return
	}

	// Increment access count asynchronously.
	go func() { // #nosec G118 -- intentionally detached
		ctx, cancel := context.WithTimeout(context.Background(), incrementAccessTimeout)
		defer cancel()
		if incErr := h.deps.ShareStore.IncrementAccess(ctx, share.ID); incErr != nil {
			slog.Warn("public collection view: failed to increment access", "error", incErr, "share_id", share.ID) // #nosec G706 -- structured log, not user-facing
		}
	}()

	h.maybeAutoPromoteViewer(r, promoteTarget{targetTypeCollection, share.CollectionID, coll.OwnerID, share.CreatedBy})

	// Build asset lookup for items referenced in the collection.
	assetIDs := collectAssetIDs(coll)
	assets := h.fetchAssetMap(r.Context(), assetIDs)

	thumbSize := coll.Config.ThumbnailSize
	if thumbSize == "" {
		thumbSize = thumbSizeLarge
	}

	collJSON, _ := json.Marshal(map[string]any{ //nolint:errcheck // string map marshaling cannot fail
		"id":            coll.ID,
		colName:         coll.Name,
		colDescription:  coll.Description,
		"thumbnailSize": thumbSize,
		"sections":      buildPublicSections(coll, assets),
		"createdAt":     coll.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     coll.UpdatedAt.UTC().Format(time.RFC3339),
	})

	csp := publicviewer.CollectionCSP()
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set(headerContentType, "text/html; charset=utf-8")

	brandName := h.deps.BrandName
	if brandName == "" {
		brandName = "MCP Data Platform"
	}
	brandLogo := h.deps.BrandLogoSVG
	if brandLogo == "" {
		brandLogo = publicviewer.DefaultLogoSVG
	}

	var expiresAtISO string
	if share.ExpiresAt != nil {
		expiresAtISO = share.ExpiresAt.UTC().Format(time.RFC3339)
	}

	// OG/Twitter metadata. See renderAssetViewer for empty-baseURL handling.
	baseURL := resolvePublicBaseURL(r, h.deps.PublicBaseURL)
	var shareURL string
	if baseURL != "" {
		shareURL = baseURL + publicViewPathPrefix + share.Token
	}

	_ = publicviewer.CollectionTemplate.Execute(w, map[string]any{
		"Name":               coll.Name,
		"Description":        coll.Description,
		"CollectionJSON":     template.JS(collJSON), // #nosec G203 -- json.Marshal output
		"ContentViewerURL":   contentviewer.EntryURL(),
		"ContentViewerCSS":   template.CSS(contentviewer.CSS), // #nosec G203 -- build artifact
		"BrandName":          brandName,
		"BrandLogoSVG":       template.HTML(brandLogo), // #nosec G203 -- operator config
		"BrandURL":           h.deps.BrandURL,
		"ImplementorName":    h.deps.ImplementorName,
		"ImplementorLogoSVG": template.HTML(h.deps.ImplementorLogoSVG), // #nosec G203 -- operator config
		"ImplementorURL":     h.deps.ImplementorURL,
		"Token":              share.Token,
		"ExpiresAtISO":       expiresAtISO,
		"HideExpiration":     share.HideExpiration,
		"NoticeText":         share.NoticeText,
		"ShareURL":           shareURL,
		"OGImageURL":         publicCollectionOGImage(coll, assets, share.Token, baseURL),
		"IsGuest":            isGuestRequest(r),
	})
}

// validateCollectionItemAccess checks that the requested assetId belongs to
// the gated share's collection. Returns the share on success, or writes an
// HTTP error and returns nil.
func (h *Handler) validateCollectionItemAccess(w http.ResponseWriter, r *http.Request) *Share {
	assetID := r.PathValue(pathKeyAssetID)
	share := shareFromRequest(r)
	if assetID == "" || share.CollectionID == "" {
		http.NotFound(w, r)
		return nil
	}

	if h.deps.CollectionStore == nil {
		http.Error(w, "Collections not configured.", http.StatusServiceUnavailable)
		return nil
	}

	coll, getErr := h.deps.CollectionStore.Get(r.Context(), share.CollectionID)
	if getErr != nil || coll.DeletedAt != nil {
		http.NotFound(w, r)
		return nil
	}

	if !collectionContainsAsset(coll, assetID) {
		http.Error(w, "Asset not in this collection.", http.StatusForbidden)
		return nil
	}

	return share
}

// publicCollectionItemContent serves individual asset content within a public collection share.
// This is a download/raw-content endpoint, so it always fetches from S3 regardless of size.
func (h *Handler) publicCollectionItemContent(w http.ResponseWriter, r *http.Request) {
	if h.validateCollectionItemAccess(w, r) == nil {
		return
	}

	asset, data, err := h.fetchAssetContent(r, r.PathValue(pathKeyAssetID))
	if err != nil {
		writePublicError(w, err)
		return
	}

	blobserve.Serve(w, r, blobserve.Options{
		Name:        asset.Name,
		ContentType: asset.ContentType,
		ModTime:     asset.UpdatedAt,
		Data:        data,
	})
}

// publicCollectionItemView renders the full public asset viewer for an item in a collection.
// This is the same template used for single-asset public shares, loaded in an iframe by the collection viewer.
func (h *Handler) publicCollectionItemView(w http.ResponseWriter, r *http.Request) {
	share := h.validateCollectionItemAccess(w, r)
	if share == nil {
		return
	}

	pad, fetchErr := h.fetchPublicAsset(r, r.PathValue(pathKeyAssetID))
	if fetchErr != nil {
		writePublicError(w, fetchErr)
		return
	}

	// Render with the exact same template and data as a single-asset public view.
	h.renderAssetViewer(w, r, pad, share)
}

// servePublicThumbnail writes a thumbnail object as an image response under the
// caching scope the share's access mode allows (sharecache.Thumbnail): shared
// caches may hold a fully public share's thumbnail, the one object on this
// surface with no audience to protect and the one a link preview refetches on
// every paste of the URL. Any other mode gets the authorized browser and
// nothing else, because there the gate's verdict is the caller's and not the
// URL's (#1070).
//
// Any fetch failure is a 404: a public viewer cannot act on the difference
// between "no thumbnail" and "storage is down", and the distinction would leak
// whether the key exists.
func (h *Handler) servePublicThumbnail(w http.ResponseWriter, r *http.Request, bucket, key string) {
	data, contentType, err := h.deps.S3Client.GetObject(r.Context(), bucket, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	share := shareFromRequest(r)
	sharecache.Thumbnail(w, share.AccessMode, share.ExpiresAt, time.Now())
	blobserve.Serve(w, r, blobserve.Options{
		Name:        "thumbnail.png",
		ContentType: cmp.Or(contentType, mimeTypePNG),
		Data:        data,
	})
}

// publicCollectionItemThumbnail serves an asset's thumbnail within a public collection share.
func (h *Handler) publicCollectionItemThumbnail(w http.ResponseWriter, r *http.Request) {
	if h.validateCollectionItemAccess(w, r) == nil {
		return
	}

	if h.deps.S3Client == nil {
		http.NotFound(w, r)
		return
	}

	asset, getErr := h.deps.AssetStore.Get(r.Context(), r.PathValue(pathKeyAssetID))
	if getErr != nil || asset.DeletedAt != nil || asset.ThumbnailS3Key == "" {
		http.NotFound(w, r)
		return
	}

	h.servePublicThumbnail(w, r, asset.S3Bucket, asset.ThumbnailS3Key)
}

// publicAssetThumbnail serves the thumbnail for the asset behind a
// single-asset share. Used as the og:image source when the asset has a
// thumbnail but is not itself an image content type. Mirrors
// publicCollectionItemThumbnail, except the asset comes from the share rather
// than from a path-bound assetId: a single-asset share exposes only one.
func (h *Handler) publicAssetThumbnail(w http.ResponseWriter, r *http.Request) {
	share := shareFromRequest(r)
	if share.AssetID == "" {
		http.NotFound(w, r)
		return
	}
	if h.deps.S3Client == nil {
		http.NotFound(w, r)
		return
	}

	asset, getErr := h.deps.AssetStore.Get(r.Context(), share.AssetID)
	if getErr != nil || asset.DeletedAt != nil || asset.ThumbnailS3Key == "" {
		http.NotFound(w, r)
		return
	}

	h.servePublicThumbnail(w, r, asset.S3Bucket, asset.ThumbnailS3Key)
}

// resolveCollectionForThumbnail loads the non-deleted, thumbnailed collection
// behind the gated share. Writes 404 directly on failure and returns nil so
// callers can early-return without their own guard ladder.
func (h *Handler) resolveCollectionForThumbnail(w http.ResponseWriter, r *http.Request) *Collection {
	share := shareFromRequest(r)
	if share.CollectionID == "" {
		http.NotFound(w, r)
		return nil
	}
	if h.deps.CollectionStore == nil || h.deps.S3Client == nil {
		http.NotFound(w, r)
		return nil
	}
	coll, collErr := h.deps.CollectionStore.Get(r.Context(), share.CollectionID)
	if collErr != nil || coll.DeletedAt != nil || coll.ThumbnailS3Key == "" {
		http.NotFound(w, r)
		return nil
	}
	return coll
}

// publicCollectionThumbnail serves the collection's own thumbnail behind a
// collection public share. Used as the og:image source when the collection
// has a thumbnail uploaded. Falls through to 404 if the collection has no
// thumbnail; the og:image gating in publicCollectionOGImage prevents the
// template from emitting a URL pointing at this endpoint in that case.
func (h *Handler) publicCollectionThumbnail(w http.ResponseWriter, r *http.Request) {
	coll := h.resolveCollectionForThumbnail(w, r)
	if coll == nil {
		return
	}

	h.servePublicThumbnail(w, r, h.deps.S3Bucket, coll.ThumbnailS3Key)
}

// collectAssetIDs extracts unique asset IDs from all collection items.
func collectAssetIDs(coll *Collection) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, sec := range coll.Sections {
		for _, item := range sec.Items {
			if !seen[item.AssetID] {
				seen[item.AssetID] = true
				ids = append(ids, item.AssetID)
			}
		}
	}
	return ids
}

// fetchAssetMap retrieves basic metadata for a set of asset IDs in a single batch query.
func (h *Handler) fetchAssetMap(ctx context.Context, assetIDs []string) map[string]*Asset {
	result, err := h.deps.AssetStore.GetByIDs(ctx, assetIDs)
	if err != nil {
		slog.Warn("fetchAssetMap: batch query failed", "error", err)
		return map[string]*Asset{}
	}
	return result
}

// buildPublicSections constructs the JSON-safe section list for the public collection viewer.
func buildPublicSections(coll *Collection, assets map[string]*Asset) []map[string]any {
	sections := make([]map[string]any, 0, len(coll.Sections))
	for _, sec := range coll.Sections {
		items := make([]map[string]any, 0, len(sec.Items))
		for _, item := range sec.Items {
			a, ok := assets[item.AssetID]
			if !ok {
				continue
			}
			items = append(items, map[string]any{
				"assetId":        a.ID,
				colName:          a.Name,
				colDescription:   a.Description,
				"contentType":    a.ContentType,
				"hasThumbnail":   a.ThumbnailS3Key != "",
				"thumbnailS3Key": a.ThumbnailS3Key,
			})
		}
		sections = append(sections, map[string]any{
			"title":        sec.Title,
			colDescription: sec.Description,
			"items":        items,
		})
	}
	return sections
}

// collectionContainsAsset checks if any section item references the given asset ID.
func collectionContainsAsset(coll *Collection, assetID string) bool {
	for _, sec := range coll.Sections {
		for _, item := range sec.Items {
			if item.AssetID == assetID {
				return true
			}
		}
	}
	return false
}
