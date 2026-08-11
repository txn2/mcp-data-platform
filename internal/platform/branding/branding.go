// Package branding owns the resolved-once brand assets behind one Handle: the
// brand logo SVG, the brand URL, and the implementor logo SVG. Each is derived
// from operator config (the portal logo, an mcpapps platform-info config map,
// and the implementor logo URL) and cached so repeated reads — and repeated
// fetches of a remote SVG — resolve only once.
//
// Construction takes the resolved config values (the portal logo URL and the
// implementor logo URL) so the subsystem is constructible and testable without a
// Platform. It imports only the standard library, never pkg/platform.
//
// InjectPortalLogo is the write seam the caller runs once while assembling the
// platform-info MCP app: it caches the brand URL and logo SVG from the app
// config, inlines the portal logo SVG (fetched from a URL) so it renders in
// sandboxed MCP App iframes that block external resource loading, and returns
// the possibly-augmented config map. The three accessors are the read seams the
// caller surfaces to its portal/admin consumers. Every method is nil-safe, so a
// caller that never built a Handle still reads empty brand assets.
package branding

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// logoFetchTimeout is the maximum duration for fetching a brand or implementor
// logo SVG.
const logoFetchTimeout = 10 * time.Second

// logoMaxBytes is the maximum size of fetched logo content (1 MB).
const logoMaxBytes = 1 << 20

// Config carries the resolved brand config values the owner caches from. The
// caller translates its own config into this shape so this package stays free of
// the platform's config types.
type Config struct {
	// PortalLogo is the portal.logo URL. When set and no explicit logo_svg /
	// logo_url is present in the app config, InjectPortalLogo fetches and inlines
	// it as logo_svg.
	PortalLogo string
	// ImplementorLogo is the portal.implementor.logo URL, fetched lazily by
	// ResolveImplementorLogo.
	ImplementorLogo string
	// BrandName is the resolved portal.brand_name. InjectPortalLogo writes it
	// into an app config that names no brand of its own.
	BrandName string
	// BrandURL is the portal.brand_url the operator configured. It takes
	// precedence over the brand_url in the mcpapps platform-info app config,
	// which InjectPortalLogo caches only when this is empty.
	BrandURL string
}

// Config keys the MCP Apps read their brand from.
const (
	keyBrandName = "brand_name"
	keyBrandURL  = "brand_url"
)

// Handle owns the resolved brand assets: the portal / implementor logo URLs it
// was configured with and the cached brand logo SVG, brand URL, and implementor
// logo SVG. The read accessors expose the cached values to the caller's
// portal/admin consumers; each resolves at most once.
type Handle struct {
	portalLogo      string
	implementorLogo string

	brandLogoSVG            string
	brandName               string
	brandURL                string
	resolvedImplementorLogo string
}

// New builds a Handle from the resolved brand config. The cached assets start
// empty; InjectPortalLogo and ResolveImplementorLogo populate them on first use.
func New(cfg Config) *Handle {
	return &Handle{
		portalLogo:      cfg.PortalLogo,
		implementorLogo: cfg.ImplementorLogo,
		brandName:       cfg.BrandName,
		brandURL:        cfg.BrandURL,
	}
}

// InjectPortalLogo auto-populates the logo in the platform-info app config from
// the configured portal logo when the operator hasn't set logo_svg or logo_url
// explicitly. When the logo is an SVG URL, it is fetched and inlined as logo_svg
// so the logo renders in sandboxed contexts (MCP App iframes) that block
// external resource loading. It also caches brand_url from the app config for
// use by BrandURL(). No-op passthrough on a nil Handle.
func (h *Handle) InjectPortalLogo(cfg any) any {
	m, ok := cfg.(map[string]any)
	if !ok {
		m = make(map[string]any)
	}
	if h == nil {
		return m
	}

	h.syncBrand(m)

	if h.portalLogo == "" {
		// Still cache logo_svg if present in the app config.
		if svg, _ := m["logo_svg"].(string); svg != "" {
			h.brandLogoSVG = svg
		}
		return m
	}

	if svg, _ := m["logo_svg"].(string); svg != "" {
		h.brandLogoSVG = svg
		return m
	}
	if m["logo_url"] != nil {
		return m
	}

	// Fetch SVG content for inline rendering; fall back to URL on failure.
	if svg, err := fetchLogoSVG(h.portalLogo); err == nil {
		m["logo_svg"] = svg
		h.brandLogoSVG = svg
	} else {
		slog.Debug("portal logo fetch failed, using URL", "url", h.portalLogo, "err", err)
		m["logo_url"] = h.portalLogo
	}
	return m
}

// syncBrand reconciles the brand between the portal block and one app config,
// in both directions. A configured portal.brand_* is written into an app config
// that names no brand of its own, so the MCP Apps render the deployment's brand
// without the operator repeating it under mcpapps. An app config that does name
// a brand keeps it, and backfills the Handle when the portal block named none.
func (h *Handle) syncBrand(m map[string]any) {
	h.brandName = reconcile(m, keyBrandName, h.brandName)
	h.brandURL = reconcile(m, keyBrandURL, h.brandURL)
}

// reconcile returns the value for key: the configured value when set (writing
// it into m if m leaves the key empty), otherwise m's own value.
func reconcile(m map[string]any, key, configured string) string {
	existing, _ := m[key].(string)
	if configured == "" {
		return existing
	}
	if existing == "" {
		m[key] = configured
	}
	return configured
}

// BrandLogoSVG returns the resolved brand logo SVG content (from the portal logo
// or the mcpapps platform-info config), or "" if none is configured or on a nil
// Handle.
func (h *Handle) BrandLogoSVG() string {
	if h == nil {
		return ""
	}
	return h.brandLogoSVG
}

// BrandURL returns the resolved brand URL — portal.brand_url when set,
// otherwise brand_url from the mcpapps platform-info config — or "" if neither
// is configured or on a nil Handle.
func (h *Handle) BrandURL() string {
	if h == nil {
		return ""
	}
	return h.brandURL
}

// ResolveImplementorLogo fetches the implementor logo SVG from the configured
// implementor logo URL. The result is cached so subsequent calls return the same
// value without another HTTP request. Returns "" if no logo URL is configured,
// the fetch fails, or the Handle is nil.
func (h *Handle) ResolveImplementorLogo() string {
	if h == nil || h.implementorLogo == "" {
		return ""
	}
	if h.resolvedImplementorLogo != "" {
		return h.resolvedImplementorLogo
	}
	svg, err := fetchLogoSVG(h.implementorLogo)
	if err != nil {
		slog.Debug("implementor logo fetch failed", "url", h.implementorLogo, "err", err)
		return ""
	}
	h.resolvedImplementorLogo = svg
	return svg
}

// FetchEmailLogoPNG downloads the raster logo used in notification emails and
// returns its bytes. Email clients strip inline SVG, so the email logo is a
// separate raster asset from the SVG the portal and MCP Apps render.
//
// The caller resolves this once at startup and hands the bytes to the renderer,
// which attaches them to each message as an inline (cid:) part. Recipients never
// fetch the URL themselves, so it need only be reachable from the server.
func FetchEmailLogoPNG(url string) ([]byte, error) {
	return fetchLogo(url, "png")
}

// fetchLogoSVG downloads an SVG from the given URL and returns its content.
// Returns an error if the URL is unreachable, returns a non-SVG content type,
// or exceeds the size limit.
func fetchLogoSVG(url string) (string, error) {
	body, err := fetchLogo(url, "svg")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// fetchLogo downloads a logo asset and returns its bytes, requiring wantType to
// appear in the response Content-Type. Only http(s) URLs are accepted: a
// container image has no portable local path for an operator to point at.
func fetchLogo(url, wantType string) ([]byte, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, errors.New("unsupported scheme")
	}

	client := &http.Client{Timeout: logoFetchTimeout}
	resp, err := client.Get(url) //nolint:gosec,noctx // URL comes from operator config, not user input
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, wantType) {
		return nil, fmt.Errorf("not %s: %s", wantType, ct)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, logoMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	return body, nil
}
