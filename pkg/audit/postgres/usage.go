package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Compile-time check: the audit store is the platform's prompt usage reader.
var _ prompt.UsageReader = (*Store)(nil)

// promptUsageQuery inlines the event kind as a literal (it is a compile-time
// constant, not user input) so the predicate textually matches the partial
// index idx_audit_logs_prompt_serve and stays index-eligible even under
// generic query plans, where a bind parameter would not.
// #nosec G202 -- the concatenated value is the compile-time constant
// audit.EventTypePromptServe, not user input; ids bind via $1.
var promptUsageQuery = `
	SELECT parameters->>'prompt_id', COUNT(*), MAX(timestamp)
	  FROM audit_logs
	 WHERE event_kind = '` + string(audit.EventTypePromptServe) + `'
	   AND parameters->>'prompt_id' = ANY($1)
	 GROUP BY 1`

// PromptUsage aggregates prompt_serve audit events into per-prompt run counts
// and last-run timestamps for the given prompt IDs. Prompts never served
// (within the audit retention window) are absent from the returned map. The
// scan is bounded by the partial expression index on
// (parameters->>'prompt_id') WHERE event_kind = 'prompt_serve'
// (migration 000085).
func (s *Store) PromptUsage(ctx context.Context, promptIDs []string) (map[string]prompt.Usage, error) {
	out := make(map[string]prompt.Usage, len(promptIDs))
	if len(promptIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, promptUsageQuery, pq.Array(promptIDs))
	if err != nil {
		return nil, fmt.Errorf("query prompt usage: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id string
		var count int64
		var last time.Time
		if err := rows.Scan(&id, &count, &last); err != nil {
			return nil, fmt.Errorf("scan prompt usage: %w", err)
		}
		lastAt := last
		out[id] = prompt.Usage{RunCount: count, LastRunAt: &lastAt}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompt usage: %w", err)
	}
	return out, nil
}

// Compile-time check: the audit store is the platform's resource usage reader.
var _ resource.UsageReader = (*Store)(nil)

// resourceUsageWindowDays are the trailing windows the resource rollup reports.
// 30 days is the "is this in use" question; 90 days catches the quarterly
// template a 30-day window would call dead.
const (
	resourceUsageShortDays = 30
	resourceUsageLongDays  = 90
)

// resourceUsageQuery aggregates resource_read events per resource, per surface,
// over both windows in one pass. The event kind is inlined as a literal (a
// compile-time constant, not user input) so the predicate textually matches the
// partial index idx_audit_logs_resource_read and stays index-eligible even
// under generic query plans, where a bind parameter would not.
// #nosec G202 -- the concatenated value is the compile-time constant
// audit.EventTypeResourceRead; ids and the window bounds bind via $1..$3.
var resourceUsageQuery = `
	SELECT parameters->>'resource_id',
	       COALESCE(parameters->>'surface', ''),
	       COUNT(*) FILTER (WHERE timestamp >= $2) AS reads_short,
	       COUNT(*) AS reads_long,
	       MAX(timestamp)
	  FROM audit_logs
	 WHERE event_kind = '` + string(audit.EventTypeResourceRead) + `'
	   AND parameters->>'resource_id' = ANY($1)
	   AND timestamp >= $3
	 GROUP BY 1, 2`

// ResourceUsage aggregates resource_read audit events into per-resource read
// counts, per-surface breakdowns, and last-read timestamps for the given
// resource IDs. Resources never read (within the audit retention window) are
// absent from the returned map. Both counts are bounded by that window: a
// deployment retaining 30 days of audit reports the same number for 30 and 90
// days, which is why the durable "last read" answer lives on the resource row.
func (s *Store) ResourceUsage(ctx context.Context, resourceIDs []string) (map[string]resource.Usage, error) {
	out := make(map[string]resource.Usage, len(resourceIDs))
	if len(resourceIDs) == 0 {
		return out, nil
	}
	now := time.Now().UTC()
	shortFrom := now.AddDate(0, 0, -resourceUsageShortDays)
	longFrom := now.AddDate(0, 0, -resourceUsageLongDays)

	rows, err := s.db.QueryContext(ctx, resourceUsageQuery, pq.Array(resourceIDs), shortFrom, longFrom)
	if err != nil {
		return nil, fmt.Errorf("query resource usage: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id, surface           string
			readsShort, readsLong int64
			last                  time.Time
		)
		if err := rows.Scan(&id, &surface, &readsShort, &readsLong, &last); err != nil {
			return nil, fmt.Errorf("scan resource usage: %w", err)
		}
		out[id] = mergeResourceUsage(out[id], surface, readsShort, readsLong, last)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource usage: %w", err)
	}
	return out, nil
}

// mergeResourceUsage folds one (resource, surface) group into the resource's
// running total: the query groups by surface, so a resource read through both
// MCP and the portal arrives as two rows that must sum rather than overwrite.
func mergeResourceUsage(u resource.Usage, surface string, readsShort, readsLong int64, last time.Time) resource.Usage {
	u.Reads30d += readsShort
	u.Reads90d += readsLong
	if readsShort > 0 && surface != "" {
		if u.BySurface30d == nil {
			u.BySurface30d = make(map[string]int64, 3)
		}
		u.BySurface30d[surface] += readsShort
	}
	if u.LastReadAt == nil || last.After(*u.LastReadAt) {
		lastAt := last
		u.LastReadAt = &lastAt
	}
	return u
}
