package portal

import (
	"bytes"
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

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
)

// incrementAccessTimeout bounds the background goroutine that increments
// share access counters after the HTTP response has been sent.
const incrementAccessTimeout = 5 * time.Second

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

	rendered, err := renderContent(asset.ContentType, data)
	if err != nil {
		slog.Error("public view: render error", "error", err, "content_type", asset.ContentType) //nolint:gosec // structured log, not user-facing
		http.Error(w, "Failed to render content.", http.StatusInternalServerError)
		return
	}

	csp := publicCSP(asset.ContentType)
	w.Header().Set("Content-Security-Policy", csp)
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

// renderContent produces sanitized HTML for the given content type.
func renderContent(contentType string, data []byte) (string, error) {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "markdown") || strings.HasSuffix(ct, ".md"):
		return renderMarkdown(data)
	case strings.Contains(ct, "svg"):
		return sanitizeSVG(data), nil
	case strings.Contains(ct, "jsx"):
		return jsxIframe(data), nil
	case strings.Contains(ct, "html"):
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
		"class", "id", "font-size", "font-family", "text-anchor",
		"dominant-baseline", "offset", "stop-color", "stop-opacity",
		"gradientUnits", "gradientTransform", "spreadMethod",
		"xlink:href", "href", "preserveAspectRatio",
	).Globally()
	return p.Sanitize(string(data))
}

// publicCSP returns the Content-Security-Policy header value for public view.
// JSX content needs a relaxed policy to load React from esm.sh.
func publicCSP(contentType string) string {
	if strings.Contains(strings.ToLower(contentType), "jsx") {
		return "default-src 'none'; " +
			"frame-src blob: data:; " +
			"script-src 'unsafe-inline'; " +
			"style-src 'unsafe-inline'; " +
			"img-src data:;"
	}
	return "default-src 'none'; style-src 'unsafe-inline'; img-src data:;"
}

// jsxIframe produces an iframe with a full client-side React environment
// that transpiles JSX with Sucrase (loaded from esm.sh) and renders it.
// This mirrors the authenticated portal's JsxRenderer.tsx behavior.
func jsxIframe(data []byte) string {
	// Encode JSX source as a JSON string to safely embed in <script>.
	encoded, _ := json.Marshal(string(data)) // #nosec G104 -- string marshaling cannot fail

	var buf bytes.Buffer
	buf.WriteString(`<iframe sandbox="allow-scripts" `)
	buf.WriteString(`style="width:100%;height:80vh;border:none;" `)
	buf.WriteString(`srcdoc="`)

	// Build the iframe srcdoc — a complete HTML page with import maps and Sucrase.
	inner := jsxIframeSrcdoc(string(encoded))
	buf.WriteString(template.HTMLEscapeString(inner))
	buf.WriteString(`"></iframe>`)
	return buf.String()
}

// jsxIframeSrcdoc returns the HTML content for the JSX rendering iframe.
func jsxIframeSrcdoc(jsonSource string) string {
	return `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-eval' 'unsafe-inline' https://esm.sh; style-src 'unsafe-inline' https://fonts.googleapis.com; img-src data: blob:; font-src data: https://fonts.gstatic.com; connect-src https://esm.sh https://fonts.googleapis.com https://fonts.gstatic.com;">
<script type="importmap">
{
  "imports": {
    "react": "https://esm.sh/react@19",
    "react/": "https://esm.sh/react@19/",
    "react-dom": "https://esm.sh/react-dom@19",
    "react-dom/": "https://esm.sh/react-dom@19/",
    "react-dom/client": "https://esm.sh/react-dom@19/client",
    "recharts": "https://esm.sh/recharts@2?bundle&external=react,react-dom",
    "lucide-react": "https://esm.sh/lucide-react@0.469?bundle&external=react"
  }
}
</script>
<style>* { margin: 0; padding: 0; box-sizing: border-box; } body { font-family: system-ui, sans-serif; padding: 16px; }</style>
</head>
<body>
<div id="root"><p style="color:#888;font-family:system-ui,sans-serif;padding:16px">Loading component...</p></div>
<script type="module">
const ERROR_STYLE = "color:#ef4444;background:#1e1e1e;padding:16px;font-size:13px;white-space:pre-wrap;overflow:auto;height:100%;font-family:monospace";

function showError(text) {
  var el = document.createElement("pre");
  el.setAttribute("style", ERROR_STYLE);
  el.textContent = text;
  var root = document.getElementById("root");
  root.textContent = "";
  root.appendChild(el);
}

window.onerror = function(msg, src, line, col, err) {
  showError(err && err.stack ? err.stack : msg);
};
window.addEventListener("unhandledrejection", function(e) {
  showError("Module load error: " + (e.reason && e.reason.stack ? e.reason.stack : e.reason));
});

const jsxSource = ` + jsonSource + `;

try {
  const { transform } = await import("https://esm.sh/sucrase@3?bundle");
  const React = await import("react");
  const { createRoot } = await import("react-dom/client");

  const result = transform(jsxSource, {
    transforms: ["jsx"],
    jsxRuntime: "automatic",
    production: true,
  });

  // Detect component name
  let componentName = null;
  let m = jsxSource.match(/export\s+default\s+(?:function|class)\s+([A-Z][A-Za-z0-9]*)/);
  if (m) componentName = m[1];
  if (!componentName) {
    m = jsxSource.match(/export\s+default\s+([A-Z][A-Za-z0-9]*)\s*;/);
    if (m) componentName = m[1];
  }
  if (!componentName) {
    const matches = [...jsxSource.matchAll(/(?:function|const|let|class)\s+([A-Z][A-Za-z0-9]*)/g)];
    if (matches.length) componentName = matches[matches.length - 1][1];
  }

  // Check for self-mounting code
  const selfMounting = /\bcreateRoot\s*\(/.test(jsxSource) || /\bReactDOM\s*\.\s*render\s*\(/.test(jsxSource);

  // Create module blob and import it
  const moduleCode = selfMounting
    ? result.code
    : result.code + "\n" + (componentName
        ? "export { " + componentName + " as __Component__ };"
        : "");

  const blob = new Blob([moduleCode], { type: "text/javascript" });
  const url = URL.createObjectURL(blob);
  const mod = await import(url);
  URL.revokeObjectURL(url);

  if (!selfMounting) {
    const Comp = mod.__Component__ || mod.default;
    if (Comp) {
      createRoot(document.getElementById("root")).render(React.createElement(Comp));
    } else {
      showError("No component found. Use: export default function MyComponent() { ... }");
    }
  }
} catch (e) {
  showError(e.stack || e.message || String(e));
}
<\/script>
</body>
</html>`
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
