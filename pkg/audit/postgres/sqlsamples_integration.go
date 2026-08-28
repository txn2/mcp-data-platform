//go:build integration

package postgres

import (
	"time"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// SQLSamples renders each statement this package composes at run time, for the
// gate that hands store SQL to a real PostgreSQL to parse and plan (#1512).
//
// Every statement here is built through the query builder, so no string in the
// package is one and nothing in the source could be prepared. These builders,
// called with representative inputs, are what the gate prepares. The inputs
// exercise the shapes that differ structurally -- every filter present, each
// sort column and direction, each breakdown dimension and resolution -- rather
// than one happy path.
//
// The file is integration-tagged, so it is absent from the default build.
func SQLSamples() map[string]string {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	success := true

	full := audit.QueryFilter{
		ID: "e-1", IDs: []string{"e-1", "e-2"},
		StartTime: &start, EndTime: &end,
		UserID: "u-1", SessionID: "s-1", ToolName: "trino_query",
		ToolkitKind: "trino", Source: "mcp", EventKind: "mcp_tool_call",
		Search: "orders", Success: &success,
		SortBy: "tool_name", SortOrder: audit.SortAsc,
		Limit: 50, Offset: 100,
	}
	bare := audit.QueryFilter{}

	metrics := audit.MetricsFilter{
		StartTime: &start, EndTime: &end, UserID: "u-1", EventKind: "mcp_tool_call",
	}

	out := map[string]string{}
	// A builder's error return is unreachable for a well-formed builder, so it
	// is carried into the statement rather than dropped: the gate then fails on
	// this sample with the message in view.
	add := func(name string, build func() (string, []any, error)) {
		query, _, err := build()
		if err != nil {
			query = "builder error: " + err.Error()
		}
		out[name] = query
	}

	add("buildQuery/full", func() (string, []any, error) { return buildQuery(full) })
	add("buildQuery/bare", func() (string, []any, error) { return buildQuery(bare) })
	add("buildCountQuery", func() (string, []any, error) { return buildCountQuery(full) })
	add("buildDistinctQuery/user_id", func() (string, []any, error) {
		return buildDistinctQuery(colUserID, &start, &end)
	})
	add("buildDistinctPairsQuery", func() (string, []any, error) {
		return buildDistinctPairsQuery(colUserID, colUserEmail, &start, &end)
	})
	add("buildOverviewQuery", func() (string, []any, error) { return buildOverviewQuery(metrics) })
	add("buildPerformanceQuery", func() (string, []any, error) { return buildPerformanceQuery(metrics) })
	add("buildEnrichmentQuery", func() (string, []any, error) { return buildEnrichmentQuery(metrics) })

	for res := range audit.ValidResolutions {
		f := audit.TimeseriesFilter{
			Resolution: res, StartTime: &start, EndTime: &end,
			UserID: "u-1", EventKind: "mcp_tool_call",
		}
		add("buildTimeseriesQuery/"+string(res), func() (string, []any, error) { return buildTimeseriesQuery(f) })
	}
	for dim := range audit.ValidBreakdownDimensions {
		f := audit.BreakdownFilter{
			GroupBy: dim, Limit: 10, StartTime: &start, EndTime: &end,
			UserID: "u-1", EventKind: "mcp_tool_call",
		}
		add("buildBreakdownQuery/"+string(dim), func() (string, []any, error) { return buildBreakdownQuery(f) })
	}
	return out
}
