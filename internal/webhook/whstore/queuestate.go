package whstore

import (
	"context"
	"fmt"
	"time"
)

// CompactionState is the compactor's backlog as the database holds it, read
// on a metrics scrape (#1897): windows that have ended and are owed a
// compaction, windows a compactor holds now, windows still receiving events
// (owed one once they end), and how long ago the oldest owed window ended.
// Every replica reads the same rows, so a reader takes the max.
type CompactionState struct {
	Pending    int64
	Running    int64
	Waiting    int64
	OldestOwed time.Duration
}

// compactionStateQuery counts the windows owed a compaction by where they
// stand. A window is owed one when its generation moved past the one last
// compacted and it has not expired.
const compactionStateQuery = `SELECT
	COUNT(*) FILTER (WHERE window_start + make_interval(secs => window_seconds) <= NOW()
		AND (claimed_until IS NULL OR claimed_until < NOW())),
	COUNT(*) FILTER (WHERE claimed_until >= NOW()),
	COUNT(*) FILTER (WHERE window_start + make_interval(secs => window_seconds) > NOW()
		AND (claimed_until IS NULL OR claimed_until < NOW())),
	COALESCE(EXTRACT(EPOCH FROM NOW() - MIN(window_start + make_interval(secs => window_seconds))
		FILTER (WHERE window_start + make_interval(secs => window_seconds) <= NOW())), 0)::DOUBLE PRECISION
	FROM webhook_windows
	WHERE generation <> compacted_generation AND expired_at IS NULL`

// CompactionState reads the compactor's backlog.
func (s *Store) CompactionState(ctx context.Context) (CompactionState, error) {
	var c CompactionState
	var oldest float64
	if err := s.db.QueryRowContext(ctx, compactionStateQuery).Scan(&c.Pending, &c.Running, &c.Waiting, &oldest); err != nil {
		return CompactionState{}, fmt.Errorf("reading the webhook compaction backlog: %w", err)
	}
	c.OldestOwed = time.Duration(oldest * float64(time.Second))
	return c, nil
}
