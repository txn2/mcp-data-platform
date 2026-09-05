package platform

import (
	"github.com/txn2/mcp-data-platform/internal/platform/resourcetemplates"
)

// registerResourceTemplates registers the three read-only MCP resource
// templates -- a table's schema with its semantic context, a glossary term,
// and a table's query availability -- from the providers this platform
// resolved during initialization.
//
// The templates themselves live in internal/platform/resourcetemplates: they
// answer from the two providers and the URN mapping alone and write nothing,
// so they needed no part of this facade beyond those (#1628).
func (p *Platform) registerResourceTemplates() {
	if !p.config.Resources.IsEnabled() {
		return
	}
	resourcetemplates.New(resourcetemplates.Deps{
		Semantic:          p.semanticProvider,
		Query:             p.queryProvider,
		URNPlatform:       p.config.Semantic.URNMapping.Platform,
		URNCatalogMapping: p.config.Semantic.URNMapping.CatalogMapping,
	}).Register(p.mcpServer)
}
