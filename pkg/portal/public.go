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

//go:embed templates/public_viewer.html
var templateFS embed.FS

var viewerTemplate = template.Must(template.ParseFS(templateFS, "templates/public_viewer.html"))

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
