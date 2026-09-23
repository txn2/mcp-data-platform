package script

import (
	"time"

	"github.com/txn2/mcp-data-platform/internal/runstate"
)

// Liveness reports what a running run's holder is doing at now, and empty for
// a run that is not running.
func (r *Run) Liveness(now time.Time) string {
	if r.Status != RunStatusRunning {
		return ""
	}
	if r.LockedUntil == nil || r.LockedUntil.Before(now) {
		return runstate.LivenessLeaseExpired
	}
	if seen := r.lastSeen(); seen != nil && now.Sub(*seen) > runstate.HeartbeatStaleAfter {
		return runstate.LivenessUnresponsive
	}
	return runstate.LivenessExecuting
}

// lastSeen is the last time the run's current holder is known to have been
// alive: its last report, or the claim when it has made none.
func (r *Run) lastSeen() *time.Time {
	switch {
	case r.HeartbeatAt != nil:
		return r.HeartbeatAt
	case r.ClaimedAt != nil:
		return r.ClaimedAt
	default:
		return r.StartedAt
	}
}
