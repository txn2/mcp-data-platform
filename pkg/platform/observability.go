package platform

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

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
