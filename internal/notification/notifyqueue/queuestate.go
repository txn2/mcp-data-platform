package notifyqueue

import (
	"context"
	"fmt"
	"time"
)

// Transport kinds the queue state is split by: a row addressed to a channel
// destination, and every other row, which is mail to a person.
const (
	KindChannel = "channel"
	KindEmail   = "email"
)

// KindState is the open rows of one transport kind, read on a metrics scrape
// (#1897): due and unclaimed, claimed and sending, scheduled for later (a
// digest window, a retry's backoff), and how long the oldest due row has
// waited. Every replica reads the same rows, so a reader takes the max.
type KindState struct {
	Kind      string
	Pending   int64
	Sending   int64
	Waiting   int64
	OldestDue time.Duration
}

// queueStateQuery groups the open rows by transport. The age is the database
// clock's, the one the claim compares scheduled_for against.
const queueStateQuery = `SELECT
	CASE WHEN ` + channelRowClause + ` THEN 'channel' ELSE 'email' END AS kind,
	COUNT(*) FILTER (WHERE status = 'pending' AND scheduled_for <= NOW()),
	COUNT(*) FILTER (WHERE status = 'sending'),
	COUNT(*) FILTER (WHERE status = 'pending' AND scheduled_for > NOW()),
	COALESCE(EXTRACT(EPOCH FROM NOW() - MIN(scheduled_for)
		FILTER (WHERE status = 'pending' AND scheduled_for <= NOW())), 0)::DOUBLE PRECISION
	FROM notifications
	WHERE status IN ('pending', 'sending')
	GROUP BY 1`

// QueueState reads the open rows by transport kind. A kind with no open row
// is reported with zeros, so its gauges fall to 0 rather than keeping the
// last value a scrape saw.
func (s *PostgresStore) QueueState(ctx context.Context) ([]KindState, error) {
	rows, err := s.db.QueryContext(ctx, queueStateQuery)
	if err != nil {
		return nil, fmt.Errorf("reading the notification queue state: %w", err)
	}
	defer func() { _ = rows.Close() }()
	byKind := map[string]KindState{KindEmail: {Kind: KindEmail}, KindChannel: {Kind: KindChannel}}
	for rows.Next() {
		var k KindState
		var oldest float64
		if err := rows.Scan(&k.Kind, &k.Pending, &k.Sending, &k.Waiting, &oldest); err != nil {
			return nil, fmt.Errorf("scanning the notification queue state: %w", err)
		}
		k.OldestDue = time.Duration(oldest * float64(time.Second))
		byKind[k.Kind] = k
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the notification queue state: %w", err)
	}
	return []KindState{byKind[KindEmail], byKind[KindChannel]}, nil
}
