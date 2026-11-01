package toolargs

import (
	"testing"
	"time"
)

// TestDiscoveryTTL_FollowsTheSessionHandle pins #2382: the gates remember a
// search for the life of the platform session handle, not the 30-minute
// transport timeout, and a longer transport timeout still wins.
func TestDiscoveryTTL_FollowsTheSessionHandle(t *testing.T) {
	tests := []struct {
		name      string
		transport time.Duration
		handle    time.Duration
		want      time.Duration
	}{
		{"defaults use the handle lifetime", 0, 0, DefaultSessionHandleTTL},
		{"the 30-minute transport timeout loses", 30 * time.Minute, 0, DefaultSessionHandleTTL},
		{"configured handle lifetime", 0, 2 * time.Hour, 2 * time.Hour},
		{"longer transport timeout wins", 48 * time.Hour, 2 * time.Hour, 48 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (SessionHandles{TTL: tt.handle}).DiscoveryTTL(tt.transport); got != tt.want {
				t.Errorf("DiscoveryTTL(%v) = %v, want %v", tt.transport, got, tt.want)
			}
		})
	}
}
