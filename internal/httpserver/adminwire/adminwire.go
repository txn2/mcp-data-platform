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
}
