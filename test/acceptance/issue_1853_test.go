//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Issue #1853: manage_script validate reported "f-strings are not supported"
// for code with no f-string -- a string literal whose content is exactly f,
// as a dict key or a subscript -- because the check read raw text.
//
// What these hold, against the running platform: the ticket's two lines
// validate clean, as do Python-isms that only appear inside strings and
// comments, while a real f-string and a real import are still reported.
//
// Wire forms: manage_script's `command` and `source` are typed string, and
// each is sent in that one form.

// findings1853 validates source and returns the finding messages.
func findings1853(t *testing.T, c *client, source string) []string {
	t.Helper()
	out := c.call("manage_script", map[string]any{"command": "validate", "source": source})
	list, _ := out["findings"].([]any)
	messages := make([]string, 0, len(list))
	for _, item := range list {
		f, _ := item.(map[string]any)
		msg, _ := f["message"].(string)
		messages = append(messages, msg)
	}
	return messages
}

func TestIssue1853_AStringLiteralFIsNotAnFString(t *testing.T) {
	c := connect(t)
	source := "q = {\"script\": {\"params\": {\"f\": \"field.name\"}}}\nr = q[\"script\"][\"params\"]\nx = r[\"f\"]\n"
	if got := findings1853(t, c, source); len(got) != 0 {
		t.Errorf("findings = %v; want none for the ticket's lines", got)
	}
}

func TestIssue1853_PythonIsmsInStringsAndCommentsAreNotFindings(t *testing.T) {
	c := connect(t)
	source := "# we used to import datetime and open(files)\nsql = \"SELECT datetime, random.x FROM t\"\n"
	if got := findings1853(t, c, source); len(got) != 0 {
		t.Errorf("findings = %v; want none for text inside a comment and a string", got)
	}
}

func TestIssue1853_ARealFStringIsStillReported(t *testing.T) {
	c := connect(t)
	got := strings.Join(findings1853(t, c, "n = 1\nx = f\"{n}\"\nimport os\n"), " | ")
	if !strings.Contains(got, "f-strings are not supported") {
		t.Errorf("a real f-string was not reported: %s", got)
	}
	if !strings.Contains(got, "`import` is not available") {
		t.Errorf("a real import was not reported: %s", got)
	}
}
