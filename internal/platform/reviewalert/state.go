package reviewalert

import (
	"context"
	"fmt"
	"time"
)

// StateStore is the re-alert marker half of one queue's persistence: whether
// that queue is currently over threshold and when it was last alerted about.
//
// The claim is the whole de-duplication mechanism. It is one conditional
// write, so it answers both questions a scheduled alert has to answer -- "has
// this already been sent recently?" and "is another replica sending it right
// now?" -- without a second coordination primitive.
type StateStore interface {
	// ClaimAlert stamps a new alert at now and reports whether this caller
	// won it. It loses when an alert for the same continuously-over-threshold
	// stretch was stamped less than cooldown ago, which is what keeps a
	// stale queue from mailing on every check.
	ClaimAlert(ctx context.Context, cooldown time.Duration, now time.Time) (bool, error)
	// Clear drops the over-threshold marker. The next crossing then alerts
	// immediately instead of serving out a cooldown that belongs to a queue
	// which has since been worked.
	Clear(ctx context.Context) error
}

// claimSQL stamps the alert for one queue only when no alert is outstanding
// (the queue was under threshold at the last check) or the cooldown has
// elapsed. It inserts the row on first use, so the state cannot be lost by a
// deployment that truncated the table.
//
// The queue key is part of the conflict target, so two queues alerting in the
// same window never contend: one queue's cooldown must not silence another's.
const claimSQL = `
INSERT INTO review_alert_state (queue, alerting, last_alert_at)
VALUES ($1, TRUE, $2)
ON CONFLICT (queue) DO UPDATE
   SET alerting = TRUE, last_alert_at = EXCLUDED.last_alert_at
 WHERE NOT review_alert_state.alerting
    OR review_alert_state.last_alert_at IS NULL
    OR review_alert_state.last_alert_at <= $3`

// ClaimAlert stamps a new alert when the cooldown allows, reporting whether
// this caller won the claim.
func (s *PostgresStore) ClaimAlert(ctx context.Context, cooldown time.Duration, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, claimSQL, s.target.Queue, now.UTC(), now.UTC().Add(-cooldown))
	if err != nil {
		return false, fmt.Errorf("claiming %s alert: %w", s.target.Queue, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("reading %s alert claim result: %w", s.target.Queue, err)
	}
	return affected > 0, nil
}

// Clear drops the over-threshold marker, keeping last_alert_at as the record
// of when the last alert went out.
func (s *PostgresStore) Clear(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE review_alert_state SET alerting = FALSE WHERE queue = $1 AND alerting`,
		s.target.Queue)
	if err != nil {
		return fmt.Errorf("clearing %s alert state: %w", s.target.Queue, err)
	}
	return nil
}

// Verify interface compliance.
var _ StateStore = (*PostgresStore)(nil)
