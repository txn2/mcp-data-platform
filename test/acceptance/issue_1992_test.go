//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// Issue #1992: the Schedules tab listed a section's rows by rhythm and then by
// first fire, so finding one script meant scanning every row, and its rows
// carried nothing to filter on. The fires route now orders every section's
// rows by script name, ignoring case, and each row carries its script's owner,
// category and tags, which the tab's filters narrow on.
//
// The criteria run against the running platform: scripts are created and
// scheduled through manage_script, and the layout is read from the route the
// Schedules tab reads. The filters themselves are the tab's own (criteria 3
// and 4), checked in the portal and recorded in build/1992/acceptance.md.
//
// Wire forms: the fires route takes tz as a query-string string, which has no
// second form. manage_script's category is a string and tags an array of
// strings, each sent once as a literal tools/call parameter.

// schedule1992 creates a script under name with a daily schedule, filed under
// category and tags when they are given, and returns its id.
func schedule1992(t *testing.T, c *client, name, category string, tags []any) string {
	t.Helper()
	create := map[string]any{
		"command": "create", "name": name, "source": source1891,
		"description": "Acceptance #1992: a schedule the Schedules tab lists by name.",
	}
	if category != "" {
		create["category"] = category
	}
	if tags != nil {
		create["tags"] = tags
	}
	created := c.saveScript(create, nil)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("manage_script create returned no id: %v", created)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	if out := c.call("manage_script", map[string]any{
		"command": "schedule_set", "name": name, "cron": "0 7 * * *", "timezone": "UTC",
	}); out["error"] != nil {
		t.Fatalf("schedule_set refused: %v", out)
	}
	return id
}

// TestIssue1992_RowsAreInNameOrderIgnoringCase is criterion 1: every section's
// rows are in case-insensitive name order, whatever else is scheduled on the
// deployment.
func TestIssue1992_RowsAreInNameOrderIgnoringCase(t *testing.T) {
	c := connect(t)
	stamp := time.Now().UnixNano() % 1_000_000_000
	ids := map[string]string{}
	for _, name := range []string{"acc-1992-c", "acc-1992-a", "acc-1992-b"} {
		full := fmt.Sprintf("%s-%d", name, stamp)
		ids[schedule1992(t, c, full, "", nil)] = full
	}

	out := fires1891(t, c, "UTC")
	sections, _ := out["sections"].([]any)
	var ours []string
	for _, s := range sections {
		sec, _ := s.(map[string]any)
		rows, _ := sec["rows"].([]any)
		names := make([]string, 0, len(rows))
		for _, r := range rows {
			row, _ := r.(map[string]any)
			name, _ := row["script_name"].(string)
			names = append(names, name)
			if id, _ := row["script_id"].(string); ids[id] != "" {
				ours = append(ours, name)
			}
		}
		if !slices.IsSortedFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }) {
			t.Errorf("section %v is not in case-insensitive name order: %v", sec["section"], names)
		}
	}
	want := []string{
		fmt.Sprintf("acc-1992-a-%d", stamp), fmt.Sprintf("acc-1992-b-%d", stamp), fmt.Sprintf("acc-1992-c-%d", stamp),
	}
	if !slices.Equal(ours, want) {
		t.Errorf("the three daily schedules read %v, want %v", ours, want)
	}
}

// TestIssue1992_RowsCarryOwnerCategoryAndTags is criterion 2: each row carries
// owner_email, category and tags, and a script with no tags carries [].
func TestIssue1992_RowsCarryOwnerCategoryAndTags(t *testing.T) {
	c := connect(t)
	stamp := time.Now().UnixNano() % 1_000_000_000
	filed := schedule1992(t, c, fmt.Sprintf("acc-1992-filed-%d", stamp), "reporting", []any{"sales", "acc-1992"})
	bare := schedule1992(t, c, fmt.Sprintf("acc-1992-bare-%d", stamp), "", nil)

	status, raw := c.rest(http.MethodGet, "/api/v1/portal/scripts/fires?tz=UTC", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /portal/scripts/fires: status %d", status)
	}
	_, filedRow := row1891(t, raw, filed)
	_, bareRow := row1891(t, raw, bare)
	if filedRow == nil || bareRow == nil {
		t.Fatalf("the scheduled scripts are not in the layout: filed %v, bare %v", filedRow, bareRow)
	}
	for _, row := range []map[string]any{filedRow, bareRow} {
		if owner, _ := row["owner_email"].(string); !strings.Contains(owner, "@") {
			t.Errorf("row %v carries no owner_email", row["script_name"])
		}
	}
	if filedRow["category"] != "reporting" {
		t.Errorf("category = %v, want reporting", filedRow["category"])
	}
	if tags, _ := filedRow["tags"].([]any); !slices.Equal(tags, []any{"sales", "acc-1992"}) {
		t.Errorf("tags = %v, want [sales acc-1992]", filedRow["tags"])
	}
	tags, ok := bareRow["tags"].([]any)
	if !ok || len(tags) != 0 {
		t.Errorf("a script with no tags carries %#v, want []", bareRow["tags"])
	}
	if bareRow["category"] != "" {
		t.Errorf("a script with no category carries %#v, want \"\"", bareRow["category"])
	}
}
