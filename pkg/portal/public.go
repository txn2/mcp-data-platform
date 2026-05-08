package portal

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/contentviewer"
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

// defaultLogoSVG is the MCP Data Platform logo used in the public viewer header
// when no brand logo is configured. Matches the platform-info app's default icon.
//
//nolint:lll // SVG markup
const defaultLogoSVG = `<svg viewBox="0 0 40 40" fill="none" xmlns="http://www.w3.org/2000/svg">` +
	`<circle cx="20" cy="20" r="4.5" fill="currentColor" opacity=".95"/>` +
	`<circle cx="6"  cy="11" r="3"   fill="currentColor" opacity=".65"/>` +
	`<circle cx="34" cy="11" r="3"   fill="currentColor" opacity=".65"/>` +
	`<circle cx="6"  cy="29" r="3"   fill="currentColor" opacity=".45"/>` +
	`<circle cx="34" cy="29" r="3"   fill="currentColor" opacity=".45"/>` +
	`<circle cx="20" cy="4"  r="2.2" fill="currentColor" opacity=".55"/>` +
	`<circle cx="20" cy="36" r="2.2" fill="currentColor" opacity=".35"/>` +
	`<line x1="20" y1="20" x2="6"  y2="11" stroke="currentColor" stroke-width="1.4" opacity=".3"/>` +
	`<line x1="20" y1="20" x2="34" y2="11" stroke="currentColor" stroke-width="1.4" opacity=".3"/>` +
	`<line x1="20" y1="20" x2="6"  y2="29" stroke="currentColor" stroke-width="1.4" opacity=".22"/>` +
	`<line x1="20" y1="20" x2="34" y2="29" stroke="currentColor" stroke-width="1.4" opacity=".22"/>` +
	`<line x1="20" y1="20" x2="20" y2="4"  stroke="currentColor" stroke-width="1.4" opacity=".28"/>` +
	`<line x1="20" y1="20" x2="20" y2="36" stroke="currentColor" stroke-width="1.4" opacity=".18"/>` +
	`</svg>`

//go:embed templates/public_viewer.html templates/public_collection_viewer.html
var templateFS embed.FS

var viewerTemplate = template.Must(template.ParseFS(templateFS, "templates/public_viewer.html"))

var collectionViewerTemplate = template.Must(template.ParseFS(templateFS, "templates/public_collection_viewer.html"))

func (h *Handler) publicView(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue(pathKeyToken)
	if token == "" {
		http.NotFound(w, r)
		return
	}

	share, err := h.deps.ShareStore.GetByToken(r.Context(), token)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if msg := validateShareAccess(share); msg != "" {
		http.Error(w, msg, http.StatusGone)
		return
	}

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
		"createdAt":    asset.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":    asset.UpdatedAt.UTC().Format(time.RFC3339),
	}
	contentJSON, _ := json.Marshal(contentData) // #nosec G104 -- simple map marshaling cannot fail

	csp := publicCSP()
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set(headerContentType, "text/html; charset=utf-8")

	brandName := h.deps.BrandName
	if brandName == "" {
		brandName = "MCP Data Platform"
	}
	brandLogo := h.deps.BrandLogoSVG
	if brandLogo == "" {
		brandLogo = defaultLogoSVG
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

	_ = viewerTemplate.Execute(w, map[string]any{
		"Name":               asset.Name,
		"ContentType":        asset.ContentType,
		"Description":        asset.Description,
		"Tags":               asset.Tags,
		"CreatedAtISO":       asset.CreatedAt.UTC().Format(time.RFC3339),
		"UpdatedAtISO":       asset.UpdatedAt.UTC().Format(time.RFC3339),
		"ContentJSON":        template.JS(contentJSON),        // #nosec G203 -- json.Marshal escapes <, >, & as \uXXXX; safe inside <script type="application/json">
		"ContentViewerJS":    template.JS(contentviewer.JS),   // #nosec G203 -- build artifact embedded at compile time, not user input
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
	})
}

// validateShareAccess checks if a share is revoked or expired.
// Returns an error message if invalid, empty string if OK.
func validateShareAccess(share *Share) string {
	if share.Revoked {
		return "This share link has been revoked."
	}
	if share.ExpiresAt != nil && share.ExpiresAt.Before(time.Now()) {
		return "This share link has expired."
	}
	return ""
}

// publicAssetContent serves the raw content for a single-asset public share.
// Always fetches from S3 regardless of size — this is a download endpoint.
func (h *Handler) publicAssetContent(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue(pathKeyToken)
	if token == "" {
		http.NotFound(w, r)
		return
	}

	share, err := h.deps.ShareStore.GetByToken(r.Context(), token)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if msg := validateShareAccess(share); msg != "" {
		http.Error(w, msg, http.StatusGone)
		return
	}

	if share.AssetID == "" {
		http.Error(w, "Not an asset share.", http.StatusBadRequest)
		return
	}

	asset, data, fetchErr := h.fetchAssetContent(r, share.AssetID)
	if fetchErr != nil {
		writePublicError(w, fetchErr)
		return
	}

	w.Header().Set(headerContentType, asset.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", asset.Name))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- content served from S3
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

// fetchPublicAsset retrieves an asset and optionally its S3 content for public viewing.
// For assets exceeding largeAssetPreviewThreshold, content is not fetched and TooLarge is set.
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

	csp := publicCollectionCSP()
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set(headerContentType, "text/html; charset=utf-8")

	brandName := h.deps.BrandName
	if brandName == "" {
		brandName = "MCP Data Platform"
	}
	brandLogo := h.deps.BrandLogoSVG
	if brandLogo == "" {
		brandLogo = defaultLogoSVG
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

	_ = collectionViewerTemplate.Execute(w, map[string]any{
		"Name":               coll.Name,
		"Description":        coll.Description,
		"CollectionJSON":     template.JS(collJSON),           // #nosec G203 -- json.Marshal output
		"ContentViewerJS":    template.JS(contentviewer.JS),   // #nosec G203 -- build artifact
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
	})
}

// validateCollectionItemAccess checks that a token/assetId pair is valid for a collection share.
// Returns the share on success, or writes an HTTP error and returns nil.
func (h *Handler) validateCollectionItemAccess(w http.ResponseWriter, r *http.Request) *Share {
	token := r.PathValue(pathKeyToken)
	assetID := r.PathValue(pathKeyAssetID)
	if token == "" || assetID == "" {
		http.NotFound(w, r)
		return nil
	}

	share, err := h.deps.ShareStore.GetByToken(r.Context(), token)
	if err != nil || share.CollectionID == "" {
		http.NotFound(w, r)
		return nil
	}

	if msg := validateShareAccess(share); msg != "" {
		http.Error(w, msg, http.StatusGone)
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

	w.Header().Set(headerContentType, asset.ContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- content served from S3, content-type set by uploader
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

	data, contentType, s3Err := h.deps.S3Client.GetObject(r.Context(), asset.S3Bucket, asset.ThumbnailS3Key)
	if s3Err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set(headerContentType, contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- thumbnail from S3, content-type set by uploader
}

// publicAssetThumbnail serves the thumbnail for the asset behind a single-asset
// public share. Used as the og:image source when the share's asset has a
// thumbnail but is not itself an image content type. Mirrors
// publicCollectionItemThumbnail but resolves the asset via the share token
// rather than a path-bound assetId, since single-asset shares only expose one
// asset and can't take an assetId path arg.
func (h *Handler) publicAssetThumbnail(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue(pathKeyToken)
	if token == "" {
		http.NotFound(w, r)
		return
	}

	share, err := h.deps.ShareStore.GetByToken(r.Context(), token)
	if err != nil || share.AssetID == "" {
		http.NotFound(w, r)
		return
	}
	if msg := validateShareAccess(share); msg != "" {
		http.Error(w, msg, http.StatusGone)
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

	data, contentType, s3Err := h.deps.S3Client.GetObject(r.Context(), asset.S3Bucket, asset.ThumbnailS3Key)
	if s3Err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set(headerContentType, contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- thumbnail from S3, content-type set by uploader
}

// resolveCollectionForThumbnail loads a non-deleted collection that has an
// uploaded thumbnail, given a share token. Writes 404/410 directly on
// failure and returns nil so callers can early-return without their own
// guard ladder. Extracted from publicCollectionThumbnail to keep that
// handler under the cyclomatic-complexity gate.
func (h *Handler) resolveCollectionForThumbnail(w http.ResponseWriter, r *http.Request) *Collection {
	token := r.PathValue(pathKeyToken)
	if token == "" {
		http.NotFound(w, r)
		return nil
	}
	share, err := h.deps.ShareStore.GetByToken(r.Context(), token)
	if err != nil || share.CollectionID == "" {
		http.NotFound(w, r)
		return nil
	}
	if msg := validateShareAccess(share); msg != "" {
		http.Error(w, msg, http.StatusGone)
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

	data, contentType, s3Err := h.deps.S3Client.GetObject(r.Context(), h.deps.S3Bucket, coll.ThumbnailS3Key)
	if s3Err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set(headerContentType, contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- thumbnail from S3, content-type set by uploader
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

// publicCollectionCSP returns the CSP for the public collection viewer.
// Adds 'self' to frame-src so the asset viewer iframe can load from the same origin.
//
//nolint:lll // CSP directives are necessarily long
func publicCollectionCSP() string {
	return "default-src 'none'; " +
		"frame-src 'self' blob: data:; " +
		"script-src 'unsafe-eval' 'unsafe-inline' blob: https: http:; " +
		"style-src 'unsafe-inline' https:; " +
		"img-src * data: blob:; " +
		"font-src * data:; " +
		"connect-src https: http:;"
}

// publicCSP returns the Content-Security-Policy header value for public view.
// All content types are rendered client-side by the content viewer bundle.
// JSX and HTML content use blob: URL iframes which inherit the parent CSP,
// so the policy must allow external resources those content types may reference.
//
//nolint:lll // CSP directives are necessarily long
func publicCSP() string {
	return "default-src 'none'; " +
		"frame-src blob: data:; " +
		"script-src 'unsafe-eval' 'unsafe-inline' blob: https: http:; " +
		"style-src 'unsafe-inline' https:; " +
		"img-src * data: blob:; " +
		"font-src * data:; " +
		"connect-src https: http:;"
}
