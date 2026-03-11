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

	rendered, err := renderContent(asset.ContentType, data)
	if err != nil {
		slog.Error("public view: render error", "error", err, "content_type", asset.ContentType) //nolint:gosec // structured log, not user-facing
		http.Error(w, "Failed to render content.", http.StatusInternalServerError)
		return
	}

	csp := publicCSP(asset.ContentType)
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
		"Content":            template.HTML(rendered), // #nosec G203 -- content is sanitized by renderContent
		"BrandName":          brandName,
		"BrandLogoSVG":       template.HTML(brandLogo), // #nosec G203 -- operator-provided SVG from config, not user input
		"BrandURL":           h.deps.BrandURL,
		"ImplementorName":    h.deps.ImplementorName,
		"ImplementorLogoSVG": template.HTML(h.deps.ImplementorLogoSVG), // #nosec G203 -- operator-provided SVG from config
		"ImplementorURL":     h.deps.ImplementorURL,
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
//
// blob: URL iframes inherit the creating document's CSP in modern browsers
// (Chromium, Firefox). Because user-uploaded HTML may reference ANY external
// CDN (Chart.js, D3, Plotly, Google Fonts, etc.), the CSP must allow external
// script/style/font/image sources. Security isolation for the embedded content
// is provided by the iframe's sandbox="allow-scripts" attribute, not by CSP.
func publicCSP(contentType string) string {
	// Both JSX and HTML content need access to external resources.
	// JSX specifically needs esm.sh for React/Recharts module imports.
	if strings.Contains(strings.ToLower(contentType), "jsx") {
		return "default-src 'none'; " +
			"frame-src blob: data:; " +
			"script-src 'unsafe-eval' 'unsafe-inline' blob: https: http:; " +
			"style-src 'unsafe-inline' https:; " +
			"img-src * data: blob:; " +
			"font-src * data:; " +
			"connect-src https: http:;"
	}
	// HTML content may embed scripts from any CDN (Chart.js, D3, Plotly, etc.).
	// The restrictive policy must still allow these external loads.
	return "default-src 'none'; " +
		"frame-src blob: data:; " +
		"script-src 'unsafe-inline' 'unsafe-eval' blob: https: http:; " +
		"style-src 'unsafe-inline' https:; " +
		"img-src * data: blob:; " +
		"font-src * data:; " +
		"connect-src https: http:;"
}

// jsxInnerTpl is parsed once at init. It renders the JSX viewer HTML
// with the user's source safely injected via html/template (which escapes
// for both HTML-element and HTML-attribute contexts, satisfying CodeQL's
// go/unsafe-quoting check). The output is embedded via blob: URL iframe.
//
//nolint:lll // HTML template lines are necessarily long
var jsxInnerTpl = template.Must(template.New("jsx").Parse(`<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-eval' 'unsafe-inline' blob: https://esm.sh; style-src 'unsafe-inline' https://fonts.googleapis.com; img-src data: blob:; font-src data: https://fonts.gstatic.com; connect-src https://esm.sh https://fonts.googleapis.com https://fonts.gstatic.com;">
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
<script type="application/json" id="jsx-source">{{.Source}}</script>
<script type="module">
var ERROR_STYLE = "color:#ef4444;background:#1e1e1e;padding:16px;font-size:13px;white-space:pre-wrap;overflow:auto;height:100%;font-family:monospace";

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

try {
  var jsxSource = JSON.parse(document.getElementById("jsx-source").textContent);

  var sucrase = await import("https://esm.sh/sucrase@3?bundle");
  var React = await import("react");
  var ReactDOMClient = await import("react-dom/client");

  var result = sucrase.transform(jsxSource, {
    transforms: ["jsx"],
    jsxRuntime: "automatic",
    production: true,
  });

  var componentName = null;
  var m = jsxSource.match(/export\s+default\s+(?:function|class)\s+([A-Z][A-Za-z0-9]*)/);
  if (m) componentName = m[1];
  if (!componentName) {
    m = jsxSource.match(/export\s+default\s+([A-Z][A-Za-z0-9]*)\s*;/);
    if (m) componentName = m[1];
  }
  if (!componentName) {
    var matches = Array.from(jsxSource.matchAll(/(?:function|var|let|const|class)\s+([A-Z][A-Za-z0-9]*)/g));
    if (matches.length) componentName = matches[matches.length - 1][1];
  }

  var selfMounting = /\bcreateRoot\s*\(/.test(jsxSource) || /\bReactDOM\s*\.\s*render\s*\(/.test(jsxSource);

  var moduleCode = selfMounting
    ? result.code
    : result.code + "\n" + (componentName
        ? "export { " + componentName + " as __Component__ };"
        : "");

  var blob = new Blob([moduleCode], { type: "text/javascript" });
  var url = URL.createObjectURL(blob);
  var mod = await import(url);
  URL.revokeObjectURL(url);

  if (!selfMounting) {
    var Comp = mod.__Component__ || mod.default;
    if (Comp) {
      ReactDOMClient.createRoot(document.getElementById("root")).render(React.createElement(Comp));
    } else {
      showError("No component found. Use: export default function MyComponent() { ... }");
    }
  }
} catch (e) {
  showError(e.stack || e.message || String(e));
}
</script>
</body>
</html>`))

// jsxIframe produces an iframe with a full client-side React environment
// that transpiles JSX with Sucrase (loaded from esm.sh) and renders it.
// This mirrors the authenticated portal's JsxRenderer.tsx behavior.
func jsxIframe(data []byte) string {
	// Encode JSX source as a JSON string for the data element.
	encoded, _ := json.Marshal(string(data)) // #nosec G104 -- string marshaling cannot fail

	// Render the inner HTML via html/template (safe injection).
	// json.Marshal escapes <, >, & as \u003c, \u003e, \u0026 — so </script>
	// breakout is impossible. template.JS marks the value as pre-sanitized
	// to prevent double-escaping inside the <script> context.
	var inner bytes.Buffer
	_ = jsxInnerTpl.Execute(&inner, map[string]template.JS{ // #nosec G104
		"Source": template.JS(encoded), // #nosec G203 -- json.Marshal escapes <, >, & as \uXXXX; safe for JS
	})

	return blobIframe(inner.String(), "width:100%;border:none;")
}

func sandboxedIframe(data []byte) string {
	return blobIframe(string(data), "width:100%;border:none;")
}

// blobIframe wraps HTML content in a sandboxed iframe that loads via blob: URL.
// NOTE: blob: URL iframes DO inherit the creating document's CSP in modern
// browsers — publicCSP() must therefore allow external resources that the
// embedded content may reference. The sandbox="allow-scripts" attribute
// provides the actual security isolation (opaque origin, no top navigation).
func blobIframe(content, iframeStyle string) string {
	// json.Marshal safely encodes the content as a JSON string, escaping
	// </script> as \u003c/script\u003e to prevent breakout.
	encoded, _ := json.Marshal(content) // #nosec G104 -- string marshaling cannot fail

	var buf bytes.Buffer
	buf.WriteString(`<script type="application/json" id="content-data">`)
	buf.Write(encoded)
	buf.WriteString("</script>\n")
	buf.WriteString(`<iframe id="content-frame" sandbox="allow-scripts" style="`)
	buf.WriteString(template.HTMLEscapeString(iframeStyle))
	buf.WriteString(`"></iframe>` + "\n")
	buf.WriteString(`<script>`)
	buf.WriteString(`(function(){`)
	buf.WriteString(`var d=JSON.parse(document.getElementById("content-data").textContent);`)
	buf.WriteString(`var b=new Blob([d],{type:"text/html;charset=utf-8"});`)
	buf.WriteString(`document.getElementById("content-frame").src=URL.createObjectURL(b);`)
	buf.WriteString(`})();`)
	buf.WriteString(`</script>`)
	return buf.String()
}
