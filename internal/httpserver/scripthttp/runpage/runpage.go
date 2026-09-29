// Package runpage is a script's run history read a page at a time (#1972):
// the filter a request's query asks for, and the total the page reports, which
// is every run the filter matches rather than the rows one page holds.
package runpage

import (
	"context"
	"net/url"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Counter counts the runs a filter matches, without the page cap. The
// PostgreSQL run store is one; a store that is not reports what it knows.
type Counter interface {
	CountRuns(ctx context.Context, filter script.RunFilter) (int, error)
}

// Filter is the run history of one script a query asks for: status, live,
// per_page (defaulting to defaultLimit) and the 1-based page.
func Filter(scriptID string, q url.Values, defaultLimit int) script.RunFilter {
	limit := httpjson.ParseLimit(q)
	if limit <= 0 {
		limit = defaultLimit
	}
	return script.RunFilter{
		ScriptID: scriptID,
		Status:   q.Get("status"),
		Live:     q.Get("live") == "true",
		Limit:    limit,
		Offset:   httpjson.ParsePageOffset(q, limit),
	}
}

// Total is every run the filter matches: the store's count when it keeps
// one, and otherwise, or when the count fails, the rows the page holds plus
// the ones it skipped, which is the most that is known.
func Total(ctx context.Context, store any, filter script.RunFilter, rows int) int {
	if c, ok := store.(Counter); ok {
		if n, err := c.CountRuns(ctx, filter); err == nil {
			return n
		}
	}
	return filter.Offset + rows
}
