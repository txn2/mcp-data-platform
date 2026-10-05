package openrun

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestOpenRunDescribe pins the words a refused run and a skipped fire name the
// open run in (#1986): its id, what started it, and since when.
func TestRunDescribe(t *testing.T) {
	at := time.Date(2026, 10, 4, 9, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		open Run
		want string
	}{
		{Run{ID: "r1", Trigger: script.TriggerTool, StartedAt: &at}, "run r1 (started by run_script, running since 2026-10-04T09:30:00Z)"},
		{Run{ID: "r2", Trigger: script.TriggerSchedule, CreatedAt: at}, "run r2 (started by its schedule, queued since 2026-10-04T09:30:00Z)"},
		{Run{ID: "r3", Trigger: script.TriggerPortal, StartedAt: &at}, "run r3 (started by the portal, running since 2026-10-04T09:30:00Z)"},
		{Run{ID: "r4", Trigger: "webhook", CreatedAt: at}, "run r4 (started by webhook, queued since 2026-10-04T09:30:00Z)"},
	} {
		assert.Equal(t, tc.want, tc.open.Describe())
	}
}

// TestError names the run, says nothing was queued, and matches the
// sentinel.
func TestError(t *testing.T) {
	err := error(&Error{Open: Run{ID: "r1", Trigger: script.TriggerTool, CreatedAt: time.Unix(0, 0)}})
	assert.True(t, errors.Is(err, ErrOpen))
	assert.Contains(t, err.Error(), "this script runs one at a time and run r1 (started by run_script")
	assert.Contains(t, err.Error(), "nothing was queued")
}
