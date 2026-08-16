package instructions

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPurposeNote(t *testing.T) {
	t.Run("states the contract and what a good purpose says", func(t *testing.T) {
		note := PurposeNote(false)
		assert.Contains(t, note, "`purpose` argument")
		assert.Contains(t, note, "one sentence")
		assert.Contains(t, note, "never put personal data, credentials, or secrets")
	})

	t.Run("names the refusal only when it can happen", func(t *testing.T) {
		assert.Contains(t, PurposeNote(true), "PURPOSE_REQUIRED",
			"a required deployment tells the agent what omitting it costs")
		assert.NotContains(t, PurposeNote(false), "PURPOSE_REQUIRED",
			"a record-only deployment must not threaten a refusal that never comes")
	})

	t.Run("is a bulleted note under one heading", func(t *testing.T) {
		lines := strings.Split(PurposeNote(true), "\n")
		assert.Equal(t, "Stating why you are calling:", lines[0])
		assert.Greater(t, len(lines), 3)
	})
}
