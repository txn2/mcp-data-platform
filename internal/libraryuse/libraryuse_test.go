package libraryuse

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInUseErrorNamesTheScripts(t *testing.T) {
	err := &InUseError{Users: []string{"daily", "weekly"}}
	assert.Equal(t, "this library is loaded by daily, weekly, which would fail at their next run, "+
		"so it was not deleted; change those scripts to stop loading it first", err.Error())
}
