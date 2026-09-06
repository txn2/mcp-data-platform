//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Issue #1639: an agent asked who wrote a script had only owner_email to
// answer with, which is where the script is filed and not who wrote the code.
// An administrator can move a script to another owner (#1404), and after a
// move that field names somebody who may never have written a line of it. The
// authors are recorded — every version carries its author and the roles that
// author held at the save — and until now that history was readable in the
// portal and nowhere else.
//
// manage_script command=versions returns it: newest first, each entry naming
// the version, its author, the roles captured at that save, the status, when
// it was written, and the descriptive fields the snapshot carried. Every
// criterion below is executed through the real surfaces — the tool through a
// real tools/call as the person it is about, the transfer through the portal
// route the transfer dialog calls, as an administrator.
//
// Wire forms: the manage_script parameters these calls send are typed in the
// tool's closed input schema and each admits exactly one JSON form — command,
// name, source, description and owner_email are strings, params an array of
// objects — and every call below sends them as literal tools/call parameters
// of that form. The one route touched, PUT /portal/scripts/{id}/owner, takes
// owner_email, a string. There is no untyped or `any` parameter on this path,
// so no parameter has a second form to send.
//
// One criterion in the ticket is not executable here and is covered by a unit
// test instead: a deployment whose store does not implement script.VersionStore
// refuses the command in the terms command=diff refuses in. The dev stack runs
// the PostgreSQL store, which implements versioning, so that shape cannot be
// stood up through the real surface; it is asserted in
// TestVersions_WithoutAVersionStoreRefusesLikeDiff.

// scriptSource1639 is a script that runs nothing external, because these
// criteria are about the record of who SAVED a version, not about execution.
const scriptSource1639 = `
print("acceptance 1639")
`

// scriptSource1639Edited is the second save, which is what produces a second
// version to read an author off.
const scriptSource1639Edited = `
print("acceptance 1639, edited")
`

// createScript1639 saves a script owned and authored by the calling person and
// returns the id the portal route addresses it by.
func createScript1639(t *testing.T, c *client, name string) string {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1639: a script whose authors outlive its ownership.",
		"source":      scriptSource1639,
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	return scriptIDOf1579(t, c, name)
}

// versions1639 reads the history through the tool and returns the entries.
func versions1639(t *testing.T, c *client, args map[string]any) []map[string]any {
	t.Helper()
	args["command"] = "versions"
	out := c.call("manage_script", args)
	raw, ok := out["versions"].([]any)
	if !ok {
		t.Fatalf("manage_script command=versions returned no versions list: %v", out)
	}
	entries := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("a version entry is not an object: %v", item)
		}
		entries = append(entries, entry)
	}
	if count, _ := out["count"].(float64); int(count) != len(entries) {
		t.Fatalf("count %v does not match the %d entries returned", out["count"], len(entries))
	}
	return entries
}

// authorOf1639 reads one entry's author, failing when it carries none.
func authorOf1639(t *testing.T, entry map[string]any) string {
	t.Helper()
	author, _ := entry["author"].(string)
	if author == "" {
		t.Fatalf("a version entry names no author: %v", entry)
	}
	return author
}

// rolesOf1639 reads one entry's captured roles, failing when it carries none.
func rolesOf1639(t *testing.T, entry map[string]any) []string {
	t.Helper()
	raw, ok := entry["author_roles"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("a version entry carries no author_roles: %v", entry)
	}
	roles := make([]string, 0, len(raw))
	for _, r := range raw {
		role, _ := r.(string)
		roles = append(roles, role)
	}
	return roles
}

// TestIssue1639_EveryVersionNamesItsAuthorNewestFirst is criterion 1: a script
// with two applied versions answers with two entries, newest first, each
// carrying a non-empty author and the roles recorded at that save.
func TestIssue1639_EveryVersionNamesItsAuthorNewestFirst(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := "acceptance-1639-history-" + unique1579()
	createScript1639(t, owner, name)
	owner.call("manage_script", map[string]any{
		"command": "update", "name": name, "source": scriptSource1639Edited,
	})

	entries := versions1639(t, owner, map[string]any{"name": name})
	if len(entries) != 2 {
		t.Fatalf("want two applied versions, got %d: %v", len(entries), entries)
	}
	newest, _ := entries[0]["version"].(float64)
	older, _ := entries[1]["version"].(float64)
	if newest != 2 || older != 1 {
		t.Fatalf("the history is not newest first: %v then %v", newest, older)
	}
	for i, entry := range entries {
		if author := authorOf1639(t, entry); author != devOwnerEmail {
			t.Fatalf("entry %d names %q as its author, want %q", i, author, devOwnerEmail)
		}
		if roles := rolesOf1639(t, entry); len(roles) == 0 {
			t.Fatalf("entry %d carries no roles from its save: %v", i, entry)
		}
		if status, _ := entry["status"].(string); status != "applied" {
			t.Fatalf("entry %d has status %q, want applied", i, status)
		}
		if created, _ := entry["created_at"].(string); created == "" {
			t.Fatalf("entry %d says nothing about when it was written: %v", i, entry)
		}
		if _, carriesSource := entry["source"]; carriesSource {
			t.Fatalf("entry %d carries the source; the history is metadata and diff reads a version's code: %v", i, entry)
		}
	}
	t.Logf("history of %s: %v", name, entries)
}

// TestIssue1639_TheAuthorSurvivesAnOwnerTransfer is criterion 2, the question
// the command exists for: after an administrator moves the script, get reports
// the new owner and the history still reports the original author on the
// oldest entry.
func TestIssue1639_TheAuthorSurvivesAnOwnerTransfer(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	// The peer connects before the transfer: an address nobody has ever
	// authenticated with is not one a script can be handed to.
	peer := connectAs(t, devPeerAPIKey)
	admin := connect(t)

	name := "acceptance-1639-transfer-" + unique1579()
	scriptID := createScript1639(t, owner, name)
	owner.call("manage_script", map[string]any{
		"command": "update", "name": name, "source": scriptSource1639Edited,
	})
	t.Cleanup(func() {
		_, _, _ = peer.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})

	before := versions1639(t, owner, map[string]any{"name": name})
	original := authorOf1639(t, before[len(before)-1])

	status, body := admin.rest(http.MethodPut, "/api/v1/portal/scripts/"+scriptID+"/owner",
		strings.NewReader(`{"owner_email":"`+devPeerEmailAddr+`"}`))
	if status != http.StatusOK {
		t.Fatalf("the transfer to %s failed: status %d: %v", devPeerEmailAddr, status, body)
	}

	got := peer.call("manage_script", map[string]any{"command": "get", "name": name})
	if ownerEmail, _ := got["owner_email"].(string); ownerEmail != devPeerEmailAddr {
		t.Fatalf("get reports %q as the owner after the transfer, want %q", ownerEmail, devPeerEmailAddr)
	}

	after := versions1639(t, peer, map[string]any{"name": name})
	oldest := after[len(after)-1]
	if v, _ := oldest["version"].(float64); v != 1 {
		t.Fatalf("the last entry is version %v, want the first version: %v", v, oldest)
	}
	if author := authorOf1639(t, oldest); author != original || author != devOwnerEmail {
		t.Fatalf("the first version now names %q as its author, want the person who wrote it (%q)",
			author, devOwnerEmail)
	}
	t.Logf("after the transfer to %s, version 1 is still written by %s", devPeerEmailAddr, original)
}

// TestIssue1639_ARefusalIsTheOneGetGives is criterion 3: somebody who is
// neither the owner nor an administrator is refused the history in exactly the
// words they are refused the script, because naming the difference would
// confirm the script exists to a caller who may not see it.
func TestIssue1639_ARefusalIsTheOneGetGives(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	stranger := connectAs(t, devPeerAPIKey)
	name := "acceptance-1639-private-" + unique1579()
	createScript1639(t, owner, name)

	versionsRes, versionsText, err := stranger.callRaw("manage_script",
		map[string]any{"command": "versions", "name": name})
	if err != nil {
		t.Fatalf("manage_script command=versions: transport error: %v", err)
	}
	getRes, getText, err := stranger.callRaw("manage_script",
		map[string]any{"command": "get", "name": name})
	if err != nil {
		t.Fatalf("manage_script command=get: transport error: %v", err)
	}
	if !versionsRes.IsError || !getRes.IsError {
		t.Fatalf("a stranger reached the script: versions error=%v (%s), get error=%v (%s)",
			versionsRes.IsError, versionsText, getRes.IsError, getText)
	}
	if versionsText != getText {
		t.Fatalf("the two refusals differ, which tells a stranger the script exists:\n  versions: %q\n  get:      %q",
			versionsText, getText)
	}
	t.Logf("both refusals read: %s", getText)
}

// TestIssue1639_AnAdministratorReadsAnybodysHistory covers the other half of
// the visibility rule get applies: an administrator reads the history of a
// script they do not own, addressed by its owner.
func TestIssue1639_AnAdministratorReadsAnybodysHistory(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	admin := connect(t)
	name := "acceptance-1639-admin-read-" + unique1579()
	createScript1639(t, owner, name)

	entries := versions1639(t, admin, map[string]any{"name": name, "owner_email": devOwnerEmail})
	if len(entries) == 0 {
		t.Fatalf("an administrator read no history for %s", name)
	}
	if author := authorOf1639(t, entries[len(entries)-1]); author != devOwnerEmail {
		t.Fatalf("the first version names %q as its author, want %q", author, devOwnerEmail)
	}
}
