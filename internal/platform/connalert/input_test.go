package connalert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsInput_Validate(t *testing.T) {
	t.Run("an unset window takes the default", func(t *testing.T) {
		in := SettingsInput{Enabled: true}
		require.Empty(t, in.Validate())
		assert.Equal(t, DefaultEscalateAfterHours, in.EscalateAfterHours)
	})

	t.Run("a window outside its bounds is refused", func(t *testing.T) {
		in := SettingsInput{EscalateAfterHours: -1}
		assert.Contains(t, in.Validate(), "escalate_after_hours must be between")

		over := SettingsInput{EscalateAfterHours: maxEscalateAfterHours + 1}
		assert.Contains(t, over.Validate(), "escalate_after_hours must be between")
	})

	t.Run("recipients are normalized and de-duplicated", func(t *testing.T) {
		in := SettingsInput{Recipients: []string{
			"Ops <OPS@Example.com>", "ops@example.com", "oncall@example.com",
		}}
		require.Empty(t, in.Validate())
		assert.Equal(t, []string{"ops@example.com", "oncall@example.com"}, in.Recipients,
			"one person listed in two shapes is one recipient")
	})

	t.Run("an unparseable address is refused", func(t *testing.T) {
		in := SettingsInput{Recipients: []string{"not-an-address"}}
		assert.Contains(t, in.Validate(), "is not a valid email address")
	})

	t.Run("too many recipients are refused", func(t *testing.T) {
		in := SettingsInput{Recipients: make([]string, MaxRecipients+1)}
		assert.Contains(t, in.Validate(), "at most")
	})

	t.Run("the validated input becomes the stored settings", func(t *testing.T) {
		in := SettingsInput{
			Enabled: true, EscalateAfterHours: 6,
			Recipients: []string{"ops@example.com"},
		}
		require.Empty(t, in.Validate())
		assert.Equal(t, Settings{
			Enabled: true, EscalateAfterHours: 6,
			Recipients: []string{"ops@example.com"},
		}, in.Settings())
	})
}

func TestSettings_View(t *testing.T) {
	t.Run("an enabled configuration with nobody to escalate to warns", func(t *testing.T) {
		view := Settings{Enabled: true, EscalateAfterHours: 24}.View()
		assert.Equal(t, []string{NoRecipientsWarning}, view.Warnings)
		assert.Equal(t, []string{}, view.Recipients, "never null, so the editor has one empty state")
	})

	t.Run("a configured escalation does not warn", func(t *testing.T) {
		view := Settings{Enabled: true, Recipients: []string{"ops@example.com"}}.View()
		assert.Empty(t, view.Warnings)
	})

	t.Run("a disabled alert does not warn about recipients it will not use", func(t *testing.T) {
		assert.Empty(t, Settings{Enabled: false}.View().Warnings)
	})

	t.Run("the audit columns are carried through", func(t *testing.T) {
		at := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
		view := Settings{UpdatedBy: "admin@example.com", UpdatedAt: at}.View()
		assert.Equal(t, "admin@example.com", view.UpdatedBy)
		assert.Equal(t, at, view.UpdatedAt)
	})
}

func TestSettings_EscalateAfter(t *testing.T) {
	assert.Equal(t, 6*time.Hour, Settings{EscalateAfterHours: 6}.EscalateAfter())
	assert.Equal(t, DefaultEscalateAfterHours*time.Hour, Settings{}.EscalateAfter(),
		"an unset window is the default, not no window at all")
	assert.Equal(t, DefaultEscalateAfterHours*time.Hour, Settings{EscalateAfterHours: -3}.EscalateAfter())
}
