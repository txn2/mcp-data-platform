//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1584: an administrator could read a managed resource in a persona
// library they are not a member of, replace its contents, register it as a
// table and move it between libraries, and was refused when they named the same
// file as an asset reference. The reference surface asked "may this caller
// reach this file?" through resource.CanReadResource, which is library
// membership alone; every other question about the same file is asked through
// resource.CanAccessResource, which admits write authority and the uploader.
//
// What these hold: an administrator declares a reference to a persona-library
// file through save_asset and through the portal's own add-a-reference route;
// a caller with no way to the file is still refused and the refusal still names
// only the URI they wrote; the reference serves through a public share to a
// reader carrying no identity at all; and moving the file into another library
// breaks neither the existing reference nor a later re-save of the asset.
//
// Wire forms: `references` is generated from a []string field, so the schema
// admits exactly one form -- a JSON array of strings -- and every call below
// sends it that way, including the one-element and empty-array cases. The
// portal route's body takes `target_kind` and `target_id` as typed strings, and
// the move route takes `scope` and `scope_id` the same way; each is sent in its
// one admitted form. The reference URI itself is a string in both doors and is
// sent identically to each, which is the point of the test: the two doors must
// answer the same question the same way.

// personaLibrary1584 is a persona the administrator does not belong to. The dev
// stack's admin key resolves to the `admin` persona, so a resource filed here
// is outside their VisibleScopes and inside their write authority, which is the
// exact shape the defect turned on.
const personaLibrary1584 = "inventory-analyst"

// refFile1584 is the referenced file's contents, distinctive enough that the
// anonymous read at the end can only be serving this object.
const refFile1584 = "region,units\nnorthwest,412\nsouthwest,318\n"

// created1584 is one managed resource the test made, with the two names the
// rest of the test needs it by.
type created1584 struct {
	id  string
	uri string
}

// createResource1584 files a resource into the named library and removes it
// when the test ends.
func createResource1584(t *testing.T, c *client, scope, scopeID, filename string) created1584 {
	t.Helper()
	args := map[string]any{
		"action": "create", "filename": filename, "scope": scope,
		"path": "acceptance/issue-1584", "content": refFile1584, "content_type": "text/csv",
		"display_name": "Acceptance #1584 " + filename,
		"description":  "Reference material for the #1584 acceptance criteria.",
	}
	if scopeID != "" {
		args["scope_id"] = scopeID
	}
	out := c.call("manage_resource", args)
	id, _ := out["resource_id"].(string)
	uri, _ := out["uri"].(string)
	if id == "" || uri == "" {
		t.Fatalf("manage_resource create returned no resource: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, nil) })
	return created1584{id: id, uri: uri}
}

// saveAsset1584 saves an asset naming the given URIs as references and removes
// it when the test ends. references is always sent as a JSON array of strings,
// the one form the generated schema admits.
func saveAsset1584(t *testing.T, c *client, name string, references []string) string {
	t.Helper()
	out := c.call("save_asset", map[string]any{
		"name": name, "content_type": "text/markdown",
		"content":    "# " + name + "\n\nSee the referenced file.\n",
		"references": references,
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset: %v", out)
	}
	// references_declared is omitempty, so a declaration of none omits it
	// rather than reporting zero. Absent and zero are the same statement here.
	declared, _ := out["references_declared"].(float64)
	if int(declared) != len(references) {
		t.Fatalf("save_asset declared %v of %d references: %v",
			out["references_declared"], len(references), out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
	})
	return id
}

// anonymousGet reads a URL with no credentials of any kind: no bearer token, no
// cookie, no session. It is how a public share reader arrives.
func anonymousGet(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("GET %s: reading the body: %v", url, err)
	}
	return res.StatusCode, string(body)
}

// TestIssue1584_AnAdministratorDeclaresAPersonaLibraryFile is criterion 1: the
// administrator who can read the file through fetch declares it as a reference,
// whatever library it sits in.
//
// Both doors are exercised on the same file in the same session, because the
// defect was one predicate reached through two of them: the agent's save
// (save_asset) and the person's add in the portal panel (the references route).
func TestIssue1584_AnAdministratorDeclaresAPersonaLibraryFile(t *testing.T) {
	admin := connect(t)
	file := createResource1584(t, admin, "persona", personaLibrary1584,
		fmt.Sprintf("declare-%d.csv", time.Now().UnixNano()))

	// The file is outside the administrator's libraries and inside their reach:
	// fetch answers with it, which is the read the refusal contradicted.
	fetched := admin.call("fetch", map[string]any{
		"reference": "mcp:resource:" + file.id,
		"purpose":   "Confirming the administrator can read the file they are about to reference.",
	})
	if !strings.Contains(fmt.Sprint(fetched), personaLibrary1584) {
		t.Fatalf("fetch did not return the persona-library file: %v", fetched)
	}

	// Door one: the agent's save.
	assetID := saveAsset1584(t, admin, "Acceptance 1584 declared", []string{file.uri})

	// Door two: the portal's add-a-reference route, over a second file, on an
	// asset that declared nothing.
	second := createResource1584(t, admin, "persona", personaLibrary1584,
		fmt.Sprintf("panel-%d.csv", time.Now().UnixNano()))
	bare := saveAsset1584(t, admin, "Acceptance 1584 panel", []string{})

	body, err := json.Marshal(map[string]any{"target_kind": "resource", "target_id": second.id})
	if err != nil {
		t.Fatalf("encoding the add-reference body: %v", err)
	}
	status, out := admin.rest(http.MethodPost,
		"/api/v1/portal/assets/"+bare+"/references", bytes.NewReader(body))
	if status != http.StatusOK {
		t.Fatalf("the portal refused an administrator the same declaration the save allowed: %d %v", status, out)
	}
	// A 200 that recorded nothing would read the same at the status line, so
	// the reference is read back through the route the panel reads.
	panelRefs := listRefs1584(t, admin, bare)
	if len(panelRefs) != 1 || panelRefs[0]["target_id"] != second.id {
		t.Fatalf("the panel reported success and recorded no reference: %v", panelRefs)
	}
	if uri, _ := panelRefs[0]["uri"].(string); uri != second.uri {
		t.Fatalf("the recorded reference names %q, want %q", uri, second.uri)
	}

	// And the panel reports the row as one this reader can open, which is what
	// makes it a link to the file's own page -- a page whose routes admit them.
	listed := listRefs1584(t, admin, assetID)
	if len(listed) != 1 {
		t.Fatalf("the asset lists %d references, want 1: %v", len(listed), listed)
	}
	if readable, _ := listed[0]["readable"].(bool); !readable {
		t.Fatalf("the panel reports a file the administrator can open as unreadable: %v", listed[0])
	}
}

// listRefs1584 reads one asset's declared references through the route the
// panel reads.
func listRefs1584(t *testing.T, c *client, assetID string) []map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID+"/references", nil)
	if status != http.StatusOK {
		t.Fatalf("listing references: %d %v", status, out)
	}
	rows, _ := out["data"].([]any)
	listed := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if m, ok := row.(map[string]any); ok {
			listed = append(listed, m)
		}
	}
	return listed
}

// TestIssue1584_ACallerWithNoWayToTheFileIsStillRefused is criterion 2. The
// widening is CanAccessResource and not an unconditional yes: a caller with no
// membership of the library, no write authority over it and no uploader claim
// on the row is refused exactly as before, and the refusal still names only the
// URI they wrote.
func TestIssue1584_ACallerWithNoWayToTheFileIsStillRefused(t *testing.T) {
	admin := connect(t)
	file := createResource1584(t, admin, "persona", personaLibrary1584,
		fmt.Sprintf("refused-%d.csv", time.Now().UnixNano()))

	peer := connectAs(t, devPeerAPIKey)
	res, text, err := peer.callRaw("save_asset", map[string]any{
		"name": "Acceptance 1584 refused", "content_type": "text/markdown",
		"content": "# refused\n", "references": []string{file.uri},
	})
	if err != nil {
		t.Fatalf("save_asset: transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("a caller with no way to the file was allowed to reference it: %s", text)
	}
	if !strings.Contains(text, file.uri) {
		t.Fatalf("the refusal does not name the URI the author wrote: %s", text)
	}
	if strings.Contains(text, file.id) {
		t.Fatalf("the refusal names the resource's id, which the author never wrote: %s", text)
	}
	if strings.Contains(text, "Acceptance #1584") {
		t.Fatalf("the refusal names the file's display name: %s", text)
	}
}

// TestIssue1584_APersonaLibraryReferenceServesThroughAPublicShare is criterion
// 3: the declaration grants the asset's audience a way to load the file, and a
// public link is the widest that audience gets. A reader carrying no identity
// receives the file, exactly as they would for a file in the author's own
// library.
func TestIssue1584_APersonaLibraryReferenceServesThroughAPublicShare(t *testing.T) {
	admin := connect(t)
	file := createResource1584(t, admin, "persona", personaLibrary1584,
		fmt.Sprintf("shared-%d.csv", time.Now().UnixNano()))
	assetID := saveAsset1584(t, admin, "Acceptance 1584 shared", []string{file.uri})

	shared := admin.call("manage_asset", map[string]any{
		"action": "share", "asset_id": assetID, "access_mode": "public",
		"permission": "viewer", "expires_in": "1h",
	})
	shareURL, _ := shared["share_url"].(string)
	if shareURL == "" {
		t.Fatalf("the share carries no link: %v", shared)
	}

	// The public content route rewrites every declared URI into the reference's
	// own serving URL, which is the only address an anonymous reader has.
	status, content := anonymousGet(t, shareURL+"/content")
	if status != http.StatusOK {
		t.Fatalf("the public share did not serve its content: %d %s", status, content)
	}

	refURL := refURLIn1584(t, admin, assetID)
	status, served := anonymousGet(t, refURL)
	if status != http.StatusOK {
		t.Fatalf("the reference did not serve to an anonymous reader: %d %s", status, served)
	}
	if served != refFile1584 {
		t.Fatalf("the reference served something other than the persona-library file: %q", served)
	}
}

// refURLIn1584 is the reference's own serving URL, taken from the route the
// panel reads so the test follows the address the platform published rather
// than one it assembled itself.
func refURLIn1584(t *testing.T, c *client, assetID string) string {
	t.Helper()
	listed := listRefs1584(t, c, assetID)
	if len(listed) != 1 {
		t.Fatalf("the asset lists %d references, want 1: %v", len(listed), listed)
	}
	url, _ := listed[0]["content_url"].(string)
	if url == "" {
		t.Fatalf("the reference carries no serving URL: %v", listed[0])
	}
	if strings.HasPrefix(url, "/") {
		url = baseURL() + url
	}
	return url
}

// TestIssue1584_MovingTheFileBreaksNeitherTheReferenceNorAReSave is criterion
// 4. A reference records the target's id; a move rewrites the row's library and
// its URI. The already-declared reference keeps serving, and a re-save of the
// same asset naming the file at its new address is accepted from a caller who
// can still reach it.
func TestIssue1584_MovingTheFileBreaksNeitherTheReferenceNorAReSave(t *testing.T) {
	admin := connect(t)
	file := createResource1584(t, admin, "user", "",
		fmt.Sprintf("moved-%d.csv", time.Now().UnixNano()))
	assetID := saveAsset1584(t, admin, "Acceptance 1584 moved", []string{file.uri})
	refURL := refURLIn1584(t, admin, assetID)

	move, err := json.Marshal(map[string]any{"scope": "persona", "scope_id": personaLibrary1584})
	if err != nil {
		t.Fatalf("encoding the move body: %v", err)
	}
	status, moved := admin.rest(http.MethodPatch, "/api/v1/resources/"+file.id, bytes.NewReader(move))
	if status != http.StatusOK {
		t.Fatalf("the move was refused: %d %v", status, moved)
	}
	newURI, _ := moved["uri"].(string)
	if newURI == "" || newURI == file.uri {
		t.Fatalf("the move did not rewrite the URI: %v", moved)
	}

	// The reference names the row, and the row still holds the same object.
	status, served := anonymousGet(t, refURL)
	if status != http.StatusOK || served != refFile1584 {
		t.Fatalf("the move broke an existing reference: %d %q", status, served)
	}

	// And the asset is re-saved naming the file at its new address, which is
	// the declaration this ticket is about: the library is now one the
	// administrator has authority over and no membership of.
	updated := admin.call("manage_asset", map[string]any{
		"action": "update", "asset_id": assetID,
		"content": "# moved\n\nStill referenced.\n", "content_type": "text/markdown",
		"references": []string{newURI},
	})
	if declared := updated["references_declared"]; declared != float64(1) {
		t.Fatalf("the re-save declared %v references, want 1: %v", declared, updated)
	}
}
