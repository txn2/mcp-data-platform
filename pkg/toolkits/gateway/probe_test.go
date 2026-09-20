package gateway

import (
	"context"
	"strings"
	"testing"
)

// A connection this toolkit does not serve is reported as a failure naming it,
// rather than as an error a caller has to interpret (#1805).
func TestProbeConnection_UnknownConnection(t *testing.T) {
	res := NewMulti(MultiConfig{}).ProbeConnection(context.Background(), "nope")

	if res.OK {
		t.Fatal("an unknown connection was reported as healthy")
	}
	if !strings.Contains(res.Detail, "did not answer a tools/list") {
		t.Errorf("detail does not say what was attempted: %q", res.Detail)
	}
	if res.Error == "" {
		t.Error("the failure carries no error")
	}
}

// A connection registered with no live client is the shape an OAuth connection
// has before anyone has authorized it: it lists like any other and cannot be
// called, which is precisely the state a test has to report.
func TestProbeConnection_RegisteredButNotLive(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	t.Cleanup(func() { _ = tk.Close() })
	tk.mu.Lock()
	tk.connections["pending"] = &upstream{config: Config{ConnectionName: "pending"}}
	tk.mu.Unlock()

	res := tk.ProbeConnection(context.Background(), "pending")

	if res.OK {
		t.Fatal("a connection with no live client was reported as healthy")
	}
	if !strings.Contains(res.Error, ErrConnectionNotLive.Error()) {
		t.Errorf("the failure does not name the not-live condition: %q", res.Error)
	}
}
