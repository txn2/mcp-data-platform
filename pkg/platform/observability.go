package platform

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/observability/proxy"
)

// observabilityReadCapability is the persona capability that grants
// access to the PromQL query proxy. It is checked through the same
// persona tool-allow filter that gates tools (see addToolVisibility
// Middleware), so operators grant it by adding it to a persona's
// allowed tools in the portal persona editor. Default-deny: a persona
// must explicitly allow it (or match it via a wildcard).
const observabilityReadCapability = "observability:read"

// observabilityAuthorizer adapts the platform's authenticator and
// authorizer to the proxy.Authorizer interface, keeping the proxy
// package free of auth/persona imports. It resolves the caller (browser
// session cookie or request token), then checks the observability:read
// capability. The persona
// name comes from the authorizer's own resolution (IsAuthorized returns
// it), NOT a separate registry lookup, so the audit persona and the
// rate-limit key stay consistent with the authorization decision even
// when OIDC PersonaMapping reaches a persona whose own roles list does
// not include the mapped role.
type observabilityAuthorizer struct {
	authn middleware.Authenticator
	authz middleware.Authorizer
}

// Authorize implements proxy.Authorizer.
//
// Identity is resolved preferring a pre-authenticated user placed on the
// context by the browser-session HTTP middleware (the portal SPA calls the
// proxy endpoints directly with its session cookie), then falling back to
// token-based authentication for programmatic Bearer/API-key callers. This
// mirrors Platform.resolveUserInfo; without the cookie path, cookie-only
// portal requests resolve no identity and the proxy returns 401, bouncing
// admins to the login page.
func (a observabilityAuthorizer) Authorize(ctx context.Context) proxy.Decision {
	info := middleware.GetPreAuthenticatedUser(ctx)
	if info == nil {
		if a.authn == nil {
			return proxy.Decision{}
		}
		var err error
		info, err = a.authn.Authenticate(ctx)
		if err != nil || info == nil {
			return proxy.Decision{}
		}
	}
	dec := proxy.Decision{Authenticated: true, UserID: info.UserID, Email: info.Email}
	if a.authz != nil {
		dec.Allowed, dec.Persona, _ = a.authz.IsAuthorized(ctx, "", info.Roles, observabilityReadCapability, "")
	}
	return dec
}

// NewObservabilityAuthorizer returns the proxy.Authorizer backed by the
// platform's auth stack. Used by cmd to build the PromQL proxy handler.
func (p *Platform) NewObservabilityAuthorizer() proxy.Authorizer {
	return observabilityAuthorizer{
		authn: p.authenticator,
		authz: p.authorizer,
	}
}

// browserCookieResolver resolves a portal session cookie to a user.
// Satisfied by *browsersession.Authenticator; kept as an interface so the
// middleware is testable without minting a signed session cookie.
type browserCookieResolver interface {
	AuthenticateHTTP(r *http.Request) (*middleware.UserInfo, error)
}

// observabilityBrowserAuth lifts a resolved browser-session user onto the
// request context as a pre-authenticated user. A nil resolver or an
// absent/invalid cookie leaves the context unchanged: it never rejects, so
// the token-extraction middleware and the proxy's own authorizer still
// handle the token path and 401/403 enforcement.
func observabilityBrowserAuth(ba browserCookieResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if ba != nil {
				if info, err := ba.AuthenticateHTTP(r); err == nil && info != nil {
					ctx = middleware.WithPreAuthenticatedUser(ctx, info)
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ObservabilityAuthMiddleware resolves the portal browser-session cookie
// into a pre-authenticated user so the PromQL proxy (which the portal SPA
// calls directly) accepts cookie auth in addition to Bearer/API-key tokens.
// Without it, cookie-only portal requests carry no credentials the proxy can
// read and get bounced to the login page.
func (p *Platform) ObservabilityAuthMiddleware() func(http.Handler) http.Handler {
	ba := p.BrowserSessionAuth()
	if ba == nil {
		// Avoid the typed-nil interface footgun: a nil *Authenticator
		// wrapped in the interface would be non-nil.
		return observabilityBrowserAuth(nil)
	}
	return observabilityBrowserAuth(ba)
}

// initObservability constructs the metrics recorder and (when
// enabled) the matching HTTP listener. Configuration is read from
// the environment so the platform can boot with no YAML changes
// when an operator flips OTEL_METRICS_ENABLED on.
//
// When metrics are disabled the recorder and listener are nil, every
// downstream consumer is nil-safe, and the platform behaves exactly
// as before this change.
func (p *Platform) initObservability() error {
	cfg := observability.ConfigFromEnv()
	m, err := observability.New(cfg)
	if err != nil {
		return fmt.Errorf("observability: %w", err)
	}
	p.metrics = m
	p.metricsListener = observability.NewListener(m)
	if m != nil {
		slog.Info("observability: metrics recorder enabled", "listen", cfg.ListenAddr)
	}
	return nil
}

// Metrics exposes the platform's observability recorder. Returns nil
// when metrics are disabled; the type is nil-safe so callers can
// record unconditionally.
func (p *Platform) Metrics() *observability.Metrics { return p.metrics }

// StartMetricsListener starts the /metrics HTTP listener if metrics
// are enabled. Safe to call when disabled (returns nil immediately).
func (p *Platform) StartMetricsListener(ctx context.Context) error {
	if err := p.metricsListener.Start(ctx); err != nil {
		return fmt.Errorf("starting metrics listener: %w", err)
	}
	return nil
}

// ShutdownMetricsListener stops the /metrics listener and flushes
// the meter provider. Both calls are nil-safe.
func (p *Platform) ShutdownMetricsListener(ctx context.Context) error {
	if err := p.metricsListener.Shutdown(ctx); err != nil {
		return fmt.Errorf("metrics listener shutdown: %w", err)
	}
	if err := p.metrics.Shutdown(ctx); err != nil {
		return fmt.Errorf("metrics provider shutdown: %w", err)
	}
	return nil
}

// WireAPIGatewayMetrics pushes the platform's metrics recorder into
// every registered apigateway toolkit. Intended to run once at
// startup, before any MCP/HTTP listener starts accepting requests.
//
// Idempotent against the same recorder: Toolkit.SetMetrics uses
// instrumentClient, which skips connections already wrapped for the
// same (connection, metrics) pair so a second call does not produce
// nested transports (and therefore double-recorded observations).
//
// No-op when metrics are disabled or when no apigateway toolkit is
// loaded. Connections added to a toolkit BEFORE this call still get
// instrumented because Toolkit.SetMetrics walks the existing
// connection map.
func (p *Platform) WireAPIGatewayMetrics() {
	if !p.metrics.Enabled() {
		return
	}
	for _, tk := range p.toolkitRegistry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			api.SetMetrics(p.metrics)
		}
	}
}

// metricsAware is implemented by toolkits that record their own metrics via a
// SetMetrics injector (S3, apigateway, ...).
type metricsAware interface {
	SetMetrics(*observability.Metrics)
}

// WireToolkitMetrics pushes the recorder into every registered toolkit that
// implements SetMetrics. It MUST run before the registry registers tool
// handlers: the S3 toolkit installs an mcp-s3 middleware in SetMetrics that is
// only effective if present at registration time. apigateway also implements
// SetMetrics; wiring it here as well is idempotent (see WireAPIGatewayMetrics).
// No-op when metrics are disabled.
func (p *Platform) WireToolkitMetrics() {
	if !p.metrics.Enabled() {
		return
	}
	for _, tk := range p.toolkitRegistry.All() {
		if ma, ok := tk.(metricsAware); ok {
			ma.SetMetrics(p.metrics)
		}
	}
}

// GatewayIdentityResolver resolves an inbound REST request's auth
// context to a display identity for the inbound metric's identity
// label. It reuses the platform's existing authenticator rather than
// forking auth. Returns "unknown" when nothing resolves so the label
// is always bounded. Structurally satisfies gatewayhttp.IdentityResolver
// (kept concrete here so the platform package does not import
// gatewayhttp).
type GatewayIdentityResolver struct {
	authn middleware.Authenticator
}

// identityUnknown is the bounded fallback label when no caller identity
// can be resolved.
const identityUnknown = "unknown"

// NewGatewayIdentityResolver builds the resolver from the platform's
// authenticator. Nil authenticator yields a resolver that always
// returns "unknown".
func (p *Platform) NewGatewayIdentityResolver() *GatewayIdentityResolver {
	return &GatewayIdentityResolver{authn: p.Authenticator()}
}

// ResolveIdentity authenticates the token already placed on ctx and
// returns a display name: the API key name for API-key auth, else the
// OIDC email, else the raw subject, else "unknown".
func (r *GatewayIdentityResolver) ResolveIdentity(ctx context.Context) string {
	if r == nil || r.authn == nil {
		return identityUnknown
	}
	info, err := r.authn.Authenticate(ctx)
	if err != nil || info == nil {
		return identityUnknown
	}
	if info.AuthType == "apikey" {
		if name := strings.TrimPrefix(info.UserID, "apikey:"); name != "" && name != info.UserID {
			return name
		}
	}
	if info.Email != "" {
		return info.Email
	}
	if info.UserID != "" {
		return info.UserID
	}
	return identityUnknown
}
