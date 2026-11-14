package runstate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCauseRetryable(t *testing.T) {
	for cause, want := range map[string]bool{
		CauseUpstream: true, CauseStateConflict: true, CauseTransient: true,
		CauseScript: false, CauseMemory: false, CauseWorkerLost: false, CausePlatform: false, "": false,
	} {
		assert.Equal(t, want, CauseRetryable(cause), cause)
	}
}

// TestAdminsOnly: only a watch's run is an administrator's to read in full.
func TestAdminsOnly(t *testing.T) {
	if !AdminsOnly(TriggerWatch) || AdminsOnly("schedule") {
		t.Error("AdminsOnly reads only a watch's run as an administrator's")
	}
}
