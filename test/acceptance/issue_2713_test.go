//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2713: the defects fuzzing found in parsers that read
// untrusted input are refused through the surfaces that reach them.
//
//   - a caller's api path whose dot segment hides before a "?" or "#"
//     (pathtemplate.ValidateCallerPath read "/..#" whole and accepted it);
//   - a webhook source's event id path "[0]]" (jsonpath.Parse panicked on it);
//   - a script's format string asking for a field a hundred trillion
//     characters wide (scriptprintf allocated it), refused by the save gate,
//     and the same width taken from an argument at run time refused by the run.
//
// Wire forms: api_invoke_endpoint's connection, method, path and purpose are
// strings; POST /api/v1/admin/webhooks/sources takes a JSON object whose
// config.event_id_path is a string; manage_script create and run_draft take
// command, name and source as strings.

// TestIssue2713_ADotSegmentBeforeAQueryIsRefused: a path whose last segment is
// ".." once its query or fragment is stripped is refused before any request is
// sent, in both forms.
func TestIssue2713_ADotSegmentBeforeAQueryIsRefused(t *testing.T) {
	c := connect(t)
	for _, path := range []string{"/..#", "/v1/..?limit=1"} {
		res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
			"connection": apiTestConnection, "method": "GET", "path": path,
			"purpose": "Acceptance: a dot segment before a query is refused.",
		})
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !res.IsError || !strings.Contains(text, `"." or ".."`) {
			t.Errorf("path %q was not refused for its dot segment: %s", path, firstRunes(text, 400))
		}
	}
}

// TestIssue2713_AMalformedEventPathIsRefused: a source whose event id path
// has text after a step is refused with a reason, not a server failure.
func TestIssue2713_AMalformedEventPathIsRefused(t *testing.T) {
	c := connect(t)
	name := issue1870Name("bad2713")
	body := issue1870HMAC(name, "acceptance-2713", map[string]any{"event_id_path": "[0]]"})
	status, out := c.rest(http.MethodPost, "/api/v1/admin/webhooks/sources", jsonBody(t, body))
	if status == http.StatusCreated {
		_, _ = c.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
		t.Fatalf("a source with event_id_path [0]] was created: %v", out)
	}
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		t.Fatalf("event_id_path [0]] answered %d %v; want a refusal naming the path", status, out)
	}
	if !strings.Contains(fmt.Sprint(out), "unexpected") {
		t.Errorf("the refusal does not say what is wrong with the path: %v", out)
	}
}

// TestIssue2713_AnUnboundedFieldWidthIsRefused: the save gate refuses a format
// string asking for a field past the maximum, and a run that takes such a
// width from an argument fails with a reason rather than allocating it.
func TestIssue2713_AnUnboundedFieldWidthIsRefused(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-2713-width-%s", unique1579())
	literal := "def main():\n    \"\"\"Pads to a huge width.\"\"\"\n    platform.result(\"%100000000000000c\" % \"x\")\n"
	res, text, err := c.callRaw("manage_script", map[string]any{"command": "create", "name": name, "source": literal})
	if err != nil {
		t.Fatal(err)
	}
	// The save gate answers a refusal as its findings, an error finding
	// naming the width, and creates nothing.
	if strings.Contains(text, `"status":"created"`) || !strings.Contains(text, "width over") {
		t.Fatalf("a literal width past the maximum was not refused at save (error=%t): %s", res.IsError, firstRunes(text, 400))
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	starred := "def main():\n    \"\"\"Pads to a width taken from an argument.\"\"\"\n    platform.result(\"%*s\" % (2000000, \"x\"))\n"
	res, text, err = c.callRaw("manage_script", map[string]any{"command": "run_draft", "name": name + "-star", "source": starred})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "width or precision over") {
		t.Fatalf("a run padding to a width past the maximum did not fail with the reason: error=%t %s", res.IsError, firstRunes(text, 400))
	}
}
