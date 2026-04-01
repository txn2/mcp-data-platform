package portal

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/contentviewer"
)

// incrementAccessTimeout bounds the background goroutine that increments
// share access counters after the HTTP response has been sent.
const incrementAccessTimeout = 5 * time.Second

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
	token := r.PathValue("token")
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

	asset, data, err := h.fetchPublicAsset(r, share.AssetID)
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

	h.renderAssetViewer(w, asset, data, share)
}

// renderAssetViewer renders the public_viewer.html template for an asset.
// Used by both single-asset public shares and collection item views.
func (h *Handler) renderAssetViewer(w http.ResponseWriter, asset *Asset, data []byte, share *Share) {
	contentJSON, _ := json.Marshal(map[string]any{ // #nosec G104 -- string/[]string map marshaling cannot fail
		"contentType": asset.ContentType,
		"content":     string(data),
		"name":        asset.Name,
		"description": asset.Description,
		"tags":        asset.Tags,
		"createdAt":   asset.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":   asset.UpdatedAt.UTC().Format(time.RFC3339),
	})

	csp := publicCSP()
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

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

// publicAssetError categorizes errors from fetchPublicAsset.
type publicAssetError struct {
	Message string
	Status  int
}

func (e *publicAssetError) Error() string { return e.Message }

// fetchPublicAsset retrieves an asset and its S3 content for public viewing.
func (h *Handler) fetchPublicAsset(r *http.Request, assetID string) (*Asset, []byte, error) {
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
		slog.Error("public view: failed to fetch content", "error", err, "asset_id", asset.ID) //nolint:gosec // structured log, not user-facing
		return nil, nil, &publicAssetError{Message: "Failed to retrieve content.", Status: http.StatusInternalServerError}
	}

	return asset, data, nil
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
			slog.Warn("public collection view: failed to increment access", "error", incErr, "share_id", share.ID)
		}
	}()

	// Build asset lookup for items referenced in the collection.
	assetIDs := collectAssetIDs(coll)
	assets := h.fetchAssetMap(r.Context(), assetIDs)

	thumbSize := coll.Config.ThumbnailSize
	if thumbSize == "" {
		thumbSize = "large"
	}

	collJSON, _ := json.Marshal(map[string]any{ //nolint:errcheck // string map marshaling cannot fail
		"id":            coll.ID,
		"name":          coll.Name,
		"description":   coll.Description,
		"thumbnailSize": thumbSize,
		"sections":      buildPublicSections(coll, assets),
		"createdAt":     coll.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     coll.UpdatedAt.UTC().Format(time.RFC3339),
	})

	csp := publicCollectionCSP()
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

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

	_ = collectionViewerTemplate.Execute(w, map[string]any{
		"Name":             coll.Name,
		"Description":      coll.Description,
		"CollectionJSON":   template.JS(collJSON),              // #nosec G203 -- json.Marshal output
		"ContentViewerJS":  template.JS(contentviewer.JS),      // #nosec G203 -- build artifact
		"ContentViewerCSS": template.CSS(contentviewer.CSS),    // #nosec G203 -- build artifact
		"BrandName":        brandName,
		"BrandLogoSVG":       template.HTML(brandLogo),          // #nosec G203 -- operator config
		"BrandURL":           h.deps.BrandURL,
		"ImplementorName":    h.deps.ImplementorName,
		"ImplementorLogoSVG": template.HTML(h.deps.ImplementorLogoSVG), // #nosec G203 -- operator config
		"ImplementorURL":     h.deps.ImplementorURL,
		"Token":              share.Token,
		"ExpiresAtISO":       expiresAtISO,
		"HideExpiration":     share.HideExpiration,
		"NoticeText":         share.NoticeText,
	})
}

// validateCollectionItemAccess checks that a token/assetId pair is valid for a collection share.
// Returns the share on success, or writes an HTTP error and returns nil.
func (h *Handler) validateCollectionItemAccess(w http.ResponseWriter, r *http.Request) *Share {
	token := r.PathValue("token")
	assetID := r.PathValue("assetId")
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
func (h *Handler) publicCollectionItemContent(w http.ResponseWriter, r *http.Request) {
	if h.validateCollectionItemAccess(w, r) == nil {
		return
	}

	asset, data, fetchErr := h.fetchPublicAsset(r, r.PathValue("assetId"))
	if fetchErr != nil {
		writePublicError(w, fetchErr)
		return
	}

	w.Header().Set("Content-Type", asset.ContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// publicCollectionItemView renders the full public asset viewer for an item in a collection.
// This is the same template used for single-asset public shares, loaded in an iframe by the collection viewer.
func (h *Handler) publicCollectionItemView(w http.ResponseWriter, r *http.Request) {
	share := h.validateCollectionItemAccess(w, r)
	if share == nil {
		return
	}

	asset, data, fetchErr := h.fetchPublicAsset(r, r.PathValue("assetId"))
	if fetchErr != nil {
		writePublicError(w, fetchErr)
		return
	}

	// Render with the exact same template and data as a single-asset public view.
	h.renderAssetViewer(w, asset, data, share)
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

	asset, getErr := h.deps.AssetStore.Get(r.Context(), r.PathValue("assetId"))
	if getErr != nil || asset.DeletedAt != nil || asset.ThumbnailS3Key == "" {
		http.NotFound(w, r)
		return
	}

	data, contentType, s3Err := h.deps.S3Client.GetObject(r.Context(), asset.S3Bucket, asset.ThumbnailS3Key)
	if s3Err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
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

// fetchAssetMap retrieves basic metadata for a set of asset IDs.
func (h *Handler) fetchAssetMap(ctx context.Context, assetIDs []string) map[string]*Asset {
	result := make(map[string]*Asset, len(assetIDs))
	for _, id := range assetIDs {
		a, err := h.deps.AssetStore.Get(ctx, id)
		if err == nil && a.DeletedAt == nil {
			result[id] = a
		}
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
				"name":           a.Name,
				"description":    a.Description,
				"contentType":    a.ContentType,
				"hasThumbnail":   a.ThumbnailS3Key != "",
				"thumbnailS3Key": a.ThumbnailS3Key,
			})
		}
		sections = append(sections, map[string]any{
			"title":       sec.Title,
			"description": sec.Description,
			"items":       items,
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
