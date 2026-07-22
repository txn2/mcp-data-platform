package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
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
