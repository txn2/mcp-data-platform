package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/httpserver/accessgate"
	"github.com/txn2/mcp-data-platform/internal/httpserver/datahubapi"
	"github.com/txn2/mcp-data-platform/internal/httpserver/gatewayhttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/httpauth"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/internal/platform/branding"
	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
	"github.com/txn2/mcp-data-platform/internal/portal/producerapi"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/internal/producedview"
	"github.com/txn2/mcp-data-platform/internal/ui"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/observability/proxy"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/pkcestore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	datahubkit "github.com/txn2/mcp-data-platform/pkg/toolkits/datahub"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// defaultAdminPathPrefix and defaultAdminPersona mirror applyAdminDefaults for
// configs injected directly (via WithConfig) that never ran through
// applyDefaults. Now that admin defaults to enabled, an empty prefix would mount
// the admin API at "/" and collide with the root MCP handler, and an empty
// persona would reject every admin request (buildAdminHandler compares the
// caller's persona against it), locking admins out.
const (
	defaultAdminPathPrefix = "/api/v1/admin"
	defaultAdminPersona    = "admin"
)

// mountAdminAPI registers the admin REST API on the mux if enabled.
func mountAdminAPI(mux *http.ServeMux, p *platform.Platform, notify *notifydelivery.Handle) {
	if p == nil || !p.Config().Admin.IsEnabled() {
		return
	}
	adminHandler := buildAdminHandler(p, notify)
	prefix := p.Config().Admin.PathPrefix
	if prefix == "" {
		prefix = defaultAdminPathPrefix
	}
	mux.Handle(prefix+"/", adminHandler)
	mountPromptVersionAdminAPI(mux, p, prefix)
	mountScriptAdminAPI(mux, p, prefix)
	log.Println("Admin API enabled on", prefix)
}

// portalDisabled returns true when portal is explicitly disabled or platform is nil.
func portalDisabled(p *platform.Platform) bool {
	if p == nil {
		return true
	}
	e := p.Config().Portal.Enabled
	return e != nil && !*e
}

// portalBrandName resolves the platform brand shown in the public viewer
// header (far right): prefer portal.brand_name, then the mcpapps platform-info
// config, then the portal title, then the server name.
//
// Config loading already backfills portal.brand_name from the app config, so
// the second step only matters for a Config assembled in code without the
// loader's defaults.
func portalBrandName(p *platform.Platform) string {
	if name := p.Config().Portal.BrandName; name != "" {
		return name
	}
	if name := mcpappsBrandName(p); name != "" {
		return name
	}
	if title := p.Config().Portal.Title; title != "" {
		return title
	}
	return p.Config().Server.Name
}

// portalRateLimitResolver builds the public viewer's trusted-proxy-aware
// client-IP resolver from the portal rate-limit config (#904). An empty
// trusted-proxy list yields the safe trust-none default (direct peer address;
// X-Forwarded-For ignored). A malformed CIDR is a configuration error wrapped
// for the boot path. It also emits the untrusted-proxy footgun warning.
func portalRateLimitResolver(cfg platform.PortalRateLimitConfig) (*ratelimit.Resolver, error) {
	resolver, err := ratelimit.NewResolver(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("portal rate limiter: %w", err)
	}
	warnOnUntrustedPortalRateLimit(cfg.TrustedProxies)
	return resolver, nil
}

// warnOnUntrustedPortalRateLimit surfaces the portal rate-limiting footgun at
// boot, mirroring the OAuth endpoints: with no trusted proxies configured,
// client attribution falls back to the direct peer address. Behind a reverse
// proxy or k8s ingress every client shares the proxy's IP, so the per-client
// limit collapses onto one bucket. The global backstop still bounds total
// load; the operator should set portal.rate_limit.trusted_proxies to restore
// per-client fairness.
func warnOnUntrustedPortalRateLimit(trustedProxies []string) {
	if len(trustedProxies) == 0 {
		log.Println("Portal rate limiting is on but portal.rate_limit.trusted_proxies is empty: " +
			"behind a reverse proxy or ingress every client shares the proxy IP, so per-client " +
			"limiting collapses to a single bucket. Set portal.rate_limit.trusted_proxies to your " +
			"proxy/ingress CIDRs (the global backstop still bounds total load meanwhile).")
	}
}

// compositeOperationResolver fans an operationId lookup across every
// registered apigateway toolkit (a multi-instance deployment splits
// connections across toolkits). The first non-empty result wins; an
// empty result means no toolkit owns the connection or no spec path
// matched, which the metrics middleware maps to "unknown".
type compositeOperationResolver []*apigatewaykit.Toolkit

// ResolveOperationID returns the first non-empty operationId any registered
// apigateway toolkit resolves for the (connection, method, path) tuple, or
// empty when none owns the connection or matches a spec path.
func (c compositeOperationResolver) ResolveOperationID(ctx context.Context, connection, method, path string) string {
	for _, tk := range c {
		if op := tk.ResolveOperationID(ctx, connection, method, path); op != "" {
			return op
		}
	}
	return ""
}

// HasConnection reports whether any registered apigateway toolkit owns the
// named connection.
func (c compositeOperationResolver) HasConnection(connection string) bool {
	for _, tk := range c {
		if tk.HasConnection(connection) {
			return true
		}
	}
	return false
}

// mountGatewayAPI registers the REST shim for the apigateway toolkit
// on the mux. The shim is only mounted when at least one apigateway
// toolkit instance is loaded; otherwise the route would always return
// "connection not found" and add noise to the route table.
//
// Auth wrapping mirrors the MCP root handler: when requireAuth is on,
// the handler is wrapped with httpauth.RequireAuth so that requests
// without a credential are rejected at the HTTP layer before reaching
// the in-memory MCP session. When auth is off, the wrapper is a no-op
// and the request flows through anonymously (matching the rest of the
// platform's behavior in that mode).
func mountGatewayAPI(mux *http.ServeMux, mcpServer *mcp.Server, p *platform.Platform, requireAuth bool) {
	if mcpServer == nil || p == nil {
		return
	}
	apiToolkits := p.ToolkitRegistry().GetByKind(apigatewaykit.Kind)
	if len(apiToolkits) == 0 {
		return
	}

	resolver := make(compositeOperationResolver, 0, len(apiToolkits))
	for _, tk := range apiToolkits {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			resolver = append(resolver, api)
		}
	}

	handler, err := gatewayhttp.NewHandler(gatewayhttp.Deps{
		MCPServer:   mcpServer,
		Metrics:     p.Metrics(),
		Resolver:    resolver,
		Identity:    p.NewGatewayIdentityResolver(),
		RawMaxBytes: p.APIGatewayRawMaxBytes(),
	})
	if err != nil {
		log.Printf("REST gateway disabled: %v", err)
		return
	}

	wrapped := handler
	if requireAuth {
		wrapped = httpauth.RequireAuth()(handler)
	}
	mux.Handle("/api/v1/gateway/", wrapped)
	log.Println("REST gateway enabled on /api/v1/gateway/{connection}/invoke")
}

// defaultPrometheusURL is the auto-discovered in-cluster Prometheus endpoint
// used when observability.prometheus.url is not configured. Set that config
// value only to point at a Prometheus deployed under a different name.
const defaultPrometheusURL = "http://mcp-data-platform-prometheus:9090"

// mountObservabilityProxy mounts the authenticated PromQL query proxy at
// /api/v1/observability/. It is always mounted (gated behind auth +
// the observability:read persona capability); when Prometheus is not
// configured its endpoints return 503 so the portal renders a clean
// empty state.
func mountObservabilityProxy(mux *http.ServeMux, p *platform.Platform, requireAuth bool) {
	if p == nil {
		return
	}
	pc := p.Config().Observability.Prometheus
	if pc.URL == "" {
		// Auto-discover the default in-cluster Prometheus; the config only
		// needs to override this to point at a non-default deployment.
		pc.URL = defaultPrometheusURL
	}
	handler, err := proxy.New(proxy.Config{
		URL:                pc.URL,
		Timeout:            pc.Timeout,
		BasicAuthUser:      pc.BasicAuth.Username,
		BasicAuthPass:      pc.BasicAuth.Password,
		RateLimitPerSecond: pc.RateLimitPerSecond,
	}, p.NewObservabilityAuthorizer())
	if err != nil {
		log.Printf("observability proxy disabled: %v", err)
		return
	}

	proxyMux := http.NewServeMux()
	handler.Register(proxyMux)

	var wrapped http.Handler = proxyMux
	if requireAuth {
		// The portal SPA calls these endpoints directly with its
		// browser-session cookie, so accept that (like the admin and
		// portal APIs) in addition to Bearer/API-key tokens. The proxy's
		// authorizer enforces authentication (401) and the
		// observability:read capability (403); OptionalAuth only lifts a
		// present token onto the context without rejecting cookie auth.
		wrapped = p.ObservabilityAuthMiddleware()(httpauth.OptionalAuth()(proxyMux))
	}
	mux.Handle("/api/v1/observability/", wrapped)
	if pc.URL == "" {
		log.Println("observability proxy mounted (Prometheus not configured; endpoints return 503)")
	} else {
		log.Println("observability proxy enabled on /api/v1/observability/{query,query_range}")
	}
}

// buildResourceClaims creates resource Claims from an authenticated user,
// resolving persona memberships and admin status from the persona registry.
//
// It returns resource.ErrForbidden when the user belongs to no persona. The
// managed-resources API authenticates through the portal authenticator but not
// through the portal handler, so this is where it applies the persona gate the
// rest of the portal gets from accessgate; without it an account no persona
// claims would still read every resource whose visibility is not
// persona-restricted.
func buildResourceClaims(user *portal.User, pr *persona.Registry, adminPersona string) (*resource.Claims, error) {
	claims := &resource.Claims{
		Sub:   user.UserID,
		Email: user.Email,
		Roles: user.Roles,
	}
	if pr != nil {
		for _, per := range pr.All() {
			if matchesAnyRole(per.Roles, user.Roles) {
				claims.Personas = append(claims.Personas, per.Name)
				if per.Name == adminPersona {
					claims.IsAdmin = true
				}
			}
		}
	}
	if len(claims.Personas) == 0 {
		return nil, resource.ErrForbidden
	}
	claims.AdminOfPersonas = extractPersonaAdminRoles(user.Roles)
	return claims, nil
}

// personaAdminInfix is the role substring that marks a persona-admin grant.
// Roles may carry an arbitrary prefix (e.g., "dp_persona-admin:finance").
const personaAdminInfix = "persona-admin:"

// extractPersonaAdminRoles extracts persona names from roles containing
// the "persona-admin:" pattern, tolerating any prefix.
func extractPersonaAdminRoles(roles []string) []string {
	var out []string
	for _, r := range roles {
		if _, name, ok := strings.Cut(r, personaAdminInfix); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}

// matchesAnyRole checks if any persona role matches any user role.
func matchesAnyRole(personaRoles, userRoles []string) bool {
	for _, pr := range personaRoles {
		if slices.Contains(userRoles, pr) {
			return true
		}
	}
	return false
}

// mcpappsBrandName extracts brand_name from the mcpapps platform-info config,
// or returns empty string if not configured.
func mcpappsBrandName(p *platform.Platform) string {
	appCfg, ok := p.Config().MCPApps.Apps["platform-info"]
	if !ok {
		return ""
	}
	name, _ := appCfg.Config["brand_name"].(string)
	return name
}

// wirePortalOptionalDeps populates optional portal dependencies (audit, knowledge, persona).
func wirePortalOptionalDeps(deps *portal.Deps, p *platform.Platform) {
	if p.Audit().Store() != nil {
		deps.AuditMetrics = p.Audit().Store()
	}
	// A session is rolled up out of the audit log and joined to what it
	// produced, so the read model needs the database rather than the audit
	// store. buildAdminHandler builds the same store over the same handle for
	// the operator surface; the two differ only in the scope each read carries.
	if db := p.DB(); db != nil {
		deps.SessionViewer = sessionview.NewPostgresStore(db)
	}
	// The call catalog and the promotion path are the same two objects the
	// operator surface takes; what differs is the scope each read carries and
	// who the action is attributed to.
	deps.CallCatalog, deps.CallPromoter = callCatalog(p)
	if p.KnowledgeInsightStore() != nil {
		deps.InsightStore = p.KnowledgeInsightStore()
	}
	if p.KnowledgeChangesetStore() != nil {
		deps.ChangesetReader = p.KnowledgeChangesetStore()
	}
	if p.MemoryStore() != nil {
		deps.MemoryStore = p.MemoryStore()
		deps.MemoryWriter = p.MemoryStore()
	}
	if ep := p.EmbeddingProvider(); ep != nil {
		deps.EmbeddingProvider = ep
	}
	if aud := mentionAudience(p); aud != nil {
		deps.MentionResolver = mention.NewService(aud)
	}
	if pr := p.PersonaRegistry(); pr != nil {
		tr := p.ToolkitRegistry()
		deps.PersonaResolver = buildPersonaResolver(pr, tr)
	}
	if router := p.KnowledgeRouter(); router != nil {
		deps.SearchRouter = portalSearchAdapter{router: router}
	}
	if bridge := buildDataHubBridge(p); bridge != nil {
		deps.DataHubRegistrar = dataHubRegistrar(p, bridge, deps.PersonaResolver, deps.AdminRoles)
		// The labeler shares the bridge with the REST surface: naming a governance
		// entity a knowledge page cites is the same read over the same
		// connections (#1159).
		deps.CatalogLabeler = datahubapi.NewLabeler(bridge)
	}
}

// buildDataHubBridge assembles read/write access to the live DataHub toolkit
// connections, or nil when none is registered. It reuses each toolkit's client
// for both the semantic read adapter and the batched write client; a read-only
// connection (read_only=true) exposes no writer.
//
// The bridge is built once and shared by everything the portal serves over
// DataHub — the Catalog REST surface (#718) and the knowledge-page catalog
// labeler (#1159) — so a connection is never opened twice for the same server.
func buildDataHubBridge(p *platform.Platform) datahubapi.Bridge {
	if p.ToolkitRegistry() == nil {
		return nil
	}
	urn := p.Config().Semantic.URNMapping
	platformName := urn.Platform
	if platformName == "" {
		platformName = "trino"
	}

	bridge := datahubapi.NewStaticBridge()
	for _, tk := range p.ToolkitRegistry().All() {
		dhTk, ok := tk.(*datahubkit.Toolkit)
		if !ok || dhTk.Client() == nil {
			continue
		}
		reader, writer, err := datahubapi.BuildConnection(dhTk.Client(), platformName, urn.CatalogMapping, dhTk.Config().ReadOnly)
		if err != nil {
			log.Printf("portal datahub: skipping connection %q: %v", dhTk.Name(), err)
			continue
		}
		bridge.Add(dhTk.Name(), reader, writer)
	}
	if bridge.Empty() {
		return nil
	}
	return bridge
}

// dataHubRegistrar returns the route registrar for the portal DataHub REST
// handler (#718) over an assembled bridge.
func dataHubRegistrar(p *platform.Platform, bridge datahubapi.Bridge, resolver portal.PersonaResolver, adminRoles []string) func(*http.ServeMux) {
	var auditLogger audit.Logger
	if store := p.Audit().Store(); store != nil {
		auditLogger = store
	}
	handler := datahubapi.NewHandler(datahubapi.Deps{
		Bridge:          bridge,
		PersonaResolver: resolver,
		AdminRoles:      adminRoles,
		Audit:           auditLogger,
	})
	return handler.Register
}

// portalSearchAdapter bridges the portal's GET /search endpoint to the unified
// knowledge router. It lives here (not pkg/portal) because pkg/knowledge
// already imports pkg/portal for the asset and knowledge-page stores; the
// adapter maps the portal-local search types to and from the knowledge types so
// neither package needs to import the other.
type portalSearchAdapter struct {
	router *knowledge.Router
}

// Search forwards a portal search to the knowledge router, mapping the request
// and response across the two type sets. No ranking or scope logic is added: the
// router owns identity scoping, fusion, and allocation.
func (a portalSearchAdapter) Search(ctx context.Context, q portal.SearchQuery) (portal.SearchResult, error) {
	res, err := a.router.Search(ctx, knowledge.Query{
		Intent:     q.Intent,
		EntityURNs: q.EntityURNs,
		Status:     q.Status,
		Sources:    q.Sources,
		Caller:     knowledge.Caller{UserID: q.Caller.UserID, Email: q.Caller.Email, Persona: q.Caller.Persona},
		Limit:      q.Limit,
	})
	if err != nil {
		return portal.SearchResult{}, fmt.Errorf("knowledge search: %w", err)
	}
	out := portal.SearchResult{Ranking: res.Ranking, UnknownSources: res.UnknownSources}
	for _, g := range res.Groups {
		hits := make([]portal.SearchHit, 0, len(g.Hits))
		for _, hit := range g.Hits {
			hits = append(hits, portal.SearchHit{
				Text:       hit.Text,
				Source:     hit.Source,
				Ref:        hit.Ref,
				Score:      hit.Score,
				Status:     hit.Status,
				EntityURNs: hit.EntityURNs,
				Dimension:  hit.Dimension,
			})
		}
		out.Groups = append(out.Groups, portal.SearchGroup{Source: g.Source, Hits: hits})
	}
	for _, c := range res.Coverage {
		out.Coverage = append(out.Coverage, portal.SearchCoverage{
			Source: c.Source, Matched: c.Matched, Shown: c.Shown,
			MatchedCapped: c.MatchedCapped, Withheld: c.Withheld,
		})
	}
	// The notice is rendered here, from the knowledge package, so the portal and the
	// MCP search tool explain a persona-withheld result with identical copy.
	out.WithheldNotice = knowledge.WithheldNotice(res.Coverage, q.Caller.Persona)
	return out, nil
}

// buildPersonaResolver creates a portal.PersonaResolver from the persona and toolkit registries.
func buildPersonaResolver(pr *persona.Registry, tr *registry.Registry) portal.PersonaResolver {
	return func(roles []string) *portal.PersonaInfo {
		per, ok := pr.GetForRoles(roles)
		if !ok || per == nil {
			return nil
		}
		info := &portal.PersonaInfo{Name: per.Name}
		if tr != nil {
			filter := persona.NewToolFilter(pr)
			info.Tools = filter.FilterTools(per, tr.AllTools())
		}
		return info
	}
}

// scriptConnectionEnumerator resolves the connections one portal caller may
// reach, which a connection-typed script parameter is chosen from (#1361).
//
// The enumeration itself is connreach's, which is also what an automatic
// approval checks a derived grant against (#1367), so the values a form offers
// and the values a run is allowed to name are one set. An administrator is
// enumerated unrestricted, which is what the admin surface already gives them
// everywhere else.
//
// A deployment that cannot enumerate its connections yields a nil enumerator,
// which leaves the choices route unmounted rather than serving an empty set a
// form would render as "this script may reach nothing".
func scriptConnectionEnumerator(lister *connreach.Lister) scripthttp.ConnectionEnumerator {
	if lister == nil {
		return nil
	}
	return func(ctx context.Context, caller scripthttp.ConnectionScope) []scripthttp.ConnectionChoice {
		conns := lister.ForPersona(ctx, caller.Persona, caller.Unrestricted)
		choices := make([]scripthttp.ConnectionChoice, 0, len(conns))
		for _, c := range conns {
			choices = append(choices, scripthttp.ConnectionChoice{
				Name: c.Name, Kind: c.Kind, Description: c.Description,
			})
		}
		return choices
	}
}

// portalAuthChain composes the portal's authentication and persona
// authorization into the single middleware every authenticated portal route is
// wrapped with. The gate is inner by necessity: it reads the user that
// RequirePortalAuth puts on the context.
func portalAuthChain(auth *portal.Authenticator, gate *accessgate.Gate) func(http.Handler) http.Handler {
	authenticate := portal.RequirePortalAuth(auth)
	return func(next http.Handler) http.Handler {
		return authenticate(gate.Require(next))
	}
}

// portalAccessGate builds the persona gate with the portal's own branding, so a
// refusal looks like the product rather than a bare server error. A nil
// resolver yields a gate that denies everyone, which is the correct reading of
// "access cannot be evaluated".
func portalAccessGate(p *platform.Platform, resolver portal.PersonaResolver) *accessgate.Gate {
	return accessgate.New(accessgate.PersonaResolver(resolver), accessgate.Brand{
		Name:                portalBrandName(p),
		LogoHTML:            p.BrandLogoHTML(),
		URL:                 p.BrandURL(),
		ImplementorName:     p.Config().Portal.Implementor.Name,
		ImplementorLogoHTML: p.ImplementorLogoHTML(),
		ImplementorURL:      p.Config().Portal.Implementor.URL,
		ImageSources:        branding.ImageSources(p.Config().Portal.Logo, p.Config().Portal.Implementor.Logo),
	})
}

// mountPortalUI registers the unified portal SPA frontend on the mux when the
// portal UI config gate is enabled and assets are available.
func mountPortalUI(mux *http.ServeMux, p *platform.Platform, assetsAvailable bool) {
	if p == nil || portalDisabled(p) || !assetsAvailable {
		return
	}
	var spa http.Handler
	spa = http.StripPrefix("/portal", ui.Handler())
	if gated := gateSPAShell(p, spa); gated != nil {
		spa = gated
	}
	mux.Handle("/portal/", spa)
	log.Println("Portal UI enabled on /portal/")
}

// shellPersonaResolver builds the resolver the portal shell's gate is judged
// with. It mirrors the sibling persona wiring's nil guard (buildPersonaResolver
// would dereference a nil registry per request). A nil resolver leaves a gate
// that admits nobody, which is the same verdict the API layer reaches when
// access cannot be evaluated.
func shellPersonaResolver(p *platform.Platform) portal.PersonaResolver {
	if pr := p.PersonaRegistry(); pr != nil {
		return buildPersonaResolver(pr, p.ToolkitRegistry())
	}
	return nil
}

// portalAppAdmitter answers, for a set of roles, whether the portal application
// would actually serve that caller: the SPA is mounted in this build and the
// shell gate in front of it admits them.
//
// The share viewer asks before redirecting a signed-in reader into the portal
// (#1473). The two surfaces are mounted independently — /portal/view/ wherever
// the portal API is, /portal/ only when the binary carries a frontend build —
// so a redirect made without asking would send a reader of a working share page
// to a route this build does not serve. It returns nil when no portal
// application is served at all, which is the answer that keeps them where they
// are.
//
// It is built from the same two facts gateSPAShell judges a caller by, through
// the same resolver, so the redirect and the page it lands on cannot disagree.
func portalAppAdmitter(p *platform.Platform, uiAvailable bool) func([]string) bool {
	if p == nil || portalDisabled(p) || !uiAvailable {
		return nil
	}
	if p.BrowserSessionAuth() == nil {
		return func([]string) bool { return true } // shell left unwrapped: everyone reaches it
	}
	gate := portalAccessGate(p, shellPersonaResolver(p))
	return gate.Allows
}

// gateSPAShell wraps the portal SPA so a signed-in caller with no persona is
// answered with the branded refusal instead of an application shell whose every
// request will 403. It returns nil when there is no cookie authenticator to
// identify the caller with, leaving the shell unwrapped.
//
// Only a caller who already holds a valid session cookie is judged here.
// Anyone else falls through to the SPA, which is what makes the sign-in flow
// still work: an unauthenticated visitor must reach the shell to be sent to the
// identity provider. The shell is static markup and grants nothing on its own;
// the data behind it is gated by portalAuthChain regardless of this wrapper.
func gateSPAShell(p *platform.Platform, spa http.Handler) http.Handler {
	browserAuth := p.BrowserSessionAuth()
	if browserAuth == nil {
		return nil
	}
	gate := portalAccessGate(p, shellPersonaResolver(p))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, err := browserAuth.AuthenticateHTTP(r)
		if err != nil || info == nil || gate.Allows(info.Roles) {
			spa.ServeHTTP(w, r)
			return
		}
		gate.Deny(w, r, info.Email)
	})
}

// buildAdminAuth constructs the admin persona-gate middleware. Shared by the
// admin API handler and the separately mounted prompt-version routes so both
// enforce identical authentication, persona, and CSRF rules.
func buildAdminAuth(p *platform.Platform) func(http.Handler) http.Handler {
	var authOpts []admin.PlatformAuthOption
	if p.BrowserSessionAuth() != nil {
		authOpts = append(authOpts, admin.WithBrowserSessionAuth(p.BrowserSessionAuth()))
	}
	adminPersona := p.Config().Admin.Persona
	if adminPersona == "" {
		adminPersona = defaultAdminPersona
	}
	return admin.RequirePersona(admin.NewPlatformAuthenticator(
		p.Authenticator(),
		adminPersona,
		p.PersonaRegistry(),
		authOpts...,
	))
}

// buildAdminHandler constructs the admin REST API handler from the platform.
func buildAdminHandler(p *platform.Platform, notify *notifydelivery.Handle) http.Handler {
	deps := admin.Deps{
		Config:            p.Config(),
		ConfigStore:       p.ConfigStore(),
		FileDefaults:      p.FileDefaults(),
		PersonaRegistry:   p.PersonaRegistry(),
		ToolkitRegistry:   p.ToolkitRegistry(),
		ReloadNotifier:    p,
		MCPServer:         p.MCPServer(),
		BrowserAuth:       p.BrowserSessionAuth(),
		DatabaseAvailable: p.Config().Database.DSN != "",
		PlatformTools:     p.PlatformTools(),
		AssetStore:        p.PortalAssetStore(),
		ShareStore:        p.PortalShareStore(),
		VersionStore:      p.PortalVersionStore(),
		CollectionStore:   p.PortalCollectionStore(),
		S3Client:          p.PortalS3Client(),
		S3Bucket:          p.Config().Portal.S3Bucket,
		// The admin console reads asset content through its own routes, so it
		// rewrites an asset's resource references the same way the portal does
		// (#1474): an administrator opening an asset sees what its owner sees.
		ContentRefs:   p.PortalContentRefStore(),
		PublicBaseURL: p.Config().Portal.PublicBaseURL,
		// An administrator's content write moves the tables that follow the
		// asset, exactly as the owner's does (#1536).
		OnAssetRevised:     tableSourceHooks(p).AssetRevised,
		ConnectionStore:    p.ConnectionStore(),
		ConnectionSources:  p.ConnectionSources(),
		EnrichmentStore:    p.EnrichmentStore(),
		ToolkitsConfig:     p.Config().Toolkits,
		PersonaStore:       p.PersonaStore(),
		APIKeyStore:        p.APIKeyStore(),
		UserStore:          p.UserStore(),
		PromptStore:        p.PromptStore(),
		PromptRegistrar:    p,
		PromptInfoProvider: p,
		FilePersonaNames:   p.FilePersonaNames(),
	}

	if p.Audit().Store() != nil {
		deps.AuditQuerier = p.Audit().Store()
		deps.AuditMetricsQuerier = p.Audit().Store()
	}

	// Sessions are read off the audit log, so they need the database rather
	// than the audit store: the read model aggregates audit_logs and joins
	// what a session produced (assets, insights) and the live session row's
	// persona. No database, no history to derive a session from, no routes.
	if db := p.DB(); db != nil {
		deps.SessionViewer = sessionview.NewPostgresStore(db)
	}
	deps.CallCatalog, deps.CallPromoter = callCatalog(p)

	// Note: WireGatewayTokenStore and WireGatewayBroadcaster run earlier
	// in the caller so they apply even when admin is disabled.
	if engine := wireEnrichmentEngine(p); engine != nil {
		deps.EnrichmentEngine = engine
	}

	// PKCE state: DB-backed (multi-replica safe) when a database is
	// configured. otherwise an in-memory store with a background GC
	// goroutine is wired here — single-replica only.
	if db := p.DB(); db != nil {
		deps.PKCEStore = pkcestore.NewPostgresStore(db, p.RestEncryptor())
	} else {
		deps.PKCEStore = pkcestore.NewMemoryStore()
	}

	// Wire the unified OAuth flow: shared connoauth.Store plus one
	// OAuthKindHandler per connection kind. When both are present the
	// admin handler activates the unified /connections/{kind}/{name}
	// routes; otherwise it falls back to the legacy per-kind routes
	// for backward compatibility during rollout.
	deps.ConnOAuthStore = p.ConnOAuthStore()
	deps.OAuthKinds = buildOAuthKindHandlers(p)
	deps.AuthEvents = p.AuthEventWriter()
	deps.AuthEventStore = p.AuthEventStore()
	deps.Embedder = p.EmbeddingProvider()
	wireAdminIndexDeps(&deps, p)

	if p.KnowledgeInsightStore() != nil {
		deps.Knowledge = admin.NewKnowledgeHandler(
			p.KnowledgeInsightStore(),
			p.KnowledgeChangesetStore(),
			p.KnowledgeDataHubWriter(),
			p.PortalKnowledgePageStore(),
			p.QueryProvider(),
		)
	}

	if p.APIKeyAuthenticator() != nil {
		deps.APIKeyManager = p.APIKeyAuthenticator()
	}

	// Email notification settings and monitoring surfaces (nil-safe: absent
	// without a DB, which also leaves the monitoring routes unregistered).
	deps.NotificationSettings = notify.Settings()
	deps.SendTestEmail = notify.SendTest
	deps.NotificationPrefs = notify.Prefs()
	deps.NotificationHistory = notify.History()
	deps.NotificationRetention = notifydelivery.HistoryRetention
	deps.ReviewQueueAlert = reviewAlertSettings(p, reviewalert.KnowledgeTarget())

	return admin.NewHandler(deps, buildAdminAuth(p))
}

// callCatalog returns the catalog of recorded data-access calls and the path
// that publishes one, or nils when this deployment keeps no catalog.
//
// The promoter is built here rather than inside the platform because what a
// promoted record becomes lives outside the audit layer: a query becomes a
// DataHub Query entity through the knowledge toolkit's writer, and an API call
// becomes an example on its endpoint in the API catalog. Each writer is passed
// only when it reaches something real: a noop DataHub writer would report a
// promotion that persisted nothing (#1321).
func callCatalog(p *platform.Platform) (callrecord.Store, *callrecord.Promoter) {
	calls := p.Audit().Calls()
	if calls == nil {
		return nil, nil
	}
	var queries callrecord.CuratedQueryWriter
	if w := p.KnowledgeDataHubWriter(); knowledgekit.DataHubWritable(w) {
		queries = w
	}
	var examples callrecord.ExampleWriter
	// The API catalog store answers endpoint examples when it is
	// database-backed, which is the same condition the catalog itself has.
	if store, ok := p.APIGatewayCatalogStore().(catalog.ExampleStore); ok {
		examples = exampleWriter{store: store}
	}
	if queries == nil && examples == nil {
		// Nothing to promote to. The catalog is still served: a record is
		// worth reading whether or not this deployment can publish it.
		return calls, nil
	}
	return calls, callrecord.NewPromoter(calls, queries, examples)
}

// exampleWriter adapts the API catalog's example store to the promotion path's
// narrower contract, which is stated in the catalog's own terms rather than the
// gateway's.
type exampleWriter struct {
	store catalog.ExampleStore
}

// SaveExample stores a promoted API call as an example on its endpoint.
func (e exampleWriter) SaveExample(ctx context.Context, ex callrecord.Example) (string, error) {
	id, err := e.store.SaveExample(ctx, catalog.Example{
		Connection:   ex.Connection,
		OperationID:  ex.OperationID,
		Method:       ex.Method,
		Path:         ex.Path,
		Name:         ex.Name,
		Description:  ex.Description,
		CallRecordID: ex.CallRecordID,
		CreatedBy:    ex.CreatedBy,
	})
	if err != nil {
		return "", fmt.Errorf("saving endpoint example: %w", err)
	}
	return id, nil
}

// wireAdminIndexDeps attaches the api-gateway catalog store, embed-job queue,
// and index-jobs reporter to the admin deps when each is available. Extracted
// from buildAdminHandler to keep its cyclomatic complexity within budget.
func wireAdminIndexDeps(deps *admin.Deps, p *platform.Platform) {
	if catStore := p.APIGatewayCatalogStore(); catStore != nil {
		deps.APICatalogStore = catStore
	}
	if jobs := p.APIGatewayEmbedJobsStore(); jobs != nil {
		deps.EmbedJobs = jobs
	}
	if reporter := p.IndexJobsReporter(); reporter != nil {
		deps.IndexJobs = reporter
	}
}

// mountBrowserAuth registers the OIDC login/callback/logout routes.
func mountBrowserAuth(mux *http.ServeMux, p *platform.Platform) {
	if p == nil || p.BrowserSessionFlow() == nil {
		return
	}
	flow := p.BrowserSessionFlow()
	mux.HandleFunc("/portal/auth/login", flow.LoginHandler)
	mux.HandleFunc("/portal/auth/callback", flow.CallbackHandler)
	mux.HandleFunc("/portal/auth/logout", flow.LogoutHandler)
	log.Println("Browser auth enabled (OIDC login on /portal/auth/login)")
}

// browserRedirectMiddleware redirects browser requests to the portal.
// Non-browser requests (MCP clients) pass through to the MCP handler.
func browserRedirectMiddleware(mcpHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/portal/", http.StatusTemporaryRedirect)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
}

// registerOAuthRoutes registers OAuth endpoints on the given mux.
// Supports both standard paths (with /oauth prefix) and Claude Desktop
// compatible paths (without /oauth prefix).
func registerOAuthRoutes(mux *http.ServeMux, handler http.Handler) {
	// Standard paths (with /oauth prefix)
	mux.Handle("/.well-known/oauth-authorization-server", handler)
	mux.Handle("/oauth/authorize", handler)
	mux.Handle("/oauth/callback", handler)
	mux.Handle("/oauth/token", handler)
	mux.Handle("/oauth/register", handler)
	// Claude Desktop compatibility paths (without /oauth prefix)
	mux.Handle("/authorize", handler)
	mux.Handle("/callback", handler)
	mux.Handle("/token", handler)
	mux.Handle("/register", handler)
}

// portalScriptNames resolves a script producer to the name it carries now, or
// nil on a deployment with no scripts -- which the producer routes read as "no
// lookup" and report every producer as still existing, rather than as "no
// script exists" and report every one of them as deleted.
func portalScriptNames(db *sql.DB) producerapi.ScriptNames {
	if db == nil {
		return nil
	}
	return producedview.New(producedby.NewPostgres(db), nil, nil, nil, scriptstore.New(db))
}
