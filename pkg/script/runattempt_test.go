package script

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/runstate"
)

// TestRun_Liveness holds #1860: a running run reads as executing only while
// its holder has the lease and reports; a holder silent past
// HeartbeatStaleAfter is unresponsive, and a lease that ran out is expired.
func TestRun_Liveness(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	cases := map[string]struct {
		run  Run
		want string
	}{
		"not running": {Run{Status: RunStatusSucceeded}, ""},
		"no lease":    {Run{Status: RunStatusRunning}, runstate.LivenessLeaseExpired},
		"lease ran out": {
			Run{Status: RunStatusRunning, LockedUntil: at(-time.Second), HeartbeatAt: at(0)}, runstate.LivenessLeaseExpired,
		},
		"reporting": {
			Run{Status: RunStatusRunning, LockedUntil: at(time.Minute), HeartbeatAt: at(-2 * time.Second)}, runstate.LivenessExecuting,
		},
		"silent": {
			Run{Status: RunStatusRunning, LockedUntil: at(time.Minute), HeartbeatAt: at(-time.Minute)}, runstate.LivenessUnresponsive,
		},
		"never reported, claimed long ago": {
			Run{Status: RunStatusRunning, LockedUntil: at(time.Minute), ClaimedAt: at(-time.Minute)}, runstate.LivenessUnresponsive,
		},
		"a row from before claims were recorded": {
			Run{Status: RunStatusRunning, LockedUntil: at(time.Minute), StartedAt: at(-time.Second)}, runstate.LivenessExecuting,
		},
		"nothing to judge by": {Run{Status: RunStatusRunning, LockedUntil: at(time.Minute)}, runstate.LivenessExecuting},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.run.Liveness(now))
		})
	}
}
