package instructions

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPurposeNote(t *testing.T) {
	gated := []string{"search", "trino_query", "s3_object"}
	kinds := []string{"mcp"}

	t.Run("states the contract and what a good purpose says", func(t *testing.T) {
		note := PurposeNote(false, gated, kinds)
		assert.Contains(t, note, "`purpose` argument")
		assert.Contains(t, note, "one sentence")
		assert.Contains(t, note, "never put personal data, credentials, or secrets")
	})

	t.Run("names the gated tools rather than a category", func(t *testing.T) {
		note := PurposeNote(false, gated, kinds)
		for _, tool := range gated {
			assert.Contains(t, note, tool,
				"a model cannot tell which tools are gated unless the note names them")
		}
		assert.NotContains(t, note, "manage_table",
			"a tool outside the gate is not named as taking the argument")
	})

	t.Run("a wholesale-gated kind is named as the kind", func(t *testing.T) {
		// An upstream MCP server proxies more tools than the platform's own
		// data-access surface has, and their names change when the upstream does.
		note := PurposeNote(false, gated, kinds)
		assert.Contains(t, note, "every tool served by a connection of kind `mcp`")
	})

	t.Run("a deployment with no gated kind says nothing about kinds", func(t *testing.T) {
		note := PurposeNote(false, gated, nil)
		assert.NotContains(t, note, "connection of kind")
		assert.Contains(t, note, "search, trino_query, s3_object. Write one sentence",
			"the tool list still terminates cleanly with no kind clause after it")
	})

	t.Run("a set that gates only kinds names only them", func(t *testing.T) {
		note := PurposeNote(false, nil, kinds)
		assert.Contains(t, note, "argument: every tool served by a connection of kind `mcp`.")
	})

	t.Run("says a purpose off the gate is tolerated", func(t *testing.T) {
		note := PurposeNote(true, gated, kinds)
		assert.Contains(t, note, "asks for one only on those")
		assert.Contains(t, note, "accepted and recorded rather than refused",
			"the note must not leave an agent to guess that stating one elsewhere fails")
	})

	t.Run("names the refusal only when it can happen", func(t *testing.T) {
		assert.Contains(t, PurposeNote(true, gated, kinds), "PURPOSE_REQUIRED",
			"a required deployment tells the agent what omitting it costs")
		assert.NotContains(t, PurposeNote(false, gated, kinds), "PURPOSE_REQUIRED",
			"a record-only deployment must not threaten a refusal that never comes")
	})

	t.Run("is a bulleted note under one heading", func(t *testing.T) {
		lines := strings.Split(PurposeNote(true, gated, kinds), "\n")
		assert.Equal(t, "Stating why you are calling:", lines[0])
		assert.Greater(t, len(lines), 3)
	})
}
