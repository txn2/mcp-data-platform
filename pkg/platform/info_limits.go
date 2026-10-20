package platform

import (
	"github.com/txn2/mcp-data-platform/internal/pagewalk"
	"github.com/txn2/mcp-data-platform/internal/platform/limitsinfo"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstmt"
	"github.com/txn2/mcp-data-platform/internal/platform/trinowiring"
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
	if trinoTk := trinowiring.First(p.toolkitRegistry); trinoTk != nil {
		defaultRows, maxRows, timeout := trinoTk.QueryLimits()
		query = &limitsinfo.Query{DefaultRows: defaultRows, MaxRows: maxRows, TimeoutSeconds: int(timeout.Seconds())}
		query.ScriptMaxTimeoutSeconds, query.ScriptMaxTimeoutKey = int(p.config.Scripts.SQL.MaxTimeout().Seconds()), scriptstmt.MaxTimeoutKey
	}
	apiDefault, apiCeiling := pagewalk.PageBounds()
	return limitsinfo.Build(accessibleTools, export, query,
		limitsinfo.WalkBounds{DefaultMaxPages: apiDefault, MaxPages: apiCeiling},
		limitsinfo.WalkBounds{DefaultMaxPages: graphqlkit.DefaultMaxPages, MaxPages: graphqlkit.MaxPagesCeiling})
}
