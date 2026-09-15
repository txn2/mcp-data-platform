// Package apigwwiring attaches the platform's dependencies to the live
// api gateway toolkits.
//
// Each of these is the same shape — walk the registry, pick out the
// toolkits of this kind, hand each one a dependency the platform built —
// and each is called once, from the platform facade's wiring sequence.
// They live here rather than on the facade because the facade is at its
// size budget and a loop over the registry is not facade logic; what the
// facade keeps is the order the wiring runs in, which is load-bearing.
package apigwwiring

import (
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// ToolkitSource is the toolkit registry as this package reads it.
type ToolkitSource interface {
	All() []registry.Toolkit
}

// Toolkits returns the live api gateway toolkits in a registry.
func Toolkits(source ToolkitSource) []*apigatewaykit.Toolkit {
	if source == nil {
		return nil
	}
	var found []*apigatewaykit.Toolkit
	for _, tk := range source.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			found = append(found, api)
		}
	}
	return found
}

// CatalogStore attaches the store the toolkit loads the OpenAPI specs a
// connection's catalog_id names from, and, when that store also reads
// them, the requests promoted on an endpoint: calls against a connection
// that are known to have worked, so reading an endpoint's schema shows
// them (#1321).
//
// Every already-registered connection is then reloaded, so a connection
// that registered before the store was available picks up its catalog
// content at once rather than staying in the "catalog_id set, zero
// operations" state until the next admin save.
func CatalogStore(source ToolkitSource, store apicatalog.Store) {
	if store == nil {
		return
	}
	for _, api := range Toolkits(source) {
		api.SetCatalogStore(store)
		if examples, ok := store.(apicatalog.ExampleStore); ok {
			api.SetExampleStore(examples)
		}
		for _, detail := range api.ListConnections() {
			if err := api.ReloadConnection(detail.Name); err != nil {
				slog.Warn("apigateway: catalog wire reload failed",
					"connection", logsan.SanitizeForLog(detail.Name),
					"error", logsan.SanitizeForLog(err.Error()))
			}
		}
	}
}

// CurrentCatalogStore returns the catalog store wired into the first api
// gateway toolkit, or nil when none has one. The admin layer shares one
// store for both its reads and its writes.
func CurrentCatalogStore(source ToolkitSource) apicatalog.Store {
	for _, api := range Toolkits(source) {
		return api.CatalogStore()
	}
	return nil
}

// ConnectionStore attaches the store a request for a connection this
// replica does not serve is answered from, so a connection saved on
// another replica is served here from the moment that save returns
// rather than when its announcement arrives (#1746). A nil store leaves
// each toolkit serving the connections it was handed.
func ConnectionStore(source ToolkitSource, store apigatewaykit.ConnectionStore) {
	if store == nil {
		return
	}
	for _, api := range Toolkits(source) {
		api.SetConnectionStore(store)
	}
}

// RoutePolicy installs a per-(connection, method, path) authorization
// gate on every live api gateway toolkit.
func RoutePolicy(source ToolkitSource, policy apigatewaykit.RoutePolicy) {
	if policy == nil {
		return
	}
	for _, api := range Toolkits(source) {
		api.SetRoutePolicy(policy)
	}
}
