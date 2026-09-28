//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1937: every save stores the script in one canonical format, so what
// an agent reads back is what runs and saving it again changes nothing. These
// criteria save through manage_script on the running platform and read the
// stored source back with get.
//
// Wire forms: manage_script's command, name, description and source and
// run_script's name are typed strings, and run_script's wait_seconds an
// integer, so each admits one JSON form and is sent as a literal tools/call
// parameter of it.

// untidy1937 has irregular spacing, key=value keyword arguments, single
// quotes, a multi-line list written with two items to a line, and comments.
const untidy1937 = `# The regions report.
REGIONS = ['west','east',
   'north']  # every region we sell in

def main( ):
  '''Exports one row per region.'''
  rows=[{'region':r,'n':len(r)} for r in REGIONS]
  platform.export(name='regions',rows=rows,format='csv')
  platform.result({'regions':len(rows)})
`

// tidy1937 is untidy1937 as the formatter stores it.
const tidy1937 = `# The regions report.
REGIONS = [
    "west",
    "east",
    "north",
]  # every region we sell in

def main():
    """Exports one row per region."""
    rows = [{"region": r, "n": len(r)} for r in REGIONS]
    platform.export(name = "regions", rows = rows, format = "csv")
    platform.result({"regions": len(rows)})
`

// TestIssue1937_ASaveStoresTheFormattedSource: the stored version is the
// formatted source with every comment kept, and run_script on it produces what
// the source as sent produced under run_draft.
func TestIssue1937_ASaveStoresTheFormattedSource(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1937-%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	checked := c.call("manage_script", map[string]any{"command": "validate", "name": name, "source": untidy1937})
	if checked["formatted_source"] != tidy1937 {
		t.Errorf("validate formatted_source:\n%v", checked["formatted_source"])
	}
	draft := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": untidy1937})

	created := c.saveScript(map[string]any{
		"command": "create", "name": name, "source": untidy1937,
		"description": "Acceptance #1937: stored in the canonical format.",
	}, nil)
	if created["status"] != "created" || created["source_formatted"] != true {
		t.Fatalf("create: %v", created)
	}
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	// The stored source is the formatted one, followed by the test it was
	// saved with (#1939).
	if !strings.HasPrefix(fmt.Sprint(got["source"]), strings.TrimRight(tidy1937, "\n")+"\n\ndef test_") {
		t.Fatalf("the stored source is not the formatted one:\n%v", got["source"])
	}
	for _, comment := range []string{"# The regions report.", "# every region we sell in"} {
		if !strings.Contains(fmt.Sprint(got["source"]), comment) {
			t.Errorf("the comment %q was not kept", comment)
		}
	}

	ran := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	want, _ := json.Marshal(draft["result"])
	have, _ := json.Marshal(ran["result"])
	if ran["status"] != "succeeded" || string(want) != string(have) || string(have) != `{"regions":3}` {
		t.Errorf("run_script on the stored version: %v (the draft of the source as sent handed back %s)", ran, want)
	}
}

// TestIssue1937_SavingTheStoredSourceChangesNothing: sending the stored source
// back is a save with no source change, and the response says nothing was
// reformatted.
func TestIssue1937_SavingTheStoredSourceChangesNothing(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1937-again-%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	c.saveScript(map[string]any{
		"command": "create", "name": name, "source": untidy1937,
		"description": "Acceptance #1937: saved twice.",
	}, nil)
	before := c.call("manage_script", map[string]any{"command": "get", "name": name})
	again := c.call("manage_script", map[string]any{"command": "update", "name": name, "source": before["source"]})
	if again["status"] != "updated" {
		t.Fatalf("update: %v", again)
	}
	if _, reformatted := again["source_formatted"]; reformatted {
		t.Errorf("saving the stored source reformatted it: %v", again)
	}
	after := c.call("manage_script", map[string]any{"command": "get", "name": name})
	if after["source"] != before["source"] || after["version"] != before["version"] {
		t.Errorf("saving the stored source changed the script: version %v -> %v, source:\n%v", before["version"], after["version"], after["source"])
	}
}
