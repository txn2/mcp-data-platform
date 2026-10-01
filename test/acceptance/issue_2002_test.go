//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2002: a failed run's save_state is discarded, and every surface an
// author reads a draft or a test on says so rather than showing the state as
// saved. These criteria draft, test and save a script that saves a watermark
// and then fails, through manage_script on the running platform.
//
// Wire forms: manage_script's command, name and source are typed strings, each
// sent as the one JSON form it admits.

const source2002 = `def main():
    """Saves a watermark, then fails the way a late page does."""
    platform.save_state({"through": run.state.get("through", 0) + 5})
    fail("the last page did not answer")
`

func TestIssue2002_AFailedDraftReportsItsSaveStateAsDiscarded(t *testing.T) {
	c := connect(t)
	ran := c.call("manage_script", map[string]any{
		"command": "run_draft", "name": fmt.Sprintf("acc-2002-draft-%d", time.Now().UnixNano()), "source": source2002,
	})
	if ran["status"] != "failed" {
		t.Fatalf("the draft did not fail: %v", ran)
	}
	if _, saved := ran["state"]; saved {
		t.Errorf("a failed draft reports state %v; a run would save none", ran["state"])
	}
	discarded, _ := ran["state_discarded"].(map[string]any)
	if discarded["through"] != float64(5) {
		t.Errorf("state_discarded = %v, want {through: 5}", ran["state_discarded"])
	}
}

func TestIssue2002_ATestReadsADiscardedStateAsNone(t *testing.T) {
	c := connect(t)
	out := c.call("manage_script", map[string]any{
		"command": "test", "name": fmt.Sprintf("acc-2002-test-%d", time.Now().UnixNano()),
		"source": source2002 + `
def test_the_watermark_is_not_saved():
    """A failed run saves no state."""
    assert.fails(main)
    out = testing.outputs()
    assert.eq(out.state, None)
    assert.eq(out.state_discarded, {"through": 5})
`,
	})
	if first := firstTest1939(t, out); first["passed"] != true {
		t.Fatalf("the test asserting the discarded state did not pass: %v", first)
	}
}

func TestIssue2002_ASaveThatFailsAfterSaveStateIsWarnedNotRefused(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := fmt.Sprintf("acc-2002-save-%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	saved := owner.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2002: fails after save_state.",
		"source": source2002,
	}, nil)
	if !strings.Contains(fmt.Sprint(saved["warnings"]), "save-state-before-fail") {
		t.Errorf("the save does not warn that the failure discards the save_state: %v", saved["warnings"])
	}
}
