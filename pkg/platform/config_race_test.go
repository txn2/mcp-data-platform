package platform

import (
	"sync"
	"testing"
)

// TestConfig_RuntimeMutableFields_NoRace asserts that the runtime-mutable
// fields (description overrides + tools.deny) can be read and written
// concurrently without the race detector flagging it. This is what makes
// MCPDescriptionOverrideMiddlewareDynamic safe in production.
//
// Run with: go test -race ./pkg/platform/ -run TestConfig_RuntimeMutableFields_NoRace.
func TestConfig_RuntimeMutableFields_NoRace(_ *testing.T) {
	cfg := &Config{}
	cfg.SetToolDescriptionOverride("trino_query", "v1")
	cfg.SetToolsDeny([]string{"a"})

	var wg sync.WaitGroup
	const goroutines = 50
	const iterations = 200

	// Readers — simulate the description-override middleware getter and
	// admin handlers reading tools.deny on every request.
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				m := cfg.ToolDescriptionOverridesSnapshot()
				_ = m["trino_query"]
				_ = cfg.ToolsDenySnapshot()
				_ = cfg.ToolsAllowSnapshot()
			}
		})
	}

	// Writers — simulate concurrent admin PUTs editing both fields.
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				cfg.ApplyConfigEntry("tool.trino_query.description", "v2")
				cfg.ApplyConfigEntry("tools.deny", `["a","b"]`)
			}
		})
	}

	wg.Wait()
}
