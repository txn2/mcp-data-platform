//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2115: changing one word inside a long single-line constant returns
// a diff of the span that changed, not the whole old and new line, and
// verbose=true shows the lines whole.
//
// Wire forms: patch's edits are an array of objects with op, find and
// replace strings; verbose is a boolean, sent true and omitted. diff takes
// the script's name as a string.

// source2115 carries a 15 KB page script on one line; word is one word in
// the middle of it.
func source2115(word string) string {
	filler := strings.Repeat("document.querySelector('#row').click(); ", 180)
	return `PAGE_JS = "` + filler + `var mode = '` + word + `'; ` + filler + `"

def main():
    """Reports how long the page script is."""
    platform.result({"length": len(PAGE_JS)})

def test_length():
    """The page script is carried whole."""
    main()
    assert.eq(testing.outputs().result, {"length": ` + fmt.Sprint(len(filler)*2+len("var mode = 'alpha'; ")) + `})
`
}

func TestIssue2115_OneWordInALongLineIsASmallDiff(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-2115-%d", time.Now().UnixNano())
	created := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2115: a long page constant.", "source": source2115("alpha"),
	})
	if created["status"] != "created" {
		t.Fatalf("the script was not saved: %v", created)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	patched := c.call("manage_script", map[string]any{
		"command": "patch", "name": name,
		"edits": []any{map[string]any{"op": "replace", "find": "var mode = 'alpha'", "replace": "var mode = 'omega'"}},
	})
	if patched["status"] != "updated" {
		t.Fatalf("the one-word patch was not saved: %v", patched)
	}
	raw, _ := json.Marshal(patched)
	diff, _ := patched["diff"].(string)
	t.Logf("patch answer %d bytes, diff %d bytes", len(raw), len(diff))
	if !strings.Contains(diff, "alpha") || !strings.Contains(diff, "omega") {
		t.Fatalf("the diff does not show the word that changed: %s", diff)
	}
	if len(diff) > 1_500 {
		t.Errorf("one changed word in a 15 KB line returned a %d-byte diff; want a few hundred", len(diff))
	}
	if len(raw) > 4_000 {
		t.Errorf("the patch answer is %d bytes", len(raw))
	}

	shown := c.call("manage_script", map[string]any{"command": "diff", "name": name})
	if d := fmt.Sprint(shown["diff"]); !strings.Contains(d, "omega") || len(d) > 1_500 {
		t.Errorf("diff of the two versions is %d bytes or does not show the word: %.300s", len(d), d)
	}
	whole := c.call("manage_script", map[string]any{"command": "diff", "name": name, "verbose": true})
	if d := fmt.Sprint(whole["diff"]); len(d) < 15_000 {
		t.Errorf("verbose=true showed %d bytes of diff; want the whole 15 KB lines", len(d))
	}
}
