// Package utilconn seeds the built-in "util" API-gateway connection
// (issue #1005): a handler=internal connection whose operations are
// served in-process (pkg/toolkits/apigateway/utilhandler) and
// discovered through the same catalog path as any other api
// connection. Composed only by pkg/platform's runtime wiring; the
// seam keeps the seed off the Platform struct (the god-object budget
// is frozen, #854).
//
// Access model: connections are deny-by-default (persona.ToolFilter
// ConnectionRules), so util is reachable only by personas whose
// connection rules allow it — the built-in admin persona's "*", or an
// explicit operator grant. There is no separate access flag: not
// granting the connection is the restriction.
package utilconn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/utilhandler"
)

// Built-in identifiers. The connection name doubles as the catalog ID
// and is what a caller passes as the `connection` argument to the api
// gateway tools.
const (
	connectionName = "util"
	catalogID      = "util"
	specName       = "util"
	catalogVersion = "builtin"

	// connectionDescription is surfaced in the admin UI and the
	// list_connections MCP tool in place of a base URL (an internal
	// connection has none). Markdown is rendered by the portal.
	connectionDescription = "Built-in utility connection. Operations are handled inside the platform " +
		"rather than proxied to an upstream API. `fetch_url` (`POST /util/fetch`) retrieves an " +
		"arbitrary public URL server-side - a presigned download link, a generated report URL - " +
		"inline via api_invoke_endpoint or streamed to a portal asset via api_export. " +
		"Destinations on internal address space are refused."
)

// Enqueuer is the minimal slice of the api-catalog embed-jobs store
// the seed needs: enqueue an index job for one spec. Narrowed to an
// interface so the seed is testable without a live Postgres-backed
// queue. nil skips embedding (ranking falls back to lexical, the
// documented degraded mode).
type Enqueuer interface {
	Enqueue(ctx context.Context, key catalogindex.SpecKey, kind catalogindex.Kind) (bool, error)
}

// Deps carries the seed's dependencies, gathered by pkg/platform's
// runtime wiring. All fields except Enqueuer and AllowPrivateCIDRs
// are required.
type Deps struct {
	Toolkit  *apigatewaykit.Toolkit
	Catalog  catalog.Store
	Enqueuer Enqueuer
	// OnStart registers the seed with the platform lifecycle (late
	// registration runs immediately, matching the platform-admin
	// self-connection's boot ordering).
	OnStart func(func(context.Context) error)
	// AllowPrivateCIDRs is the operator's exemption list for the
	// fetch handler's internal-range block
	// (apigateway.util_connection.allow_private_cidrs).
	AllowPrivateCIDRs []string
}

// Register wires the in-process handler onto the toolkit and
// schedules the idempotent catalog + connection seed. The handler is
// built eagerly so a bad allow_private_cidrs entry fails registration
// (and is logged by the caller) instead of surfacing on the first
// fetch. Seed failures at start are non-fatal: they are logged and
// the next boot retries, mirroring the platform-admin self-connection.
func Register(d Deps) error {
	h, err := utilhandler.New(utilhandler.Options{AllowPrivateCIDRs: d.AllowPrivateCIDRs})
	if err != nil {
		return fmt.Errorf("utilconn: building handler: %w", err)
	}
	d.Toolkit.SetInternalHandler(h)
	d.OnStart(func(ctx context.Context) error {
		if serr := seed(ctx, d); serr != nil {
			slog.Warn("util connection: seed failed", "error", serr)
		}
		return nil
	})
	return nil
}

// seed performs the idempotent boot work: ensure the util catalog and
// its embedded spec exist (re-upserted every boot so a release that
// adds util operations re-indexes them), enqueue embedding, and
// register (or reload) the connection.
func seed(ctx context.Context, d Deps) error {
	content := utilhandler.SpecJSON()
	opCount := 0
	if items, berr := apigatewaykit.BuildOperationItems(content, specName); berr == nil {
		opCount = len(items)
	}
	if err := ensureCatalog(ctx, d.Catalog); err != nil {
		return fmt.Errorf("ensuring catalog: %w", err)
	}
	if err := d.Catalog.UpsertSpec(ctx, catalogID, catalog.SpecEntry{
		SpecName:       specName,
		Content:        content,
		SourceKind:     catalog.SourceEmbedded,
		Title:          "Platform Utilities",
		Description:    "Built-in utility operations handled inside the platform.",
		OperationCount: opCount,
	}); err != nil {
		return fmt.Errorf("upserting spec: %w", err)
	}
	if d.Enqueuer != nil {
		if _, eerr := d.Enqueuer.Enqueue(ctx, catalogindex.SpecKey{
			CatalogID: catalogID, SpecName: specName,
		}, catalogindex.KindSpecWrite); eerr != nil {
			slog.Warn("util connection: enqueue embedding failed", "error", eerr)
		}
	}
	if err := registerConnection(d.Toolkit); err != nil {
		return fmt.Errorf("registering connection: %w", err)
	}
	slog.Info("util connection: registered",
		"connection", connectionName, "operations", opCount)
	return nil
}

// ensureCatalog creates the util catalog header if absent. An
// existing catalog is left untouched (its spec is upserted
// separately).
func ensureCatalog(ctx context.Context, store catalog.Store) error {
	_, err := store.GetCatalog(ctx, catalogID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, catalog.ErrNotFound) {
		return fmt.Errorf("looking up catalog: %w", err)
	}
	if err := store.CreateCatalog(ctx, catalog.Catalog{
		ID:          catalogID,
		Name:        catalogID,
		Version:     catalogVersion,
		DisplayName: "Platform Utilities",
		Description: "Built-in catalog for the platform's utility operations.",
		CreatedBy:   "system",
	}); err != nil {
		return fmt.Errorf("creating catalog: %w", err)
	}
	return nil
}

// registerConnection adds the util connection, or reloads it when
// already present (a re-seed on a later boot picks up an updated spec).
func registerConnection(tk *apigatewaykit.Toolkit) error {
	if tk.HasConnection(connectionName) {
		if err := tk.ReloadConnection(connectionName); err != nil {
			return fmt.Errorf("reloading connection: %w", err)
		}
		return nil
	}
	if err := tk.AddConnection(connectionName, map[string]any{
		"handler":         apigatewaykit.HandlerInternal,
		"auth_mode":       apigatewaykit.AuthModeNone,
		"catalog_id":      catalogID,
		"connection_name": connectionName,
		"description":     connectionDescription,
	}); err != nil {
		return fmt.Errorf("adding connection: %w", err)
	}
	return nil
}
