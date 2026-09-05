//go:build integration

package acceptance

import (
	"encoding/json"
	"testing"
)

// Issue #1606: the inline budget defaulted to 128 KiB, roughly twice what an
// MCP client accepts in one tool result, so the cut #1587 added did not help:
// a response cut at 128 KiB was still refused by the client and spilled to a
// file, and a 64,213-byte response was under the budget entirely, so it
// arrived with no body_truncated, no hint and no export_arguments -- just a
// refused result and a file path.
//
// The default is now 32 KiB, and the budget is applied to the rendered tool
// result rather than to the bytes read from the upstream. The two differ by
// more than a constant: the envelope and the indentation the result is
// rendered with expand a parsed JSON body several times over, so a body inside
// any read budget can still render past what a client accepts.
const (
	// issue1606RefusedResult is the tool-result size the ticket measured
	// refused and spilled to a file. It is the ceiling every assertion here
	// is written against.
	issue1606RefusedResult = 64_213
	// issue1606RefusedResponse is the upstream response size of the ticket's
	// second row. It equals the result size above because that response was
	// under the old budget, so it was returned uncut and its body was
	// substantially the whole tool result.
	issue1606RefusedResponse = 64_213
	// issue1606Budget is DefaultMaxInlineBytes, the budget on the rendered
	// result a connection that sets no max_inline_bytes gets.
	issue1606Budget = 32 * 1024
	// issue1606NarrowBudget is small enough that an ordinary nested JSON page
	// renders past it, indented, while its compact bytes stay under it -- the
	// shape that went past the ceiling unflagged when the budget was applied
	// to the read.
	issue1606NarrowBudget = 4096
)

// issue1606SizedArgs addresses the fixture's sized endpoint for a response of
// the given size. bytes is untyped in the schema, so callers send both JSON
// forms it admits, a number and a string.
func issue1606SizedArgs(bytes any) map[string]any {
	return map[string]any{
		"connection":   issue1587FixtureConn,
		"method":       "GET",
		"path":         "/v1/sized",
		"query_params": map[string]any{"bytes": bytes},
		"purpose":      "Acceptance #1606: the default inline budget sits under what the client accepts.",
	}
}

// issue1606ResultText invokes api_invoke_endpoint and returns the tool result's
// text verbatim. The budget is on the rendered result, so the assertions below
// measure the text the client receives, not the fields inside it.
func issue1606ResultText(t *testing.T, c *client, args map[string]any) string {
	t.Helper()
	res, text, err := c.callRaw("api_invoke_endpoint", args)
	if err != nil {
		t.Fatalf("api_invoke_endpoint: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("api_invoke_endpoint: tool error: %s", text)
	}
	return text
}

// TestIssue1606_TheResponseTheClientRefusedIsNowCutAndSteered is the ticket's
// second measured row: 64,213 bytes was under the old default, so it came back
// whole and unflagged and the client refused it. It is now past the budget, so
// it is cut and carries the steer to api_export.
func TestIssue1606_TheResponseTheClientRefusedIsNowCutAndSteered(t *testing.T) {
	c := connect(t)
	for name, bytes := range map[string]any{"number": issue1606RefusedResponse, "string": "64213"} {
		t.Run("bytes_as_"+name, func(t *testing.T) {
			out := c.call("api_invoke_endpoint", issue1606SizedArgs(bytes))
			assertCutAtInlineBudget(t, out, issue1606Budget)
		})
	}
}

// TestIssue1606_TheWholeToolResultFitsTheBudget asserts the property the
// budget exists for: not that the body was cut, but that the result the client
// receives is inside the budget, and so inside the size the client refused.
func TestIssue1606_TheWholeToolResultFitsTheBudget(t *testing.T) {
	c := connect(t)
	sizes := map[string]any{
		"the response the client refused": issue1606RefusedResponse,
		"a response far past the budget":  issue1587SizedBytes,
	}
	for name, bytes := range sizes {
		t.Run(name, func(t *testing.T) {
			assertResultWithin(t, issue1606ResultText(t, c, issue1606SizedArgs(bytes)), issue1606Budget)
		})
	}
}

// TestIssue1606_AJSONResponseInsideTheReadBudgetIsStillHeldToIt is the defect
// the read-side budget could not see. A nested JSON page whose compact bytes
// are under the connection's budget renders, indented and enveloped, to well
// over it, and came back unflagged for the client to refuse. The budget is now
// on the rendered result, and re-encoding is the first lever spent, so the
// page comes back whole and inside the budget rather than cut. per_page is
// untyped in the schema, so both JSON forms it admits are sent.
func TestIssue1606_AJSONResponseInsideTheReadBudgetIsStillHeldToIt(t *testing.T) {
	c := connect(t)
	issue1587Connection(t, c, "issue-1606-narrow", issue1606NarrowBudget)
	for name, perPage := range map[string]any{"number": 100, "string": "100"} {
		t.Run("per_page_as_"+name, func(t *testing.T) {
			args := map[string]any{
				"connection":   "issue-1606-narrow",
				"method":       "GET",
				"path":         "/v1/pagination/link",
				"query_params": map[string]any{"per_page": perPage},
				"purpose":      "Acceptance #1606: a JSON body inside the read budget still renders past it.",
			}
			out := c.call("api_invoke_endpoint", args)
			readBytes := number(t, out, "body_bytes")
			if readBytes <= 0 || readBytes >= issue1606NarrowBudget {
				t.Fatalf("body_bytes = %v; the case needs a read inside the %d budget", readBytes, issue1606NarrowBudget)
			}
			text := issue1606ResultText(t, c, args)
			// The defect: indented, this result is past the budget that read fits inside.
			indented, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if len(indented) <= issue1606NarrowBudget {
				t.Fatalf("the indented rendering is %d bytes; the case needs one past the %d budget", len(indented), issue1606NarrowBudget)
			}
			assertResultWithin(t, text, issue1606NarrowBudget)
			if truncated, _ := out["body_truncated"].(bool); truncated {
				t.Errorf("body_truncated = true; want the whole body returned, re-encoding alone having made it fit")
			}
			body, _ := out["body"].(map[string]any)
			if items, _ := body["items"].([]any); len(items) != 100 {
				t.Errorf("body holds %d items; want all 100 returned", len(items))
			}
		})
	}
}

// TestIssue1606_TheUtilConnectionCarriesTheSameDefault covers the built-in util
// connection's fetch_url, whose url argument the caller reaches through a body
// the schema takes as an object or as a string of JSON.
func TestIssue1606_TheUtilConnectionCarriesTheSameDefault(t *testing.T) {
	c := connect(t)
	target := apiTestFixtureURL() + "/v1/sized?bytes=64213"
	forms := map[string]any{
		"object": map[string]any{"url": target},
		"string": `{"url": "` + target + `"}`,
	}
	for name, body := range forms {
		t.Run("body_as_"+name, func(t *testing.T) {
			args := map[string]any{
				"connection": "util",
				"method":     "POST",
				"path":       "/util/fetch",
				"body":       body,
				"purpose":    "Acceptance #1606: a fetch_url response takes the same default budget.",
			}
			assertCutAtInlineBudget(t, c.call("api_invoke_endpoint", args), issue1606Budget)
			assertResultWithin(t, issue1606ResultText(t, c, args), issue1606Budget)
		})
	}
}

// assertResultWithin asserts the rendered tool result is inside the budget,
// and so inside the size the client was measured refusing.
func assertResultWithin(t *testing.T, text string, budget int) {
	t.Helper()
	if len(text) > budget {
		t.Errorf("tool result is %d characters; want it inside the %d budget", len(text), budget)
	}
	if len(text) >= issue1606RefusedResult {
		t.Errorf("tool result is %d characters; want it under the %d-character result the client refused", len(text), issue1606RefusedResult)
	}
}
