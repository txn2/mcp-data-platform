//go:build integration

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2836: an imap source's attachments keep one address for the
// life of the source, whatever the mail server does to its UIDs.
//
// Wire forms: the admin API takes one JSON object per route (as #2247's
// criteria send it); trino_query is called through the MCP client with string
// `connection`, `sql` and `purpose`; the library is read over
// GET /api/v1/resources (query parameters scope, scope_id, path) and
// GET /api/v1/resources/facets (scope, scope_id, parent), a folder moved with
// POST /api/v1/resources/folders/move (a JSON object) and a file uploaded with
// POST /api/v1/resources (multipart form, the file last).
//
// The criteria run against the dev stack's Dovecot server, as #2247's do. A
// folder is renumbered with doveadm, which gives it a new UIDVALIDITY.

// issue2836Message is a message with two attachments of one name and a third.
func issue2836Message(run string) string {
	att := func(name, body string) []string {
		return []string{
			"--b1",
			`Content-Type: text/plain; name="` + name + `"`,
			`Content-Disposition: attachment; filename="` + name + `"`,
			"",
			body,
		}
	}
	lines := []string{
		"Authentication-Results: " + issue2247AuthServ + "; dkim=pass header.d=zoom.example; spf=pass smtp.mailfrom=zoom.example; dmarc=pass header.from=zoom.example",
		"From: Zoom <" + issue2247Sender + ">",
		"To: platform@acme.example",
		"Subject: Minutes " + run,
		"Date: Wed, 21 Oct 2026 16:00:00 +0000",
		"Message-ID: <minutes-" + run + "@zoom.example>",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="b1"`,
		"",
		"--b1",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Minutes attached.",
	}
	lines = append(lines, att("minutes.txt", "first minutes")...)
	lines = append(lines, att("minutes.txt", "second minutes, longer")...)
	lines = append(lines, att("agenda.txt", "agenda")...)
	lines = append(lines, "--b1--", "")
	return issue2247CRLF(lines...)
}

// issue2836Setup makes a folder holding the message and a source reading it,
// and returns the source's name, the folder and the run id.
func issue2836Setup(t *testing.T, c *client) (name, folder, run string) {
	t.Helper()
	run = strconv.FormatInt(time.Now().UnixNano()%1_000_000_000_000, 36)
	folder = "Acc2836-" + run
	mb := issue2247Mailbox(t)
	if err := mb.Create(folder, nil).Wait(); err != nil {
		t.Fatalf("creating folder %s: %v", folder, err)
	}
	t.Cleanup(func() { _ = mb.Delete(folder).Wait() })
	issue2247Append(t, mb, folder, issue2836Message(run))
	name = "mail2836-" + run
	issue1870Create(t, c, issue2247Source(t, name, folder, issue2247Password))
	return name, folder, run
}

// issue2836Attachment is one attachment as the event names it.
type issue2836Attachment struct {
	Name, URI, ID string
}

// issue2836Attachments are the attachments the source's events name.
func issue2836Attachments(t *testing.T, c *client, name string) []issue2836Attachment {
	t.Helper()
	rows := issue2247Await(t, c, name)
	var out []issue2836Attachment
	for _, row := range rows {
		atts, _ := issue2247Payload(t, row)["attachments"].([]any)
		for _, a := range atts {
			m, _ := a.(map[string]any)
			out = append(out, issue2836Attachment{Name: fmt.Sprint(m["name"]), URI: fmt.Sprint(m["uri"]), ID: fmt.Sprint(m["resource_id"])})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URI < out[j].URI })
	return out
}

// issue2836Library is the scope and id of the library an attachment's URI
// names: mcp://{scope}/{id}/... .
func issue2836Library(t *testing.T, uri string) (scope, scopeID string) {
	t.Helper()
	rest, ok := strings.CutPrefix(uri, "mcp://")
	parts := strings.SplitN(rest, "/", 3)
	if !ok || len(parts) < 3 {
		t.Fatalf("an attachment URI of an unexpected form: %s", uri)
	}
	return parts[0], parts[1]
}

// issue2836Files lists the files under a folder of a library.
func issue2836Files(t *testing.T, c *client, scope, scopeID, folder string) []map[string]any {
	t.Helper()
	q := url.Values{"scope": {scope}, "scope_id": {scopeID}, "path": {folder}, "limit": {"200"}}
	status, out := c.rest(http.MethodGet, "/api/v1/resources?"+q.Encode(), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("listing %s: %d %v", folder, status, out)
	}
	raw, _ := out["resources"].([]any)
	files := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			files = append(files, m)
		}
	}
	return files
}

// issue2836Distinct is how many distinct events the source's table holds.
func issue2836Distinct(t *testing.T, c *client, name string) float64 {
	t.Helper()
	got := c.call("trino_query", map[string]any{
		"connection": issue1870Conn, "purpose": "Acceptance for #2836: one event per message.",
		"sql": "SELECT count(DISTINCT event_id) AS n FROM " + issue1870Table(name),
	})
	rows, _ := got["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("count: %v", got)
	}
	n, _ := rows[0].(map[string]any)["n"].(float64)
	return n
}

// issue2836Validity is the folder's UIDVALIDITY the source's last poll read.
func issue2836Validity(t *testing.T, c *client, name string) float64 {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the source: %d %v", status, out)
	}
	folders, _ := out["imap"].([]any)
	if len(folders) != 1 {
		return 0
	}
	v, _ := folders[0].(map[string]any)["uid_validity"].(float64)
	return v
}

// issue2836Renumber gives folder a new UIDVALIDITY on the dev server.
func issue2836Renumber(t *testing.T, folder string, validity int64) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "docker", "exec", "acme-dev-dovecot", // #nosec G204 -- the dev stack's own container
		"doveadm", "mailbox", "update", "-u", issue2247User, "--uid-validity", strconv.FormatInt(validity, 10), folder).CombinedOutput()
	if err != nil {
		t.Fatalf("renumbering %s: %v: %s", folder, err, out)
	}
}

// Criterion 1: the folder is renumbered and polled again; the library holds
// each attachment once, at the same uri and resource id, with a second
// version, and the source's events are the same messages.
func TestIssue2836_ARenumberedFolderKeepsEachAttachmentOnce(t *testing.T) {
	c := connect(t)
	name, folder, _ := issue2836Setup(t, c)
	first := issue2836Attachments(t, c, name)
	if len(first) != 3 {
		t.Fatalf("attachments: %v", first)
	}
	scope, scopeID := issue2836Library(t, first[0].URI)
	events := issue2836Distinct(t, c, name)
	before := issue2836Validity(t, c, name)

	issue2836Renumber(t, folder, int64(before)+1000)
	waitFor(t, "the source's poll of the renumbered folder", func() (bool, string) {
		v := issue2836Validity(t, c, name)
		return v != before, fmt.Sprintf("uid_validity %v", v)
	})
	waitFor(t, "a second version of each attachment", func() (bool, string) {
		for _, a := range first {
			if n := versionOf1576(t, c, a.ID); n < 2 {
				return false, fmt.Sprintf("%s has %d versions", a.URI, n)
			}
		}
		return true, ""
	})
	files := issue2836Files(t, c, scope, scopeID, "webhooks/"+name+"/attachments")
	if len(files) != 3 {
		t.Fatalf("the library holds %d attachments after the re-read, want the same 3: %v", len(files), files)
	}
	ids := map[string]string{}
	for _, f := range files {
		ids[fmt.Sprint(f["id"])] = fmt.Sprint(f["uri"])
	}
	for _, a := range first {
		if ids[a.ID] != a.URI {
			t.Fatalf("attachment %s is no longer at %s: %v", a.ID, a.URI, ids)
		}
	}
	if got := issue2836Distinct(t, c, name); got != events {
		t.Fatalf("the source holds %v messages after the re-read, want %v", got, events)
	}
}

// Criterion 2: a message with two attachments of the same name keeps both.
func TestIssue2836_TwoAttachmentsOfOneNameAreBothKept(t *testing.T) {
	c := connect(t)
	name, _, _ := issue2836Setup(t, c)
	atts := issue2836Attachments(t, c, name)
	names := map[string]string{}
	for _, a := range atts {
		names[path.Base(a.URI)] = a.ID
	}
	if len(names) != 3 || names["minutes.txt"] == "" || names["minutes-2.txt"] == "" || names["agenda.txt"] == "" {
		t.Fatalf("attachments by name: %v", names)
	}
	for file, want := range map[string]string{"minutes.txt": "first minutes", "minutes-2.txt": "second minutes, longer"} {
		status, body := c.restText("/api/v1/resources/" + names[file] + "/content")
		if status != http.StatusOK || strings.TrimSpace(body) != want {
			t.Fatalf("%s answered %d %q, want %q", file, status, body, want)
		}
	}
}

// Criterion 3: deleting the source leaves no file and no folder under
// webhooks/{source}/attachments in the library's listing or its folder facets.
func TestIssue2836_DeletingTheSourceLeavesNoFileOrFolder(t *testing.T) {
	c := connect(t)
	name, _, _ := issue2836Setup(t, c)
	atts := issue2836Attachments(t, c, name)
	scope, scopeID := issue2836Library(t, atts[0].URI)
	if st, out := c.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+name, http.NoBody); st != http.StatusNoContent {
		t.Fatalf("deleting the source: %d %v", st, out)
	}
	if files := issue2836Files(t, c, scope, scopeID, "webhooks/"+name); len(files) != 0 {
		t.Fatalf("the deleted source left %d files: %v", len(files), files)
	}
	q := url.Values{"scope": {scope}, "scope_id": {scopeID}}
	status, out := c.rest(http.MethodGet, "/api/v1/resources/facets?"+q.Encode(), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the facets: %d %v", status, out)
	}
	folders, _ := out["folders"].([]any)
	for _, f := range folders {
		if p := fmt.Sprint(f.(map[string]any)["path"]); strings.HasPrefix(p, "webhooks/"+name) {
			t.Fatalf("the deleted source left the folder %s", p)
		}
	}
	q.Set("parent", "webhooks")
	_, level := c.rest(http.MethodGet, "/api/v1/resources/facets?"+q.Encode(), http.NoBody)
	if text, _ := json.Marshal(level); strings.Contains(string(text), "webhooks/"+name) {
		t.Fatalf("the level of webhooks still lists the deleted source: %s", text)
	}
}

// issue2836Upload files content as name in folder of a library.
func issue2836Upload(t *testing.T, c *client, scope, scopeID, folder, name string, content []byte) string {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	for k, v := range map[string]string{
		"scope": scope, "scope_id": scopeID, "path": folder, "display_name": name,
		"description": "A file an acceptance run filed.",
	} {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, baseURL()+"/api/v1/resources", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		t.Fatalf("uploading %s/%s: %d %s", folder, name, res.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(out["id"])
}

// Criterion 4: the repair, run against a library holding two generations of a
// message's attachments, keeps the one the source's event names, at the
// message's address, deletes the other, and lists what it deleted.
func TestIssue2836_TheRepairKeepsOneGenerationAndListsWhatItRemoved(t *testing.T) {
	c := connect(t)
	name, folder, _ := issue2836Setup(t, c)
	atts := issue2836Attachments(t, c, name)
	scope, scopeID := issue2836Library(t, atts[0].URI)
	messageFolder := path.Dir(strings.SplitN(strings.TrimPrefix(atts[0].URI, "mcp://"), "/", 3)[2])
	day := path.Dir(messageFolder)

	// The library as a release before #2836 left it: the event's files in a
	// folder named by the server's numbering, and the copy a renumbering filed
	// beside it with the same names and sizes.
	old, copyFolder := day+"/"+strings.ToLower(folder)+"-7-1", day+"/"+strings.ToLower(folder)+"-9-1"
	if st, out := c.rest(http.MethodPost, "/api/v1/resources/folders/move", jsonBody(t, map[string]any{
		"scope": scope, "scope_id": scopeID, "from": messageFolder, "to": old,
	})); st != http.StatusOK {
		t.Fatalf("moving %s to %s: %d %v", messageFolder, old, st, out)
	}
	var copies []string
	for _, f := range issue2836Files(t, c, scope, scopeID, old) {
		status, content := c.restText("/api/v1/resources/" + fmt.Sprint(f["id"]) + "/content")
		if status != http.StatusOK {
			t.Fatalf("reading %v: %d", f["id"], status)
		}
		copies = append(copies, issue2836Upload(t, c, scope, scopeID, copyFolder, fmt.Sprint(f["filename"]), []byte(content)))
	}
	db := platformDB2539(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `DELETE FROM webhook_imap_repairs WHERE source = $1`, name); err != nil {
		t.Fatalf("making the repair owed: %v", err)
	}

	var repair map[string]any
	waitFor(t, "the source's repair", func() (bool, string) {
		_, out := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
		repair, _ = out["attachment_repair"].(map[string]any)
		return repair != nil && repair["removed"] == float64(1), fmt.Sprintf("%v", repair)
	})
	if repair["moved"] != float64(1) || repair["removed_files"] != float64(len(copies)) || repair["kept"] != float64(0) {
		t.Fatalf("the repair: %v", repair)
	}
	report, _ := repair["report"].(map[string]any)
	if text, _ := json.Marshal(report["removed"]); !strings.Contains(string(text), copyFolder) {
		t.Fatalf("the repair does not list %s as removed: %s", copyFolder, text)
	}
	for _, a := range atts {
		status, out := c.rest(http.MethodGet, "/api/v1/resources/"+a.ID, http.NoBody)
		if status != http.StatusOK || out["uri"] != a.URI {
			t.Fatalf("attachment %s answered %d at %v, want it back at %s", a.ID, status, out["uri"], a.URI)
		}
	}
	for _, id := range copies {
		if status, _ := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody); status != http.StatusNotFound {
			t.Fatalf("the later copy %s answered %d, want it deleted", id, status)
		}
	}
	q := url.Values{"scope": {scope}, "scope_id": {scopeID}, "parent": {day}}
	_, level := c.rest(http.MethodGet, "/api/v1/resources/facets?"+q.Encode(), http.NoBody)
	text, _ := json.Marshal(level)
	if strings.Contains(string(text), old) || strings.Contains(string(text), copyFolder) {
		t.Fatalf("a folder of the old form is still listed: %s", text)
	}
}
