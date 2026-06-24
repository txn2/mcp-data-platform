package dedup

import "testing"

func TestConfig_IsEnabled(t *testing.T) {
	if !(&Config{}).IsEnabled() {
		t.Error("nil Enabled should default to true")
	}
	on, off := true, false
	if !(&Config{Enabled: &on}).IsEnabled() {
		t.Error("Enabled=true should be enabled")
	}
	if (&Config{Enabled: &off}).IsEnabled() {
		t.Error("Enabled=false should be disabled")
	}
}

func TestConfig_EffectiveMode(t *testing.T) {
	if got := (&Config{}).EffectiveMode(); got != "reference" {
		t.Errorf("empty Mode = %q, want reference", got)
	}
	if got := (&Config{Mode: "summary"}).EffectiveMode(); got != "summary" {
		t.Errorf("Mode = %q, want summary", got)
	}
}
