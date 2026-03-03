package portal

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
)

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

	// Increment access count asynchronously.
	go func() {
		if incErr := h.deps.ShareStore.IncrementAccess(r.Context(), share.ID); incErr != nil {
			slog.Warn("public view: failed to increment access", "error", incErr, "share_id", share.ID) // #nosec G706 -- structured log, not user-facing
		}
	}()

	rendered, err := renderContent(asset.ContentType, data)
	if err != nil {
		slog.Error("public view: render error", "error", err, "content_type", asset.ContentType) //nolint:gosec // structured log, not user-facing
		http.Error(w, "Failed to render content.", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = viewerTemplate.Execute(w, map[string]any{
		"Name":        asset.Name,
		"ContentType": asset.ContentType,
		"Content":     template.HTML(rendered), // #nosec G203 -- content is sanitized by renderContent
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

// renderContent produces sanitized HTML for the given content type.
func renderContent(contentType string, data []byte) (string, error) {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "markdown") || strings.HasSuffix(ct, ".md"):
		return renderMarkdown(data)
	case strings.Contains(ct, "svg"):
		return sanitizeSVG(data), nil
	case strings.Contains(ct, "html") || strings.Contains(ct, "jsx"):
		return sandboxedIframe(data), nil
	default:
		// Plain text / unknown: wrap in <pre>.
		escaped := template.HTMLEscapeString(string(data))
		return "<pre>" + escaped + "</pre>", nil
	}
}

func renderMarkdown(data []byte) (string, error) {
	var buf bytes.Buffer
	if err := goldmark.Convert(data, &buf); err != nil {
		return "", fmt.Errorf("rendering markdown: %w", err)
	}
	// Sanitize the rendered HTML.
	p := bluemonday.UGCPolicy()
	return p.Sanitize(buf.String()), nil
}

func sanitizeSVG(data []byte) string {
	p := bluemonday.UGCPolicy()
	p.AllowElements("svg", "path", "circle", "rect", "line", "polyline", "polygon",
		"ellipse", "g", "defs", "use", "text", "tspan", "clipPath", "mask",
		"linearGradient", "radialGradient", "stop", "pattern", "image", "symbol",
		"marker", "filter", "feGaussianBlur", "feOffset", "feMerge", "feMergeNode",
		"feBlend", "feFlood", "feComposite")
	p.AllowAttrs("viewBox", "xmlns", "width", "height", "fill", "stroke",
		"stroke-width", "d", "cx", "cy", "r", "rx", "ry", "x", "y",
		"x1", "y1", "x2", "y2", "points", "transform", "opacity",
		"class", "id", "style", "font-size", "font-family", "text-anchor",
		"dominant-baseline", "offset", "stop-color", "stop-opacity",
		"gradientUnits", "gradientTransform", "spreadMethod",
		"xlink:href", "href", "preserveAspectRatio",
	).Globally()
	return p.Sanitize(string(data))
}

func sandboxedIframe(data []byte) string {
	// Encode content as a data URI with base64 to avoid escaping issues.
	// The iframe is sandboxed: allow-scripts enables execution but
	// no allow-same-origin prevents any access to the parent page.
	var buf bytes.Buffer
	buf.WriteString(`<iframe sandbox="allow-scripts" `)
	buf.WriteString(`style="width:100%;height:80vh;border:1px solid #ddd;border-radius:4px;" `)
	buf.WriteString(`srcdoc="`)
	// Escape for HTML attribute.
	escaped := template.HTMLEscapeString(string(data))
	buf.WriteString(escaped)
	buf.WriteString(`"></iframe>`)
	return buf.String()
}
