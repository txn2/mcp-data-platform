//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Issue #1980: an external integration signs in with an API key and calls
// api_invoke_endpoint once per upstream record, and every call was cataloged
// and embedded unless an operator knew to name its persona in
// calls.exclude_personas. A persona now carries its own service_account
// setting, edited in the portal persona editor, and the admin Indexing page
// names the callers holding most of the catalog.
//
// Each criterion creates its own database persona and an API key whose role
// maps to it, so the setting is exercised the way an operator uses it: through
// the admin persona API the editor saves with, on the persona the key reaches
// through its roles. Neither is in calls.exclude_personas, so what excludes the
// calls is the persona's own setting.
//
// Wire forms: service_account is a JSON boolean on the admin persona body;
// the true and false forms are sent, and a string "true" is refused.

const (
	issue1980Path     = "/v1/pagination/link"
	issue1980Attempts = 40
	issue1980Pause    = 250 * time.Millisecond
)

// issue1980Caller is one automated caller: a persona and an API key mapped to
// it, both removed at cleanup.
type issue1980Caller struct {
	persona string
	key     string
}

// newIssue1980Caller creates a database persona with the given service-account
// setting and an API key whose only role maps to it.
func newIssue1980Caller(t *testing.T, admin *client, serviceAccount bool) issue1980Caller {
	t.Helper()
	suffix := time.Now().UnixNano()
	name := fmt.Sprintf("acc-1980-%d", suffix)
	role := fmt.Sprintf("acc_1980_%d", suffix)

	status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": name, "display_name": "Acceptance 1980 integration", "roles": []any{role},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
		"service_account": serviceAccount,
	}))
	if status != http.StatusCreated {
		t.Fatalf("creating persona %s answered %d %v", name, status, out)
	}
	if got, _ := out["service_account"].(bool); got != serviceAccount {
		t.Fatalf("persona %s saved with service_account=%v, the response says %v", name, serviceAccount, out["service_account"])
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody) })

	status, out = admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": name, "roles": []any{role}, "description": "Acceptance #1980 automated caller",
	}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("creating key %s answered %d %v", name, status, out)
	}
	key, _ := out["key"].(string)
	if key == "" {
		t.Fatalf("key %s was created without a secret: %v", name, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, http.NoBody) })
	return issue1980Caller{persona: name, key: key}
}

// issue1980SetServiceAccount saves the persona with the setting, the way the persona
// editor's Save does.
func setServiceAccount(t *testing.T, admin *client, caller issue1980Caller, on bool) {
	t.Helper()
	status, out := admin.rest(http.MethodGet, "/api/v1/admin/personas/"+caller.persona, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading persona %s answered %d %v", caller.persona, status, out)
	}
	body := map[string]any{
		"display_name": out["display_name"], "roles": out["roles"],
		"allow_tools": out["allow_tools"], "deny_tools": out["deny_tools"],
		"allow_connections": out["allow_connections"], "service_account": on,
	}
	status, out = admin.rest(http.MethodPut, "/api/v1/admin/personas/"+caller.persona, jsonBody(t, body))
	if status != http.StatusOK {
		t.Fatalf("saving persona %s with service_account=%v answered %d %v", caller.persona, on, status, out)
	}
	if got, _ := out["service_account"].(bool); got != on {
		t.Fatalf("persona %s saved with service_account=%v, the response says %v", caller.persona, on, out["service_account"])
	}
}

// issue1980CallsTotal is the admin catalog's count of records matching the query.
func callsTotal(t *testing.T, admin *client, query string) int {
	t.Helper()
	status, out := admin.rest(http.MethodGet, "/api/v1/admin/calls?per_page=1&"+query, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/calls?%s answered %d %v", query, status, out)
	}
	return int(number(t, out, "total"))
}

// issue1980IndexExpected is the calls kind's expected vector count on the admin
// Indexing page, or -1 when the deployment has no calls index.
func callsIndexExpected(t *testing.T, admin *client) int {
	t.Helper()
	status, out := admin.rest(http.MethodGet, "/api/v1/admin/index-jobs", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/index-jobs answered %d %v", status, out)
	}
	kinds, _ := out["kinds"].([]any)
	for _, k := range kinds {
		kind, _ := k.(map[string]any)
		if kind["kind"] != "calls" {
			continue
		}
		cov, _ := kind["coverage"].(map[string]any)
		if cov == nil {
			return -1
		}
		return int(number(t, cov, "expected"))
	}
	return -1
}

// issue1980Units is how many index units of the calls kind the record has.
func callsUnits(t *testing.T, admin *client, recordID string) int {
	t.Helper()
	status, out := admin.rest(http.MethodGet,
		"/api/v1/admin/index-jobs/jobs?kind=calls&limit=500&source_id="+url.QueryEscape(recordID), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("listing the calls units of %s answered %d %v", recordID, status, out)
	}
	jobs, _ := out["jobs"].([]any)
	return len(jobs)
}

// issue1980Eventually polls until check holds, for work the platform does after it has
// answered: a sweep started by a save runs in the background.
func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	for attempt := range issue1980Attempts {
		if attempt > 0 {
			time.Sleep(issue1980Pause)
		}
		if check() {
			return
		}
	}
	t.Fatalf("%s: still not true after %s", what, time.Duration(issue1980Attempts)*issue1980Pause)
}

// TestIssue1980_AMarkedCallersCallIsAuditedAndWritesNoRecordOrIndexUnit is
// criterion 1.
func TestIssue1980_AMarkedCallersCallIsAuditedAndWritesNoRecordOrIndexUnit(t *testing.T) {
	admin := connect(t)
	caller := newIssue1980Caller(t, admin, true)
	machine := connectAs(t, caller.key)
	citable := fetchOnce(machine, "Acceptance #1980: an integration fetches one upstream record.")
	person := drainPast(t, admin, "Acceptance #1980: the barrier call an ordinary caller makes.")

	events := admin.list("/api/v1/admin/audit/events?session_id=" + machine.sessionID +
		"&tool_name=api_invoke_endpoint&per_page=50")
	if len(events) == 0 {
		t.Fatal("a service account's call must be audited; no audit event for the session")
	}
	ev, _ := events[0].(map[string]any)
	if got, _ := ev["persona"].(string); got != caller.persona {
		t.Errorf("audit event persona = %v, want %q", ev["persona"], caller.persona)
	}
	for _, field := range []string{"id", "user_id", "timestamp", "duration_ms", "connection"} {
		if ev[field] == nil || ev[field] == "" {
			t.Errorf("audit event field %s is empty; the audit row must be complete: %v", field, ev)
		}
	}

	// No record, so no index unit can exist for it: a calls unit is keyed by
	// its record's id, and the catalog holds no record of this session.
	if n := callsTotal(t, admin, "session_id="+machine.sessionID); n != 0 {
		t.Errorf("a service account's call must write no call record, got %d", n)
	}
	if citable {
		t.Error("a service account's result carries a call reference that would resolve to nothing")
	}
	if !person.citedLastCall {
		t.Error("an ordinary caller's result must still carry its call reference")
	}
}

// TestIssue1980_MarkingACallerRemovesItsRecordsVectorsAndUnits is criterion 2.
func TestIssue1980_MarkingACallerRemovesItsRecordsVectorsAndUnits(t *testing.T) {
	admin := connect(t)
	caller := newIssue1980Caller(t, admin, false)
	machine := connectAs(t, caller.key)

	// Unmarked, the integration's calls are cataloged like anyone's.
	const calls = 3
	for i := range calls {
		fetchOnce(machine, fmt.Sprintf("Acceptance #1980: an integration fetches upstream record %d.", i))
	}
	rec := awaitRecord(admin, machine.sessionID)
	userID, _ := rec["user_id"].(string)
	if userID == "" {
		t.Fatalf("the integration's record carries no user id: %v", rec)
	}
	person := drainPast(t, admin, "Acceptance #1980: the barrier call an ordinary caller makes.")
	personRecord := awaitRecord(admin, person.sessionID)
	eventually(t, "every integration call is cataloged", func() bool {
		return callsTotal(t, admin, "session_id="+machine.sessionID) == calls
	})

	// Every record gets an index unit, whether the reconciler has reached it
	// yet or not: this is the pending work the sweep must take with it.
	records := admin.list("/api/v1/admin/calls?per_page=50&session_id=" + machine.sessionID)
	ids := make([]string, 0, len(records))
	for _, r := range records {
		row, _ := r.(map[string]any)
		id, _ := row["id"].(string)
		ids = append(ids, id)
		status, out := admin.rest(http.MethodPost, "/api/v1/admin/index-jobs/reindex",
			jsonBody(t, map[string]any{"kind": "calls", "source_id": id}))
		if status != http.StatusOK && status != http.StatusAccepted {
			t.Fatalf("queueing an index unit for %s answered %d %v", id, status, out)
		}
		if callsUnits(t, admin, id) == 0 {
			t.Fatalf("record %s has no index unit to remove", id)
		}
	}
	expectedBefore := callsIndexExpected(t, admin)

	setServiceAccount(t, admin, caller, true)

	eventually(t, "the marked caller's records are swept", func() bool {
		return callsTotal(t, admin, "user_id="+url.QueryEscape(userID)) == 0
	})
	for _, id := range ids {
		if n := callsUnits(t, admin, id); n != 0 {
			t.Errorf("record %s is gone and still has %d index unit(s)", id, n)
		}
		if status, _ := admin.rest(http.MethodGet, "/api/v1/admin/calls/"+id, http.NoBody); status != http.StatusNotFound {
			t.Errorf("GET /api/v1/admin/calls/%s = %d, want 404: the record and its vector are one row", id, status)
		}
	}
	if expectedBefore >= 0 {
		if after := callsIndexExpected(t, admin); expectedBefore-after != calls {
			t.Errorf("the calls index total fell from %d to %d, want a fall of exactly %d", expectedBefore, after, calls)
		}
	}

	// A person's own record, under an unmarked persona, is untouched.
	if id, _ := personRecord["id"].(string); id != "" {
		if status, _ := admin.rest(http.MethodGet, "/api/v1/admin/calls/"+id, http.NoBody); status != http.StatusOK {
			t.Errorf("an ordinary caller's record answered %d after the sweep, want 200", status)
		}
	}
}

// TestIssue1980_TheIndexPageNamesTheLargestCallers is criterion 3: the route
// the Indexing page's Top callers list reads.
func TestIssue1980_TheIndexPageNamesTheLargestCallers(t *testing.T) {
	admin := connect(t)
	person := connectAs(t, devOwnerAPIKey)
	fetchOnce(person, "Acceptance #1980: a call so the catalog is not empty.")
	awaitRecord(admin, person.sessionID)

	status, out := admin.rest(http.MethodGet, "/api/v1/admin/calls/top-callers", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/calls/top-callers answered %d %v", status, out)
	}
	if number(t, out, "total") <= 0 {
		t.Fatalf("the catalog holds records and the count says %v", out["total"])
	}
	for _, list := range []string{"principals", "personas"} {
		rows, ok := out[list].([]any)
		if !ok || len(rows) == 0 {
			t.Fatalf("%s is empty or not a list: %v", list, out[list])
		}
		previous := -1.0
		for _, r := range rows {
			row, _ := r.(map[string]any)
			records, share := number(t, row, "records"), number(t, row, "share")
			if previous >= 0 && records > previous {
				t.Errorf("%s is not largest first: %v", list, rows)
			}
			previous = records
			if share <= 0 || share > 1 {
				t.Errorf("%s row share %v is outside (0, 1]: %v", list, share, row)
			}
			if _, ok := row["service_account"].(bool); !ok {
				t.Errorf("%s row carries no service_account flag: %v", list, row)
			}
		}
	}

	// The mark a row carries is the persona's own setting.
	personas, _ := out["personas"].([]any)
	top, _ := personas[0].(map[string]any)
	name, _ := top["persona"].(string)
	if name == "" {
		return // calls made without a persona, which has no editor to link to
	}
	status, detail := admin.rest(http.MethodGet, "/api/v1/admin/personas/"+url.PathEscape(name), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the largest persona %q has no editor to link to: %d %v", name, status, detail)
	}
	if top["service_account"] != detail["service_account"] {
		t.Errorf("top callers says %q service_account=%v, the persona says %v",
			name, top["service_account"], detail["service_account"])
	}
}

// TestIssue1980_APersonsCallUnderAnUnmarkedPersonaIsCataloged is criterion 5.
func TestIssue1980_APersonsCallUnderAnUnmarkedPersonaIsCataloged(t *testing.T) {
	admin := connect(t)
	newIssue1980Caller(t, admin, true) // a marked persona exists beside it
	person := connectAs(t, devOwnerAPIKey)
	fetchOnce(person, "Acceptance #1980: a person's own call under an unmarked persona.")
	rec := awaitRecord(admin, person.sessionID)
	if got, _ := rec["kind"].(string); got != "api" {
		t.Errorf("record kind = %v, want api (record: %v)", rec["kind"], rec)
	}
	if got, _ := rec["persona"].(string); strings.HasPrefix(got, "acc-1980-") {
		t.Errorf("the person's record is under the marked persona: %v", rec)
	}
}

// TestIssue1980_TheSettingIsABoolean pins the wire form: service_account is a
// JSON boolean, and a string is refused rather than read as true.
func TestIssue1980_TheSettingIsABoolean(t *testing.T) {
	admin := connect(t)
	name := fmt.Sprintf("acc-1980-str-%d", time.Now().UnixNano())
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": name, "display_name": "Acceptance 1980 string form", "roles": []any{"acc_1980_str"},
		"service_account": "true",
	}))
	if status == http.StatusCreated {
		admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody)
	}
	if status != http.StatusBadRequest {
		t.Errorf(`service_account:"true" answered %d %v, want 400`, status, out)
	}
}
