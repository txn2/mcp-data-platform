//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// Acceptance for #2435: the Automations list narrows by handler type and
// shows each script's handler types. Filtering by Schedule lists exactly the
// scripts holding a schedule handler, through the list API and in the portal;
// a script with no handler appears under None; a script with a form and a
// schedule carries both types and appears under both filters; and an owner
// adds a second schedule from a script's Handlers section and sees both.
// The portal's half runs in Chromium (ui/e2e/handlers) against the dev stack.
//
// Wire forms: GET /api/v1/portal/scripts takes type, repeatable, and search as
// query strings; manage_script handler_set takes kind, cron and timezone as
// strings as literal tools/call params.

type scripts2435 struct {
	tag, scheduled, both, none, scheduledID string
}

// newScripts2435 makes three scripts sharing a fresh prefix: one with a
// schedule, a form with a schedule, and one with no handler.
func newScripts2435(t *testing.T, admin *client) scripts2435 {
	t.Helper()
	tag := endpointName("acc2435")
	s := scripts2435{tag: tag, scheduled: tag + "-sched", none: tag + "-none"}
	for _, name := range []string{s.scheduled, s.none} {
		admin.saveScript(map[string]any{"command": "create", "name": name, "source": issue2434Tick}, nil)
		t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	}
	admin.scheduleSet(s.scheduled, "17 3 * * 2", "UTC", nil)
	s.scheduledID = issue2179ScriptID(t, admin, s.scheduled)
	form, _ := bindForm(t, admin, tag+"-form", formFields, map[string]any{"auth_mode": "authenticated", "callers": []string{"*"}})
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": form}) })
	admin.scheduleSet(form, "0 6 * * *", "UTC", nil)
	s.both = form
	return s
}

// rows2435 is the listing's rows for the scripts named with the prefix, by
// name, narrowed by the query.
func rows2435(t *testing.T, c *client, tag, query string) map[string]map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/portal/scripts?search="+url.QueryEscape(tag)+query, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the listing %s: %d %v", query, status, out)
	}
	got := map[string]map[string]any{}
	for _, row := range listOf(out, "data") {
		sc, _ := row["script"].(map[string]any)
		name, _ := sc["name"].(string)
		got[name] = row
	}
	return got
}

func names2435(rows map[string]map[string]any) []string {
	out := make([]string, 0, len(rows))
	for name := range rows {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func types2435(row map[string]any) []string {
	raw, _ := row["handler_types"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestIssue2435_TheListNarrowsByHandlerType: type=schedule lists the two
// scripts with a schedule, type=none the one with no handler, type=form the
// form; every row carries its types, [] for none; type=form&type=none lists
// both.
func TestIssue2435_TheListNarrowsByHandlerType(t *testing.T) {
	admin := connect(t)
	s := newScripts2435(t, admin)
	cases := map[string][]string{
		"&type=schedule":           {s.both, s.scheduled},
		"&type=none":               {s.none},
		"&type=form":               {s.both},
		"&type=form&type=none":     {s.both, s.none},
		"&type=endpoint":           {},
		"&type=webhook&type=form":  {s.both},
		"":                         {s.both, s.none, s.scheduled},
		"&type=schedule&type=none": {s.both, s.none, s.scheduled},
	}
	for query, want := range cases {
		slices.Sort(want)
		got := names2435(rows2435(t, admin, s.tag, query))
		if !slices.Equal(got, want) {
			t.Errorf("the listing %q holds %v, want %v", query, got, want)
		}
	}
	all := rows2435(t, admin, s.tag, "")
	if got := types2435(all[s.both]); !slices.Equal(got, []string{"form", "schedule"}) {
		t.Errorf("the form with a schedule carries the types %v", got)
	}
	if got := types2435(all[s.scheduled]); !slices.Equal(got, []string{"schedule"}) {
		t.Errorf("the scheduled script carries the types %v", got)
	}
	if raw, ok := all[s.none]["handler_types"].([]any); !ok || len(raw) != 0 {
		t.Errorf("a script with no handler carries handler_types %v, want []", all[s.none]["handler_types"])
	}
	if status, out := admin.rest(http.MethodGet, "/api/v1/portal/scripts?endpoint=bound", http.NoBody); status != http.StatusBadRequest {
		t.Errorf("the removed endpoint= answered %d %v, want 400", status, out)
	}
}

// browser2435 runs one case of ui/e2e/handlers against the dev stack.
func browser2435(t *testing.T, s scripts2435, grep string) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/handlers/playwright.config.ts", "--grep", grep)
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(), "HANDLERS_BASE_URL="+baseURL(), "HANDLERS_API_KEY="+devAPIKey(),
		"HANDLERS_TAG="+s.tag, "HANDLERS_SCHEDULED="+s.scheduled, "HANDLERS_BOTH="+s.both, "HANDLERS_NONE="+s.none,
		"HANDLERS_SCRIPT_ID="+s.scheduledID)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright against %s:\n%s", baseURL(), out)
	if err != nil {
		t.Fatalf("the browser case %q failed: %v", grep, err)
	}
}

func TestIssue2435_InChromiumScheduleListsTheScheduledScripts(t *testing.T) {
	browser2435(t, newScripts2435(t, connect(t)), "criterion 1:")
}

func TestIssue2435_InChromiumAScriptWithNoHandlerIsUnderNone(t *testing.T) {
	browser2435(t, newScripts2435(t, connect(t)), "criterion 2:")
}

func TestIssue2435_InChromiumAFormWithAScheduleShowsBothBadges(t *testing.T) {
	browser2435(t, newScripts2435(t, connect(t)), "criterion 3:")
}

func TestIssue2435_InChromiumTheOwnerAddsASecondSchedule(t *testing.T) {
	admin := connect(t)
	s := newScripts2435(t, admin)
	browser2435(t, s, "criterion 4:")
	if held := admin.handlersOf(s.scheduled, "schedule", ""); len(held) != 2 {
		t.Fatalf("the script holds %d schedules after the second was added in the portal, want 2", len(held))
	}
}
