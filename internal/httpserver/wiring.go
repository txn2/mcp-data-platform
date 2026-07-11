package httpserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	datahubkit "github.com/txn2/mcp-data-platform/pkg/toolkits/datahub"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/sources"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// connOAuthConfigResolver bridges the connoauth refresher's
// ConfigResolver interface to the platform's ConnectionStore +
// OAuthKindHandlers wiring. The refresher cannot import the platform
// package directly (import cycle), so this adapter lives here
// where both packages are already imported.
type connOAuthConfigResolver struct {
	store     admin.ConnectionStore
	kinds     admin.OAuthKindHandlers
	maxLifeFn func(kind, name string, cfg map[string]any) time.Duration
}

// ResolveConfig fetches the connection_instances row for (kind, name)
// and parses out the connoauth.Config via the per-kind handler. The
// ErrConfigNotResolvable sentinel is returned when the connection no
// longer exists OR is configured for a non-OAuth auth mode — the
// refresher treats either as "skip" rather than "fail" so a stale
// token row doesn't stall keepalive for other connections.
func (r *connOAuthConfigResolver) ResolveConfig(ctx context.Context, key connoauth.Key) (connoauth.Config, error) {
	handler, ok := r.kinds[key.Kind]
	if !ok {
		return connoauth.Config{}, connoauth.ErrConfigNotResolvable
	}
	inst, err := r.store.Get(ctx, key.Kind, key.Name)
	if err != nil {
		return connoauth.Config{}, connoauth.ErrConfigNotResolvable
	}
	cfg, err := handler.ParseOAuthConfig(inst.Config)
	if err != nil {
		return connoauth.Config{}, connoauth.ErrConfigNotResolvable
	}
	return cfg, nil
}

// MaxLifetime reads the per-connection oauth2_refresh_max_lifetime
// config field. Zero when unset; the refresher then relies on
// IdP-disclosed deadlines only (which is correct for Keycloak / Auth0
// / Okta but inadequate for Microsoft / Salesforce / Google APIs
// that don't disclose refresh-token deadlines but enforce wall-clock
// max lifetimes anyway).
func (r *connOAuthConfigResolver) MaxLifetime(ctx context.Context, key connoauth.Key) time.Duration {
	if r.maxLifeFn == nil {
		return 0
	}
	inst, err := r.store.Get(ctx, key.Kind, key.Name)
	if err != nil {
		return 0
	}
	return r.maxLifeFn(key.Kind, key.Name, inst.Config)
}

// readMaxLifetime extracts the operator-configured wall-clock max
// lifetime for the refresh token, parsing the standard Go duration
// string format ("60d" via the d-suffix helper). Returns zero when
// the field is absent, empty, or unparseable — the refresher
// gracefully degrades to IdP-disclosed-deadline-only mode in that
// case.
func readMaxLifetime(_, _ string, cfg map[string]any) time.Duration {
	raw, _ := cfg[configKeyOAuthRefreshMaxLifetime].(string)
	if raw == "" {
		return 0
	}
	d, err := parseDurationWithDays(raw)
	if err != nil {
		return 0
	}
	return d
}

// configKeyOAuthRefreshMaxLifetime is the connection_instances
// config key that holds the operator's wall-clock refresh-token max
// lifetime hint. Stored as a duration string ("60d", "90d", "30d").
const configKeyOAuthRefreshMaxLifetime = "oauth2_refresh_max_lifetime"

// hoursPerDay names the magic number 24 so the lint rule on
// numeric literals doesn't fire and so the math reads as intent.
const hoursPerDay = 24

// parseDurationWithDays is time.ParseDuration with a "d" suffix
// shorthand added. The stdlib's time.ParseDuration tops out at "h",
// so "60d" is unparseable. Refresh-token deadlines are routinely
// expressed in days by operators (Microsoft 90d, Salesforce 30d), so
// asking them to write "1440h" instead would be a thousand-cuts UX
// failure.
func parseDurationWithDays(s string) (time.Duration, error) {
	if head, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(head)
		if err != nil {
			return 0, fmt.Errorf("parse duration %q: %w", s, err)
		}
		if days < 0 {
			return 0, fmt.Errorf("parse duration %q: negative days", s)
		}
		return time.Duration(days) * hoursPerDay * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", s, err)
	}
	return d, nil
}

// startConnOAuthRefresher kicks off the keepalive loop. Called after
// the toolkit registry + connection store are wired so the resolver
// can read connection_instances rows. multi-replica is taken from
// the platform's session-store mode — database-backed sessions
// implies multi-replica intent.
func startConnOAuthRefresher(p *platform.Platform) {
	if p.ConnOAuthStore() == nil {
		return
	}
	store := p.ConnectionStore()
	if store == nil {
		return
	}
	kinds := buildOAuthKindHandlers(p)
	if len(kinds) == 0 {
		return
	}
	resolver := &connOAuthConfigResolver{
		store:     store,
		kinds:     kinds,
		maxLifeFn: readMaxLifetime,
	}
	multiReplica := p.Config() != nil && p.Config().Sessions.Store == platform.SessionStoreDatabase
	p.StartConnOAuthRefresher(resolver, multiReplica)
}

// buildOAuthKindHandlers assembles the per-kind OAuth adapter registry
// the admin handler dispatches on. Each registered toolkit kind
// contributes one handler; missing toolkits produce no entry, and the
// unified handler returns 400 "unsupported connection kind" for
// requests targeting an unregistered kind.
func buildOAuthKindHandlers(p *platform.Platform) admin.OAuthKindHandlers {
	out := admin.OAuthKindHandlers{}
	if p.ToolkitRegistry() == nil {
		return out
	}
	for _, tk := range p.ToolkitRegistry().All() {
		switch v := tk.(type) {
		case *gatewaykit.Toolkit:
			if h := gatewaykit.NewOAuthKindHandler(v); h != nil {
				out[connoauth.KindMCP] = h
			}
		case *apigatewaykit.Toolkit:
			out[connoauth.KindAPI] = apigatewaykit.NewOAuthKindHandler(v)
		}
	}
	return out
}

// wireEnrichmentEngine builds the gateway enrichment engine when a rule
// store is available, registers the built-in source adapters (Trino,
// DataHub) bound to the platform's live toolkits, and attaches the
// engine to the live gateway toolkit so forwarded calls pick it up.
func wireEnrichmentEngine(p *platform.Platform) *enrichment.Engine {
	store := p.EnrichmentStore()
	if store == nil {
		return nil
	}
	sourceReg := enrichment.NewSourceRegistry()
	registerEnrichmentSources(p, sourceReg)

	engine := enrichment.NewEngine(store, sourceReg)
	for _, tk := range p.ToolkitRegistry().All() {
		gw, ok := tk.(*gatewaykit.Toolkit)
		if !ok {
			continue
		}
		gw.SetEnrichmentEngine(engine)
	}
	return engine
}

// registerEnrichmentSources binds source adapters to the platform's
// active toolkits. A toolkit that isn't running results in no
// registration for that source — rules referencing it will surface a
// "source not registered" warning at evaluation time.
func registerEnrichmentSources(p *platform.Platform, reg *enrichment.SourceRegistry) {
	if exec := buildTrinoQueryFunc(p); exec != nil {
		reg.Register(sources.NewTrinoSource(exec))
	}
	if getEntity, getTerm := buildDataHubFuncs(p); getEntity != nil || getTerm != nil {
		// DataHub source registers even when only a subset of operations
		// is wired; missing operations report unsupported on dispatch.
		reg.Register(sources.NewDataHubSource(getEntity, getTerm))
	}
}

// buildTrinoQueryFunc returns a TrinoQueryFunc bound to the live trino
// toolkit's manager, or nil if no trino toolkit is registered.
func buildTrinoQueryFunc(p *platform.Platform) sources.TrinoQueryFunc {
	for _, tk := range p.ToolkitRegistry().All() {
		trinoTk, ok := tk.(*trinokit.Toolkit)
		if !ok || trinoTk.Manager() == nil {
			continue
		}
		mgr := trinoTk.Manager()
		return func(ctx context.Context, connection, sql string) ([]map[string]any, error) {
			c, err := mgr.Client(connection)
			if err != nil {
				return nil, fmt.Errorf("trino manager: %w", err)
			}
			res, qerr := c.Query(ctx, sql, trinoclient.DefaultQueryOptions())
			if qerr != nil {
				return nil, fmt.Errorf("trino query: %w", qerr)
			}
			return res.Rows, nil
		}
	}
	return nil
}

// buildDataHubFuncs returns get-entity and get-glossary-term closures
// bound to the live datahub toolkit's client, or nils if no datahub
// toolkit is registered.
func buildDataHubFuncs(p *platform.Platform) (sources.DataHubGetEntityFunc, sources.DataHubGetGlossaryTermFunc) {
	for _, tk := range p.ToolkitRegistry().All() {
		dhTk, ok := tk.(*datahubkit.Toolkit)
		if !ok || dhTk.Client() == nil {
			continue
		}
		client := dhTk.Client()
		getEntity := func(ctx context.Context, urn string) (any, error) {
			return client.GetEntity(ctx, urn)
		}
		getTerm := func(ctx context.Context, urn string) (any, error) {
			return client.GetGlossaryTerm(ctx, urn)
		}
		return getEntity, getTerm
	}
	return nil, nil
}
