package scriptwiring

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epwire"
	"github.com/txn2/mcp-data-platform/internal/platform/integration"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siterouting"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
)

// wireIntegrations assembles the integrations (#2659): the service an
// administrator binds an operation's script through, each operation's items,
// and the passes the platform starts with its lifecycle. Nil with no database
// or no approvals queue: an integration runs only a version an administrator
// approved for handlers.
func wireIntegrations(deps Deps, endpoints *epwire.Endpoints, sites *sitewire.Runtime) *integration.Service {
	if deps.DB == nil || endpoints == nil || endpoints.Approvals == nil {
		return nil
	}
	svc := &integration.Service{
		Store: &integration.Store{DB: deps.DB}, Scripts: scriptstore.New(deps.DB), Approvals: endpoints.Approvals,
		Sources: map[string]integration.Source{},
	}
	if sites != nil {
		svc.Sources[integration.OpSiteRouting] = siterouting.Source{
			Sites:   sites.Store.List,
			Domains: sites.Store.Domains,
			Apps: func(ctx context.Context) ([]application.Application, error) {
				return sites.Apps.List(ctx) //nolint:wrapcheck // the source names what it listed
			},
			Addressing: func() site.Addressing { return sites.Service.Addressing() },
		}
		if sites.Routing != nil {
			sites.Routing.Service = svc
		}
	}
	endpoints.Integrations = svc
	endpoints.IntegrationRuns = &integration.Runtime{Service: svc, DSN: deps.DSN}
	return svc
}
