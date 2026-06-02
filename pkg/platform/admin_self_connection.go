package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"

	"github.com/txn2/mcp-data-platform/internal/apidocs"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

// Built-in identifiers for the platform-admin self-connection (issue
// #543). The connection name doubles as the catalog ID and is what an
// admin passes as the `connection` argument to the api gateway tools.
const (
	adminSelfConnectionName = "platform-admin"
	adminSelfCatalogID      = "platform-admin"
	adminSelfSpecName       = "admin"
	adminSelfCatalogVersion = "builtin"

	// adminSelfDefaultPort mirrors the --address default (":8080") and is
	// used only when the listen address carries no parseable port.
	adminSelfDefaultPort = "8080"
)

// WireAdminSelfConnection registers a lifecycle hook that seeds the
// built-in platform-admin API-gateway connection, which points the
// gateway at the platform's own /api/v1/admin/* surface so an admin can
// operate the platform through api_list_endpoints / api_invoke_endpoint.
//
// listenAddr is the server's bind address (e.g. ":8080"); the loopback
// base URL is derived from its port unless overridden in config. The
// caller is responsible for invoking this only when the admin REST API is
// actually mounted (HTTP transport), which together with a wired catalog
// store and api-gateway toolkit forms the feature's prerequisites. A
// disabled feature, or any absent prerequisite, makes this a no-op.
//
// Idempotent: the seed re-runs on every boot. The catalog spec is
// re-upserted from the embedded OpenAPI document so a release that adds
// admin endpoints re-indexes them with no manual catalog action; the
// embed pipeline dedups unchanged operations by text hash.
func (p *Platform) WireAdminSelfConnection(listenAddr string) {
	tk := p.firstAPIGatewayToolkit()
	catalogStore := p.APIGatewayCatalogStore()
	prereqsMet := tk != nil && catalogStore != nil
	if !p.config.APIGateway.SelfConnection.SelfConnectionEnabled(prereqsMet) {
		if !prereqsMet {
			slog.Debug("platform-admin self-connection: prerequisites not met; skipping",
				"have_toolkit", tk != nil, "have_catalog_store", catalogStore != nil)
		}
		return
	}

	baseURL := p.config.APIGateway.SelfConnection.BaseURL
	if baseURL == "" {
		baseURL = loopbackBaseURL(listenAddr)
	}

	// Pass the enqueuer as a nil interface (not a typed nil) when the
	// embed queue is unwired, so the seed's nil check works correctly.
	var enqueuer embedEnqueuer
	if p.apiGatewayEmbedAdminStore != nil {
		enqueuer = p.apiGatewayEmbedAdminStore
	}

	p.lifecycle.OnStart(func(ctx context.Context) error {
		if err := p.seedAdminSelfConnection(ctx, tk, catalogStore, enqueuer, baseURL); err != nil {
			// Non-fatal: a failed self-connection must not block startup.
			// The admin can still use the Portal; the error is logged for
			// diagnosis and the next boot retries.
			slog.Warn("platform-admin self-connection: seed failed", "error", err)
		}
		return nil
	})
}

// embedEnqueuer is the minimal slice of the api-catalog embed-jobs admin
// store the self-connection seed needs: enqueue an index job for one
// spec. Narrowed to an interface so the seed is testable without a live
// Postgres-backed queue.
type embedEnqueuer interface {
	Enqueue(ctx context.Context, key catalogindex.SpecKey, kind catalogindex.Kind) (bool, error)
}

// seedAdminSelfConnection performs the idempotent seed: ensure the
// catalog + embedded spec exist, enqueue embedding, register (or reload)
// the connection, and refresh the authorizer's restricted set so the
// connection is admin-only by default. enqueuer may be nil (file mode /
// no embedder), in which case embedding is skipped and ranking falls back
// to lexical.
func (p *Platform) seedAdminSelfConnection(
	ctx context.Context,
	tk *apigatewaykit.Toolkit,
	catalogStore apigatewaycatalog.Store,
	enqueuer embedEnqueuer,
	baseURL string,
) error {
	content, err := adminSelfSpecContent()
	if err != nil {
		return fmt.Errorf("building admin spec: %w", err)
	}
	opCount := 0
	if items, berr := apigatewaykit.BuildOperationItems(content, adminSelfSpecName); berr == nil {
		opCount = len(items)
	}

	if err := ensureAdminSelfCatalog(ctx, catalogStore); err != nil {
		return fmt.Errorf("ensuring catalog: %w", err)
	}
	if err := catalogStore.UpsertSpec(ctx, adminSelfCatalogID, apigatewaycatalog.SpecEntry{
		SpecName:       adminSelfSpecName,
		Content:        content,
		SourceKind:     apigatewaycatalog.SourceEmbedded,
		Title:          "Platform Admin API",
		Description:    "The platform's own /api/v1/admin/* REST API, embedded from the running binary.",
		OperationCount: opCount,
	}); err != nil {
		return fmt.Errorf("upserting spec: %w", err)
	}

	// Enqueue embedding so semantic ranking works; absent a queue
	// (file mode / no embedder) the gateway falls back to lexical
	// ranking, which is the documented degraded mode.
	if enqueuer != nil {
		if _, eerr := enqueuer.Enqueue(ctx, catalogindex.SpecKey{
			CatalogID: adminSelfCatalogID, SpecName: adminSelfSpecName,
		}, catalogindex.KindSpecWrite); eerr != nil {
			slog.Warn("platform-admin self-connection: enqueue embedding failed", "error", eerr)
		}
	}

	if err := registerAdminSelfConnection(tk, baseURL); err != nil {
		return fmt.Errorf("registering connection: %w", err)
	}

	p.refreshRestrictedConnections(tk)
	slog.Info("platform-admin self-connection: registered",
		"connection", adminSelfConnectionName, "base_url", baseURL, "operations", opCount)
	return nil
}

// ensureAdminSelfCatalog creates the platform-admin catalog header if it
// is not already present. An existing catalog is left untouched (its
// specs are upserted separately).
func ensureAdminSelfCatalog(ctx context.Context, store apigatewaycatalog.Store) error {
	_, err := store.GetCatalog(ctx, adminSelfCatalogID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, apigatewaycatalog.ErrNotFound) {
		return fmt.Errorf("looking up catalog: %w", err)
	}
	if err := store.CreateCatalog(ctx, apigatewaycatalog.Catalog{
		ID:          adminSelfCatalogID,
		Name:        adminSelfCatalogID,
		Version:     adminSelfCatalogVersion,
		DisplayName: "Platform Admin",
		Description: "Built-in catalog for the platform's own admin REST API.",
		CreatedBy:   "system",
	}); err != nil {
		return fmt.Errorf("creating catalog: %w", err)
	}
	return nil
}

// registerAdminSelfConnection adds the platform-admin connection to the
// toolkit, or reloads it when it already exists (a re-seed on a later
// boot). The connection uses identity passthrough (the acting admin's
// token authenticates the loopback call) and is admin-only by default.
func registerAdminSelfConnection(tk *apigatewaykit.Toolkit, baseURL string) error {
	if tk.HasConnection(adminSelfConnectionName) {
		// Already registered this process. Reload so an updated catalog
		// spec / freshly computed embeddings are picked up.
		if err := tk.ReloadConnection(adminSelfConnectionName); err != nil {
			return fmt.Errorf("reloading connection: %w", err)
		}
		return nil
	}
	if err := tk.AddConnection(adminSelfConnectionName, map[string]any{
		"base_url":             baseURL,
		"auth_mode":            apigatewaykit.AuthModeNone,
		"catalog_id":           adminSelfCatalogID,
		"identity_passthrough": true,
		"admin_only":           true,
		"connection_name":      adminSelfConnectionName,
	}); err != nil {
		return fmt.Errorf("adding connection: %w", err)
	}
	return nil
}

// refreshRestrictedConnections feeds every admin-only api-gateway
// connection name into the persona authorizer so non-admin personas are
// denied them by default. A no-op when the authorizer is not the persona
// implementation (e.g. a test double).
func (p *Platform) refreshRestrictedConnections(tk *apigatewaykit.Toolkit) {
	pa, ok := p.authorizer.(*persona.Authorizer)
	if !ok {
		return
	}
	pa.SetRestrictedConnections(tk.AdminOnlyConnections())
}

// firstAPIGatewayToolkit returns the first registered api-gateway
// toolkit, or nil when none is registered.
func (p *Platform) firstAPIGatewayToolkit() *apigatewaykit.Toolkit {
	for _, tk := range p.toolkitRegistry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			return api
		}
	}
	return nil
}

// adminSelfSpecContent converts the binary's embedded OpenAPI 2.0
// (Swagger) document to OpenAPI 3.0 JSON, the form the api-gateway spec
// parser accepts. swaggo emits 2.0; the gateway's kin-openapi parser is
// 3.x-only, so the conversion is mandatory rather than cosmetic.
func adminSelfSpecContent() (string, error) {
	return convertSwaggerToV3(apidocs.SwaggerJSON())
}

// convertSwaggerToV3 converts an OpenAPI 2.0 (Swagger) JSON document to
// OpenAPI 3.0 JSON. Extracted from adminSelfSpecContent so the error
// paths are testable with malformed input independent of the embedded
// (always-valid) document.
func convertSwaggerToV3(raw string) (string, error) {
	var v2 openapi2.T
	if err := json.Unmarshal([]byte(raw), &v2); err != nil {
		return "", fmt.Errorf("decoding embedded swagger 2.0: %w", err)
	}
	v3, err := openapi2conv.ToV3(&v2)
	if err != nil {
		return "", fmt.Errorf("converting swagger 2.0 to openapi 3.0: %w", err)
	}
	out, err := v3.MarshalJSON()
	if err != nil {
		return "", fmt.Errorf("encoding openapi 3.0: %w", err)
	}
	return string(out), nil
}

// loopbackBaseURL derives the loopback base URL for the admin API from
// the server's listen address. Only the port matters: the admin API is
// reached over the loopback interface regardless of which interface the
// listener binds. An address with no parseable port falls back to the
// platform's default HTTP port.
func loopbackBaseURL(listenAddr string) string {
	port := adminSelfDefaultPort
	if _, p, err := net.SplitHostPort(listenAddr); err == nil && p != "" {
		port = p
	}
	return "http://127.0.0.1:" + port
}
