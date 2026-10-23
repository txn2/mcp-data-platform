// Package adminwire attaches the admin handler's optional stores to its
// dependencies: the api-gateway catalog store and embed-job queue, the
// index-jobs reporter, and the memory store the admin memory list reads
// (#1926). Each is attached only when the deployment has it, so a typed nil
// never reaches an interface field and a missing store leaves its routes
// unregistered.
//
// It is a package of its own rather than another function in the
// composition root because that root is at its size budget.
package adminwire

import (
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/admin/resindexapi"
	"github.com/txn2/mcp-data-platform/internal/admin/secretapi"
	"github.com/txn2/mcp-data-platform/internal/httpserver/personaaccesswire"
	"github.com/txn2/mcp-data-platform/internal/secretstore"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// StoreDeps attaches each optional store p holds to deps.
func StoreDeps(deps *admin.Deps, p *platform.Platform) {
	if catStore := p.APIGatewayCatalogStore(); catStore != nil {
		deps.APICatalogStore = catStore
	}
	if jobs := p.APIGatewayEmbedJobsStore(); jobs != nil {
		deps.EmbedJobs = jobs
	}
	if reporter := p.IndexJobsReporter(); reporter != nil {
		deps.IndexJobs = reporter
	}
	if records := p.MemoryStore(); records != nil {
		deps.MemoryRecords = records
	}
	if db := p.DB(); db != nil {
		deps.Secrets = secretstore.NewStore(db, p.RestEncryptor())
		deps.PersonaReferences = personaaccesswire.Service(p).References
	}
}

// MountDatabaseAPIs registers the admin routes over database rows, behind the
// admin API's authentication (auth): what a persona reaches (#2257), which
// reads references from the database when there is one, the stored secrets
// (#2051), and how managed resources are indexed (#2111) where there are
// resources. author resolves the acting administrator.
func MountDatabaseAPIs(mux *http.ServeMux, p *platform.Platform,
	auth func(*platform.Platform) func(http.Handler) http.Handler, author func(*http.Request) string,
) {
	if p == nil || !p.Config().Admin.IsEnabled() {
		return
	}
	personaaccesswire.Mount(mux, p, auth(p))
	if p.DB() == nil {
		return
	}
	secretapi.Register(mux, auth(p), secretapi.Config{Store: secretstore.NewStore(p.DB(), p.RestEncryptor()), Author: author})
	slog.Info("Stored secret admin API enabled on /api/v1/admin/secrets")
	if p.ResourceStore() != nil {
		resindexapi.Mount(mux, auth(p), p.DB(), p.Config().Resources.Index.Resolver())
	}
}
