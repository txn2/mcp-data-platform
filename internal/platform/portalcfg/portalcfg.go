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
