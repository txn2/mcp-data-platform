package trino

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrinoExecuteAnswersAQueryBodyWhenItAnswersJSON(t *testing.T) {
	contracts := (&Toolkit{}).AnswerContracts()
	require.Len(t, contracts, 1)
	c := contracts[0]
	assert.Equal(t, toolExecute, c.Tool)
	assert.Equal(t, "format", c.Arg)
	assert.ElementsMatch(t, []string{"", "json"}, c.Values)
	assert.ElementsMatch(t, []string{"columns", "rows", "row_count", "stats"}, c.Schema.Required)
}
