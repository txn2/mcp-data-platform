//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIssue2383_FetchOfAScriptByNameIsNotFound is the ticket's criterion:
// fetch of mcp:script:<non-uuid> (an agent fetching a script by its name)
// returns the not-found document the other providers return, and the result
// carries no database error text.
func TestIssue2383_FetchOfAScriptByNameIsNotFound(t *testing.T) {
	c := connect(t)
	for _, ref := range []string{"mcp:script:daily-sales", "mcp:script:12345", "mcp:script:Daily Sales"} {
		res, text, err := c.callRaw("fetch", map[string]any{
			"reference": ref,
			"purpose":   "Acceptance #2383: fetch a script by something other than its id.",
		})
		if err != nil {
			t.Fatalf("%s: transport error: %v", ref, err)
		}
		if res.IsError {
			t.Fatalf("%s: fetch answered a tool error, want the not-found document: %s", ref, text)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			t.Fatalf("%s: fetch answered something that is not JSON: %v\n%s", ref, err, text)
		}
		if found, ok := doc["found"].(bool); !ok || found || !strings.Contains(text, "no content found") {
			t.Fatalf("%s: fetch answered %s, want found false with the not-found message", ref, text)
		}
		for _, leaked := range []string{"pq:", "22P02", "invalid input syntax", "uuid"} {
			if strings.Contains(text, leaked) {
				t.Fatalf("%s: fetch result carries database error text %q: %s", ref, leaked, text)
			}
		}
	}
}
