package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

func TestApplyStatusTransition(t *testing.T) {
	t.Run("no change is a no-op", func(t *testing.T) {
		p := &prompt.Prompt{Status: prompt.StatusDraft}
		assert.Empty(t, applyStatusTransition(p, "", "", "a@x", true))
		assert.Empty(t, applyStatusTransition(p, prompt.StatusDraft, "", "a@x", true))
		assert.Equal(t, prompt.StatusDraft, p.Status)
	})

	t.Run("unknown status is rejected", func(t *testing.T) {
		p := &prompt.Prompt{Status: prompt.StatusDraft}
		assert.Contains(t, applyStatusTransition(p, "bogus", "", "a@x", true), "invalid status")
		assert.Equal(t, prompt.StatusDraft, p.Status)
	})

	t.Run("invalid transition is rejected", func(t *testing.T) {
		p := &prompt.Prompt{Status: prompt.StatusDraft}
		// draft cannot jump straight to deprecated.
		assert.Contains(t, applyStatusTransition(p, prompt.StatusDeprecated, "", "a@x", true), "invalid status transition")
		assert.Equal(t, prompt.StatusDraft, p.Status)
	})

	t.Run("approval is admin-only", func(t *testing.T) {
		p := &prompt.Prompt{Status: prompt.StatusDraft, ReviewRequested: true}
		assert.Contains(t, applyStatusTransition(p, prompt.StatusApproved, "", "u@x", false), "only admins can approve")
		assert.Equal(t, prompt.StatusDraft, p.Status)
	})

	t.Run("admin approval stamps metadata and clears review flag", func(t *testing.T) {
		p := &prompt.Prompt{Status: prompt.StatusDraft, ReviewRequested: true}
		assert.Empty(t, applyStatusTransition(p, prompt.StatusApproved, "", "admin@x", true))
		assert.Equal(t, prompt.StatusApproved, p.Status)
		assert.Equal(t, "admin@x", p.ApprovedBy)
		assert.NotNil(t, p.ApprovedAt)
		assert.False(t, p.ReviewRequested)
	})

	t.Run("deprecate stamps deprecated_at", func(t *testing.T) {
		p := &prompt.Prompt{Status: prompt.StatusApproved}
		assert.Empty(t, applyStatusTransition(p, prompt.StatusDeprecated, "", "admin@x", true))
		assert.Equal(t, prompt.StatusDeprecated, p.Status)
		assert.NotNil(t, p.DeprecatedAt)
	})

	t.Run("supersede records the replacement", func(t *testing.T) {
		p := &prompt.Prompt{Status: prompt.StatusApproved}
		assert.Empty(t, applyStatusTransition(p, prompt.StatusSuperseded, "daily-report-v2", "admin@x", true))
		assert.Equal(t, prompt.StatusSuperseded, p.Status)
		assert.Equal(t, "daily-report-v2", p.SupersededBy)
	})
}

func TestPromptStateMachine(t *testing.T) {
	// Allowed edges.
	for _, e := range [][2]string{
		{prompt.StatusDraft, prompt.StatusApproved},
		{prompt.StatusDraft, prompt.StatusSuperseded},
		{prompt.StatusApproved, prompt.StatusDeprecated},
		{prompt.StatusApproved, prompt.StatusSuperseded},
		{prompt.StatusDeprecated, prompt.StatusSuperseded},
	} {
		assert.NoError(t, prompt.ValidateStatusTransition(e[0], e[1]), "%s->%s should be allowed", e[0], e[1])
	}
	// Disallowed edges.
	for _, e := range [][2]string{
		{prompt.StatusDraft, prompt.StatusDeprecated},
		{prompt.StatusApproved, prompt.StatusDraft},
		{prompt.StatusSuperseded, prompt.StatusApproved},
	} {
		assert.Error(t, prompt.ValidateStatusTransition(e[0], e[1]), "%s->%s should be rejected", e[0], e[1])
	}
}
