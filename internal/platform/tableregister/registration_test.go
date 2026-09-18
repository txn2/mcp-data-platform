package tableregister

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFailedf covers what a platform failure carries: the whole error for the
// log and the audit event, and the stage on its own for the caller (#1775).
func TestFailedf(t *testing.T) {
	cause := errors.New("context deadline exceeded")
	err := failedf("reading the file", cause)

	assert.EqualError(t, err, "reading the file: context deadline exceeded")
	assert.Equal(t, "reading the file", StageOf(err))
	assert.ErrorIs(t, err, cause, "the cause stays reachable")

	assert.Equal(t, "reading the file", StageOf(fmt.Errorf("wrapped: %w", err)),
		"and is found through a wrap")
	assert.Empty(t, StageOf(cause), "an error that is not one names no stage")
	assert.Empty(t, StageOf(nil))
}
