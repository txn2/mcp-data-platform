//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// Issue #1795: the scripts listing could not be ordered and every row carried
// a pill wall. The store emitted `ORDER BY updated_at DESC LIMIT $n` and took
// no ordering from the caller, the portal route parsed only category, tag and
// search, and the response's `total` was len(rows) -- so past the 200-row page
// cap a deployment read its own cap back as the number of scripts it had, with
// nothing saying the page was truncated.
//
// What these hold, against the running platform: the listing is ordered by a
// whitelisted column in the store, ahead of the cap; an unknown column falls
// back to the default rather than erroring; owner, status, enabled and scope
// narrow it; a non-admin sees a script they do not own without its source or
// its run state; and `manage_script command=list` lists scripts authored by
// other people, carrying no source on any row.
//
// Wire forms: every parameter these send admits exactly one form, so each is
// sent once. sort, dir, owner, scope, status and enabled are query parameters
// on the REST route, typed string and boolean. `manage_script`'s `tags` is
// typed `array` and REFUSES a bare string ("type: acc1795 has type \"string\",
// want \"array\""), which was checked against the running server rather than
// assumed; `command`, `name`, `display_name`, `source`, `category` and
// `purpose` are typed string.

const issue1795Purpose = "Acceptance for #1795: the scripts listing is ordered and scoped by the server."

// issue1795Scripts is what this file authors: three scripts whose display
// names order differently from their update times, so an ordering assertion
// cannot pass by accident on the default ordering.
var issue1795Scripts = []struct{ name, display string }{
	{"acc-1795-zulu", "Zulu Quarterly Rollup"},
	{"acc-1795-alpha", "Alpha Daily Digest"},
	{"acc-1795-mike", "Mike Weekly Summary"},
}

// seedIssue1795 authors the scripts, newest last, and returns their names.
//
// They are created in an order that is NOT their alphabetical one: created
// last-to-first alphabetically, so `updated_at DESC` and `display_name ASC`
// disagree on every pair and an ordering that was ignored is visible.
func seedIssue1795(t *testing.T, c *client) []string {
	t.Helper()
	names := make([]string, 0, len(issue1795Scripts))
	for _, s := range issue1795Scripts {
		// Idempotent: a script name is unique per owner, so a second run of
		// this file would otherwise fail on every create. Each case seeds, and
		// every case shares one deployment.
		res, _, err := c.callRaw("manage_script", map[string]any{
			"command": "get",
			"name":    s.name,
			"purpose": issue1795Purpose,
		})
		if err == nil && res != nil && !res.IsError {
			names = append(names, s.name)
			continue
		}
		c.call("manage_script", map[string]any{
			"command":      "create",
			"name":         s.name,
			"display_name": s.display,
			"source":       "x = 1\n",
			"category":     "acceptance",
			"tags":         []any{"acc1795"},
			"purpose":      issue1795Purpose,
		})
		names = append(names, s.name)
	}
	return names
}

// listedNames1795 reads the display names the REST listing returned, in order.
func listedNames1795(t *testing.T, out map[string]any) []string {
	t.Helper()
	data, ok := out["data"].([]any)
	if !ok {
		t.Fatalf("the listing carries no data array: %v", out)
	}
	names := make([]string, 0, len(data))
	for _, row := range data {
		r, ok := row.(map[string]any)
		if !ok {
			continue
		}
		sc, ok := r["script"].(map[string]any)
		if !ok {
			continue
		}
		if display, ok := sc["display_name"].(string); ok {
			names = append(names, display)
		}
	}
	return names
}

// ours1795 keeps only the display names this file authored, so other scripts
// on the deployment cannot make an ordering assertion pass or fail.
func ours1795(names []string) []string {
	want := map[string]bool{}
	for _, s := range issue1795Scripts {
		want[s.display] = true
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if want[n] {
			out = append(out, n)
		}
	}
	return out
}

func TestIssue1795_TheListingIsOrderedByTheServer(t *testing.T) {
	c := connect(t)
	seedIssue1795(t, c)

	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts?sort=name&dir=asc", nil)
	if status != http.StatusOK {
		t.Fatalf("sort=name&dir=asc: status %d: %v", status, out)
	}
	ascending := ours1795(listedNames1795(t, out))

	want := make([]string, 0, len(issue1795Scripts))
	for _, s := range issue1795Scripts {
		want = append(want, s.display)
	}
	sort.Slice(want, func(i, j int) bool {
		// name ascending, and the display names sort the same way here: alpha,
		// mike, zulu.
		return want[i] < want[j]
	})
	if strings.Join(ascending, "|") != strings.Join(want, "|") {
		t.Fatalf("sort=name&dir=asc did not order the listing:\n got %v\nwant %v", ascending, want)
	}

	status, out = c.rest(http.MethodGet, "/api/v1/portal/scripts?sort=name&dir=desc", nil)
	if status != http.StatusOK {
		t.Fatalf("sort=name&dir=desc: status %d: %v", status, out)
	}
	descending := ours1795(listedNames1795(t, out))
	for i := range descending {
		if descending[i] != ascending[len(ascending)-1-i] {
			t.Fatalf("reversing dir did not reverse the listing:\n asc %v\ndesc %v", ascending, descending)
		}
	}
}

func TestIssue1795_AnUnknownSortFallsBackRatherThanFailing(t *testing.T) {
	c := connect(t)
	seedIssue1795(t, c)

	// `dir=asc` is sent deliberately: an unknown column must fall back to the
	// DEFAULT ordering, most recently updated first, and not to the named
	// column at the caller's direction. Substituting updated_at while honouring
	// `dir` would answer oldest-first, which is neither what was asked for nor
	// the default.
	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts?sort=last_run&dir=asc", nil)
	if status != http.StatusOK {
		t.Fatalf("an unknown sort column must not fail the listing: status %d: %v", status, out)
	}
	fallback := listedNames1795(t, out)
	if len(fallback) == 0 {
		t.Fatal("an unknown sort column answered an empty listing")
	}

	status, def := c.rest(http.MethodGet, "/api/v1/portal/scripts", nil)
	if status != http.StatusOK {
		t.Fatalf("the default listing: status %d: %v", status, def)
	}
	want := listedNames1795(t, def)
	if strings.Join(fallback, "|") != strings.Join(want, "|") {
		t.Fatalf("an unknown sort column did not answer the default ordering:\n got %v\nwant %v",
			fallback, want)
	}
}

func TestIssue1795_TheTotalCountsThePredicateNotThePage(t *testing.T) {
	c := connect(t)
	seedIssue1795(t, c)

	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts?category=acceptance", nil)
	if status != http.StatusOK {
		t.Fatalf("category=acceptance: status %d: %v", status, out)
	}
	data, _ := out["data"].([]any)
	total, ok := out["total"].(float64)
	if !ok {
		t.Fatalf("the listing carries no total: %v", out)
	}

	// EXACT, not ">= the page". The count is a second query, and a count that
	// fails is swallowed by the route and falls back to the page length --
	// which looks precisely like the defect this ticket is about. An
	// inequality against len(data) therefore passes on a count that never
	// ran, which is how a count naming a table that does not exist survived
	// this criterion once already.
	if len(data) != int(total) {
		t.Fatalf("this page is not truncated, so total (%v) must equal the rows returned (%d)",
			total, len(data))
	}
	if int(total) < len(issue1795Scripts) {
		t.Fatalf("total %v does not count the %d scripts this file authored", total, len(issue1795Scripts))
	}

	// The scheduled count is the server's too, and is checked against the rows
	// rather than merely for presence: the same swallowed failure answers 0.
	scheduled, ok := out["scheduled"].(float64)
	if !ok {
		t.Fatalf("the listing carries no scheduled count: %v", out)
	}
	withCadence := 0
	for _, row := range data {
		r, _ := row.(map[string]any)
		if r["schedule"] != nil {
			withCadence++
		}
	}
	if int(scheduled) != withCadence {
		t.Fatalf("scheduled (%v) does not match the %d rows carrying a cadence", scheduled, withCadence)
	}
	if _, ok := out["failing"]; !ok {
		t.Fatalf("the listing carries no failing count: %v", out)
	}
}

// TestIssue1795_TheCountsAreSecondQueriesThatRun proves the health counts are
// really counted rather than derived from the page.
//
// It turns on `scheduled`, and that is not incidental. The route treats a
// failed count as "no better total available" and falls back to the page
// length, so a `total` that never ran is INDISTINGUISHABLE from one that did
// on any page the cap did not truncate -- an equality check against the rows
// passes either way, and so does "narrowing lowers the total". The first
// version of the count named a table that does not exist and survived exactly
// those assertions. `scheduled` has no such fallback: a failed count answers
// zero, so scheduling one script and requiring it to be counted is the
// assertion a broken count cannot pass.
func TestIssue1795_TheCountsAreSecondQueriesThatRun(t *testing.T) {
	c := connect(t)
	seedIssue1795(t, c)

	// Give one of them a cadence. Idempotent like the seed: setting a
	// schedule replaces it rather than adding a second.
	c.call("manage_script", map[string]any{
		"command":  "schedule_set",
		"name":     issue1795Scripts[0].name,
		"cron":     "0 7 * * 1-5",
		"timezone": "UTC",
		"purpose":  issue1795Purpose,
	})

	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts?category=acceptance", nil)
	if status != http.StatusOK {
		t.Fatalf("category=acceptance: status %d: %v", status, out)
	}
	scheduled, ok := out["scheduled"].(float64)
	if !ok {
		t.Fatalf("the listing carries no scheduled count: %v", out)
	}
	if scheduled < 1 {
		t.Fatalf("a script with a cadence was not counted: scheduled=%v. A count that "+
			"failed answers zero, which is what a count over a table that does not "+
			"exist looks like.", scheduled)
	}

	// And the total still moves with the predicate.
	status, one := c.rest(http.MethodGet,
		"/api/v1/portal/scripts?category=acceptance&search=Alpha", nil)
	if status != http.StatusOK {
		t.Fatalf("the narrowed listing: status %d: %v", status, one)
	}
	allTotal, _ := out["total"].(float64)
	oneTotal, _ := one["total"].(float64)
	if allTotal <= oneTotal {
		t.Fatalf("narrowing the listing did not lower the total: %v then %v", allTotal, oneTotal)
	}
}

func TestIssue1795_OwnerAndStatusAndEnabledNarrowTheListing(t *testing.T) {
	c := connect(t)
	seedIssue1795(t, c)

	// Whose scripts these are, read off the listing itself rather than assumed.
	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts?category=acceptance", nil)
	if status != http.StatusOK {
		t.Fatalf("listing: status %d: %v", status, out)
	}
	data, _ := out["data"].([]any)
	if len(data) == 0 {
		t.Fatal("the scripts this file authored are not in the listing")
	}
	first, _ := data[0].(map[string]any)
	sc, _ := first["script"].(map[string]any)
	owner, _ := sc["owner_email"].(string)
	if owner == "" {
		t.Fatalf("a listed script carries no owner_email: %v", sc)
	}

	status, out = c.rest(http.MethodGet, "/api/v1/portal/scripts?owner="+owner, nil)
	if status != http.StatusOK {
		t.Fatalf("owner=%s: status %d: %v", owner, status, out)
	}
	rows, _ := out["data"].([]any)
	for _, row := range rows {
		r, _ := row.(map[string]any)
		s, _ := r["script"].(map[string]any)
		if got, _ := s["owner_email"].(string); got != owner {
			t.Fatalf("owner=%s returned a script owned by %q", owner, got)
		}
	}

	// enabled=false must narrow to the disabled scripts rather than being read
	// as an absent parameter.
	status, out = c.rest(http.MethodGet, "/api/v1/portal/scripts?enabled=false", nil)
	if status != http.StatusOK {
		t.Fatalf("enabled=false: status %d: %v", status, out)
	}
	rows, _ = out["data"].([]any)
	for _, row := range rows {
		r, _ := row.(map[string]any)
		s, _ := r["script"].(map[string]any)
		if enabled, _ := s["enabled"].(bool); enabled {
			t.Fatal("enabled=false returned an enabled script")
		}
	}

	status, out = c.rest(http.MethodGet, "/api/v1/portal/scripts?status=archived", nil)
	if status != http.StatusOK {
		t.Fatalf("status=archived: status %d: %v", status, out)
	}
	rows, _ = out["data"].([]any)
	for _, row := range rows {
		r, _ := row.(map[string]any)
		s, _ := r["script"].(map[string]any)
		if got, _ := s["status"].(string); got != "archived" {
			t.Fatalf("status=archived returned a script whose status is %q", got)
		}
	}
}

func TestIssue1795_ScopeMineIsTheDefaultAndScopeAllWidensIt(t *testing.T) {
	c := connect(t)
	seedIssue1795(t, c)

	status, unscoped := c.rest(http.MethodGet, "/api/v1/portal/scripts", nil)
	if status != http.StatusOK {
		t.Fatalf("the unscoped listing: status %d: %v", status, unscoped)
	}
	status, mine := c.rest(http.MethodGet, "/api/v1/portal/scripts?scope=mine", nil)
	if status != http.StatusOK {
		t.Fatalf("scope=mine: status %d: %v", status, mine)
	}
	if fmt.Sprint(unscoped["total"]) != fmt.Sprint(mine["total"]) {
		t.Fatalf("scope=mine is not the default: unscoped total %v, scope=mine %v",
			unscoped["total"], mine["total"])
	}

	status, all := c.rest(http.MethodGet, "/api/v1/portal/scripts?scope=all", nil)
	if status != http.StatusOK {
		t.Fatalf("scope=all: status %d: %v", status, all)
	}
	allTotal, _ := all["total"].(float64)
	mineTotal, _ := mine["total"].(float64)
	if allTotal < mineTotal {
		t.Fatalf("scope=all (%v) lists fewer scripts than scope=mine (%v)", allTotal, mineTotal)
	}

	// A row the caller does not own carries no source and no run state. Where
	// the caller owns everything on this deployment there is nothing unowned to
	// assert against, so the check applies to whatever unowned rows exist.
	rows, _ := all["data"].([]any)
	for _, row := range rows {
		r, _ := row.(map[string]any)
		owned, _ := r["owned"].(bool)
		if owned {
			continue
		}
		s, _ := r["script"].(map[string]any)
		if src, _ := s["source"].(string); src != "" {
			t.Fatal("a script the caller does not own carried its source")
		}
		if _, hasRun := r["last_run"]; hasRun && r["last_run"] != nil {
			t.Fatal("a script the caller does not own carried its last run")
		}
	}
}

// TestIssue1795_AListedScriptOpens holds the listing and the page to the same
// rule. Widening the listing without widening the page it links to would have
// produced a row that lists a script and a click that lands on "no such
// script": the page answers with the same projection the listing applies --
// the contract, and no source.
func TestIssue1795_AListedScriptOpens(t *testing.T) {
	c := connect(t)
	seedIssue1795(t, c)

	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts?scope=all", nil)
	if status != http.StatusOK {
		t.Fatalf("scope=all: status %d: %v", status, out)
	}
	rows, _ := out["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("scope=all listed nothing")
	}

	opened := 0
	for _, row := range rows {
		r, _ := row.(map[string]any)
		sc, _ := r["script"].(map[string]any)
		id, _ := sc["id"].(string)
		if id == "" {
			continue
		}
		owned, _ := r["owned"].(bool)

		status, page := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+id, nil)
		if status != http.StatusOK {
			t.Fatalf("a listed script did not open: GET /portal/scripts/%s: status %d: %v",
				id, status, page)
		}
		if !owned {
			if src, _ := page["source"].(string); src != "" {
				t.Fatal("a script the caller does not own carried its source on its own page")
			}
		}
		opened++
	}
	if opened == 0 {
		t.Fatal("no listed row carried a script id")
	}
}

func TestIssue1795_TheAgentListsEveryScript(t *testing.T) {
	c := connect(t)
	seedIssue1795(t, c)

	out := c.call("manage_script", map[string]any{
		"command": "list",
		"tags":    []any{"acc1795"},
		"purpose": issue1795Purpose,
	})
	count, ok := out["count"].(float64)
	if !ok {
		t.Fatalf("command=list carries no count: %v", out)
	}
	if count < float64(len(issue1795Scripts)) {
		t.Fatalf("command=list counted %v, below the %d this file authored",
			count, len(issue1795Scripts))
	}

	scripts, _ := out["scripts"].([]any)
	for _, row := range scripts {
		r, _ := row.(map[string]any)
		if _, hasSource := r["source"]; hasSource {
			t.Fatal("a listed script carried its source, which the projection must never do")
		}
	}

	// `tags` is typed array and admits no second form: a bare string is
	// refused by the schema before the handler. Asserted rather than assumed,
	// because the wire-forms rule turns on it.
	res, text, err := c.callRaw("manage_script", map[string]any{
		"command": "list",
		"tags":    "acc1795",
		"purpose": issue1795Purpose,
	})
	if err != nil {
		t.Fatalf("command=list with a string tag: transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("manage_script accepted a bare string for tags, which is typed array")
	}
	if !strings.Contains(text, "array") {
		t.Fatalf("the refusal does not name the type it wanted: %s", text)
	}
}
