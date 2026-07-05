package platform

import (
	"testing"
	"time"
)

func TestSessionHandlesConfig_Defaults(t *testing.T) {
	var c SessionHandlesConfig // zero value

	if !c.IsEnabled() {
		t.Error("IsEnabled() = false, want true by default")
	}
	if !c.IsRequired() {
		t.Error("IsRequired() = false, want true by default")
	}
	if got := c.HandleTTL(); got != defaultSessionHandleTTL {
		t.Errorf("HandleTTL() = %v, want %v", got, defaultSessionHandleTTL)
	}
}

func TestSessionHandlesConfig_Overrides(t *testing.T) {
	off := false
	c := SessionHandlesConfig{
		Enabled: &off,
		Require: &off,
		TTL:     2 * time.Hour,
	}
	if c.IsEnabled() {
		t.Error("IsEnabled() = true, want false when explicitly disabled")
	}
	if c.IsRequired() {
		t.Error("IsRequired() = true, want false when explicitly disabled")
	}
	if got := c.HandleTTL(); got != 2*time.Hour {
		t.Errorf("HandleTTL() = %v, want 2h", got)
	}
}

func TestSessionHandlesConfig_NonPositiveTTLFallsBack(t *testing.T) {
	c := SessionHandlesConfig{TTL: -1 * time.Second}
	if got := c.HandleTTL(); got != defaultSessionHandleTTL {
		t.Errorf("HandleTTL() = %v, want default %v for non-positive TTL", got, defaultSessionHandleTTL)
	}
}
