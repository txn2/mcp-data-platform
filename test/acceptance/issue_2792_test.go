//go:build integration

package acceptance

import (
	"net/http"
	"testing"
)

// Acceptance for #2792: the Webhooks page's search and filters read what feeds
// each source from the status the page loads. The page filters in the browser
// (ui/src/pages/webhooks/sourceFilter.ts, its tests and webhooks.test.tsx);
// what the server owes it is each source's kind.
//
// Wire forms: GET /api/v1/admin/webhooks/status takes range as a query
// string; no parameter admits a second form.

// TestIssue2792_TheOverviewSaysWhatFeedsEachSource: an imap source is listed
// as imap, and every source carries one of the four kinds the page filters by.
func TestIssue2792_TheOverviewSaysWhatFeedsEachSource(t *testing.T) {
	c := connect(t)
	name, _, _ := issue2836Setup(t, c)
	status, out := c.rest(http.MethodGet, "/api/v1/admin/webhooks/status?range=day", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the overview: %d %v", status, out)
	}
	kinds := map[string]bool{"http": true, "imap": true, "form": true, "postgres": true}
	found := false
	for _, s := range asList(out["sources"]) {
		input, _ := s["input"].(string)
		if !kinds[input] {
			t.Errorf("source %v has input %q, want one of http, imap, form, postgres", s["name"], input)
		}
		if s["name"] == name {
			found = true
			if input != "imap" {
				t.Errorf("the mailbox source %s is listed as %q", name, input)
			}
		}
	}
	if !found {
		t.Fatalf("the overview does not list %s", name)
	}
}
