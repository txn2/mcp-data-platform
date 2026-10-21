// Package portalcfg resolves the defaults of the portal config block: the
// deployment brand, the portal title composed from it, the asset size cap, and
// the asset storage location.
//
// Every function is pure and takes the operator's configured values as
// primitives, so the package depends on nothing outside the standard library
// and the config loader reduces to a sequence of calls.
package portalcfg

import (
	"fmt"
	"strings"
	"time"
)

// DefaultTitle is the portal title of a deployment that names no brand.
const DefaultTitle = "MCP Data Platform"

// DefaultS3Bucket is the bucket portal assets are stored in.
const DefaultS3Bucket = "portal-assets"

// DefaultS3Prefix is the key prefix portal assets are stored under. It keeps
// its historical "artifacts/" value: it addresses stored blobs, not the tool
// surface #1029 renamed, so it is deliberately out of that rename's scope.
const DefaultS3Prefix = "artifacts/"

// DefaultMaxContentSize is the default maximum asset size (10 MB).
const DefaultMaxContentSize = 10 * 1024 * 1024

// brandKeyName and brandKeyURL are the platform-info MCP App config keys the
// brand falls back to.
const (
	brandKeyName = "brand_name"
	brandKeyURL  = "brand_url"
)

// Title returns configured when non-empty, otherwise the title composed from
// brand: "ACME" yields "ACME Portal". A brand that already ends in "Portal" is
// returned unchanged. An empty brand yields DefaultTitle.
func Title(configured, brand string) string {
	if configured != "" {
		return configured
	}
	brand = strings.TrimSpace(brand)
	switch {
	case brand == "":
		return DefaultTitle
	case strings.EqualFold(brand, "portal"), strings.HasSuffix(strings.ToLower(brand), " portal"):
		return brand
	default:
		return brand + " Portal"
	}
}

// Brand returns the deployment brand name and URL, falling back to the
// platform-info MCP App config for whichever of the two the portal block
// leaves empty. A nil appConfig, or appsEnabled false, contributes nothing:
// a disabled MCP Apps subsystem must not drive what the portal renders.
//
// Both results are trimmed, so every surface reads the same value.
func Brand(name, url string, appsEnabled bool, appConfig map[string]any) (brandName, brandURL string) {
	brandName, brandURL = strings.TrimSpace(name), strings.TrimSpace(url)
	if !appsEnabled {
		return brandName, brandURL
	}
	if brandName == "" {
		brandName = appConfigString(appConfig, brandKeyName)
	}
	if brandURL == "" {
		brandURL = appConfigString(appConfig, brandKeyURL)
	}
	return brandName, brandURL
}

// appConfigString reads key from an MCP App config as a trimmed string, or ""
// when the key is absent or holds a non-string.
func appConfigString(appConfig map[string]any, key string) string {
	s, _ := appConfig[key].(string)
	return strings.TrimSpace(s)
}

// MaxContentSize returns configured when non-zero, otherwise
// DefaultMaxContentSize.
func MaxContentSize(configured int) int {
	if configured == 0 {
		return DefaultMaxContentSize
	}
	return configured
}

// S3Location returns the asset storage bucket and key prefix, substituting the
// defaults for whichever is empty.
func S3Location(bucket, prefix string) (s3Bucket, s3Prefix string) {
	s3Bucket, s3Prefix = bucket, prefix
	if s3Bucket == "" {
		s3Bucket = DefaultS3Bucket
	}
	if s3Prefix == "" {
		s3Prefix = DefaultS3Prefix
	}
	return s3Bucket, s3Prefix
}

// MaxVersionsError reports why a configured asset version-retention default is
// unusable, or "" when it is fine. Only a negative value is rejected: 0 asks for
// unlimited history and any positive number is a cap.
//
// It exists because nothing downstream would refuse one. The per-asset column's
// CHECK guards an override, not this value, and the resolver reads a negative
// cap as "keep everything" rather than deleting history on the strength of a
// number nobody could have meant — which would silently invert the ask. The
// per-asset counterpart is portaldomain.ValidateMaxVersions; the two carry
// different messages because they name different things to different readers.
func MaxVersionsError(configured *int) string {
	if configured != nil && *configured < 0 {
		return fmt.Sprintf("portal.max_versions must be 0 (keep every version) or greater, got %d", *configured)
	}
	return ""
}

// Duration parses a portal duration setting such as portal.export.max_timeout,
// returning zero for an empty or unparseable value so the consumer applies its
// own default.
func Duration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

// Implementor configures the optional implementor brand shown in the
// far-left zone of the public viewer header (e.g., "ACME Corp").
type Implementor struct {
	Name string `yaml:"name"` // display name (e.g., "ACME Corp")
	Logo string `yaml:"logo"` // URL to logo SVG (fetched at startup for inline rendering)
	URL  string `yaml:"url"`  // link URL (e.g., "https://acme.com")
}

// RateLimit configures rate limiting for the public portal viewer.
type RateLimit struct {
	RequestsPerMinute int `yaml:"requests_per_minute"` // default: 60
	BurstSize         int `yaml:"burst_size"`          // default: 10
	// TrustedProxies lists CIDRs whose X-Forwarded-For headers are trusted for
	// client attribution, mirroring oauth.rate_limit.trusted_proxies. Empty (the
	// default) trusts none: the direct peer address is used and forwarding
	// headers are ignored, which is the correct safe default when the
	// deployment's proxy topology is unknown. Set this to your reverse-proxy or
	// ingress CIDRs so per-client limiting attributes to the real client instead
	// of collapsing onto the proxy IP.
	TrustedProxies []string `yaml:"trusted_proxies"`
}

// LoginRedirectIgnored is the configuration warning for portal.login_redirect
// set where nothing signs in through an identity provider.
const LoginRedirectIgnored = "portal.login_redirect is ignored: it needs auth.oidc.enabled and auth.browser_session.enabled"

// LoginRedirect is whether a signed-out portal visitor is sent straight to
// the identity provider instead of the login screen (#2242): the setting, in
// force only where the portal signs in through OIDC browser sessions. ignored
// reports the setting made where it cannot take effect.
func LoginRedirect(set, oidcSignIn bool) (redirect, ignored bool) {
	return set && oidcSignIn, set && !oidcSignIn
}

// APIKeyLoginAllowed is whether /portal/?login=api_key still offers the API
// key form on a redirecting deployment; unset is on. It changes only the
// portal login screen: keys keep working on the MCP endpoint and the REST API.
func APIKeyLoginAllowed(allow *bool) bool {
	return allow == nil || *allow
}
