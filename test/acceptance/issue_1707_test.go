//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Issue #1707: api_invoke_endpoint with method and path on the built-in
// platform-admin connection joined the path to the host root. Every operation
// in the connection's catalog lives under /api/v1, and the operation_id an
// agent copies out of api_discover or search reads "GET /admin/tools", so the
// raw path that looks like it -- "/admin/tools" -- answered a 405 text body
// from the portal's routes, and "/portal/knowledge-pages/{slug}" answered the
// portal SPA's index.html with a 200.
//
// What these hold: a raw path outside /api/v1 on platform-admin is refused
// before it is sent, by both tools that take a raw path, and the refusal names
// the path that would have worked and, where the catalog declares one, the
// operation_id; the forms that already worked still do.
//
// Wire forms: `connection`, `method`, `path`, `operation_id` and `purpose` are
// `{"type":"string"}` in both tools' schemas, so each admits one JSON form and
// is sent as a JSON string. `query_params`, `headers` and `body` are untouched
// by this change and not sent.

const (
	issue1707Conn    = "platform-admin"
	issue1707Purpose = "Acceptance for #1707: a raw path on platform-admin outside /api/v1 is refused and names the prefix."
)

// issue1707Refusal calls tool with a raw method+path and returns the refusal
// text, failing when the call was not refused.
func issue1707Refusal(t *testing.T, c *client, tool, path string, extra map[string]any) string {
	t.Helper()
	args := map[string]any{"connection": issue1707Conn, "method": "GET", "path": path, "purpose": issue1707Purpose}
	for k, v := range extra {
		args[k] = v
	}
	res, text, err := c.callRaw(tool, args)
	if err != nil {
		t.Fatalf("%s path=%s: transport error: %v", tool, path, err)
	}
	if !res.IsError {
		t.Fatalf("%s path=%s was sent rather than refused:\n%s", tool, path, text)
	}
	// The platform's error contract hands the agent the sentence as plain text
	// with its code appended, which is what is asserted on.
	if !strings.Contains(text, "was not sent") {
		t.Fatalf("%s path=%s failed for some other reason than the prefix refusal:\n%s", tool, path, text)
	}
	return text
}

// TestIssue1707_ARawAdminPathOutsideTheAPIPrefixIsRefusedNamingIt is the
// ticket's 405 case: the refusal names the path that reaches the listing and
// the operation_id the catalog lists for it.
func TestIssue1707_ARawAdminPathOutsideTheAPIPrefixIsRefusedNamingIt(t *testing.T) {
	c := connect(t)
	text := issue1707Refusal(t, c, "api_invoke_endpoint", "/admin/tools", nil)
	for _, want := range []string{`"/api/v1/admin/tools"`, `"GET /admin/tools"`} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not name %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, "405") || strings.Contains(text, "Method Not Allowed") {
		t.Errorf("the request reached the portal's routes instead of being refused:\n%s", text)
	}
}

// TestIssue1707_ARawPortalPathIsRefusedRatherThanAnsweredWithTheSPA is the
// ticket's worse case, the one that answered 200 with HTML.
func TestIssue1707_ARawPortalPathIsRefusedRatherThanAnsweredWithTheSPA(t *testing.T) {
	c := connect(t)
	text := issue1707Refusal(t, c, "api_invoke_endpoint", "/portal/knowledge-pages/retail-seasons", nil)
	if !strings.Contains(text, `"/api/v1/portal/knowledge-pages/retail-seasons"`) {
		t.Errorf("the refusal does not name the prefixed path:\n%s", text)
	}
	if strings.Contains(strings.ToLower(text), "<!doctype html") {
		t.Errorf("the SPA's index.html came back:\n%s", text)
	}
}

// TestIssue1707_APIExportRefusesTheSamePath holds the second tool that takes a
// raw path to the same rule, so the export path cannot land an HTML page as an
// asset.
func TestIssue1707_APIExportRefusesTheSamePath(t *testing.T) {
	c := connect(t)
	text := issue1707Refusal(t, c, "api_export", "/admin/tools", map[string]any{"name": "issue-1707-refused"})
	if !strings.Contains(text, `"/api/v1/admin/tools"`) {
		t.Errorf("api_export's refusal does not name the prefixed path:\n%s", text)
	}
}

// TestIssue1707_ThePrefixedPathAndTheOperationIDStillWork holds the two forms
// that reached the listing before this change to it after.
func TestIssue1707_ThePrefixedPathAndTheOperationIDStillWork(t *testing.T) {
	c := connect(t)
	for name, args := range map[string]map[string]any{
		"raw path under /api/v1": {"method": "GET", "path": "/api/v1/admin/tools"},
		"operation_id":           {"operation_id": "GET /admin/tools"},
	} {
		t.Run(name, func(t *testing.T) {
			args["connection"] = issue1707Conn
			args["purpose"] = issue1707Purpose
			out := c.call("api_invoke_endpoint", args)
			if status := number(t, out, "status"); status != 200 {
				t.Fatalf("status %v; want 200: %v", status, out)
			}
			body, _ := out["body"].(map[string]any)
			if _, ok := body["tools"]; !ok {
				t.Errorf("the body is not the tool listing: %v", out["body"])
			}
		})
	}
}
