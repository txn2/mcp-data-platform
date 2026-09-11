//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1677: a managed script run filed managed resources in a user library
// keyed by its author's address while the author's session used their
// subject, so one path was two files. These run the ticket's sentences through
// the real client against a running platform: a run and its author's session
// resolve scope=user to one library; a get at the address a run wrote finds the
// file from the author's session; and a file a run filed by address before the
// platform knew the subject is folded into the session's library, keeping its
// id and its old address.
//
// Wire forms: this ticket adds and changes no parameter. Every parameter sent
// is typed and its schema closed -- `scope`, `scope_id`, `path`, `filename`,
// `content`, `content_type`, `reference` are strings, `wait_seconds` an
// integer -- so each admits one JSON form, and that is the form sent.

const issue1677Folder = "acceptance/issue-1677"

// issue1677ScriptSource writes two files at the ticket's folder from inside a
// run -- one through the managed-resource destination of platform.export, one
// through manage_resource -- and records where each landed.
const issue1677ScriptSource = `
out = platform.export(
    name="Acceptance 1677 export",
    rows=[{"probe": 1}],
    format="csv",
    destination="resources",
    key="acceptance/issue-1677/%s",
)
made = platform.call("manage_resource", {
    "action": "create",
    "path": "acceptance/issue-1677",
    "filename": "%s",
    "display_name": "Acceptance 1677 run probe",
    "description": "Written by manage_resource from inside a run.",
    "content": "probe,1\n",
    "content_type": "text/csv",
    "if_exists": "replace",
})
platform.save_state({
    "export_id": out["resource_id"],
    "export_uri": out["uri"],
    "export_version": str(out["version"]),
    "probe_uri": made["uri"],
    "probe_scope_id": made["scope_id"],
})
`

func issue1677Address(filename string) map[string]any {
	return map[string]any{"path": issue1677Folder, "filename": filename}
}

// issue1677Create files a resource at the ticket's folder from the session.
func issue1677Create(c *client, filename string, extra map[string]any) map[string]any {
	c.t.Helper()
	args := map[string]any{
		"action": "create", "path": issue1677Folder, "filename": filename,
		"display_name": "Acceptance 1677 " + filename,
		"description":  "Written by the #1677 acceptance run.",
		"content":      "day,high\nmon,71\n", "content_type": "text/csv",
		"if_exists": "replace",
	}
	for k, v := range extra {
		args[k] = v
	}
	return c.call("manage_resource", args)
}

func issue1677Get(c *client, filename string, extra map[string]any) map[string]any {
	c.t.Helper()
	args := issue1677Address(filename)
	args["action"] = "get"
	for k, v := range extra {
		args[k] = v
	}
	return c.call("manage_resource", args)
}

// issue1677Library is the library key of a URI the platform minted:
// mcp://user/<key>/<path>/<filename>.
func issue1677Library(t *testing.T, uri string) string {
	t.Helper()
	rest, ok := strings.CutPrefix(uri, "mcp://user/")
	if !ok {
		t.Fatalf("not a user-library address: %q", uri)
	}
	key, _, ok := strings.Cut(rest, "/"+issue1677Folder+"/")
	if !ok {
		t.Fatalf("the address is not at the ticket's folder: %q", uri)
	}
	return key
}

func issue1677Script(t *testing.T, c *client, name, exportFile, probeFile string) map[string]any {
	t.Helper()
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1677: a run files where its author's session looks.",
		"source":      fmt.Sprintf(issue1677ScriptSource, exportFile, probeFile),
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	got := c.call("manage_script", map[string]any{"command": "state", "name": name, "state_action": "get"})
	state, _ := got["state"].(map[string]any)
	if state == nil {
		t.Fatalf("the run recorded no state: %v", got)
	}
	return state
}

func issue1677Cleanup(t *testing.T, c *client, references ...string) {
	t.Helper()
	for _, ref := range references {
		if ref == "" {
			continue
		}
		_, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref, "force": true})
	}
}

// TestIssue1677_ARunAndItsAuthorsSessionResolveOneLibrary is the ticket's
// first, second and fourth sentences: the session files a resource in its own
// library, a run of a script the same person authored files two more at the
// same folder with no scope named, and every one of them lands in the one
// library the session resolves scope=user to -- so the session's get at the
// address the run wrote finds the run's file, and the folder lists one library.
func TestIssue1677_ARunAndItsAuthorsSessionResolveOneLibrary(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	sessionFile := "session-" + stamp + ".csv"
	exportFile := "export-" + stamp + ".csv"
	probeFile := "probe-" + stamp + ".csv"

	mine := issue1677Create(c, sessionFile, nil)
	mineRef, _ := mine["reference"].(string)
	t.Cleanup(func() { issue1677Cleanup(t, c, mineRef) })
	mineURI, _ := mine["uri"].(string)
	library := issue1677Library(t, mineURI)
	if strings.Contains(library, "@") {
		t.Fatalf("the session's own library is keyed by address, not subject: %q", mineURI)
	}

	state := issue1677Script(t, c, "acc-1677-"+stamp, exportFile, probeFile)
	exportURI, _ := state["export_uri"].(string)
	probeURI, _ := state["probe_uri"].(string)
	t.Cleanup(func() {
		issue1677Cleanup(t, c, "mcp:resource:"+fmt.Sprint(state["export_id"]))
		_, _, _ = c.callRaw("manage_resource", map[string]any{
			"action": "delete", "path": issue1677Folder, "filename": probeFile, "force": true,
		})
	})

	t.Logf("session filed %s; the run's export landed at %s and its manage_resource create at %s",
		mineURI, exportURI, probeURI)
	if got := issue1677Library(t, exportURI); got != library {
		t.Errorf("platform.export landed in library %q; the author's session files in %q", got, library)
	}
	if got := issue1677Library(t, probeURI); got != library {
		t.Errorf("manage_resource from the run landed in library %q; the author's session files in %q", got, library)
	}
	if got, _ := state["probe_scope_id"].(string); got != library {
		t.Errorf("the run's create reported scope_id %q; want the session's library %q", got, library)
	}

	// The get at the address the run wrote, from the author's session.
	found := issue1677Get(c, exportFile, nil)
	if ok, _ := found["found"].(bool); !ok {
		t.Fatalf("the author's session does not find the file the run wrote: %v", found)
	}
	record, _ := found["resource"].(map[string]any)
	if got, _ := record["resource_id"].(string); got != fmt.Sprint(state["export_id"]) {
		t.Errorf("the session found a different file: %q vs the run's %v", got, state["export_id"])
	}

	// The folder lists one library, with each file once.
	listed := c.call("manage_resource", map[string]any{"action": "list", "path": issue1677Folder, "limit": 100})
	rows, _ := listed["resources"].([]any)
	names := map[string]int{}
	for _, row := range rows {
		entry, _ := row.(map[string]any)
		name, _ := entry["filename"].(string)
		if !strings.Contains(name, stamp) {
			continue
		}
		names[name]++
		if got, _ := entry["scope_id"].(string); got != library {
			t.Errorf("%s is listed in library %q; want %q", name, got, library)
		}
	}
	for _, name := range []string{sessionFile, exportFile, probeFile} {
		if names[name] != 1 {
			t.Errorf("%s is listed %d times; want once", name, names[name])
		}
	}
}

// TestIssue1677_AFileARunFiledByAddressIsFoldedIntoTheSessionsLibrary is the
// ticket's third sentence. A file already filed under mcp://user/<address>/...
// -- what a run wrote before the platform knew the author's subject -- is
// folded into the session's library before the next run writes: the run's
// export records version 2 of that same file, the session's get at its own
// address finds it, and the old address still resolves to it.
func TestIssue1677_AFileARunFiledByAddressIsFoldedIntoTheSessionsLibrary(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	rolling := "rolling-" + stamp + ".csv"
	probeFile := "fold-probe-" + stamp + ".csv"

	// The caller's own address, read off a script the session owns rather than
	// assumed from the key the harness holds.
	name := "acc-1677-fold-" + stamp
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "description": "Acceptance #1677: fold.",
		"source": fmt.Sprintf(issue1677ScriptSource, rolling, probeFile),
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	owned := c.call("manage_script", map[string]any{"command": "get", "name": name})
	address, _ := owned["owner_email"].(string)
	if !strings.Contains(address, "@") {
		t.Fatalf("the script does not name its owner's address: %v", owned)
	}

	// A file filed by address, as a run did before the platform knew the subject.
	legacy := issue1677Create(c, rolling, map[string]any{"scope": "user", "scope_id": address})
	legacyID, _ := legacy["resource_id"].(string)
	legacyRef, _ := legacy["reference"].(string)
	legacyURI, _ := legacy["uri"].(string)
	t.Cleanup(func() {
		issue1677Cleanup(t, c, legacyRef)
		_, _, _ = c.callRaw("manage_resource", map[string]any{
			"action": "delete", "path": issue1677Folder, "filename": probeFile, "force": true,
		})
	})
	if issue1677Library(t, legacyURI) != address {
		t.Fatalf("the file was not filed by address: %q", legacyURI)
	}
	t.Logf("filed by address as a run once did: %s (%s)", legacyURI, legacyRef)
	if before := issue1677Get(c, rolling, nil); before["found"] == true {
		t.Fatalf("the address-keyed file is already at the session's own address: %v", before)
	}

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}
	got := c.call("manage_script", map[string]any{"command": "state", "name": name, "state_action": "get"})
	state, _ := got["state"].(map[string]any)
	if state == nil {
		t.Fatalf("the run recorded no state: %v", got)
	}
	if id := fmt.Sprint(state["export_id"]); id != legacyID {
		t.Fatalf("the run wrote a second file %q instead of versioning the folded one %q: %v", id, legacyID, state)
	}
	if v, _ := state["export_version"].(string); v != "2" {
		t.Errorf("the run recorded version %q of the folded file; want 2", v)
	}
	exportURI, _ := state["export_uri"].(string)
	t.Logf("after the run the same file (%s) is version %v at %s", legacyID, state["export_version"], exportURI)
	if strings.Contains(issue1677Library(t, exportURI), "@") {
		t.Errorf("the run still wrote by address: %q", exportURI)
	}

	// From the author's session: the file is at its own address, the old
	// address still resolves to it, and the reference still fetches it.
	found := issue1677Get(c, rolling, nil)
	if ok, _ := found["found"].(bool); !ok {
		t.Fatalf("the folded file is not at the session's own address: %v", found)
	}
	record, _ := found["resource"].(map[string]any)
	if id, _ := record["resource_id"].(string); id != legacyID {
		t.Errorf("the session found %q; want the folded file %q", id, legacyID)
	}
	if got, _ := record["uri"].(string); got != exportURI {
		t.Errorf("the session's address is %q; the run wrote %q", got, exportURI)
	}
	old := issue1677Get(c, rolling, map[string]any{"scope": "user", "scope_id": address})
	if ok, _ := old["found"].(bool); !ok {
		t.Fatalf("the address the file vacated no longer resolves: %v", old)
	}
	oldRecord, _ := old["resource"].(map[string]any)
	if id, _ := oldRecord["resource_id"].(string); id != legacyID {
		t.Errorf("the old address resolves to %q; want %q", id, legacyID)
	}
	doc := c.call("fetch", map[string]any{"reference": legacyRef, "purpose": "Acceptance #1677: the reference survives the fold."})
	if ok, _ := doc["found"].(bool); !ok {
		t.Fatalf("fetch of the reference no longer resolves: %v", doc)
	}
}
