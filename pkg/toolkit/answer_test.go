package toolkit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContractForKeepsTheRequiredFields(t *testing.T) {
	type body struct {
		ID   string `json:"id"`
		Note string `json:"note,omitempty"`
	}
	c := ContractFor[body]("tool", "action", "make")
	assert.Equal(t, "tool", c.Tool)
	assert.Equal(t, "action", c.Arg)
	assert.Equal(t, []string{"make"}, c.Values)
	require.NotNil(t, c.Schema)
	assert.Equal(t, []string{"id"}, c.Schema.Required)
	assert.Contains(t, c.Schema.Properties, "note")
}

func TestContractForPanicsOnAnUnreflectableType(t *testing.T) {
	assert.Panics(t, func() { ContractFor[func()]("tool", "") })
}
