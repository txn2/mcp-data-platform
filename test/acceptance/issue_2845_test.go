//go:build integration

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2845: the imap attachment repair leaves a folder an event
// names where it is when the message's folder of the new form already holds
// files, records the pass instead of failing it on every poll, and lists the
// folder as not moved.
//
// Wire forms: the admin API takes one JSON object per route and answers the
// source with GET /api/v1/admin/webhooks/sources/{name}; a folder is moved
// with POST /api/v1/resources/folders/move (a JSON object), a file uploaded
// with POST /api/v1/resources (multipart form, the file last) and read with
// GET /api/v1/resources/{id}.
//
// The library is laid out as a source the first version of the repair ran on
// leaves a message filed twice: the event's files in a folder of the old form,
// and the message's folder of the new form already holding the files the
// first version moved there for the message's other event.

// issue2845Setup returns a source owing the repair whose event's files sit in
// a folder of the old form while the message's folder holds the files fill
// uploads there, the old folder, the message folder, and the event's
// attachments.
func issue2845Setup(t *testing.T, c *client, fill func(scope, scopeID, messageFolder string, files []map[string]any)) (
	name, old, messageFolder string, atts []issue2836Attachment,
) {
	t.Helper()
	name, folder, _ := issue2836Setup(t, c)
	atts = issue2836Attachments(t, c, name)
	scope, scopeID := issue2836Library(t, atts[0].URI)
	messageFolder = path.Dir(strings.SplitN(strings.TrimPrefix(atts[0].URI, "mcp://"), "/", 3)[2])
	old = path.Dir(messageFolder) + "/" + strings.ToLower(folder) + "-5-16690"
	if st, out := c.rest(http.MethodPost, "/api/v1/resources/folders/move", jsonBody(t, map[string]any{
		"scope": scope, "scope_id": scopeID, "from": messageFolder, "to": old,
	})); st != http.StatusOK {
		t.Fatalf("moving %s to %s: %d %v", messageFolder, old, st, out)
	}
	fill(scope, scopeID, messageFolder, issue2836Files(t, c, scope, scopeID, old))

	db := platformDB2539(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `UPDATE webhook_imap_repairs SET version = 1 WHERE source = $1`, name); err != nil {
		t.Fatalf("recording the first version's repair: %v", err)
	}
	return name, old, messageFolder, atts
}

// issue2845Repaired waits for the source's repair to be recorded by this
// version and returns it as the source's page reads it.
func issue2845Repaired(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	db := platformDB2539(t)
	var repair map[string]any
	waitFor(t, "the source's repair recorded by this version", func() (bool, string) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var version int
		if err := db.QueryRowContext(ctx, `SELECT version FROM webhook_imap_repairs WHERE source = $1`, name).Scan(&version); err != nil {
			return false, err.Error()
		}
		_, out := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
		repair, _ = out["attachment_repair"].(map[string]any)
		return version == 2 && repair != nil, fmt.Sprintf("version %d: %v", version, repair)
	})
	return repair
}

// issue2845Unmoved checks the repair listed old as not moved, and nothing as
// failed or moved.
func issue2845Unmoved(t *testing.T, repair map[string]any, old string) {
	t.Helper()
	if repair["unmoved"] != float64(1) || repair["failed"] != float64(0) {
		t.Fatalf("the repair: %v", repair)
	}
	report, _ := repair["report"].(map[string]any)
	unmoved, _ := json.Marshal(report["unmoved"])
	failed, _ := json.Marshal(report["failed"])
	moved, _ := json.Marshal(report["moved"])
	if !strings.Contains(string(unmoved), old) || string(failed) != "[]" || strings.Contains(string(moved), old) {
		t.Fatalf("unmoved %s, failed %s, moved %s; want %s unmoved alone", unmoved, failed, moved, old)
	}
}

// TestIssue2845_AFolderWhoseMessageFolderHoldsItsFilesIsLeftWhereItIs: the
// message's folder already holds files of the names the event's folder holds,
// which the library refuses to move onto. The repair is recorded, lists the
// folder as not moved and none as failed, and every attachment the event
// names answers by its id at its address; the files already in the message's
// folder stay.
func TestIssue2845_AFolderWhoseMessageFolderHoldsItsFilesIsLeftWhereItIs(t *testing.T) {
	c := connect(t)
	var taken []string
	name, old, _, atts := issue2845Setup(t, c, func(scope, scopeID, messageFolder string, files []map[string]any) {
		for _, f := range files {
			status, content := c.restText("/api/v1/resources/" + fmt.Sprint(f["id"]) + "/content")
			if status != http.StatusOK {
				t.Fatalf("reading %v: %d", f["id"], status)
			}
			taken = append(taken, issue2836Upload(t, c, scope, scopeID, messageFolder, fmt.Sprint(f["filename"]), []byte(content)))
		}
	})
	issue2845Unmoved(t, issue2845Repaired(t, c, name), old)

	for _, a := range atts {
		status, out := c.rest(http.MethodGet, "/api/v1/resources/"+a.ID, http.NoBody)
		if status != http.StatusOK || !strings.Contains(fmt.Sprint(out["uri"]), old+"/") {
			t.Fatalf("attachment %s answered %d at %v, want it where it was, under %s", a.ID, status, out["uri"], old)
		}
	}
	for _, id := range taken {
		if status, _ := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody); status != http.StatusOK {
			t.Fatalf("the file already at the message's address, %s, answered %d", id, status)
		}
	}
}

// TestIssue2845_AFolderWhoseMessageFolderHoldsOtherFilesIsNotMergedIntoIt:
// the message's folder holds a file of another name, onto which the library
// would accept the move and merge two events' files into one folder. The
// repair leaves the event's folder where it is and lists it as not moved, and
// the message's folder holds only its own file.
func TestIssue2845_AFolderWhoseMessageFolderHoldsOtherFilesIsNotMergedIntoIt(t *testing.T) {
	c := connect(t)
	var scope, scopeID, target string
	name, old, _, atts := issue2845Setup(t, c, func(s, id, messageFolder string, _ []map[string]any) {
		scope, scopeID, target = s, id, messageFolder
		issue2836Upload(t, c, s, id, messageFolder, "other-event.txt", []byte("the message's other event's file"))
	})
	issue2845Unmoved(t, issue2845Repaired(t, c, name), old)

	if files := issue2836Files(t, c, scope, scopeID, old); len(files) != len(atts) {
		t.Fatalf("%s holds %d files, want the event's %d", old, len(files), len(atts))
	}
	files := issue2836Files(t, c, scope, scopeID, target)
	if len(files) != 1 || files[0]["filename"] != "other-event.txt" {
		t.Fatalf("the message's folder holds %v, want only its own file", files)
	}
}
