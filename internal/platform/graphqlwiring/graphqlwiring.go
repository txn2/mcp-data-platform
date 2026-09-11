// Package graphqlwiring attaches the platform's dependencies to the
// live graphql toolkits and brings their schemas up.
//
// It exists here rather than on the platform facade for the reason the
// other seams under internal/platform do: the facade is at its size
// budget, and a connection kind's wiring is cohesive enough to own its
// own file. pkg/platform keeps one thin entry point that assembles the
// dependencies it holds and hands them over.
package graphqlwiring

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/membudget"
	"github.com/txn2/mcp-data-platform/internal/platform/graphqlstore"
	"github.com/txn2/mcp-data-platform/internal/platform/routepolicy"
	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// Deps is everything the graphql kind needs from the platform. Every
// field is optional: a deployment with no database, no embedding
// provider or no portal still runs graphql connections, with the
// capabilities those dependencies enable absent rather than the
// connection broken.
type Deps struct {
	// Registry holds the live toolkits.
	Registry *registry.Registry
	// DB backs the schema store and the operation-embedding reads. Nil
	// keeps schemas in memory for the process's life.
	DB *sql.DB
	// Embedder embeds a caller's query for semantic and hybrid ranking.
	Embedder embedding.Provider
	// RoutePolicy authorizes one operation. Nil leaves the platform's
	// tool and connection gates as the only ones.
	RoutePolicy *routepolicy.Policy
	// OAuthStore and AuthEvents back the authorization_code grant.
	OAuthStore connoauth.Store
	AuthEvents *authevents.Writer
	// MemBudget is the process-wide in-flight response budget, shared
	// with the other buffered gateway kinds.
	MemBudget *membudget.Budget
	// Metrics instruments outbound calls.
	Metrics *observability.Metrics
}

// Toolkits returns the live graphql toolkits in a registry.
func Toolkits(reg *registry.Registry) []*graphqlkit.Toolkit {
	if reg == nil {
		return nil
	}
	var out []*graphqlkit.Toolkit
	for _, tk := range reg.GetByKind(graphqlkit.Kind) {
		if gql, ok := tk.(*graphqlkit.Toolkit); ok {
			out = append(out, gql)
		}
	}
	return out
}

// Wire attaches every dependency and then reads each connection's
// schema: the stored one when the store holds it, a fresh introspection
// otherwise. Schemas are read last because the read goes through the
// connection's own credential and its own transport, both of which the
// steps above install.
//
// Every step is nil-safe and idempotent, so Wire is safe to call once
// per boot whatever the deployment is missing.
func Wire(ctx context.Context, d Deps) {
	toolkits := Toolkits(d.Registry)
	if len(toolkits) == 0 {
		return
	}
	var store *graphqlstore.Store
	if d.DB != nil {
		store = graphqlstore.New(d.DB)
	}
	for _, tk := range toolkits {
		attach(tk, d, store)
	}
	for _, tk := range toolkits {
		tk.HydrateSchemas(ctx)
	}
	slog.Info("graphql connections wired",
		"toolkits", len(toolkits), "schema_store", store != nil)
}

// ReloadStoredSchema installs the stored schema on every live graphql
// toolkit holding the connection. The reload bus calls it when a peer
// replica stored a schema, an upload or a re-read, so every replica
// answers from the store rather than from whatever its own last read
// left it with (#1676). The endpoint is not touched: the store already
// holds the answer.
func ReloadStoredSchema(ctx context.Context, reg *registry.Registry, name string) {
	for _, tk := range Toolkits(reg) {
		if !tk.HasConnection(name) {
			continue
		}
		if err := tk.LoadStoredSchema(ctx, name); err != nil {
			slog.Warn("graphql: loading the stored schema a peer announced failed",
				"connection", logsan.SanitizeForLog(name), "error", logsan.SanitizeForLog(err.Error()))
		}
	}
}

// AttachExport enables graphql_export on every live graphql toolkit. A nil
// deps leaves the tool unregistered, which is what a deployment with no
// portal wants.
//
// Separate from Wire, and called from a much earlier point in the boot
// sequence, because a toolkit registers its export tool only when the
// dependencies are already set: the platform registers every toolkit's tools
// before it reaches Wire, so attaching here would leave the name in the
// toolkit's Tools() list and the tool unknown to every client (#1675).
func AttachExport(reg *registry.Registry, deps *graphqlkit.ExportDeps) {
	if deps == nil {
		return
	}
	for _, tk := range Toolkits(reg) {
		tk.SetExportDeps(*deps)
	}
}

// attach installs one toolkit's dependencies. The auth-event writer
// goes in before the token store so a refresh triggered by the store
// wiring already has somewhere to record itself.
func attach(tk *graphqlkit.Toolkit, d Deps, store *graphqlstore.Store) {
	tk.SetAuthEvents(d.AuthEvents)
	if d.OAuthStore != nil {
		tk.SetConnOAuthStore(d.OAuthStore)
	}
	if store != nil {
		tk.SetSchemaStore(store)
		tk.SetVectorReader(store)
	}
	if d.Embedder != nil {
		tk.SetEmbeddingProvider(d.Embedder)
	}
	if d.RoutePolicy != nil {
		tk.SetRoutePolicy(d.RoutePolicy)
	}
	if d.MemBudget != nil {
		tk.SetMemBudget(d.MemBudget)
	}
	if d.Metrics != nil {
		tk.SetMetrics(d.Metrics)
	}
}
