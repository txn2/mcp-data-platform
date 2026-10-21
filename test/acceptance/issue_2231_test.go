//go:build integration

package acceptance

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// Acceptance for #2231: filtering the call list by outcome took 36 seconds on
// a catalog of about 95,000 records, because the outcome was derived per
// record. Each outcome now answers within a second, on the admin list and on
// the portal's own list.
//
// Wire forms: this ticket adds no tool parameter. GET /api/v1/admin/calls and
// GET /api/v1/portal/calls take outcome and per_page as query strings, the one
// form a query takes.

// issue2231Bound is the time one filtered page and its total may take.
const issue2231Bound = time.Second

// issue2231Page reads one page of a call list and returns its total and how
// long the request took.
func issue2231Page(t *testing.T, c *client, path string) (total int, took time.Duration) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.base+path, http.NoBody) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	start := time.Now()
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	took = time.Since(start)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d: %s", path, res.StatusCode, raw)
	}
	var page struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return page.Total, took
}

// TestIssue2231_EachOutcomeAnswersWithinASecond: on the dev catalog, each
// outcome filter on the admin call list and on the portal's own answers within
// a second, and the outcomes' totals add up to the unfiltered total, read
// before and after them.
func TestIssue2231_EachOutcomeAnswersWithinASecond(t *testing.T) {
	c := connect(t)
	for _, list := range []string{"/api/v1/admin/calls", "/api/v1/portal/calls"} {
		// The dev stack records calls while this runs, so the outcomes are
		// held between the totals read before and after them.
		before, _ := issue2231Page(t, c, list+"?per_page=50")
		sum := 0
		for _, outcome := range []string{"failed", "satisfied", "superseded", "ran"} {
			total, took := issue2231Page(t, c, list+"?per_page=50&outcome="+outcome)
			t.Logf("%s outcome=%s: %d records in %s", list, outcome, total, took)
			if took > issue2231Bound {
				t.Errorf("%s filtered to %s took %s; the bound is %s", list, outcome, took, issue2231Bound)
			}
			sum += total
		}
		after, _ := issue2231Page(t, c, list+"?per_page=50")
		t.Logf("%s: %d records before the outcomes were read, %d after", list, before, after)
		if sum < before || sum > after {
			t.Errorf("%s: the outcomes total %d, outside the list's %d to %d", list, sum, before, after)
		}
	}
}
