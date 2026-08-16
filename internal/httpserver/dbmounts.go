package httpserver

// This file isolates the composition-root mount functions whose bodies can
// only run against a live Postgres: they early-return unless the platform has
// constructed its portal/resource stores or a versioning-capable prompt store,
// which requires a real database connection (ping-gated in platform.New). The project confines all real-DB
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
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpserver/attachhttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/mentionhttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/versionhttp"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/resourceaudit"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
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

	// Authentication proves identity; the gate decides access. Every
	// authenticated portal route runs through both, so an account the IdP will
	// issue a token for but no persona claims reaches nothing. The public
	// share viewer under /portal/view/ is deliberately outside this chain —
	// Handler.ServeHTTP routes it to its own unauthenticated mux.
	wrap := portalAuthChain(portalAuth, portalAccessGate(p, deps.PersonaResolver))

	handler := portal.NewHandler(deps, wrap)
	mux.Handle("/api/v1/portal/", handler)
	mux.Handle("/portal/view/", handler)
	mountPromptVersionPortalAPI(mux, p, wrap, adminRoles)
	mountScriptPortalAPI(mux, p, wrap, adminRoles)
	mountMentionAPI(mux, p, wrap, adminRoles)
	log.Println("Portal API enabled on /api/v1/portal/ (persona required)")
	return nil
}

// mountPromptVersionAdminAPI registers the admin prompt-version routes when
// the platform has a versioning store (database deployments). Called from
// mountAdminAPI; a no-DB platform early-returns.
func mountPromptVersionAdminAPI(mux *http.ServeMux, p *platform.Platform, prefix string) {
	deps, ok := promptVersionDeps(p)
	if !ok {
		return
	}
	deps.AdminEmail = adminEmail
	versionhttp.New(deps).RegisterAdmin(mux, prefix, buildAdminAuth(p))
	attachhttp.New(promptAttachmentDeps(p, adminAttachmentIdentity(p.PersonaRegistry(), p.Config().Admin.Persona))).
		Register(mux, prefix, buildAdminAuth(p))
}

// mountScriptAdminAPI registers the managed-script review routes when the
// deployment has a database to keep scripts in. Called from mountAdminAPI.
//
// The store is built here, over the pool the platform already holds, rather
// than reached through a facade accessor: this is a composition root, the
// script store is stateless over that pool, and the alternative would put a
// pass-through accessor on a package that is at its size budget.
func mountScriptAdminAPI(mux *http.ServeMux, p *platform.Platform, prefix string) {
	deps, ok := scriptDeps(p)
	if !ok {
		return
	}
	deps.AdminEmail = adminEmail
	scripthttp.New(deps).RegisterAdmin(mux, prefix, buildAdminAuth(p))
}

// mountScriptPortalAPI registers the portal script routes: the scripts a caller
// may see, one script's contract, and — for the scripts they own — its
// versions, its run history, and its cadence. Called from mountPortalAPI with
// the portal's assembled auth middleware and admin roles.
//
// The portal surface exists because a script's owner is frequently not an
// administrator, and every other script route is admin-only. What it mutates is
// the cadence and the source (#1307), and neither grants anything: a cadence
// carries no authority, and an edit to an approved script becomes a draft for
// review. Approval stays on the admin surface.
func mountScriptPortalAPI(mux *http.ServeMux, p *platform.Platform, wrap func(http.Handler) http.Handler, adminRoles []string) {
	deps, ok := scriptDeps(p)
	if !ok {
		return
	}
	// Mirror the sibling persona wiring's nil guard (a nil registry would
	// otherwise panic per-request inside the resolver closure).
	var resolver portal.PersonaResolver
	if pr := p.PersonaRegistry(); pr != nil {
		resolver = buildPersonaResolver(pr, p.ToolkitRegistry())
	}
	deps.PortalUser = scriptPortalIdentity(adminRoles, resolver)
	scripthttp.New(deps).RegisterPortal(mux, wrap)
}

// scriptDeps assembles the surface-independent script handler dependencies,
// reporting ok=false when the deployment has nowhere to keep scripts.
//
// The store is built here, over the pool the platform already holds, rather
// than reached through a facade accessor: this is a composition root, the
// script store is stateless over that pool, and the alternative would put a
// pass-through accessor on a package that is at its size budget.
func scriptDeps(p *platform.Platform) (scripthttp.Deps, bool) {
	if p.DB() == nil {
		return scripthttp.Deps{}, false
	}
	store := scriptstore.New(p.DB())
	return scripthttp.Deps{
		Scripts:    store,
		Versions:   store,
		Approvals:  store,
		Reviews:    store,
		Rejections: store,
		Schedules:  store,
		Runs:       store,
		Contracts:  store,
		LatestRuns: store,
	}, true
}

// scriptPortalIdentity resolves the portal caller for the script routes: who
// they are, the persona they resolved to, and whether they hold the admin
// roles that make the surface unrestricted for them.
func scriptPortalIdentity(adminRoles []string, resolver portal.PersonaResolver) func(r *http.Request) *scripthttp.PortalIdentity {
	return func(r *http.Request) *scripthttp.PortalIdentity {
		user := portal.GetUser(r.Context())
		if user == nil {
			return nil
		}
		id := &scripthttp.PortalIdentity{
			UserID: user.UserID, Email: user.Email, Roles: user.Roles,
			IsAdmin: rolesIntersect(user.Roles, adminRoles),
		}
		if resolver != nil {
			if pi := resolver(user.Roles); pi != nil {
				id.Persona = pi.Name
			}
		}
		return id
	}
}

// mountPromptVersionPortalAPI registers the portal prompt-version routes.
// Called from mountPortalAPI with the portal's assembled auth middleware and
// admin roles.
func mountPromptVersionPortalAPI(mux *http.ServeMux, p *platform.Platform, wrap func(http.Handler) http.Handler, adminRoles []string) {
	deps, ok := promptVersionDeps(p)
	if !ok {
		return
	}
	// Mirror the sibling persona wiring's nil guard (a nil registry would
	// otherwise panic per-request inside the resolver closure).
	var resolver portal.PersonaResolver
	if pr := p.PersonaRegistry(); pr != nil {
		resolver = buildPersonaResolver(pr, p.ToolkitRegistry())
	}
	deps.PortalUser = portalIdentityResolver(adminRoles, resolver)
	versionhttp.New(deps).RegisterPortal(mux, wrap)
	attachhttp.New(promptAttachmentDeps(p, portalAttachmentIdentity(p.PersonaRegistry(), p.Config().Admin.Persona))).
		Register(mux, "/api/v1/portal", wrap)
}

// promptVersionDeps assembles the surface-independent handler dependencies,
// reporting ok=false when the platform has no prompt versioning (no database).
// The versioning capability is asserted from the prompt store, which the
// prompt layer's notifying wrapper preserves.
func promptVersionDeps(p *platform.Platform) (versionhttp.Deps, bool) {
	store := p.PromptStore()
	versions, _ := store.(prompt.VersionStore)
	if versions == nil {
		return versionhttp.Deps{}, false
	}
	deps := versionhttp.Deps{
		Store:       store,
		Versions:    versions,
		Registrar:   p,
		Collections: prompt.AsCollectionStore(store),
	}
	if s := p.AuditStore(); s != nil {
		deps.Usage = s
	}
	// Prompts shared person-to-person are as visible as the caller's own, so
	// their usage joins the portal rollup (#1010).
	if ss := p.PortalShareStore(); ss != nil {
		deps.SharedPromptIDs = func(ctx context.Context, userID, email string) ([]string, error) {
			refs, err := ss.ListSharedPromptsWithUser(ctx, userID, email)
			if err != nil {
				return nil, err //nolint:wrapcheck // handler maps any failure to one HTTP error
			}
			ids := make([]string, 0, len(refs))
			for _, ref := range refs {
				ids = append(ids, ref.PromptID)
			}
			return ids, nil
		}
	}
	return deps, true
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
		return buildResourceClaims(user, pr, adminPersona)
	}

	deps := resource.Deps{
		Store:       p.ResourceStore(),
		S3Client:    p.ResourceS3Client(),
		S3Bucket:    p.Config().Resources.Managed.S3Bucket,
		URIScheme:   p.Config().Resources.Managed.URIScheme,
		MaxVersions: p.Config().Resources.Managed.MaxVersions,
		OnCreate:    p.RegisterManagedResource,
		OnDelete:    p.UnregisterManagedResource,
	}
	// Content revision and version history are a capability of the store, not a
	// requirement of it: the Postgres store implements VersionStore, and a store
	// that does not leaves the revision routes answering 503 while metadata CRUD
	// keeps working (#1014).
	if vs, ok := deps.Store.(resource.VersionStore); ok {
		deps.Versions = vs
	}
	// Read audit and usage stats are gated on the audit store existing, which is
	// the same switch that gates audit everywhere else. The writes go through the
	// store directly rather than the platform's async writer: this surface serves
	// a human clicking Download, not an agent's read path, and the audit row is
	// one insert (the same choice the DataHub REST surface makes).
	if store := p.AuditStore(); store != nil {
		deps.Usage = store
		tracker, _ := deps.Store.(resource.ReadTracker) // nil when unsupported
		deps.ReadRecorder = resourceaudit.New(middleware.NewAuditStoreAdapter(store), tracker)
	}

	handler := resource.NewHandler(deps, extractClaims, nil)
	mux.Handle("/api/v1/resources/", handler)
	mux.Handle("/api/v1/resources", handler)
	log.Println("Managed resources API enabled on /api/v1/resources")
}

// mountMentionAPI registers the people and mention routes with the portal's
// authentication middleware. Called from mountPortalAPI; each route registers
// only when the dependency behind it exists.
func mountMentionAPI(mux *http.ServeMux, p *platform.Platform, wrap func(http.Handler) http.Handler, adminRoles []string) {
	deps := mentionhttp.Deps{
		Threads: p.PortalThreadStore(),
		Caller:  mentionIdentityResolver(adminRoles),
	}
	// Assign only a live audience: a typed nil in the interface field would
	// read as wired and panic on the first lookup.
	if aud := mentionAudience(p); aud != nil {
		deps.Audience = aud
	}
	if us := p.UserStore(); us != nil {
		deps.Directory = us
	}
	mentionhttp.New(deps).Register(mux, wrap)
}
