//go:build integration

package acceptance

import (
	"fmt"
	"testing"
	"time"
)

// Issue #2003: platform.checkpoint stages state the platform commits however
// the run ends. These criteria save scripts through manage_script, run them
// with run_script on the dev stack's run worker, and read the run, the state
// and the failing-automation briefing back.
//
// Wire forms: manage_script's command, name, source, state_action and run_id,
// and run_script's name, are typed strings, and wait_seconds a typed integer;
// each is sent as the one JSON form it admits.

const source2003Fails = `def main():
    """Gets through two pages, then the third does not answer."""
    platform.checkpoint({"through": 1})
    platform.checkpoint({"through": 2})
    fail("page 3 did not answer")
`

// saved2003 saves source as the owner's script and returns its name.
func saved2003(t *testing.T, c *client, label, source string) string {
	t.Helper()
	name := fmt.Sprintf("acc-2003-%s-%d", label, time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2003: " + label + ".", "source": source,
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return name
}

// state2003 is the script's state as the owner reads it.
func state2003(c *client, name string) map[string]any {
	got := c.call("manage_script", map[string]any{"command": "state", "name": name, "state_action": "get"})
	state, _ := got["state"].(map[string]any)
	return state
}

func TestIssue2003_AFailedRunCommitsItsLastCheckpoint(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := saved2003(t, owner, "fails", source2003Fails)

	run := owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if run["status"] != "failed" {
		t.Fatalf("the run did not fail: %v", run)
	}
	if state := state2003(owner, name); state["through"] != float64(2) {
		t.Errorf("the script's state is %v, want the last checkpoint {through: 2}", state)
	}
	got := owner.call("manage_script", map[string]any{"command": "get_run", "name": name, "run_id": run["run_id"]})
	if got["state_checkpoint"] != true {
		t.Errorf("get_run does not mark the saved state as the checkpoint: %v", got)
	}
	written, _ := got["state_written"].(map[string]any)
	if written["through"] != float64(2) {
		t.Errorf("get_run state_written = %v, want {through: 2}", got["state_written"])
	}
	notice := issue1934Failing(connectAs(t, devOwnerAPIKey).info, name)
	checkpoint, _ := notice["checkpoint"].(map[string]any)
	if checkpoint["through"] != float64(2) {
		t.Errorf("the failing automation's briefing does not carry the checkpoint: %v", notice)
	}
}

func TestIssue2003_ASaveStateReplacesTheCheckpointOnSuccess(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := saved2003(t, owner, "saves", `def main():
    """Checkpoints, then saves the final watermark."""
    start = run.state.get("through", 0)
    platform.checkpoint({"through": start + 1})
    platform.save_state({"through": start + 9})
`)
	run := owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if run["status"] != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	if state := state2003(owner, name); state["through"] != float64(9) {
		t.Errorf("the script's state is %v, want the save_state {through: 9}", state)
	}
	got := owner.call("manage_script", map[string]any{"command": "get_run", "name": name, "run_id": run["run_id"]})
	if _, marked := got["state_checkpoint"]; marked {
		t.Errorf("a save_state is reported as a checkpoint: %v", got)
	}
}

func TestIssue2003_ASuccessfulRunWithoutSaveStateCommitsItsCheckpoint(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := saved2003(t, owner, "checkpoints", `def main():
    """Checkpoints and finishes."""
    platform.checkpoint({"through": 4})
`)
	run := owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if run["status"] != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	if state := state2003(owner, name); state["through"] != float64(4) {
		t.Errorf("the script's state is %v, want its checkpoint {through: 4}", state)
	}
}
