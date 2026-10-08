package platformstate

import (
	"cmp"
	"context"
	"database/sql"
	"log/slog"
	"slices"
	"time"

	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// connectionStates counts every connection of every toolkit that lists them
// by the state the last call through it found it in (internal/connstate). A
// connection no call has used on this replica is configured. Nothing is sent
// to an upstream to find out.
func connectionStates(reg *registry.Registry) []observability.ConnectionStateCount {
	if reg == nil {
		return nil
	}
	type key struct{ kind, state string }
	counts := map[key]int64{}
	for _, tk := range reg.All() {
		lister, ok := tk.(toolkit.ConnectionLister)
		if !ok {
			continue
		}
		kind := tk.Kind()
		for _, c := range lister.ListConnections() {
			state, seen := connstate.State(kind, c.Name)
			if !seen {
				state = opsobs.ConnectionConfigured
			}
			counts[key{kind, state}]++
		}
	}
	out := make([]observability.ConnectionStateCount, 0, len(counts))
	for k, n := range counts {
		out = append(out, observability.ConnectionStateCount{Kind: k.kind, State: k.state, Count: n})
	}
	return out
}

// indexAgesQuery reads, per source kind, how long ago its newest successful
// index pass finished.
const indexAgesQuery = `SELECT source_kind, EXTRACT(EPOCH FROM (NOW() - MAX(completed_at)))::float8
FROM index_jobs
WHERE status = 'succeeded' AND completed_at IS NOT NULL
GROUP BY source_kind`

// indexAges reads the search-index ages; nil without a database or on a read
// failure, which is logged.
func indexAges(ctx context.Context, db *sql.DB) []observability.IndexAgeSample {
	if db == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, indexAgesQuery)
	if err != nil {
		slog.WarnContext(ctx, "platform state: reading search index ages failed", "error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []observability.IndexAgeSample
	for rows.Next() {
		var (
			kind    string
			seconds float64
		)
		if err := rows.Scan(&kind, &seconds); err != nil {
			slog.WarnContext(ctx, "platform state: reading search index ages failed", "error", logsan.SanitizeForLog(err.Error()))
			return nil
		}
		out = append(out, observability.IndexAgeSample{Kind: kind, Age: time.Duration(seconds * float64(time.Second))})
	}
	if err := rows.Err(); err != nil {
		slog.WarnContext(ctx, "platform state: reading search index ages failed", "error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	return out
}

// sortSample orders a sample so successive scrapes list series identically.
func sortSample(s *observability.PlatformStateSample) {
	slices.SortFunc(s.Dependencies, func(a, b observability.DependencySample) int {
		return cmp.Compare(a.Dependency, b.Dependency)
	})
	slices.SortFunc(s.Connections, func(a, b observability.ConnectionStateCount) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.State, b.State))
	})
	slices.SortFunc(s.IndexAges, func(a, b observability.IndexAgeSample) int {
		return cmp.Compare(a.Kind, b.Kind)
	})
}
