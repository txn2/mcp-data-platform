package scriptwiring

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epwire"
	"github.com/txn2/mcp-data-platform/internal/platform/integration"
	"github.com/txn2/mcp-data-platform/internal/site/siterouting"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
)

// TestWireIntegrations: with a database and an approvals queue the service is
// wired, given the Site routing source, and handed to sites and the layer the
// platform starts; with neither nothing is.
func TestWireIntegrations(t *testing.T) {
	assert.Nil(t, wireIntegrations(Deps{}, nil, nil))

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	endpoints := &epwire.Endpoints{Approvals: &approval.Service{}}
	sites := &sitewire.Runtime{Service: &sitesvc.Service{}, Routing: &siterouting.Lookup{}}
	svc := wireIntegrations(Deps{DB: db, DSN: "postgres://x"}, endpoints, sites)
	require.NotNil(t, svc)
	assert.NotNil(t, svc.Sources[integration.OpSiteRouting])
	assert.Same(t, svc, endpoints.Integrations)
	assert.Same(t, svc, sites.Routing.Service)
	require.NotNil(t, endpoints.IntegrationRuns)
	assert.Same(t, svc, endpoints.IntegrationRuns.Service)

	bare := wireIntegrations(Deps{DB: db}, &epwire.Endpoints{Approvals: &approval.Service{}}, nil)
	assert.Empty(t, bare.Sources, "no sites, no site routing")
}
