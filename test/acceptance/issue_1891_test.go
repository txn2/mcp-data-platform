//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #1891: the scripts page gained a Schedules tab, drawn from
// GET /api/v1/portal/scripts/fires, which expands every visible schedule's
// fires on the server with the parse the scheduler fires from.
//
// What these hold, against the running platform: the ticket's four schedules
// land in the sections and with the mark counts it names; a schedule in
// another zone is expanded in its own zone and drawn on the viewer's axis; a
// paused schedule is listed with its marks; a row with no fire in the window
// is [] rather than null; the per-row cap holds; an owner sees their own
// schedules and not another person's, where an administrator sees both; and an
// unknown zone is refused.
//
// Wire forms: `tz` is a query-string parameter and admits one form, a string.
// manage_script's `command`, `name`, `source`, `description`, `cron` and
// `timezone` are typed string in its schema, so each is sent once as that
// literal.

const source1891 = "x = 1\n"

// schedule1891 creates a script owned by c's identity, schedules it, and
// returns its id. The script and its schedule are deleted when the test ends,
// so nothing this file sets keeps firing on the stack.
func schedule1891(t *testing.T, c *client, label, cron, zone string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1891-%s-%d", label, time.Now().UnixNano())
	created := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source1891,
		"description": "Acceptance #1891: a schedule the Schedules tab draws.",
	})
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("manage_script create returned no id: %v", created)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	sched := c.call("manage_script", map[string]any{
		"command": "schedule_set", "name": name, "cron": cron, "timezone": zone,
	})
	if msg, _ := sched["error"].(string); msg != "" {
		t.Fatalf("schedule_set %q refused: %v", cron, sched)
	}
	return id
}

// fires1891 reads the layout for a viewer in zone.
func fires1891(t *testing.T, c *client, zone string) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts/fires?tz="+zone, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /portal/scripts/fires?tz=%s: status %d: %v", zone, status, out)
	}
	return out
}

// row1891 finds a script's row, and the section it was filed under.
func row1891(t *testing.T, out map[string]any, scriptID string) (section string, row map[string]any) {
	t.Helper()
	sections, ok := out["sections"].([]any)
	if !ok || len(sections) != 3 {
		t.Fatalf("the layout does not carry three sections: %v", out["sections"])
	}
	for _, s := range sections {
		sec, _ := s.(map[string]any)
		rows, ok := sec["rows"].([]any)
		if !ok {
			t.Fatalf("section %v has no rows array (null is not an empty list): %v", sec["section"], sec)
		}
		for _, r := range rows {
			m, _ := r.(map[string]any)
			if m["script_id"] == scriptID {
				name, _ := sec["section"].(string)
				return name, m
			}
		}
	}
	return "", nil
}

// fireTimes1891 decodes a row's fires.
func fireTimes1891(t *testing.T, row map[string]any) []time.Time {
	t.Helper()
	raw, ok := row["fires"].([]any)
	if !ok {
		t.Fatalf("the row's fires is not an array: %v", row["fires"])
	}
	out := make([]time.Time, 0, len(raw))
	for _, f := range raw {
		s, _ := f.(string)
		at, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("fire %q: %v", s, err)
		}
		out = append(out, at)
	}
	return out
}

func TestIssue1891_TheTicketsFourSchedulesLandWhereItSays(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	fiveMinutes := schedule1891(t, c, "5m", "*/5 * * * *", "UTC")
	hourly := schedule1891(t, c, "35", "35 * * * *", "UTC")
	weekdays := schedule1891(t, c, "wd", "0 7 * * 1-5", "UTC")
	monthly := schedule1891(t, c, "mo", "0 6 1 * *", "UTC")

	out := fires1891(t, c, "UTC")

	for _, want := range []struct {
		id, section string
		count       int
	}{
		{fiveMinutes, "intraday", 288},
		{hourly, "intraday", 24},
		{weekdays, "multi_day", 5},
		{monthly, "long_term", 3},
	} {
		section, row := row1891(t, out, want.id)
		if row == nil {
			t.Fatalf("script %s is not in the layout", want.id)
		}
		if section != want.section {
			t.Errorf("script %s filed under %s, want %s", want.id, section, want.section)
		}
		fires := fireTimes1891(t, row)
		if len(fires) != want.count {
			t.Errorf("script %s (%s) has %d marks, want %d", want.id, row["cron_spec"], len(fires), want.count)
		}
	}

	_, row := row1891(t, out, weekdays)
	for i, f := range fireTimes1891(t, row) {
		if f.UTC().Weekday() != time.Weekday(i+1) || f.UTC().Hour() != 7 {
			t.Errorf("weekday fire %d is %s, want Monday through Friday at 7 AM", i, f.UTC())
		}
	}
	_, row = row1891(t, out, monthly)
	for _, f := range fireTimes1891(t, row) {
		if f.UTC().Day() != 1 || f.UTC().Hour() != 6 {
			t.Errorf("monthly fire %s is not the 1st at 6 AM", f.UTC())
		}
	}
}

func TestIssue1891_AScheduleIsExpandedInItsOwnZone(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	ny := schedule1891(t, c, "ny", "0 7 * * *", "America/New_York")
	la := schedule1891(t, c, "la", "0 7 * * *", "America/Los_Angeles")

	out := fires1891(t, c, "America/Los_Angeles")
	if out["timezone"] != "America/Los_Angeles" {
		t.Fatalf("the windows were not cut in the viewer's zone: %v", out["timezone"])
	}
	pacific, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	_, nyRow := row1891(t, out, ny)
	_, laRow := row1891(t, out, la)
	nyFires, laFires := fireTimes1891(t, nyRow), fireTimes1891(t, laRow)
	if len(nyFires) == 0 || len(laFires) == 0 {
		t.Fatalf("a daily schedule has no fire this week: ny %v, la %v", nyFires, laFires)
	}
	if got := nyFires[0].In(pacific).Hour(); got != 4 {
		t.Errorf("7 AM New York drawn at %d:00 on a Los Angeles axis, want 4", got)
	}
	if gap := laFires[0].Sub(nyFires[0]); gap != 3*time.Hour {
		t.Errorf("7 AM New York and 7 AM Los Angeles are %s apart, want 3h", gap)
	}
}

func TestIssue1891_APausedScheduleKeepsItsMarks(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id := schedule1891(t, c, "paused", "0 * * * *", "UTC")
	_, _, name := c.scriptByID1891(t, id)
	c.call("manage_script", map[string]any{"command": "schedule_disable", "name": name})

	_, row := row1891(t, fires1891(t, c, "UTC"), id)
	if row == nil {
		t.Fatal("a paused schedule is left off the layout")
	}
	if row["enabled"] != false {
		t.Errorf("a paused schedule reads enabled=%v", row["enabled"])
	}
	if n := len(fireTimes1891(t, row)); n != 24 {
		t.Errorf("a paused hourly schedule has %d marks, want 24", n)
	}
}

func TestIssue1891_EmptyListsAreArraysAndTheCapHolds(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	// A yearly fire six months out: long-term by rate, and absent from the
	// three-month window.
	month := int(time.Now().UTC().AddDate(0, 6, 0).Month())
	silent := schedule1891(t, c, "yearly", fmt.Sprintf("0 6 1 %d *", month), "UTC")
	dense := schedule1891(t, c, "1m", "@every 1m", "UTC")

	out := fires1891(t, c, "UTC")
	if u, ok := out["unreadable"].([]any); !ok {
		t.Errorf("unreadable is not an array: %v", out["unreadable"])
	} else if len(u) != 0 {
		t.Errorf("unreadable schedules on a clean stack: %v", u)
	}

	section, row := row1891(t, out, silent)
	if section != "long_term" {
		t.Errorf("a yearly schedule filed under %s", section)
	}
	if fires, ok := row["fires"].([]any); !ok || len(fires) != 0 {
		t.Errorf("a row with no fire in the window has fires=%v, want []", row["fires"])
	}

	_, row = row1891(t, out, dense)
	if n := len(fireTimes1891(t, row)); n != 500 {
		t.Errorf("an every-minute row carries %d fires, want the cap of 500", n)
	}
	if count, _ := row["fire_count"].(float64); count != 1440 {
		t.Errorf("an every-minute row counts %v fires in a UTC day, want 1440", row["fire_count"])
	}
	if row["truncated"] != true {
		t.Errorf("a capped row reads truncated=%v", row["truncated"])
	}
}

func TestIssue1891_AnOwnerSeesTheirOwnAndAnAdministratorSeesAll(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := schedule1891(t, owner, "vis", "0 9 * * *", "UTC")

	if _, row := row1891(t, fires1891(t, connectAs(t, devPeerAPIKey), "UTC"), id); row != nil {
		t.Error("another person sees the owner's schedule on their Schedules tab")
	}
	if _, row := row1891(t, fires1891(t, connect(t), "UTC"), id); row == nil {
		t.Error("an administrator does not see the owner's schedule")
	}
}

func TestIssue1891_AnUnknownZoneIsRefused(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts/fires?tz=Mars/Olympus", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("tz=Mars/Olympus: status %d, want 400: %v", status, out)
	}
}

// scriptByID1891 resolves a script id to its name through the portal detail
// route, which is what the schedule commands address a script by.
func (c *client) scriptByID1891(t *testing.T, id string) (status int, out map[string]any, name string) {
	t.Helper()
	status, out = c.rest(http.MethodGet, "/api/v1/portal/scripts/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /portal/scripts/%s: status %d: %v", id, status, out)
	}
	contract, _ := out["contract"].(map[string]any)
	name, _ = contract["name"].(string)
	if name == "" {
		t.Fatalf("the contract carries no name: %v", out)
	}
	return status, out, name
}
