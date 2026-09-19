package thumbworker

import "testing"

// Drawing tiles is on unless a deployment turns it off, and with no address
// the renderer is looked for beside the platform in its own pod.
func TestConfig_DefaultsOnBesideThePlatform(t *testing.T) {
	off, on := false, true
	for _, tt := range []struct {
		name    string
		cfg     Config
		enabled bool
		url     string
	}{
		{"no section", Config{}, true, DefaultRendererURL},
		{"explicitly on", Config{Enabled: &on}, true, DefaultRendererURL},
		{"explicitly off", Config{Enabled: &off}, false, DefaultRendererURL},
		{"a renderer elsewhere", Config{RendererURL: "ws://renderer:9222"}, true, "ws://renderer:9222"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsEnabled(); got != tt.enabled {
				t.Errorf("IsEnabled = %v, want %v", got, tt.enabled)
			}
			if got := tt.cfg.EffectiveRendererURL(); got != tt.url {
				t.Errorf("EffectiveRendererURL = %q, want %q", got, tt.url)
			}
		})
	}
}
