//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"
)

// Issue #1569: an asset or a resource records what produced it, read from both
// ends.
//
// Before this, nothing on the platform could answer "what wrote this file" or
// "what has this script written". An asset's provenance records the data calls
// its CONTENT was built from and cannot be read backwards; a script's link to
// its declared outputs was one idempotency-key string nothing joins on, so a
// file a run modified without declaring it left no trace; and a resource
// recorded an uploader that for a run was the script's NAME, by string.
//
// Every criterion below is executed through the surface a person or an agent
// actually uses: managed-script runs, save_asset, manage_resource, a multipart
// upload through the resources API, and the three portal routes the Written by
// panels and the Produced section read.
//
// Two criteria are deliberately not executed here and are proved where they can
// be. Criterion 9 -- that a failed producer note does not fail the write -- has
// no external lever: the note is best effort inside the write funnel, and there
// is no way from a client to make the record fail while the write succeeds. It
// is proved by TestNoteSwallowsStoreFailure in internal/producedby. Criterion
// 10 -- what the migration derives from history -- cannot be re-run against an
// already-migrated server; it is proved against a real PostgreSQL by
// TestBackfillDerivesWhatItCan_RealDB in the same package.
//
// Criterion 2's second clause names renaming a script. No surface on this
// platform renames one: manage_script addresses a script BY name and its update
// command does not touch the field, and neither does the portal's metadata
// route. What the criterion is about -- that the record is keyed on an identity
// a name cannot impersonate -- is executed in the form the platform does admit:
// a producer row names the script's id, and a NEW script created under a
// deleted one's name does not inherit what the old one wrote.
//
// Wire forms: every parameter this exercises is typed in its tool schema, so
// each admits exactly one JSON form -- manage_script's command, name,
// description and source are strings and params an array of objects;
// run_script's name is a string, wait_seconds a number, args an object;
// save_asset's name, content, content_type and description are strings;
// manage_resource's action, filename, display_name, path, description, content,
// content_type and reference are strings. Each is sent below as a literal
// tools/call parameter of that form. The one parameter whose schema admits more
// than one form is run_script's args, an object of free-form values: every run
// below passes it as an object of string-valued members, which is the one form
// this script's declared parameters admit. The multipart upload sends its
// fields as form values, the one form that surface takes, and the three portal
// reads take no parameters at all beyond the path.

// scriptSource1569 branches on the mode a run is given, so one script proves
// three different writes: a declared portal output, a managed resource it
// creates, and a managed resource it only replaces the content of.
const scriptSource1569 = `
mode = run.params["mode"]
if mode == "export":
    platform.export(
        name=run.params["target"],
        rows=[{"region": "north", "units": 41}],
        format="csv",
    )
elif mode == "create_resource":
    platform.call("manage_resource", {
        "action": "create",
        "filename": run.params["target"] + ".txt",
        "display_name": run.params["target"],
        "path": "acceptance-1569",
        "description": "Acceptance #1569: a managed resource a run created.",
        "content": "created by a run\n",
        "content_type": "text/plain",
    })
else:
    platform.call("manage_resource", {
        "action": "replace_content",
        "reference": run.params["target"],
        "content": "replaced by a run\n",
        "content_type": "text/plain",
    })
`

func unique1569() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// createScript1569 saves a script owned by the calling person, and removes it
// when the test ends unless the test removed it itself.
func createScript1569(t *testing.T, c *client, name string) {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1569: a script whose writes are recorded against it.",
		"source":      scriptSource1569,
		"params": []any{
			map[string]any{
				"name": "mode", "type": "string", "required": true,
				"description": "Which write this run makes.",
			},
			map[string]any{
				"name": "target", "type": "string", "required": true,
				"description": "The output name, resource name, or resource reference.",
			},
		},
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
}

// runScript1569 runs the script and fails on a run that did not succeed.
func runScript1569(t *testing.T, c *client, name string, args map[string]any) map[string]any {
	t.Helper()
	out := c.call("run_script", map[string]any{
		"name": name, "args": args, "wait_seconds": 120,
	})
	if status, _ := out["status"].(string); status != "succeeded" {
		t.Fatalf("run of %s did not succeed: %v", name, out)
	}
	return out
}

// scriptID1569 is the id the platform holds this script under, which is what a
// producer row names and what the produced listing is addressed by.
func scriptID1569(t *testing.T, c *client, name string) string {
	t.Helper()
	out := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ := out["id"].(string)
	if id == "" {
		if sc, ok := out["script"].(map[string]any); ok {
			id, _ = sc["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("manage_script get returned no id for %q: %v", name, out)
	}
	return id
}

// producer1569 is one row of a Written by panel.
type producer1569 struct {
	kind    string
	id      string
	label   string
	exists  bool
	created bool
	writes  int
}

// producers1569 reads the route the Written by panel reads, for either kind.
func producers1569(t *testing.T, c *client, kind, id string) []producer1569 {
	t.Helper()
	section := "assets"
	if kind == "resource" {
		section = "resources"
	}
	status, body := c.rest(http.MethodGet, "/api/v1/portal/"+section+"/"+id+"/producers", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET %s %s producers: status %d: %v", kind, id, status, body)
	}
	list, _ := body["data"].([]any)
	out := make([]producer1569, 0, len(list))
	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		writes, _ := row["write_count"].(float64)
		exists, _ := row["exists"].(bool)
		created, _ := row["created"].(bool)
		kind, _ := row["kind"].(string)
		pid, _ := row["id"].(string)
		label, _ := row["label"].(string)
		out = append(out, producer1569{
			kind: kind, id: pid, label: label,
			exists: exists, created: created, writes: int(writes),
		})
	}
	return out
}

// findProducer1569 returns the row naming one producer, or fails saying what
// the file did list.
func findProducer1569(t *testing.T, rows []producer1569, kind, id string) producer1569 {
	t.Helper()
	for _, row := range rows {
		if row.kind == kind && row.id == id {
			return row
		}
	}
	t.Fatalf("no %s producer %q among %v", kind, id, rows)
	return producer1569{}
}

// produced1569 reads the route the script page's Produced section reads, keyed
// by "<kind>:<id>".
func produced1569(t *testing.T, c *client, scriptID string) map[string]map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/produced", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET script %s produced: status %d: %v", scriptID, status, body)
	}
	list, _ := body["data"].([]any)
	out := map[string]map[string]any{}
	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := row["target_kind"].(string)
		id, _ := row["target_id"].(string)
		out[kind+":"+id] = row
	}
	return out
}

// ownedAssetID1569 finds an asset this person owns by name.
func ownedAssetID1569(t *testing.T, c *client, name string) string {
	t.Helper()
	out := c.call("manage_asset", map[string]any{"action": "list", "limit": 200})
	list, _ := out["assets"].([]any)
	for _, item := range list {
		a, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if got, _ := a["name"].(string); got == name {
			id, _ := a["id"].(string)
			return id
		}
	}
	t.Fatalf("no asset named %q among this person's assets", name)
	return ""
}

// TestIssue1569_AScriptsOutputAssetNamesTheScript is criterion 1: the asset a
// declared output writes carries a producer row naming the script by id and
// marked as having created it, and a second run folds into that same row rather
// than adding another.
func TestIssue1569_AScriptsOutputAssetNamesTheScript(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1569()
	scriptName := "acceptance-1569-export-" + id
	outputName := "acceptance-1569-output-" + id

	createScript1569(t, owner, scriptName)
	scriptID := scriptID1569(t, owner, scriptName)

	runScript1569(t, owner, scriptName, map[string]any{
		"mode": "export", "target": outputName,
	})
	assetID := ownedAssetID1569(t, owner, outputName)

	first := findProducer1569(t, producers1569(t, owner, "asset", assetID), "script", scriptID)
	if !first.created {
		t.Fatalf("the script that wrote the asset into existence is not marked as having created it: %+v", first)
	}
	if first.label != scriptName {
		t.Fatalf("the producer is labelled %q, not the script's name %q", first.label, scriptName)
	}
	if !first.exists {
		t.Fatalf("a script that is still there reads as gone: %+v", first)
	}

	runScript1569(t, owner, scriptName, map[string]any{
		"mode": "export", "target": outputName,
	})

	rows := producers1569(t, owner, "asset", assetID)
	scripts := 0
	for _, row := range rows {
		if row.kind == "script" {
			scripts++
		}
	}
	if scripts != 1 {
		t.Fatalf("a second run of one script added a second row: %v", rows)
	}
	second := findProducer1569(t, rows, "script", scriptID)
	if second.writes <= first.writes {
		t.Fatalf("a second run did not advance the write count (%d then %d)", first.writes, second.writes)
	}
	if !second.created {
		t.Fatalf("a modification demoted the creator: %+v", second)
	}
}

// TestIssue1569_AScriptWritingAResourceNamesTheScriptByID is criterion 2: a
// resource a run creates through manage_resource records the script by id, and
// a later script bearing a deleted one's name does not inherit what it wrote.
func TestIssue1569_AScriptWritingAResourceNamesTheScriptByID(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1569()
	scriptName := "acceptance-1569-res-" + id
	resourceName := "acceptance-1569-file-" + id

	createScript1569(t, owner, scriptName)
	firstID := scriptID1569(t, owner, scriptName)

	runScript1569(t, owner, scriptName, map[string]any{
		"mode": "create_resource", "target": resourceName,
	})
	resourceID := resourceIDByName1569(t, owner, resourceName)
	t.Cleanup(func() {
		_, _ = owner.rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody)
	})

	row := findProducer1569(t, producers1569(t, owner, "resource", resourceID), "script", firstID)
	if !row.created {
		t.Fatalf("the run that created the resource is not marked as having created it: %+v", row)
	}
	if row.id == scriptName {
		t.Fatalf("the producer is recorded by name, which a second script can impersonate: %+v", row)
	}

	// The identity a name cannot impersonate: delete the script and create a
	// new one under exactly the same name.
	owner.call("manage_script", map[string]any{"command": "delete", "name": scriptName})
	createScript1569(t, owner, scriptName)
	secondID := scriptID1569(t, owner, scriptName)
	if secondID == firstID {
		t.Fatalf("the replacement script reused the deleted one's id (%s), so this proves nothing", firstID)
	}

	rows := producers1569(t, owner, "resource", resourceID)
	kept := findProducer1569(t, rows, "script", firstID)
	if kept.exists {
		t.Fatalf("a deleted script reads as still existing: %+v", kept)
	}
	if kept.label != scriptName {
		t.Fatalf("a deleted script lost the name it wrote under: %+v", kept)
	}
	for _, r := range rows {
		if r.id == secondID {
			t.Fatalf("a new script of the same name inherited what the deleted one wrote: %v", rows)
		}
	}

	// Criterion 8, at the surface: the file still lists its producers.
	if len(produced1569(t, owner, secondID)) != 0 {
		t.Fatalf("a brand-new script is credited with somebody else's writes")
	}
}

// TestIssue1569_AFileListsEveryProducer is criteria 3 and 4: a script that
// replaces the content of a resource it did not create is recorded as having
// modified it, and the file then lists both writers -- the person who uploaded
// it and the script that rewrites it.
func TestIssue1569_AFileListsEveryProducer(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1569()
	scriptName := "acceptance-1569-revise-" + id
	resourceName := "acceptance-1569-owned-" + id

	// The person's own upload, through the tool a person's agent calls.
	created := owner.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     resourceName + ".txt",
		"display_name": resourceName,
		"path":         "acceptance-1569",
		"description":  "Acceptance #1569: a file its owner uploaded and a script rewrites.",
		"content":      "uploaded by a person\n",
		"content_type": "text/plain",
	})
	resourceID, _ := created["resource_id"].(string)
	reference, _ := created["reference"].(string)
	if resourceID == "" || reference == "" {
		t.Fatalf("manage_resource create returned no resource_id or reference: %v", created)
	}
	t.Cleanup(func() {
		_, _ = owner.rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody)
	})

	createScript1569(t, owner, scriptName)
	scriptID := scriptID1569(t, owner, scriptName)
	runScript1569(t, owner, scriptName, map[string]any{
		"mode": "revise", "target": reference,
	})

	rows := producers1569(t, owner, "resource", resourceID)
	if len(rows) < 2 {
		t.Fatalf("a file written by a person and by a script lists %d producer(s): %v", len(rows), rows)
	}
	script := findProducer1569(t, rows, "script", scriptID)
	if script.created {
		t.Fatalf("a script that only replaced the content is recorded as having created the file: %+v", script)
	}
	if !hasCreator1569(rows) {
		t.Fatalf("the file lists no creator at all: %v", rows)
	}
}

// TestIssue1569_AnAgentsSaveIsRecordedAgainstItsSession is criterion 5: an
// ordinary agent session that saves an asset is filed under the session, which
// is the unit a reader can open and follow.
func TestIssue1569_AnAgentsSaveIsRecordedAgainstItsSession(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := "acceptance-1569-saved-" + unique1569()

	saved := owner.call("save_asset", map[string]any{
		"name":         name,
		"content":      "# Saved by an agent in an ordinary session\n",
		"content_type": "text/markdown",
		"description":  "Acceptance #1569: a save made in an ordinary session.",
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		assetID = ownedAssetID1569(t, owner, name)
	}

	rows := producers1569(t, owner, "asset", assetID)
	session := findProducer1569(t, rows, "session", owner.sessionID)
	if !session.created {
		t.Fatalf("the session that saved the asset is not marked as having created it: %+v", session)
	}
	for _, row := range rows {
		if row.kind == "person" {
			t.Fatalf("an MCP save was filed under the person as well as the session: %v", rows)
		}
	}

	// The session producer names a session this person can open, which is what
	// makes the row a link rather than an opaque string.
	status, body := owner.rest(http.MethodGet, "/api/v1/portal/sessions/"+session.id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the session a producer row links to does not open: status %d: %v", status, body)
	}
}

// TestIssue1569_AScriptListsEverythingItHasProduced is criterion 7: one list,
// across runs, holding both kinds, drawn from the producer relation rather than
// walked out of the run history.
func TestIssue1569_AScriptListsEverythingItHasProduced(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1569()
	scriptName := "acceptance-1569-both-" + id
	outputName := "acceptance-1569-both-output-" + id
	resourceName := "acceptance-1569-both-file-" + id

	createScript1569(t, owner, scriptName)
	scriptID := scriptID1569(t, owner, scriptName)

	runScript1569(t, owner, scriptName, map[string]any{
		"mode": "export", "target": outputName,
	})
	runScript1569(t, owner, scriptName, map[string]any{
		"mode": "create_resource", "target": resourceName,
	})

	assetID := ownedAssetID1569(t, owner, outputName)
	resourceID := resourceIDByName1569(t, owner, resourceName)
	t.Cleanup(func() {
		_, _ = owner.rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody)
	})

	produced := produced1569(t, owner, scriptID)
	asset, ok := produced["asset:"+assetID]
	if !ok {
		t.Fatalf("the script's produced list does not hold the asset it wrote: %v", produced)
	}
	if got, _ := asset["name"].(string); got != outputName {
		t.Fatalf("the produced asset is named %q, not %q", got, outputName)
	}
	if created, _ := asset["created"].(bool); !created {
		t.Fatalf("the asset the script brought into existence is not marked created: %v", asset)
	}
	res, ok := produced["resource:"+resourceID]
	if !ok {
		t.Fatalf("the script's produced list does not hold the resource it wrote: %v", produced)
	}
	if got, _ := res["name"].(string); got != resourceName {
		t.Fatalf("the produced resource is named %q, not %q", got, resourceName)
	}

	// Somebody else's script is not this person's to read, and the list is not
	// public knowledge about it.
	peer := connectAs(t, devPeerAPIKey)
	status, _ := peer.rest(http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/produced", http.NoBody)
	if status != http.StatusNotFound {
		t.Fatalf("another person read what this script produced: status %d", status)
	}
}

// TestIssue1569_APortalUploadIsRecordedAgainstThePerson is criterion 4's first
// half through the surface a person actually uses: the resources API's own
// multipart upload, which carries no MCP session at all.
func TestIssue1569_APortalUploadIsRecordedAgainstThePerson(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := "acceptance-1569-upload-" + unique1569()

	resourceID := uploadResource1569(t, owner, name)
	t.Cleanup(func() {
		_, _ = owner.rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody)
	})

	rows := producers1569(t, owner, "resource", resourceID)
	if len(rows) != 1 {
		t.Fatalf("an upload by one person lists %d producer(s): %v", len(rows), rows)
	}
	if rows[0].kind != "person" {
		t.Fatalf("a person's upload through the portal is filed as %q: %v", rows[0].kind, rows[0])
	}
	if !rows[0].created {
		t.Fatalf("the person who uploaded the file is not marked as having created it: %+v", rows[0])
	}
}

// resourceIDByName1569 finds a managed resource by its display name through the
// resources API, which is the listing the portal's own library reads.
func resourceIDByName1569(t *testing.T, c *client, displayName string) string {
	t.Helper()
	status, body := c.rest(http.MethodGet,
		"/api/v1/resources?path=acceptance-1569&limit=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("listing resources: status %d: %v", status, body)
	}
	list, _ := body["resources"].([]any)
	for _, item := range list {
		res, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if got, _ := res["display_name"].(string); got == displayName {
			id, _ := res["id"].(string)
			return id
		}
	}
	t.Fatalf("no resource named %q in the acceptance-1569 folder: %v", displayName, body)
	return ""
}

// uploadResource1569 files a resource the way the portal's upload dialog does:
// a multipart POST carrying no MCP session at all, which is what makes the
// producer a person rather than a session.
func uploadResource1569(t *testing.T, c *client, displayName string) string {
	t.Helper()
	body := new(bytes.Buffer)
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("file", displayName+".txt")
	if err != nil {
		t.Fatalf("building the upload: %v", err)
	}
	if _, err := part.Write([]byte("uploaded through the resources API\n")); err != nil {
		t.Fatalf("writing the upload: %v", err)
	}
	for field, value := range map[string]string{
		// The person's own library: an ordinary person may write there, and
		// what this proves is who the write is recorded against rather than
		// where it was filed.
		"scope":        "user",
		"scope_id":     devOwnerEmail,
		"path":         "acceptance-1569",
		"display_name": displayName,
		"description":  "Acceptance #1569: a file a person uploaded through the portal.",
	} {
		if err := w.WriteField(field, value); err != nil {
			t.Fatalf("writing %s: %v", field, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the upload: %v", err)
	}

	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, baseURL()+"/api/v1/resources", body)
	if err != nil {
		t.Fatalf("building the upload request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("uploading: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the upload response: %v", err)
	}
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		t.Fatalf("uploading: status %d: %s", res.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the upload response is not JSON: %v\n%s", err, raw)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("the upload returned no id: %s", raw)
	}
	return id
}

// hasCreator1569 reports whether any row is marked as having created the file.
func hasCreator1569(rows []producer1569) bool {
	for _, row := range rows {
		if row.created {
			return true
		}
	}
	return false
}
