package platform

import "testing"

func TestNotificationsConfig_IsEnabled(t *testing.T) {
	var cfg NotificationsConfig
	if !cfg.IsEnabled() {
		t.Error("IsEnabled() = false, want true (default on)")
	}
	on := true
	cfg.Enabled = &on
	if !cfg.IsEnabled() {
		t.Error("IsEnabled() = false with explicit true")
	}
	off := false
	cfg.Enabled = &off
	if cfg.IsEnabled() {
		t.Error("IsEnabled() = true with explicit false")
	}
}

func TestNotificationsConfig_DigestHour(t *testing.T) {
	tests := []struct {
		name string
		hour *int
		want int
	}{
		{name: "default", hour: nil, want: DefaultDigestHourUTC},
		{name: "explicit", hour: new(6), want: 6},
		{name: "midnight", hour: new(0), want: 0},
		{name: "too high clamps to default", hour: new(24), want: DefaultDigestHourUTC},
		{name: "negative clamps to default", hour: new(-1), want: DefaultDigestHourUTC},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := NotificationsConfig{DigestHourUTC: tc.hour}
			if got := cfg.DigestHour(); got != tc.want {
				t.Errorf("DigestHour() = %d, want %d", got, tc.want)
			}
		})
	}
}
