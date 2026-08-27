// Package branding owns the resolved brand assets behind one Handle: the brand
// logo, the brand URL, and the implementor logo. Each is derived from operator
// config (the portal logo, an mcpapps platform-info config map, and the
// implementor logo URL).
//
// A logo may be any image format, and how it reaches a surface depends on what
// that surface can load. The platform's own HTML pages link it: they are served
// from the platform's origin under a policy that admits the image host, so an
// <img> element takes the operator's URL as it is, and the browser caches it
// across page loads. An MCP App cannot: it runs in a sandboxed iframe on a host
// that blocks external loads, so its config carries the logo inlined -- an SVG
// as its own markup, a raster image as a data: URI -- which is what
// InjectPortalLogo fetches for. pkg/contenttype decides what those fetched
// bytes are, so a logo is accepted on the same terms as any other content the
// platform stores.
//
// Construction takes the resolved config values (the portal logo URL, the
// implementor logo URL, and the names each is shown beside) so the subsystem is
// constructible and testable without a Platform. It imports the standard
// library, pkg/contenttype and internal/logsan, never pkg/platform.
//
// InjectPortalLogo is the write seam the caller runs once while assembling the
// platform-info MCP app. The accessors are the read seams the caller surfaces
// to its portal/admin consumers: two that render a brand slot's markup and one
// for the brand URL. Every method is nil-safe, so a caller that never built a
// Handle still reads empty brand assets. ImageSources is the package function
// beside them, naming the origins a page's Content-Security-Policy has to admit
// for that markup to load.
package branding

import (
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// logoFetchTimeout is the maximum duration for fetching a brand or implementor
// logo.
const logoFetchTimeout = 10 * time.Second

// mediaTypePNG is the one type the email logo may be. The brand logo takes any
// image; an email logo may not, because the clients that render it strip SVG
// and the operator documentation names PNG.
const mediaTypePNG = "image/png"

// logoMaxBytes is the maximum size of fetched logo content (1 MB). Content
// larger than this is refused rather than truncated: half a PNG is a broken
// image, and the operator is owed the reason.
const logoMaxBytes = 1 << 20

// Config carries the resolved brand config values the owner caches from. The
// caller translates its own config into this shape so this package stays free of
// the platform's config types.
type Config struct {
	// PortalLogo is the portal.logo URL. When set and no explicit logo_svg /
	// logo_url is present in the app config, InjectPortalLogo fetches and inlines
	// it.
	PortalLogo string
	// ImplementorLogo is the portal.implementor.logo URL. The pages link it, so
	// nothing here fetches it; ImplementorLogoHTML renders it as an <img>.
	ImplementorLogo string
	// BrandName is the resolved portal.brand_name. InjectPortalLogo writes it
	// into an app config that names no brand of its own, and it is the alt text
	// of a raster brand logo.
	BrandName string
	// BrandURL is the portal.brand_url the operator configured. It takes
	// precedence over the brand_url in the mcpapps platform-info app config,
	// which InjectPortalLogo caches only when this is empty.
	BrandURL string
	// ImplementorName is the resolved portal.implementor.name. It is the alt
	// text of a raster implementor logo; this package renders it nowhere else.
	ImplementorName string
}

// Config keys the MCP Apps read their brand from.
const (
	keyBrandName = "brand_name"
	keyBrandURL  = "brand_url"
	keyLogoSVG   = "logo_svg"
	keyLogoURL   = "logo_url"
)

// Handle owns the brand assets: the portal / implementor logo URLs it was
// configured with, the brand name and URL, and the portal logo's fetched bytes
// once an MCP App has needed them. The markup accessors are pure reads over
// config. Only InjectPortalLogo fetches, and it caches, so the caller assembles
// the apps during startup rather than from concurrent request handlers.
type Handle struct {
	portalLogo      string
	implementorLogo string
	implementorName string

	brandName string
	brandURL  string

	// appLogoSVG is an inline SVG an app config named for itself. It is the one
	// logo that is markup rather than a URL, so the brand slots render it as it
	// is instead of linking the portal logo.
	appLogoSVG string
	// portalLogoAsset is the portal logo in the form the MCP App config carries
	// it, fetched at most once; portalResolved records that the fetch has been
	// attempted, so a failure is neither retried per app nor warned about twice.
	portalLogoAsset logo
	portalResolved  bool
}

// New builds a Handle from the resolved brand config. Nothing is fetched here,
// nor by the markup accessors; only InjectPortalLogo goes to the network, once,
// for the app config that has to carry the logo inline.
func New(cfg Config) *Handle {
	return &Handle{
		portalLogo:      cfg.PortalLogo,
		implementorLogo: cfg.ImplementorLogo,
		implementorName: cfg.ImplementorName,
		brandName:       cfg.BrandName,
		brandURL:        cfg.BrandURL,
	}
}

// InjectPortalLogo auto-populates the logo in the platform-info app config from
// the configured portal logo when the operator hasn't set logo_svg or logo_url
// explicitly. The logo is fetched and inlined so it renders in sandboxed
// contexts (MCP App iframes) that block external resource loading: an SVG as
// logo_svg, a raster image as a data: URI under logo_url, which is the key the
// apps already render through an <img> element. It also caches brand_url from
// the app config for use by BrandURL(). No-op passthrough on a nil Handle.
func (h *Handle) InjectPortalLogo(cfg any) any {
	// A nil map inside a non-nil any satisfies the assertion, and is what the
	// caller hands in when the operator declares no app config of their own:
	// writing the brand into it would panic.
	m, ok := cfg.(map[string]any)
	if !ok || m == nil {
		m = make(map[string]any)
	}
	if h == nil {
		return m
	}

	h.syncBrand(m)

	if svg, _ := m[keyLogoSVG].(string); svg != "" {
		// An operator-supplied inline SVG wins over the portal logo URL, and is
		// still cached when there is no portal logo at all.
		h.appLogoSVG = svg
		return m
	}
	if h.portalLogo == "" || m[keyLogoURL] != nil {
		return m
	}
	if !isFetchable(h.portalLogo) {
		// A logo the platform serves itself is a path, which the platform's own
		// pages resolve and an app on another origin cannot. There is nothing
		// to inline and nothing worth warning about: the page surfaces render
		// the logo, and the app renders its own mark.
		slog.Debug("portal logo is not an absolute http(s) URL; MCP Apps render their default mark",
			"url", logsan.SanitizeForLog(h.portalLogo))
		return m
	}

	lg := h.resolvePortalLogo()
	switch {
	case lg.svg != "":
		m[keyLogoSVG] = lg.svg
	case lg.dataURI != "":
		m[keyLogoURL] = lg.dataURI
	default:
		// Unresolvable: hand the app the URL itself, so a host that does allow
		// an external load still shows the operator's logo.
		m[keyLogoURL] = h.portalLogo
	}
	return m
}

// resolvePortalLogo fetches the configured portal logo at most once and caches
// the outcome, failure included: both built-in apps ask for the same logo, and
// one misconfiguration is one warning. Its one caller has already established
// that a portal logo is configured.
func (h *Handle) resolvePortalLogo() logo {
	if h.portalResolved {
		return h.portalLogoAsset
	}
	h.portalResolved = true
	lg, err := resolveLogo(h.portalLogo)
	if err != nil {
		warnLogo("portal.logo", h.portalLogo, err)
		return logo{}
	}
	h.portalLogoAsset = lg
	return lg
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

// BrandLogoHTML returns the brand logo as the markup a brand slot inlines: an
// <img> element sourced at the configured portal logo URL, or the inline SVG an
// app config named instead. Empty when no logo is configured or on a nil
// Handle, which leaves each surface to its own fallback.
//
// The page linking the image rather than carrying it is what keeps a logo of
// any size off every render, and lets the browser cache it across page loads.
// The one surface that cannot link it is an MCP App, whose config
// InjectPortalLogo fills with inlined bytes instead.
func (h *Handle) BrandLogoHTML() string {
	if h == nil {
		return ""
	}
	if h.appLogoSVG != "" {
		return h.appLogoSVG
	}
	return imgMarkup(h.portalLogo, h.brandName)
}

// ImplementorLogoHTML returns the implementor logo as the markup a brand slot
// inlines: an <img> element sourced at the configured implementor logo URL.
// Empty when no logo is configured or on a nil Handle, which leaves a
// configured implementor name rendering on its own.
func (h *Handle) ImplementorLogoHTML() string {
	if h == nil {
		return ""
	}
	return imgMarkup(h.implementorLogo, h.implementorName)
}

// ImageSources returns the sources a page's Content-Security-Policy must admit
// under img-src for brand markup to load: one entry per distinct source across
// the logo URLs given, and nothing at all when none of them names one. Naming
// them keeps a page that renders a logo from having to admit every image host
// on the internet.
//
// It takes the URLs rather than a Handle because the caller that builds a
// page's policy holds the config already, and a pure function over that config
// keeps the platform facade from growing another accessor.
func ImageSources(logoURLs ...string) []string {
	var out []string
	for _, raw := range logoURLs {
		src := imageSource(raw)
		if src == "" || slices.Contains(out, src) {
			continue
		}
		out = append(out, src)
	}
	return out
}

// imgMarkup renders one logo URL as the <img> element a brand slot inlines,
// carrying the name it is shown beside as its alt text. A logo the deployment
// names nothing alongside is decorative and carries an empty alt rather than an
// invented one. Both values are operator config and are escaped anyway, because
// the slot inserts the result as trusted HTML.
func imgMarkup(logoURL, alt string) string {
	if logoURL == "" {
		return ""
	}
	return `<img src="` + html.EscapeString(logoURL) + `" alt="` + html.EscapeString(alt) + `">`
}

// imageSource names the CSP source one logo URL needs: its own scheme://host
// when the operator hosts it elsewhere, 'self' when the URL is a path the
// platform serves itself, and "" for a setting that names no image a page can
// load. These policies list no 'self' of their own, so a logo served from the
// platform must add it rather than assume it.
func imageSource(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Host == "" {
		if u.Scheme == "" {
			return "'self'"
		}
		// A scheme a page cannot load an image from (data: is already in every
		// policy here, and the rest name no image at all).
		return ""
	}
	if u.Scheme == "" {
		// Scheme-relative: the host alone is a valid CSP source, and matches
		// whichever scheme the page itself was served over.
		return u.Host
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.Scheme + "://" + u.Host
	}
	return ""
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

// warnLogo reports a configured logo the platform could not fetch for an MCP
// App. It is a warning, not a debug line: the operator set a logo, the app
// renders the URL it cannot load or its own default mark, and this is the only
// place that says why.
func warnLogo(setting, logoURL string, err error) {
	slog.Warn("configured logo could not be resolved, rendering without it",
		"setting", setting,
		"url", logsan.SanitizeForLog(logoURL),
		"err", logsan.SanitizeForLog(err.Error()))
}

// FetchEmailLogoPNG downloads the raster logo used in notification emails and
// returns its bytes. Email clients strip inline SVG, so the email logo is a
// separate raster asset from the logo the portal and MCP Apps render.
//
// The caller resolves this once at startup and hands the bytes to the renderer,
// which attaches them to each message as an inline (cid:) part. Recipients never
// fetch the URL themselves, so it need only be reachable from the server.
func FetchEmailLogoPNG(logoURL string) ([]byte, error) {
	body, ct, err := fetchLogo(logoURL)
	if err != nil {
		return nil, err
	}
	if ct != mediaTypePNG {
		return nil, fmt.Errorf("not a PNG: %s", ct)
	}
	return body, nil
}

// logo is a fetched brand logo in the form an MCP App config carries it.
// Exactly one field is set: svg holds the markup of a vector logo, which the
// app inlines as itself, and dataURI the encoded bytes of a raster one, which
// it renders as an <img> source. Both forms render where an external image load
// is blocked, which is the whole reason the app config carries bytes at all.
type logo struct {
	svg     string
	dataURI string
}

// resolveLogo downloads a logo and returns it in the form its family inlines
// as. An SVG keeps its own markup; every other image family becomes a data:
// URI. Anything that is not an image is refused, because the slot holds a
// picture and nothing else.
//
// Detection never names SVG from content alone -- the active-type rule in
// pkg/contenttype keeps a mislabeled upload from turning itself into
// script-bearing content -- so a vector logo a server declares generically
// arrives as XML or plain text and is recognized here instead. Reading the
// root element adds no exposure: the URL is operator config, and a host that
// serves the same bytes under image/svg+xml has them inlined either way.
func resolveLogo(logoURL string) (logo, error) {
	body, ct, err := fetchLogo(logoURL)
	if err != nil {
		return logo{}, err
	}
	if ct == contenttype.SVG || (!contenttype.IsImage(ct) && isSVGDocument(body)) {
		return logo{svg: string(body)}, nil
	}
	if !contenttype.IsImage(ct) {
		return logo{}, fmt.Errorf("not an image: %s", ct)
	}
	return logo{dataURI: "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(body)}, nil
}

// svgSniffLen is how much of a document is read looking for its root element.
// A prolog, a doctype and a license comment fit well inside it.
const svgSniffLen = 4096

// isSVGDocument reports whether the body's root element is <svg>. An XML
// declaration, a doctype and comments may precede it; anything else -- an HTML
// page whose body happens to contain an inline <svg>, a JSON document, a text
// file -- does not qualify, because only the first element decides what the
// document is.
func isSVGDocument(body []byte) bool {
	head := body
	if len(head) > svgSniffLen {
		head = head[:svgSniffLen]
	}
	rest := strings.TrimSpace(string(head))
	for {
		prefix, terminator := prologTerminator(rest)
		if prefix == "" {
			return strings.HasPrefix(strings.ToLower(rest), "<svg")
		}
		_, after, found := strings.Cut(rest, terminator)
		if !found {
			return false
		}
		rest = strings.TrimSpace(after)
	}
}

// prologTerminator names the closing token of the prolog construct rest opens
// with -- an XML declaration or processing instruction, a comment, a doctype --
// and returns empty strings when rest opens with the root element itself.
func prologTerminator(rest string) (prefix, terminator string) {
	switch {
	case strings.HasPrefix(rest, "<?"):
		return "<?", "?>"
	case strings.HasPrefix(rest, "<!--"):
		return "<!--", "-->"
	case strings.HasPrefix(rest, "<!"):
		return "<!", doctypeTerminator(rest)
	default:
		return "", ""
	}
}

// doctypeTerminator picks the token a doctype ends at. One carrying an internal
// subset ends after the closing bracket, not at the first '>', which belongs to
// a declaration inside the subset.
func doctypeTerminator(rest string) string {
	bracket := strings.Index(rest, "[")
	if bracket >= 0 && bracket < indexOrLen(rest, ">") {
		return "]>"
	}
	return ">"
}

// indexOrLen is strings.Index with the string's length standing in for absence,
// so a caller comparing two positions treats "not present" as "after".
func indexOrLen(s, substr string) int {
	if i := strings.Index(s, substr); i >= 0 {
		return i
	}
	return len(s)
}

// isFetchable reports whether a logo URL is one this package can retrieve. Only
// http(s) is accepted: a container image has no portable local path for an
// operator to point at, and a path the platform serves is fetched by the
// browser rendering the page rather than by the server.
func isFetchable(logoURL string) bool {
	return strings.HasPrefix(logoURL, "http://") || strings.HasPrefix(logoURL, "https://")
}

// fetchLogo downloads a logo asset and returns its bytes and the media type the
// platform's own detection names them, rather than the Content-Type header
// alone: a bucket that serves every object as application/octet-stream still
// serves a usable logo. Only http(s) URLs are accepted: a container image has
// no portable local path for an operator to point at.
func fetchLogo(logoURL string) (body []byte, mediaType string, err error) {
	if !isFetchable(logoURL) {
		return nil, "", errors.New("unsupported scheme")
	}

	client := &http.Client{Timeout: logoFetchTimeout}
	resp, err := client.Get(logoURL) //nolint:gosec,noctx // URL comes from operator config, not user input
	if err != nil {
		return nil, "", fmt.Errorf("fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}

	// One byte past the cap distinguishes "exactly at the limit" from "larger
	// than the limit", which is refused rather than served truncated.
	body, err = io.ReadAll(io.LimitReader(resp.Body, logoMaxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read: %w", err)
	}
	if len(body) > logoMaxBytes {
		return nil, "", fmt.Errorf("larger than %d bytes", logoMaxBytes)
	}

	return body, contenttype.DetectFileBytes(resp.Header.Get("Content-Type"), logoFilename(logoURL), body), nil
}

// logoFilename recovers the file name the URL ends in, which detection uses to
// resolve a declaration the extension and the content both contradict. A URL
// that names no file yields "", which makes detection read the bytes alone.
func logoFilename(logoURL string) string {
	u, err := url.Parse(logoURL)
	if err != nil {
		return ""
	}
	// path.Base answers "." for an empty path and "/" for a bare root; neither
	// names a file.
	base := path.Base(u.Path)
	if base == "." || base == "/" {
		return ""
	}
	return base
}
