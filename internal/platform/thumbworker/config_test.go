package thumbworker

import (
	"testing"

	"github.com/txn2/mcp-data-platform/internal/thumbtypes"
)

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

// The source bounds default to today's when unset, refuse a value below zero,
// and once installed are what the claims and reads apply (#2072).
func TestConfig_SourceLimits(t *testing.T) {
	t.Cleanup(func() { thumbtypes.SetSourceLimits(thumbtypes.Limits{}) })

	got, err := Config{}.SourceLimits()
	if err != nil || got != (thumbtypes.Limits{Default: thumbtypes.DefaultSourceLimit, Large: thumbtypes.LargeSourceLimit}) {
		t.Errorf("unset: %+v, %v", got, err)
	}
	for _, c := range []Config{{MaxSourceBytes: -1}, {LargeSourceBytes: -1}} {
		if err := c.InstallSourceLimits(); err == nil {
			t.Errorf("%+v: a negative bound was accepted", c)
		}
	}
	if err := (Config{MaxSourceBytes: 8 << 20}).InstallSourceLimits(); err != nil {
		t.Fatal(err)
	}
	if got := thumbtypes.SourceLimit("text/html"); got != 8<<20 {
		t.Errorf("installed bound: SourceLimit(text/html) = %d, want %d", got, 8<<20)
	}
}
