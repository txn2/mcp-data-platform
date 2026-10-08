package platform

import (
	"github.com/txn2/mcp-data-platform/internal/pagewalk"
	"github.com/txn2/mcp-data-platform/internal/platform/limitsinfo"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// Limits is the ceilings platform_info reports (#2057), aliased so a library
// consumer can name the type the Info field carries.
type Limits = limitsinfo.Limits

// buildLimits is the limits block for a caller reaching accessibleTools: the
// export caps every export tool runs under, trino_query's row limits, and each
// page walk's bounds, each only when the caller reaches a tool it bounds.
func (p *Platform) buildLimits(accessibleTools []string) *Limits {
	exp := trinokit.ResolveExportConfig(p.parseExportConfig())
	export := limitsinfo.Export{
		MaxRows: exp.MaxRows, MaxBytes: exp.MaxBytes,
		DefaultTimeoutSeconds: int(exp.DefaultTimeout.Seconds()), MaxTimeoutSeconds: int(exp.MaxTimeout.Seconds()),
	}
	var query *limitsinfo.Query
	for _, tk := range p.toolkitRegistry.GetByKind("trino") {
		if trinoTk, ok := tk.(*trinokit.Toolkit); ok {
			defaultRows, maxRows, timeout := trinoTk.QueryLimits()
			query = &limitsinfo.Query{DefaultRows: defaultRows, MaxRows: maxRows, TimeoutSeconds: int(timeout.Seconds())}
			break
		}
	}
	apiDefault, apiCeiling := pagewalk.PageBounds()
	return limitsinfo.Build(accessibleTools, export, query,
		limitsinfo.WalkBounds{DefaultMaxPages: apiDefault, MaxPages: apiCeiling},
		limitsinfo.WalkBounds{DefaultMaxPages: graphqlkit.DefaultMaxPages, MaxPages: graphqlkit.MaxPagesCeiling})
}
