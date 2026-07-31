package reviewalert

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsInputValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      SettingsInput
		wantMsg string
		assert  func(*testing.T, SettingsInput)
	}{
		{
			name: "valid input normalizes recipients",
			in: SettingsInput{
				Enabled: true, OldestPendingDays: 30, CooldownHours: 12,
				Recipients: []string{"Ops Lead <Ops@Example.com>", " second@example.com "},
			},
			assert: func(t *testing.T, in SettingsInput) {
				t.Helper()
				assert.Equal(t, []string{"ops@example.com", "second@example.com"}, in.Recipients)
			},
		},
		{
			name: "duplicate addresses in different shapes collapse",
			in: SettingsInput{
				Recipients: []string{"ops@example.com", "Ops Lead <OPS@example.com>"},
			},
			assert: func(t *testing.T, in SettingsInput) {
				t.Helper()
				assert.Equal(t, []string{"ops@example.com"}, in.Recipients,
					"one person listed twice must not be mailed twice")
			},
		},
		{
			name: "omitted cooldown takes the default",
			in:   SettingsInput{Enabled: true, OldestPendingDays: 30},
			assert: func(t *testing.T, in SettingsInput) {
				t.Helper()
				assert.Equal(t, DefaultCooldownHours, in.CooldownHours)
			},
		},
		{
			name:    "invalid recipient is refused",
			in:      SettingsInput{Recipients: []string{"not-an-address"}},
			wantMsg: `"not-an-address" is not a valid email address`,
		},
		{
			name:    "negative pending threshold is refused",
			in:      SettingsInput{PendingThreshold: -1},
			wantMsg: "pending_threshold must be between 0 and 1000000",
		},
		{
			name:    "absurd pending threshold is refused",
			in:      SettingsInput{PendingThreshold: maxPendingThreshold + 1},
			wantMsg: "pending_threshold must be between 0 and 1000000",
		},
		{
			name:    "absurd age threshold is refused",
			in:      SettingsInput{OldestPendingDays: maxOldestPendingDays + 1},
			wantMsg: "oldest_pending_days must be between 0 and 3650",
		},
		{
			name:    "cooldown below one hour is refused",
			in:      SettingsInput{CooldownHours: -1},
			wantMsg: "cooldown_hours must be between 1 and 720",
		},
		{
			name:    "cooldown above the bound is refused",
			in:      SettingsInput{CooldownHours: maxCooldownHours + 1},
			wantMsg: "cooldown_hours must be between 1 and 720",
		},
		{
			name:    "an oversized recipient list is refused",
			in:      SettingsInput{Recipients: make([]string, MaxRecipients+1)},
			wantMsg: "at most 20 recipients may be configured",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			msg := in.Validate()
			assert.Equal(t, tt.wantMsg, msg)
			if tt.assert != nil {
				tt.assert(t, in)
			}
		})
	}
}

func TestSettingsInputSettings(t *testing.T) {
	in := SettingsInput{
		Enabled: true, PendingThreshold: 25, OldestPendingDays: 14,
		CooldownHours: 6, Recipients: []string{"ops@example.com"},
	}
	require.Empty(t, in.Validate())

	s := in.Settings()
	assert.True(t, s.Enabled)
	assert.Equal(t, 25, s.PendingThreshold)
	assert.Equal(t, 14, s.OldestPendingDays)
	assert.Equal(t, 6, s.CooldownHours)
	assert.Equal(t, []string{"ops@example.com"}, s.Recipients)
}

func TestSettingsView(t *testing.T) {
	t.Run("carries the stored values and audit columns", func(t *testing.T) {
		updated := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
		v := Settings{
			Enabled: true, PendingThreshold: 25, OldestPendingDays: 30, CooldownHours: 24,
			Recipients: []string{"ops@example.com"},
			UpdatedBy:  "admin@example.com", UpdatedAt: updated,
		}.View()

		assert.True(t, v.Enabled)
		assert.Equal(t, 25, v.PendingThreshold)
		assert.Equal(t, []string{"ops@example.com"}, v.Recipients)
		assert.Equal(t, "admin@example.com", v.UpdatedBy)
		assert.Equal(t, updated, v.UpdatedAt)
		assert.Empty(t, v.Warnings, "a deliverable configuration warns about nothing")
	})

	t.Run("an unset recipient list serializes as an empty array", func(t *testing.T) {
		v := Settings{}.View()
		require.NotNil(t, v.Recipients)
		assert.Empty(t, v.Recipients)
	})

	t.Run("enabled with no recipients warns", func(t *testing.T) {
		v := Settings{Enabled: true, OldestPendingDays: 30}.View()
		assert.Equal(t, []string{NoRecipientsWarning}, v.Warnings)
	})

	t.Run("enabled with no threshold warns", func(t *testing.T) {
		v := Settings{Enabled: true, Recipients: []string{"ops@example.com"}}.View()
		assert.Equal(t, []string{NoThresholdWarning}, v.Warnings)
	})

	t.Run("both gaps warn together", func(t *testing.T) {
		v := Settings{Enabled: true}.View()
		assert.Len(t, v.Warnings, 2)
		assert.True(t, strings.Contains(strings.Join(v.Warnings, " "), "recipients"))
	})

	t.Run("a disabled check warns about nothing", func(t *testing.T) {
		assert.Empty(t, Settings{}.View().Warnings,
			"an operator who turned the check off is not missing a recipient list")
	})
}
