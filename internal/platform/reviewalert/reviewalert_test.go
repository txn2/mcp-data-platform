package reviewalert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultSettings(t *testing.T) {
	s := KnowledgeTarget().DefaultSettings()
	assert.True(t, s.Enabled, "the check is on before an operator configures it")
	assert.Equal(t, DefaultKnowledgeOldestDays, s.OldestPendingDays)
	assert.Equal(t, DefaultCooldownHours, s.CooldownHours)
	assert.Empty(t, s.Recipients, "defaults name nobody; the operator supplies the list")
	assert.False(t, s.Deliverable(), "with no recipients the default configuration delivers nothing")
}

func TestSettingsCooldown(t *testing.T) {
	tests := []struct {
		name  string
		hours int
		want  time.Duration
	}{
		{"configured", 6, 6 * time.Hour},
		{"zero falls back to the default", 0, DefaultCooldownHours * time.Hour},
		{"negative falls back to the default", -4, DefaultCooldownHours * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Settings{CooldownHours: tt.hours}.Cooldown())
		})
	}
}

func TestSettingsCrossed(t *testing.T) {
	tests := []struct {
		name          string
		settings      Settings
		pending       int
		oldestAgeDays int
		want          bool
	}{
		{
			name:     "empty queue never crosses",
			settings: Settings{PendingThreshold: 1, OldestPendingDays: 1},
			want:     false,
		},
		{
			name:     "count at the threshold crosses",
			settings: Settings{PendingThreshold: 10},
			pending:  10,
			want:     true,
		},
		{
			name:     "count below the threshold does not",
			settings: Settings{PendingThreshold: 10},
			pending:  9,
			want:     false,
		},
		{
			name:          "age at the threshold crosses",
			settings:      Settings{OldestPendingDays: 30},
			pending:       1,
			oldestAgeDays: 30,
			want:          true,
		},
		{
			name:          "age below the threshold does not",
			settings:      Settings{OldestPendingDays: 30},
			pending:       1,
			oldestAgeDays: 29,
			want:          false,
		},
		{
			name:          "either condition alone is enough",
			settings:      Settings{PendingThreshold: 100, OldestPendingDays: 30},
			pending:       2,
			oldestAgeDays: 45,
			want:          true,
		},
		{
			name:          "a zero threshold disables its condition",
			settings:      Settings{PendingThreshold: 0, OldestPendingDays: 0},
			pending:       10_000,
			oldestAgeDays: 900,
			want:          false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.settings.Crossed(tt.pending, tt.oldestAgeDays))
		})
	}
}

func TestSettingsDeliverable(t *testing.T) {
	full := Settings{Enabled: true, OldestPendingDays: 30, Recipients: []string{"ops@example.com"}}
	assert.True(t, full.Deliverable())

	off := full
	off.Enabled = false
	assert.False(t, off.Deliverable(), "a disabled check delivers nothing")

	nobody := full
	nobody.Recipients = nil
	assert.False(t, nobody.Deliverable(), "an empty recipient list delivers nothing")

	noThreshold := full
	noThreshold.OldestPendingDays = 0
	assert.False(t, noThreshold.Deliverable(), "with both thresholds cleared nothing can cross")
}
