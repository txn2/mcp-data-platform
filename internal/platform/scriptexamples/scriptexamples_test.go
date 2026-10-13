package scriptexamples

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFieldsMarksTheExampleBuiltin(t *testing.T) {
	got := Example{Name: "example-daily-sales", Description: "Daily sales.", Source: "def main():\n    pass\n"}.Fields()
	assert.Equal(t, "example-daily-sales", got["name"])
	assert.Equal(t, true, got["builtin"])
	assert.Contains(t, got["message"], "not a stored script")
}
