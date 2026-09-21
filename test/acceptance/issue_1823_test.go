//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1823: saving a script with `def load(...)` failed with "not an
// identifier" at the line, which names neither the word nor the mistake.
//
// What these hold, against the running platform: the save is refused with a
// finding that names `load` as a reserved word and says to rename it, validate
// reports the same finding for an unsaved source, and the dialect help lists the
// reserved words under WHAT IS NOT.
//
// Wire forms: manage_script's `command`, `name`, `description` and `source` are
// typed string, and each is sent in that one form.

// issue1823Source is the ticket's script: an ETL step named load.
const issue1823Source = `
def load(table, ids):
    return ids

print(load("t", [1]))
`

// TestIssue1823_ASaveNamesTheReservedWord is the ticket's reproduction.
func TestIssue1823_ASaveNamesTheReservedWord(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1823-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})

	_, text, err := c.callRaw("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1823: a reserved word used as a name.",
		"source":      issue1823Source,
	})
	if err != nil {
		t.Fatalf("manage_script create: transport error: %v", err)
	}
	// A source that does not parse is answered, not failed: status "invalid"
	// with the findings, and nothing saved.
	if !strings.Contains(text, `"status":"invalid"`) {
		t.Fatalf("a script that does not parse was not reported invalid: %s", text)
	}
	if _, got, _ := c.callRaw("manage_script", map[string]any{"command": "get", "name": name}); strings.Contains(got, "def load") {
		t.Errorf("the invalid script was saved: %s", got)
	}
	for _, want := range []string{"`load` is a reserved word in Starlark", "load_rows"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not say %q: %s", want, text)
		}
	}
}

// TestIssue1823_ValidateNamesTheReservedWord holds the same finding on the
// authoring loop's fast half, for a source nobody has saved.
func TestIssue1823_ValidateNamesTheReservedWord(t *testing.T) {
	c := connect(t)
	_, text, err := c.callRaw("manage_script", map[string]any{"command": "validate", "source": issue1823Source})
	if err != nil {
		t.Fatalf("manage_script validate: transport error: %v", err)
	}
	if !strings.Contains(text, "`load` is a reserved word in Starlark") {
		t.Errorf("validate does not name the reserved word: %s", text)
	}
	if strings.Contains(text, `"message":"not an identifier"`) {
		t.Errorf("validate still reports only the parser's message: %s", text)
	}
}

// TestIssue1823_TheHelpListsTheReservedWords holds the ticket's second ask.
func TestIssue1823_TheHelpListsTheReservedWords(t *testing.T) {
	c := connect(t)
	_, text, err := c.callRaw("manage_script", map[string]any{"command": "help"})
	if err != nil {
		t.Fatalf("manage_script help: transport error: %v", err)
	}
	flat := strings.Join(strings.Fields(text), " ")
	for _, want := range []string{"a reserved word", "lambda, load, nonlocal", "def load(...)"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the help does not say %q", want)
		}
	}
}
