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
	if !AdminsOnly(TriggerIntegration) {
		t.Error("an integration's run is an administrator's to read (#2659)")
	}
	if !AdminsOnly(TriggerWatch) || AdminsOnly("schedule") {
		t.Error("AdminsOnly reads only a watch's run as an administrator's")
	}
}

// TestCanceledError: a canceled run names who asked, or that it was asked.
func TestCanceledError(t *testing.T) {
	if got := CanceledError("ops@example.com"); got != "canceled by ops@example.com" {
		t.Errorf("CanceledError(who) = %q", got)
	}
	if got := CanceledError(""); got != "canceled on request" {
		t.Errorf("CanceledError(\"\") = %q", got)
	}
	if !AdminsOnly(TriggerIntegration) || !AdminsOnly(TriggerWatch) || AdminsOnly("schedule") {
		t.Error("only watch and integration runs are administrators' alone")
	}
}
