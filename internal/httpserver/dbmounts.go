package httpserver

// This file isolates the two composition-root mount functions whose bodies can
// only run against a live Postgres: they early-return unless the platform has
// constructed its portal/resource stores, which requires a real database
// connection (ping-gated in platform.New). The project confines all real-DB
// tests to the //go:build integration suite (run via `make test-realdb`), and
// that suite produces no coverage profile, so these lines can never appear
// covered in the unit-test coverage.out the patch-coverage gate reads. They are
// exercised by the RealDB integration tests in dbmounts_realdb_integration_test.go
// and, like cmd/dev-mcp-mock, are excluded from the coverage gates in
// scripts/patch-coverage.sh and codecov.yml. The unit-testable helpers they call
// (portalRateLimitResolver, portalBrandName, wirePortalOptionalDeps,
// buildResourceClaims, buildPersonaResolver, buildDataHubRegistrar) stay in
// mounts.go where their coverage is measured normally.

import (
	"errors"
	"log"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// mountPortalAPI registers the portal REST API on the mux if portal is enabled.
func mountPortalAPI(mux *http.ServeMux, p *platform.Platform, notify *notifydelivery.Handle) error {
	if p == nil || portalDisabled(p) {
		return nil
	}
	if p.PortalAssetStore() == nil || p.PortalShareStore() == nil {
		log.Println("Portal enabled but stores not available (database required)")
		return nil
	}

	// Build the trusted-proxy-aware client-IP resolver for the public viewer's
	// rate limiter (#904). A malformed CIDR is a boot-time configuration error
	// surfaced here, the genuine startup path for the portal.
	rlResolver, err := portalRateLimitResolver(p.Config().Portal.RateLimit)
	if err != nil {
		return err
	}

	var portalAuthOpts []portal.AuthenticatorOption
	if p.BrowserSessionAuth() != nil {
		portalAuthOpts = append(portalAuthOpts, portal.WithBrowserAuth(p.BrowserSessionAuth()))
	}
	portalAuth := portal.NewAuthenticator(p.Authenticator(), portalAuthOpts...)

	var adminRoles []string
	if pr := p.PersonaRegistry(); pr != nil {
		if adminP, ok := pr.Get(p.Config().Admin.Persona); ok {
			adminRoles = adminP.Roles
		}
	}

	brandName := portalBrandName(p)

	deps := portal.Deps{
		AssetStore:                  p.PortalAssetStore(),
		ShareStore:                  p.PortalShareStore(),
		VersionStore:                p.PortalVersionStore(),
		CollectionStore:             p.PortalCollectionStore(),
		ThreadStore:                 p.PortalThreadStore(),
		KnowledgePageStore:          p.PortalKnowledgePageStore(),
		KnowledgePageDedupThreshold: p.Config().Knowledge.Pages.Resolve().DedupThreshold,
		S3Client:                    p.PortalS3Client(),
		S3Bucket:                    p.Config().Portal.S3Bucket,
		PublicBaseURL:               p.Config().Portal.PublicBaseURL,
		RateLimit: portal.RateLimitConfig{
			RequestsPerMinute: p.Config().Portal.RateLimit.RequestsPerMinute,
			BurstSize:         p.Config().Portal.RateLimit.BurstSize,
		},
		RateLimitResolver:  rlResolver,
		OIDCEnabled:        p.BrowserSessionFlow() != nil,
		AdminRoles:         adminRoles,
		Authenticator:      portalAuth,
		PromptStore:        p.PromptStore(),
		PromptRegistrar:    p,
		PromptInfoProvider: p,
		BrandName:          brandName,
		BrandLogoSVG:       p.BrandLogoSVG(),
		BrandURL:           p.BrandURL(),
		ImplementorName:    p.Config().Portal.Implementor.Name,
		ImplementorLogoSVG: p.ResolveImplementorLogo(),
		ImplementorURL:     p.Config().Portal.Implementor.URL,
	}
	wirePortalNotifications(&deps, p, notify)

	wirePortalOptionalDeps(&deps, p)

	// Guest access path for email shares (#1001): branded denial pages plus
	// one-time view links for recipients without an account. The share store
	// is present on this path (checked at the top of this function).
	deps.ShareGuest = newShareGuestService(p, notify, p.PortalShareStore(), p.DB())

	handler := portal.NewHandler(deps, portal.RequirePortalAuth(portalAuth))
	mux.Handle("/api/v1/portal/", handler)
	mux.Handle("/portal/view/", handler)
	log.Println("Portal API enabled on /api/v1/portal/")
	return nil
}

// mountResourcesAPI registers the managed resources REST API on the mux if enabled.
func mountResourcesAPI(mux *http.ServeMux, p *platform.Platform) {
	if p == nil || p.ResourceStore() == nil {
		return
	}

	var portalAuthOpts []portal.AuthenticatorOption
	if p.BrowserSessionAuth() != nil {
		portalAuthOpts = append(portalAuthOpts, portal.WithBrowserAuth(p.BrowserSessionAuth()))
	}
	portalAuth := portal.NewAuthenticator(p.Authenticator(), portalAuthOpts...)

	pr := p.PersonaRegistry()
	adminPersona := p.Config().Admin.Persona
	extractClaims := func(r *http.Request) (*resource.Claims, error) {
		user, err := portalAuth.Authenticate(r)
		if err != nil {
			// Surface a CSRF rejection as resource.ErrForbidden so the handler
			// responds 403 (recoverable) instead of 401 (force-logout), matching
			// the admin/portal surfaces.
			if errors.Is(err, browsersession.ErrCSRFInvalid) {
				return nil, resource.ErrForbidden
			}
			return nil, errors.New("authentication required")
		}
		if user == nil {
			return nil, errors.New("authentication required")
		}
		return buildResourceClaims(user, pr, adminPersona), nil
	}

	deps := resource.Deps{
		Store:     p.ResourceStore(),
		S3Client:  p.ResourceS3Client(),
		S3Bucket:  p.Config().Resources.Managed.S3Bucket,
		URIScheme: p.Config().Resources.Managed.URIScheme,
		OnCreate:  p.RegisterManagedResource,
		OnDelete:  p.UnregisterManagedResource,
	}

	handler := resource.NewHandler(deps, extractClaims, nil)
	mux.Handle("/api/v1/resources/", handler)
	mux.Handle("/api/v1/resources", handler)
	log.Println("Managed resources API enabled on /api/v1/resources")
}
