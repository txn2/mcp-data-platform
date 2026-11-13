//go:build integration

package acceptance

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2434: schedules and webhook triggers on the handler model.
// A script holding two schedules fires on both and the schedule timeline lays
// out both; two scripts bound to one webhook source each get exactly one run
// per event, a redelivered event included; a webhook source not made for a
// form the caller owns is refused to anyone but an administrator, naming the
// rule; the schedule commands and update's event_source are refused naming
// their replacement; and the migration leaves no scripts.event_source and no
// UNIQUE(script_id) on script_schedules.
//
// Wire forms: manage_script handler_set takes kind, cron, timezone and
// event_source as strings and args as a JSON object, the only form its
// schema admits, as literal tools/call params; each webhook delivery is a
// signed JSON object body; the schedule timeline is a GET with tz in the
// query.

const issue2434Tick = "def main():\n    \"\"\"Prints the event it ran for, if one started it.\"\"\"\n    ev = run.event or {}\n    print(ev.get(\"id\", \"\"))\n"

// TestIssue2434_TwoSchedulesFireOnTheirOwnAndTheTimelineShowsBoth: a script
// with two schedule handlers is fired by each, each run carrying the schedule
// that fired it, and the timeline has a row for each schedule.
func TestIssue2434_TwoSchedulesFireOnTheirOwnAndTheTimelineShowsBoth(t *testing.T) {
	c := connect(t)
	name := endpointName("acc-2434-two")
	c.saveScript(map[string]any{"command": "create", "name": name, "source": issue2434Tick}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	scriptID := issue2179ScriptID(t, c, name)
	first := idOf(c.scheduleSet(name, "0 4 * * *", "UTC", nil))
	second := idOf(c.scheduleSet(name, "30 5 * * 1", "America/Los_Angeles", nil))
	t.Cleanup(func() { c.schedulesOff(name) })
	if first == "" || first == second {
		t.Fatalf("two schedules want two ids: %q %q", first, second)
	}

	status, fires := c.rest(http.MethodGet, "/api/v1/portal/scripts/fires?tz=UTC", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the timeline: %d %v", status, fires)
	}
	laid := map[string]string{}
	for _, section := range listOf(fires, "sections") {
		for _, row := range listOf(section, "rows") {
			if row["script_id"] == scriptID {
				laid[fmt.Sprint(row["schedule_id"])] = fmt.Sprint(row["cron_spec"])
			}
		}
	}
	if laid[first] != "0 4 * * *" || laid[second] != "30 5 * * 1" {
		t.Fatalf("the timeline lays out %v for the script, want both schedules", laid)
	}

	db := issue2257DB(t)
	issue1904Exec(t, db, `UPDATE script_schedules SET next_run_at = NOW() - INTERVAL '1 second' WHERE script_id = $1`, scriptID)
	deadline := time.Now().Add(90 * time.Second)
	for {
		var fired int
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT schedule_id) FROM script_runs
			 WHERE script_id = $1 AND trigger_kind = 'schedule' AND schedule_id IN ($2, $3)`, scriptID, first, second).Scan(&fired)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if fired == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of the two schedules fired a run", fired)
		}
		time.Sleep(time.Second)
	}
}

// TestIssue2434_TwoScriptsOnOneSourceRunOncePerEvent: two scripts with a
// webhook handler for one source each get one run per event, and an event
// delivered again starts no second run of either.
func TestIssue2434_TwoScriptsOnOneSourceRunOncePerEvent(t *testing.T) {
	admin := connect(t)
	source := issue2480Source(t, admin)
	var names []string
	for _, label := range []string{"acc-2434-a", "acc-2434-b"} {
		name := endpointName(label)
		admin.saveScript(map[string]any{"command": "create", "name": name, "source": issue2434Tick}, nil)
		t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
		admin.webhookSet(name, source)
		names = append(names, name)
	}
	issue2480Deliver(t, source, 2, "acc2434")
	issue2480Deliver(t, source, 2, "acc2434") // the same two event ids again
	for _, name := range names {
		runs := issue2332Runs(t, admin, name, 2)
		time.Sleep(3 * time.Second) // a duplicate, if one were made, is enqueued with its delivery
		runs = issue2332Runs(t, admin, name, 2)
		ids := map[string]int{}
		for _, r := range runs {
			ids[fmt.Sprint(r["event_id"])]++
		}
		if len(runs) != 2 || ids["acc2434-0"] != 1 || ids["acc2434-1"] != 1 {
			t.Errorf("%s made %d event runs for two events delivered twice: %v", name, len(runs), ids)
		}
	}
}

// TestIssue2434_ASourceNotMadeForTheirOwnFormIsAnAdministrators: anyone but
// an administrator naming a source that holds no form of theirs is refused,
// in the rule's words, on the tool and the portal.
func TestIssue2434_ASourceNotMadeForTheirOwnFormIsAnAdministrators(t *testing.T) {
	admin, owner := connect(t), connectAs(t, devOwnerAPIKey)
	source := issue2480Source(t, admin)
	name := endpointName("acc-2434-owner")
	owner.saveScript(map[string]any{"command": "create", "name": name, "source": issue2434Tick}, nil)
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	out, text := owner.handlerSetRaw(name, "webhook", map[string]any{"event_source": source})
	want := fmt.Sprintf("only an administrator may run a script for source %q; you may name the source of a form you own", source)
	if out != nil || !strings.Contains(text, want) {
		t.Fatalf("the owner naming another source: accepted=%v, refusal %q", out != nil, text)
	}
	status, body := owner.rest(http.MethodPost, "/api/v1/portal/scripts/"+issue2179ScriptID(t, owner, name)+"/handlers",
		jsonBody(t, map[string]any{"kind": "webhook", "event_source": source}))
	if status != http.StatusForbidden || !strings.Contains(fmt.Sprint(body["detail"]), want) {
		t.Fatalf("the owner naming another source on the portal: %d %v", status, body)
	}
	if got := owner.webhookSources(name); len(got) != 0 {
		t.Fatalf("a refused source was bound: %v", got)
	}
}

// TestIssue2434_TheScheduleCommandsAndEventSourceAreRefusedNamingTheReplacement.
func TestIssue2434_TheScheduleCommandsAndEventSourceAreRefusedNamingTheReplacement(t *testing.T) {
	c := connect(t)
	name := endpointName("acc-2434-gone")
	c.saveScript(map[string]any{"command": "create", "name": name, "source": issue2434Tick}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	for args, want := range map[string]string{
		"schedule_set":     "schedule_set was removed: use handler_set with kind=schedule",
		"schedule_list":    "schedule_list was removed: use handler_list with kind=schedule",
		"schedule_enable":  "schedule_enable was removed: use handler_enable",
		"schedule_disable": "schedule_disable was removed: use handler_disable",
		"update":           "event_source on update was removed: run the script for a webhook source with handler_set kind=webhook",
	} {
		params := map[string]any{"command": args, "name": name, "cron": "0 1 * * *"}
		if args == "update" {
			params = map[string]any{"command": "update", "name": name, "event_source": "x"}
		}
		res, text, err := c.callRaw("manage_script", params)
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		if !res.IsError || !strings.Contains(text, want) {
			t.Errorf("%s answered error=%v %q", args, res.IsError, text)
		}
	}
}

// TestIssue2434_TheMigrationLeavesNoEventSourceAndNoOneScheduleRule.
func TestIssue2434_TheMigrationLeavesNoEventSourceAndNoOneScheduleRule(t *testing.T) {
	db := issue2257DB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var column, unique int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_name = 'scripts' AND column_name = 'event_source'`).Scan(&column); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_constraint c JOIN pg_class r ON r.oid = c.conrelid
		 WHERE r.relname = 'script_schedules' AND c.contype = 'u'`).Scan(&unique); err != nil {
		t.Fatal(err)
	}
	if column != 0 || unique != 0 {
		t.Fatalf("scripts.event_source columns: %d; unique constraints on script_schedules: %d; want none of either", column, unique)
	}
}

// TestIssue2434_AnOwnerCannotTurnBackOnAnAdministratorsSource: a webhook
// handler an administrator added for a source the owner may not name, then
// turned off, stays off when the owner asks to turn it on, in the rule's
// words; the owner may still turn it off and delete it.
func TestIssue2434_AnOwnerCannotTurnBackOnAnAdministratorsSource(t *testing.T) {
	admin, owner := connect(t), connectAs(t, devOwnerAPIKey)
	source := issue2480Source(t, admin)
	name := endpointName("acc-2434-reon")
	owner.saveScript(map[string]any{"command": "create", "name": name, "source": issue2434Tick}, nil)
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	added := admin.handlerSet(name, "webhook", map[string]any{"event_source": source, "owner_email": devOwnerEmail})
	if off := admin.call("manage_script", map[string]any{"command": "handler_disable", "handler": idOf(added)}); off["enabled"] != false {
		t.Fatalf("the administrator turning the handler off: %v", off)
	}
	res, text, err := owner.callRaw("manage_script", map[string]any{"command": "handler_enable", "handler": idOf(added)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, fmt.Sprintf("only an administrator may run a script for source %q", source)) {
		t.Fatalf("the owner turning the administrator's source back on: error=%v %q", res.IsError, text)
	}
	if got := owner.call("manage_script", map[string]any{"command": "handler_get", "handler": idOf(added)}); got["enabled"] != false {
		t.Fatalf("the handler was turned on: %v", got)
	}
	if out := owner.call("manage_script", map[string]any{"command": "handler_delete", "handler": idOf(added)}); out["deleted"] != true {
		t.Fatalf("the owner deleting the handler: %v", out)
	}
}

// TestIssue2434_SavingAWebhookHandlerLeavesItOff: a webhook handler turned
// off stays off when it is saved again, by handler_set naming it and by the
// portal's change route; turning it on is handler_enable's. Adding the source
// again, naming no handler, turns it on.
func TestIssue2434_SavingAWebhookHandlerLeavesItOff(t *testing.T) {
	admin := connect(t)
	source := issue2480Source(t, admin)
	name := endpointName("acc-2434-off")
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": issue2434Tick}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	id := idOf(admin.webhookSet(name, source))
	admin.call("manage_script", map[string]any{"command": "handler_disable", "handler": id})

	saved := admin.handlerSet(name, "webhook", map[string]any{"event_source": source, "handler": id})
	if idOf(saved) != id || saved["enabled"] != false {
		t.Fatalf("handler_set naming the handler turned it on or made another: %v", saved)
	}
	status, out := admin.rest(http.MethodPut, "/api/v1/portal/handlers/"+id, jsonBody(t, map[string]any{"event_source": source}))
	if status != http.StatusOK || out["enabled"] != false {
		t.Fatalf("the portal's change route: %d %v; want 200 and still off", status, out)
	}
	if got := admin.call("manage_script", map[string]any{"command": "handler_get", "handler": id}); got["enabled"] != false {
		t.Fatalf("after both saves the handler reads %v; want off", got["enabled"])
	}

	again := admin.webhookSet(name, source)
	if idOf(again) != id || again["enabled"] != true {
		t.Fatalf("adding the source again: %v; want handler %s, on", again, id)
	}
}
