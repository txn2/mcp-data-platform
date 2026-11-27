//go:build integration

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Acceptance for #2842: the imap attachment repair collapses the copies of a
// message no event names, the same folder and UID under different UIDVALIDITY
// values on one day holding the same files, into the first numbering, and a
// source the first version repaired is repaired again by this one.
//
// Wire forms: the admin API takes one JSON object per route and answers the
// source with GET /api/v1/admin/webhooks/sources/{name}; a file is uploaded
// with POST /api/v1/resources (multipart form, the file last) and read with
// GET /api/v1/resources/{id}; resources/read takes `uri`, a string.
//
// The library is laid out as a release before #2836 left a mailbox whose
// events have expired: a message's attachments filed under two numberings of
// its folder, and a third numbering of the same UID holding another message.

// TestIssue2842_ARenumberedCopyNoEventNamesIsCollapsedIntoTheFirst: the repair
// a source owes after the upgrade leaves the first numbering where it is,
// deletes the later one, counts it as collapsed rather than kept, keeps the
// other message, and the deleted copy's address resolves to the file that
// stays.
func TestIssue2842_ARenumberedCopyNoEventNamesIsCollapsedIntoTheFirst(t *testing.T) {
	c := connect(t)
	name, _, _ := issue2836Setup(t, c)
	atts := issue2836Attachments(t, c, name)
	scope, scopeID := issue2836Library(t, atts[0].URI)
	// The mail folder name the old form carries; a library folder segment is at
	// most 31 characters, so the numbering beside it leaves room for "inbox".
	mbox := "inbox"
	day := "webhooks/" + name + "/attachments/2018-06-20"
	first, later, other := day+"/"+mbox+"-1538884683-11033", day+"/"+mbox+"-1794972606-11033", day+"/"+mbox+"-1894972606-11033"

	files := map[string]string{"plan.txt": "the plan, as sent", "logo.txt": "a signature image"}
	keep := map[string]string{}
	var copies []string
	for file, body := range files {
		keep[file] = issue2836Upload(t, c, scope, scopeID, first, file, []byte(body))
		copies = append(copies, issue2836Upload(t, c, scope, scopeID, later, file, []byte(body)))
	}
	otherID := issue2836Upload(t, c, scope, scopeID, other, "plan.txt", []byte("another message's plan"))

	// The source as the first version of the repair recorded it.
	db := platformDB2539(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `UPDATE webhook_imap_repairs SET version = 1 WHERE source = $1`, name); err != nil {
		t.Fatalf("recording the first version's repair: %v", err)
	}

	var repair map[string]any
	waitFor(t, "the second repair of the source", func() (bool, string) {
		_, out := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
		repair, _ = out["attachment_repair"].(map[string]any)
		return repair != nil && repair["collapsed"] == float64(1), fmt.Sprintf("%v", repair)
	})
	if repair["collapsed_files"] != float64(len(copies)) || repair["kept"] != float64(2) {
		t.Fatalf("the repair: %v", repair)
	}
	report, _ := repair["report"].(map[string]any)
	collapsed, _ := json.Marshal(report["collapsed"])
	kept, _ := json.Marshal(report["kept"])
	if !strings.Contains(string(collapsed), later) || strings.Contains(string(collapsed), first) {
		t.Fatalf("collapsed lists %s, want %s alone", collapsed, later)
	}
	if !strings.Contains(string(kept), first) || !strings.Contains(string(kept), other) || strings.Contains(string(kept), later) {
		t.Fatalf("kept lists %s, want %s and %s", kept, first, other)
	}

	for _, id := range copies {
		if status, _ := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody); status != http.StatusNotFound {
			t.Fatalf("the later copy %s answered %d, want it deleted", id, status)
		}
	}
	for file, id := range keep {
		status, out := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
		if status != http.StatusOK || !strings.Contains(fmt.Sprint(out["uri"]), first+"/"+file) {
			t.Fatalf("the first numbering's %s answered %d at %v, want it where it was", file, status, out["uri"])
		}
	}
	if status, _ := c.rest(http.MethodGet, "/api/v1/resources/"+otherID, http.NoBody); status != http.StatusOK {
		t.Fatalf("another message under the same UID answered %d, want it kept", status)
	}

	// A reference to the deleted copy still reads the file that stays.
	copyURI := "mcp://" + scope + "/" + scopeID + "/" + later + "/plan.txt"
	res, err := c.session.ReadResource(c.ctx, &mcp.ReadResourceParams{URI: copyURI})
	if err != nil {
		t.Fatalf("resources/read of the deleted copy's address: %v", err)
	}
	if len(res.Contents) == 0 || !strings.Contains(res.Contents[0].Text+string(res.Contents[0].Blob), files["plan.txt"]) {
		t.Fatalf("the deleted copy's address read %v, want the kept file's content", res.Contents)
	}
}
