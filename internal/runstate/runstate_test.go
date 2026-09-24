package runstate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCauseRetryable(t *testing.T) {
	for cause, want := range map[string]bool{
		CauseUpstream: true, CauseStateConflict: true,
		CauseScript: false, CauseMemory: false, CauseWorkerLost: false, CausePlatform: false, "": false,
	} {
		assert.Equal(t, want, CauseRetryable(cause), cause)
	}
}
