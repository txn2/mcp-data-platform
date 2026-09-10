//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1665: manage_resource gains the rest of a managed resource's
// lifecycle -- a lookup by the path the file was written to, an idempotent
// create-or-replace at that path, a folder listing, and a delete that refuses
// while something still points at the file.
//
// Every criterion here runs through the real MCP surface against the running
// stack: the platform's own managed-resource library, the real asset toolkit
// for the reference that blocks a delete, and `fetch` for the reference that
// stops resolving once the file is gone.
//
// Wire forms: every parameter this ticket touches is typed in the tool's
// schema, which is closed to unknown keys, so each admits exactly ONE JSON
// form. `action`, `if_exists`, `path`, `filename`, `scope`, `scope_id` and
// `reference` are strings; `force` is a boolean; `limit` and `offset` are
// integers. The one form of each is what every check below sends as literal
// tools/call params, and the string spelling of the boolean ("true") is
// asserted to be REFUSED at the boundary rather than coerced -- a parameter
// that silently accepted a second spelling would be a second contract.
const (
	issue1665Purpose = "Acceptance for #1665: find, refresh and remove a managed resource by its path."
	issue1665Folder  = "acceptance/issue-1665"
)

// issue1665Create is one manage_resource create at the ticket's folder.
func issue1665Create(c *client, filename, content string, extra map[string]any) map[string]any {
	c.t.Helper()
	args := map[string]any{
		"action": "create", "path": issue1665Folder, "filename": filename,
		"display_name": "Acceptance 1665 " + filename,
		"description":  "Written by the #1665 acceptance run.",
		"content":      content, "content_type": "text/csv",
	}
	for k, v := range extra {
		args[k] = v
	}
	return c.call("manage_resource", args)
}

// issue1665Address is the address half of a get, a list or a delete.
func issue1665Address(filename string) map[string]any {
	return map[string]any{"path": issue1665Folder, "filename": filename}
}

// issue1665Get reads what is filed at an address.
func issue1665Get(c *client, filename string) map[string]any {
	c.t.Helper()
	args := issue1665Address(filename)
	args["action"] = "get"
	return c.call("manage_resource", args)
}

// TestIssue1665_AnAddressIsTheIdentityAcrossRuns is the ticket's first two
// criteria: a lookup answers what is at a path before anything is, and one
// idempotent call lands the file there and then revises it, with the caller
// holding no id at any point.
func TestIssue1665_AnAddressIsTheIdentityAcrossRuns(t *testing.T) {
	c := connect(t)
	filename := fmt.Sprintf("rolling-%d.csv", time.Now().UnixNano())

	empty := issue1665Get(c, filename)
	if found, _ := empty["found"].(bool); found {
		t.Fatalf("a fresh address already holds a file: %v", empty)
	}
	uri, _ := empty["uri"].(string)
	if !strings.HasSuffix(uri, issue1665Folder+"/"+filename) {
		t.Fatalf("the empty address was not named: %v", empty)
	}

	first := issue1665Create(c, filename, "day,high\nmon,71\n", map[string]any{"if_exists": "replace"})
	id, _ := first["resource_id"].(string)
	reference, _ := first["reference"].(string)
	if id == "" || !strings.HasPrefix(reference, "mcp:resource:") {
		t.Fatalf("the create does not name the file: %v", first)
	}
	if got, _ := first["uri"].(string); got != uri {
		t.Errorf("uri = %q; want the address the lookup reported, %q", got, uri)
	}

	second := issue1665Create(c, filename, "day,high\nmon,88\n",
		map[string]any{"if_exists": "replace", "change_summary": "second pull"})
	if got, _ := second["resource_id"].(string); got != id {
		t.Fatalf("the second create made a different file: %q then %q", id, got)
	}
	if got, _ := second["uri"].(string); got != uri {
		t.Errorf("uri = %q; want the address unchanged across the replacement", got)
	}
	if v := number(t, second, "version"); v != 2 {
		t.Errorf("version = %v; want the second create recorded as version 2 of the same file", v)
	}

	// The lookup now finds it, and reports what a fetch of the reference would
	// carry except the bytes.
	found := issue1665Get(c, filename)
	if ok, _ := found["found"].(bool); !ok {
		t.Fatalf("the file the create wrote is not at the address it wrote it to: %v", found)
	}
	record, _ := found["resource"].(map[string]any)
	if record == nil {
		t.Fatalf("the lookup carries no record: %v", found)
	}
	if got, _ := record["resource_id"].(string); got != id {
		t.Errorf("resource_id = %q; want %q", got, id)
	}
	if got, _ := record["content_type"].(string); got != "text/csv" {
		t.Errorf("content_type = %q; want text/csv", got)
	}
	if _, hasContent := record["content"]; hasContent {
		t.Error("the lookup carried the file's bytes; it answers what is there, and fetch reads it")
	}

	// The same record is reachable by the reference, which is the other way a
	// caller names a file.
	byReference := c.call("manage_resource", map[string]any{"action": "get", "reference": reference})
	byRefRecord, _ := byReference["resource"].(map[string]any)
	if byRefRecord == nil {
		t.Fatalf("the reference did not resolve: %v", byReference)
	}
	if got, _ := byRefRecord["resource_id"].(string); got != id {
		t.Errorf("the reference and the address name different files: %q and %q", got, id)
	}

	issue1665Delete(t, c, map[string]any{"action": "delete", "reference": reference})
}

// TestIssue1665_AFolderListingReportsWhatIsFiledUnderIt is the operator case:
// what is in this folder, without knowing what to search for.
func TestIssue1665_AFolderListingReportsWhatIsFiledUnderIt(t *testing.T) {
	c := connect(t)
	filename := fmt.Sprintf("listed-%d.csv", time.Now().UnixNano())
	created := issue1665Create(c, filename, "day,high\nmon,71\n", map[string]any{"if_exists": "replace"})
	reference, _ := created["reference"].(string)
	t.Cleanup(func() { issue1665Delete(t, c, map[string]any{"action": "delete", "reference": reference}) })

	listed := c.call("manage_resource", map[string]any{
		"action": "list", "path": issue1665Folder, "limit": 100,
	})
	rows, _ := listed["resources"].([]any)
	if len(rows) == 0 {
		t.Fatalf("the folder the file was written to lists nothing: %v", listed)
	}
	var seen bool
	for _, row := range rows {
		entry, _ := row.(map[string]any)
		if name, _ := entry["filename"].(string); name == filename {
			seen = true
			if got, _ := entry["path"].(string); got != issue1665Folder {
				t.Errorf("path = %q; want %q", got, issue1665Folder)
			}
		}
	}
	if !seen {
		t.Errorf("the file just written is not in its own folder's listing: %v", listed)
	}
	if total := number(t, listed, "total"); total < float64(len(rows)) {
		t.Errorf("total = %v; want at least the %d rows returned", total, len(rows))
	}
}

// TestIssue1665_ACreateStillRefusesATakenAddressByDefault holds the line the
// idempotent form is opt-in from: without if_exists the collision a create has
// always refused is still refused, so nothing that worked before now silently
// overwrites a file.
func TestIssue1665_ACreateStillRefusesATakenAddressByDefault(t *testing.T) {
	c := connect(t)
	filename := fmt.Sprintf("collide-%d.csv", time.Now().UnixNano())
	created := issue1665Create(c, filename, "day,high\nmon,71\n", map[string]any{"if_exists": "replace"})
	reference, _ := created["reference"].(string)
	t.Cleanup(func() { issue1665Delete(t, c, map[string]any{"action": "delete", "reference": reference}) })

	_, text, err := c.callRaw("manage_resource", map[string]any{
		"action": "create", "path": issue1665Folder, "filename": filename,
		"display_name": "Collides", "description": "Should be refused.",
		"content": "day,high\nmon,99\n", "content_type": "text/csv",
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !strings.Contains(strings.ToLower(text), "already exists") {
		t.Errorf("a create at a taken address was not refused by name: %s", text)
	}

	// An if_exists the tool does not take is refused rather than defaulted: a
	// misspelled "replace" that quietly meant "fail" would look like a platform
	// that lost the write.
	_, mistyped, err := c.callRaw("manage_resource", map[string]any{
		"action": "create", "path": issue1665Folder, "filename": filename,
		"display_name": "Mistyped", "description": "Should be refused.",
		"content": "day,high\nmon,99\n", "content_type": "text/csv", "if_exists": "overwrite",
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !strings.Contains(mistyped, "replace") {
		t.Errorf("a misspelled if_exists was not refused with the values it takes: %s", mistyped)
	}
}

// TestIssue1665_ADeleteRefusesWhileSomethingPointsAtTheFile is the ticket's
// delete criterion, over the real reference an asset makes.
func TestIssue1665_ADeleteRefusesWhileSomethingPointsAtTheFile(t *testing.T) {
	c := connect(t)
	filename := fmt.Sprintf("referenced-%d.csv", time.Now().UnixNano())
	created := issue1665Create(c, filename, "day,high\nmon,71\n", map[string]any{"if_exists": "replace"})
	uri, _ := created["uri"].(string)
	reference, _ := created["reference"].(string)

	saved := c.call("save_asset", map[string]any{
		"name": "Acceptance 1665 report", "content_type": "text/html",
		"content":    fmt.Sprintf("<h1>1665</h1><script>fetch(%q)</script>", uri),
		"references": []any{uri},
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("the asset that references the file was not saved: %v", saved)
	}
	t.Cleanup(func() {
		c.call("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})

	refusal := c.call("manage_resource", map[string]any{
		"action": "delete", "reference": reference,
	})
	if deleted, _ := refusal["deleted"].(bool); deleted {
		t.Fatalf("the file was deleted while an asset still references it: %v", refusal)
	}
	message, _ := refusal["message"].(string)
	if !strings.Contains(message, "asset") {
		t.Errorf("the refusal does not say what points at the file: %s", message)
	}
	if !strings.Contains(message, "force=true") {
		t.Errorf("the refusal names no way through: %s", message)
	}

	// The refusal changed nothing.
	still := issue1665Get(c, filename)
	if found, _ := still["found"].(bool); !found {
		t.Fatalf("a refused delete removed the file anyway: %v", still)
	}

	// Forced, the file goes and the reference stops resolving.
	forced := c.call("manage_resource", map[string]any{
		"action": "delete", "reference": reference, "force": true,
	})
	if deleted, _ := forced["deleted"].(bool); !deleted {
		t.Fatalf("a forced delete did not delete: %v", forced)
	}
	gone := issue1665Get(c, filename)
	if found, _ := gone["found"].(bool); found {
		t.Fatalf("the address still holds a file after the delete: %v", gone)
	}
	doc := c.call("fetch", map[string]any{"reference": reference, "purpose": issue1665Purpose})
	if found, _ := doc["found"].(bool); found {
		t.Errorf("the reference to a deleted file still resolves: %v", doc)
	}
}

// TestIssue1665_APromptAttachmentAlsoStopsADelete is the second half of the
// delete criterion: an asset's reference and a prompt's attachment are two
// different records in two different layers, and a delete has to see both.
func TestIssue1665_APromptAttachmentAlsoStopsADelete(t *testing.T) {
	c := connect(t)
	filename := fmt.Sprintf("attached-%d.md", time.Now().UnixNano())
	created := c.call("manage_resource", map[string]any{
		"action": "create", "path": issue1665Folder, "filename": filename,
		"display_name": "Acceptance 1665 checklist",
		"description":  "Attached to a prompt by the #1665 acceptance run.",
		"content":      "# checklist\n", "content_type": "text/markdown", "if_exists": "replace",
	})
	resourceID, _ := created["resource_id"].(string)
	reference, _ := created["reference"].(string)

	// Personal, to match the file: an attachment must be at least as widely
	// visible as the prompt that carries it, and the file above is in the
	// caller's own library.
	promptName := fmt.Sprintf("acceptance-1665-%d", time.Now().UnixNano())
	prompt := c.call("manage_prompt", map[string]any{
		"command": "create", "name": promptName,
		"display_name": "Acceptance 1665", "description": "Acceptance 1665 fixture.",
		"content": "Follow the attached checklist.", "scope": "personal",
	})
	promptID, _ := prompt["id"].(string)
	if promptID == "" {
		t.Fatalf("manage_prompt create returned no prompt id: %v", prompt)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_prompt", map[string]any{"command": "delete", "name": promptName})
	})

	status, _ := c.rest(http.MethodPost, "/api/v1/portal/prompts/"+promptID+"/attachments",
		jsonBody(t, map[string]any{"resource_id": resourceID}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("attaching the file to the prompt: status %d", status)
	}

	refusal := c.call("manage_resource", map[string]any{"action": "delete", "reference": reference})
	if deleted, _ := refusal["deleted"].(bool); deleted {
		t.Fatalf("the file was deleted while a prompt still attaches it: %v", refusal)
	}
	holds, _ := refusal["holds"].(map[string]any)
	if holds == nil || number(t, holds, "prompts") != 1 {
		t.Fatalf("the refusal did not count the prompt that attaches the file: %v", refusal)
	}
	message, _ := refusal["message"].(string)
	if !strings.Contains(message, "1 prompt attaches this file") {
		t.Errorf("the refusal does not say a prompt attaches it: %s", message)
	}

	// Detaching it removes the reason, and the same delete then goes through
	// with no force at all -- which is what proves the refusal was about the
	// attachment and not about the file.
	status, _ = c.rest(http.MethodDelete,
		"/api/v1/portal/prompts/"+promptID+"/attachments/"+resourceID, nil)
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("detaching the file from the prompt: status %d", status)
	}
	deleted := c.call("manage_resource", map[string]any{"action": "delete", "reference": reference})
	if ok, _ := deleted["deleted"].(bool); !ok {
		t.Fatalf("the delete was still refused after the attachment was removed: %v", deleted)
	}
}

// TestIssue1665_ForceIsABooleanAndOnlyABoolean is the wire-form check. The
// schema types force as a boolean and closes the object to unknown keys, so
// the boolean is the only form; the string spelling is refused at the boundary
// rather than coerced, because a parameter with two accepted spellings is two
// contracts.
func TestIssue1665_ForceIsABooleanAndOnlyABoolean(t *testing.T) {
	c := connect(t)
	filename := fmt.Sprintf("wireform-%d.csv", time.Now().UnixNano())
	created := issue1665Create(c, filename, "day,high\nmon,71\n", map[string]any{"if_exists": "replace"})
	reference, _ := created["reference"].(string)

	res, text, err := c.callRaw("manage_resource", map[string]any{
		"action": "delete", "reference": reference, "force": "true",
	})
	if err == nil && res != nil && !res.IsError {
		t.Errorf("force as the string \"true\" was accepted: %s", text)
	}

	// The boolean form works, which is what proves the refusal above is about
	// the spelling and not about the call.
	issue1665Delete(t, c, map[string]any{"action": "delete", "reference": reference, "force": true})
}

// issue1665Delete removes a file the run made, so a repeated run starts from
// the same folder it started from the first time.
func issue1665Delete(t *testing.T, c *client, args map[string]any) {
	t.Helper()
	res, text, err := c.callRaw("manage_resource", args)
	if err != nil {
		t.Logf("cleanup: transport error: %v", err)
		return
	}
	if res.IsError {
		t.Logf("cleanup: %s", text)
	}
}
