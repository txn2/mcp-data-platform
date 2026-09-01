//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1576: moving a resource out of its uploader's own library used to
// revoke the script that maintains it while the person kept the authority.
//
// A run authenticates as script:<name>, a principal that owns nothing, so the
// resource rules read the address of the person it acts for. The uploader grant
// that made a run able to replace its author's file held only while the file sat
// in its own uploader's user library, and a move rewrites the library and never
// the uploader columns -- so after a move the person could still replace the
// content and their script could not.
//
// Every criterion below is executed as the person it is about: the two ordinary
// non-administrator people the dev stack carries, and the administrator for the
// move only criterion 2 needs one for. The refresh runs through run_script, so
// what is exercised is a real run of a saved script presenting its author's
// identity, not a tool call made by the person.
//
// Criterion 5 -- a run acting for nobody, and an empty address never matching an
// empty uploader -- is not reachable from this surface: every call here carries
// an authenticated identity and every resource records an uploader, which is
// what the platform's own doors enforce. It is proved in
// pkg/resource/permission_test.go (TestTheModifyArmNeverMatchesAnAbsentIdentity).
//
// Wire forms: every parameter touched here admits exactly one JSON form.
// manage_resource's action, reference, content, content_type, filename,
// display_name, path, description and change_summary are strings and tags an
// array of strings; manage_table's action, reference, connection, table_name and
// registration_id are strings and follow a boolean; manage_script's command,
// name, description and source are strings, params an array of objects, and
// run_script's name a string, args an object and wait_seconds a number. The move
// is a REST PATCH whose body carries scope, scope_id and path as strings, and
// whose {id} is a path segment. Each is sent below in that one form, as literal
// tools/call params or as the literal request body.

// scriptSource1576 is the body of a scheduled refresh: it writes new bytes over
// a managed resource its author uploaded, which is the write a move used to
// revoke.
const scriptSource1576 = `
result = platform.call("manage_resource", {
    "action": "replace_content",
    "reference": run.params["reference"],
    "content": run.params["content"],
    "change_summary": "Acceptance #1576: the scheduled refresh.",
})
print("replaced to version %s" % result["version"])
`

func stamp1576() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// upload1576 files a CSV in the calling person's own library and returns the
// reference and the id: the reference is what a script names it by, the id is
// what the move route is addressed by.
func upload1576(t *testing.T, c *client, name string) (reference, id string) {
	t.Helper()
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"display_name": "Acceptance 1576 " + name,
		"description":  "Acceptance #1576: a CSV a scheduled script refreshes, then moved to another library.",
		"filename":     "acc-1576-" + name + ".csv",
		"path":         "acceptance/issue-1576",
		"content_type": "text/csv",
		"content":      "region,units\nnorth,10\n",
	})
	reference, _ = out["reference"].(string)
	id, _ = out["resource_id"].(string)
	if reference == "" || id == "" {
		t.Fatalf("manage_resource create returned no reference or id: %v", out)
	}
	t.Cleanup(func() {
		admin := connect(t)
		_, _ = admin.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
	})
	return reference, id
}

// refreshScript1576 saves a script owned and authored by the calling person that
// replaces a named resource's content.
func refreshScript1576(t *testing.T, c *client, name string) string {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1576: replaces the content of a managed resource on a schedule.",
		"source":      scriptSource1576,
		"params": []any{
			map[string]any{
				"name": "reference", "type": "string", "required": true,
				"description": "The managed resource this run refreshes.",
			},
			map[string]any{
				"name": "content", "type": "string", "required": true,
				"description": "The bytes the run writes over it.",
			},
		},
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	return name
}

// refresh1576 runs the script and returns the finished run, whatever its status:
// a criterion about a refusal reads the run's own error, which is where a person
// with a schedule reads it.
func refresh1576(t *testing.T, c *client, script, reference, content string) map[string]any {
	t.Helper()
	out := c.call("run_script", map[string]any{
		"name":         script,
		"args":         map[string]any{"reference": reference, "content": content},
		"wait_seconds": 120,
	})
	if status, _ := out["status"].(string); status == "" {
		t.Fatalf("run of %s reported no status: %v", script, out)
	}
	return out
}

// moveTo1576 refiles the resource through the route the portal's Library control
// calls, as whoever is making the move.
func moveTo1576(t *testing.T, c *client, id string, body string) {
	t.Helper()
	status, out := c.rest(http.MethodPatch, "/api/v1/resources/"+id, strings.NewReader(body))
	if status != http.StatusOK {
		t.Fatalf("moving %s with %s: status %d: %v", id, body, status, out)
	}
}

// versionOf1576 reads the resource's current version count through the route the
// portal's version panel reads, so a successful refresh is checked by what it
// left behind rather than only by the run saying it succeeded.
func versionOf1576(t *testing.T, c *client, id string) int {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources/"+id+"/versions", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET versions of %s: status %d: %v", id, status, body)
	}
	rows, _ := body["versions"].([]any)
	return len(rows)
}

// TestIssue1576_AMoveIntoAPersonaTheAuthorBelongsToKeepsTheScriptWriting is
// criterion 1: a person who is not a platform administrator uploads a CSV into
// their own library, has a script that replaces its content, then moves the file
// into a persona library they belong to. The next run replaces the content.
func TestIssue1576_AMoveIntoAPersonaTheAuthorBelongsToKeepsTheScriptWriting(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := stamp1576()
	reference, id := upload1576(t, owner, name)
	script := refreshScript1576(t, owner, "acc-1576-persona-"+name)

	before := refresh1576(t, owner, script, reference, "region,units\nnorth,11\n")
	if status, _ := before["status"].(string); status != "succeeded" {
		t.Fatalf("the premise fails: the run before the move did not succeed: %v", before)
	}

	// The move the ticket is about: into a persona the person BELONGS to, which
	// CanMoveToLibrary permits without persona-admin authority.
	moveTo1576(t, owner, id, `{"scope":"persona","scope_id":"collaborator","path":"acceptance/issue-1576"}`)

	after := refresh1576(t, owner, script, reference, "region,units\nnorth,12\n")
	if status, _ := after["status"].(string); status != "succeeded" {
		t.Fatalf("the run after the move did not succeed: %v", after)
	}
	if got := versionOf1576(t, owner, id); got != 3 {
		t.Fatalf("the file should carry three versions after two refreshes, got %d", got)
	}
}

// TestIssue1576_AnAdministratorsMoveToGlobalKeepsTheAuthorsScriptWriting is
// criterion 2: the same move to the global library, made by an administrator on
// behalf of the non-administrator author, leaves the author's script able to
// replace the content. Whether the automation survives a move must not depend on
// who wrote the script.
func TestIssue1576_AnAdministratorsMoveToGlobalKeepsTheAuthorsScriptWriting(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	admin := connect(t)
	name := stamp1576()
	reference, id := upload1576(t, owner, name)
	script := refreshScript1576(t, owner, "acc-1576-global-"+name)

	moveTo1576(t, admin, id, `{"scope":"global","scope_id":"","path":"acceptance/issue-1576"}`)

	run := refresh1576(t, owner, script, reference, "region,units\nnorth,21\n")
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the author's script was refused a file an administrator published for them: %v", run)
	}
	if got := versionOf1576(t, owner, id); got != 2 {
		t.Fatalf("the refresh left no new version: %d", got)
	}
}

// TestIssue1576_TheScriptReachesExactlyWhatItsAuthorReaches is criterion 3:
// wherever the person may replace the file's content their script may, and
// wherever they may not it may not. Both halves are executed on the same file,
// in the global library, by two people: the author, who uploaded it, and a peer,
// who did not and holds no authority over that library.
func TestIssue1576_TheScriptReachesExactlyWhatItsAuthorReaches(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	admin := connect(t)
	name := stamp1576()
	reference, id := upload1576(t, owner, name)
	moveTo1576(t, admin, id, `{"scope":"global","scope_id":"","path":"acceptance/issue-1576"}`)

	// The author, as a person and as their script.
	res, text, err := owner.callRaw("manage_resource", map[string]any{
		"action": "replace_content", "reference": reference, "content": "region,units\nnorth,31\n",
	})
	if err != nil || res.IsError {
		t.Fatalf("the author may not replace their own moved file: %v %s", err, text)
	}
	ownerScript := refreshScript1576(t, owner, "acc-1576-mine-"+name)
	run := refresh1576(t, owner, ownerScript, reference, "region,units\nnorth,32\n")
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the author's script is refused where the author is admitted: %v", run)
	}

	// The peer, as a person and as their script. The refusal has to name the
	// library, which is what a scheduled run writes into its log.
	res, text, err = peer.callRaw("manage_resource", map[string]any{
		"action": "replace_content", "reference": reference, "content": "region,units\nnorth,33\n",
	})
	if err != nil {
		t.Fatalf("peer replace: transport error: %v", err)
	}
	if !res.IsError && !strings.Contains(text, "cannot replace") {
		t.Fatalf("a peer with no claim on the file replaced its content: %s", text)
	}
	if !strings.Contains(text, "global") {
		t.Fatalf("the refusal does not name the library the file is in: %s", text)
	}
	peerScript := refreshScript1576(t, peer, "acc-1576-theirs-"+name)
	peerRun := refresh1576(t, peer, peerScript, reference, "region,units\nnorth,34\n")
	if status, _ := peerRun["status"].(string); status == "succeeded" {
		t.Fatalf("a peer's script replaced a file its author has no claim on: %v", peerRun)
	}
	if failure, _ := peerRun["error"].(string); !strings.Contains(failure, "global") {
		t.Fatalf("the run's own error does not name the library: %v", peerRun)
	}
}

// TestIssue1576_ARunStillCannotSeeWhatItsAuthorCannotSee is criterion 4. The
// modify rule reads the uploader address wherever the file now sits; the
// visibility rule still does not, so nothing here became visible that was not.
// The peer's own library is the case: the author holds no claim on a file
// uploaded there, and their script is answered that there is no such resource
// rather than being refused a write on one.
func TestIssue1576_ARunStillCannotSeeWhatItsAuthorCannotSee(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	name := stamp1576()
	reference, _ := upload1576(t, peer, name)

	_, text, err := owner.callRaw("manage_resource", map[string]any{
		"action": "replace_content", "reference": reference, "content": "region,units\nnorth,41\n",
	})
	if err != nil {
		t.Fatalf("owner replace: transport error: %v", err)
	}
	if !strings.Contains(text, "you can see") {
		t.Fatalf("the premise fails: the person is not answered that the file is absent: %s", text)
	}

	script := refreshScript1576(t, owner, "acc-1576-unseen-"+name)
	run := refresh1576(t, owner, script, reference, "region,units\nnorth,42\n")
	if status, _ := run["status"].(string); status == "succeeded" {
		t.Fatalf("a run reached a file its author cannot see: %v", run)
	}
	if failure, _ := run["error"].(string); !strings.Contains(failure, "you can see") {
		t.Fatalf("the run was refused for the wrong reason: %v", run)
	}
}

// TestIssue1576_ATableOverAMovedFileFollowsTheVersionsTheScriptWrites is
// criterion 6: a registration made before the move goes on serving what the
// script writes after it. A registration that stops following is the reason the
// broken case is worth catching -- a scheduled refresh whose table silently
// stops moving is a report that quietly goes stale.
func TestIssue1576_ATableOverAMovedFileFollowsTheVersionsTheScriptWrites(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	admin := connect(t)
	name := stamp1576()
	reference, id := upload1576(t, owner, name)
	script := refreshScript1576(t, owner, "acc-1576-table-"+name)

	registered := owner.call("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": scratchConnection,
		"table_name": "acc_1576_" + name,
	})
	registrationID, _ := registered["registration_id"].(string)
	queryTable, _ := registered["query_table"].(string)
	if registrationID == "" || queryTable == "" {
		t.Fatalf("manage_table register returned no registration: %v", registered)
	}
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_table", map[string]any{
			"action": "unregister", "registration_id": registrationID,
		})
	})

	moveTo1576(t, admin, id, `{"scope":"global","scope_id":"","path":"acceptance/issue-1576"}`)

	run := refresh1576(t, owner, script, reference, "region,units\nnorth,51\nsouth,52\n")
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the refresh behind the table did not run: %v", run)
	}

	listing := owner.call("manage_table", map[string]any{"action": "list", "reference": reference})
	rows, _ := listing["registrations"].([]any)
	var followError string
	var found bool
	for _, entry := range rows {
		reg, _ := entry.(map[string]any)
		if reg["registration_id"] == registrationID {
			found = true
			followError, _ = reg["follow_error"].(string)
		}
	}
	if !found {
		t.Fatalf("the registration is gone after the move: %v", listing)
	}
	if followError != "" {
		t.Fatalf("the table stopped following the file the script writes: %s", followError)
	}

	queried := owner.call("trino_query", map[string]any{
		"connection": scratchConnection,
		"sql":        "SELECT count(*) AS n FROM " + queryTable,
		"purpose":    "Acceptance #1576: the table registered before the move serves what the script wrote after it.",
	})
	if !strings.Contains(fmt.Sprintf("%v", queried), "2") {
		t.Fatalf("the table does not serve the two rows the run wrote: %v", queried)
	}
}
