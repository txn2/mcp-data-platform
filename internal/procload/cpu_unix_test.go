//go:build unix

package procload

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProcessCPUTime reads this process's own CPU time, which a test process
// has spent some of by the time it runs.
func TestProcessCPUTime(t *testing.T) {
	d, ok := processCPUTime()
	assert.True(t, ok)
	assert.Positive(t, d)
}
