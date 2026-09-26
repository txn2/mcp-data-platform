package storeresync

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// Catalog rebuilds every connection that mounts the given catalog. A catalog
// serves both kinds that reference one: an api connection takes its OpenAPI
// specs from it, and a graphql connection the GraphQL schema it holds (#1745).
func Catalog(toolkits []registry.Toolkit, catalogID string) {
	for _, tk := range toolkits {
		switch kit := tk.(type) {
		case *apigatewaykit.Toolkit:
			kit.ReloadConnectionsByCatalog(catalogID)
		case *graphqlkit.Toolkit:
			kit.ReloadConnectionsByCatalog(context.Background(), catalogID)
		}
	}
}
