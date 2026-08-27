package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// The metrics and listing statements are composed through the query builder, so
// they are functions a test can call rather than strings in the source (#1512).
// These assert what each filter puts into the statement; the real-Postgres gate
// asserts the database accepts it.
func TestAuditQueryBuilders(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)

	t.Run("buildQuery orders, limits and offsets as the filter asks", func(t *testing.T) {
		query, args, err := buildQuery(audit.QueryFilter{
			UserID:    "u-1",
			SortBy:    "tool_name",
			SortOrder: audit.SortAsc,
			Limit:     50,
			Offset:    100,
		})
		require.NoError(t, err)
		assert.Contains(t, query, "ORDER BY tool_name ASC")
		assert.Contains(t, query, "LIMIT 50")
		assert.Contains(t, query, "OFFSET 100")
		assert.Contains(t, args, "u-1")
	})

	t.Run("buildQuery defaults to newest-first with no paging", func(t *testing.T) {
		query, _, err := buildQuery(audit.QueryFilter{})
		require.NoError(t, err)
		assert.Contains(t, query, "ORDER BY timestamp DESC")
		assert.NotContains(t, query, "LIMIT")
		assert.NotContains(t, query, "OFFSET")
	})

	// A sort column the caller invents is ignored rather than spliced in.
	t.Run("buildQuery refuses an unlisted sort column", func(t *testing.T) {
		query, _, err := buildQuery(audit.QueryFilter{SortBy: "; DROP TABLE audit_logs"})
		require.NoError(t, err)
		assert.Contains(t, query, "ORDER BY timestamp DESC")
		assert.NotContains(t, query, "DROP TABLE")
	})

	t.Run("buildCountQuery counts over the same filter", func(t *testing.T) {
		query, args, err := buildCountQuery(audit.QueryFilter{UserID: "u-1", ToolName: "trino_query"})
		require.NoError(t, err)
		assert.Contains(t, query, "SELECT COUNT(*)")
		assert.Contains(t, query, "FROM audit_logs")
		assert.Len(t, args, 2)
	})

	t.Run("the distinct lookups bind the time window when given one", func(t *testing.T) {
		query, args, err := buildDistinctQuery(colUserID, &start, &end)
		require.NoError(t, err)
		assert.Contains(t, query, "SELECT DISTINCT user_id")
		assert.Contains(t, query, "ORDER BY user_id")
		assert.Equal(t, []any{start, end}, args)

		pairs, pairArgs, err := buildDistinctPairsQuery(colUserID, colUserEmail, nil, nil)
		require.NoError(t, err)
		assert.Contains(t, pairs, "SELECT DISTINCT user_id, user_email")
		assert.Len(t, pairArgs, 1, "only the non-empty-label predicate binds")
	})

	t.Run("every resolution renders its own bucket expression", func(t *testing.T) {
		for res := range audit.ValidResolutions {
			query, _, err := buildTimeseriesQuery(audit.TimeseriesFilter{
				Resolution: res, StartTime: &start, EndTime: &end,
			})
			require.NoError(t, err)
			assert.Contains(t, query, "date_trunc('"+string(res)+"', timestamp) AS bucket")
			assert.Contains(t, query, "GROUP BY bucket")
		}
	})

	// render is the one place a builder failure is wrapped, and squirrel refuses
	// a SELECT with no result column, which is how that path is reachable.
	t.Run("render names the statement that could not be built", func(t *testing.T) {
		_, _, err := render(psq.Select(), "overview")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "building overview query")
	})

	t.Run("the metrics builders scope to the caller and kind when asked", func(t *testing.T) {
		f := audit.MetricsFilter{StartTime: &start, EndTime: &end, UserID: "u-1", EventKind: "tool_call"}
		for name, build := range map[string]func() (string, []any, error){
			"overview":    func() (string, []any, error) { return buildOverviewQuery(f) },
			"performance": func() (string, []any, error) { return buildPerformanceQuery(f) },
			"enrichment":  func() (string, []any, error) { return buildEnrichmentQuery(f) },
		} {
			query, args, err := build()
			require.NoError(t, err, name)
			assert.Contains(t, query, "FROM audit_logs", name)
			assert.Equal(t, []any{start, end, "u-1", "tool_call"}, args, name)
		}
	})
}
