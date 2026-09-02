//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Issue #1588: a transferred script keeps refreshing assets its new owner
// cannot reach, and the transfer says nothing about it. An output records the
// script owner's address when it is first written, and the transfer rewrote
// nothing about it, so after a move the new owner's runs went on writing new
// versions into files the new owner could not open, share or delete, silently.
//
// The transfer now states what happens to the files the script's runs have
// created: `outputs: move` hands them to the new owner in the same transaction
// as the script, `outputs: keep` leaves them behind and the response lists what
// the new owner cannot reach, and a script that has created any is not moved
// until the request says which. Every criterion here is executed through the
// real surfaces: the script is created and run by an ordinary person, the
// move is made by an administrator over the portal route the transfer dialog
// calls, and reach is proved by the tool calls the new owner would make.
//
// Wire forms: the transfer body's owner_email and outputs are strings in the
// route's schema and admit one JSON form each; outputs is sent as "keep" and as
// "MOVE" to exercise the case-insensitive read through the real route. Every
// manage_asset, manage_script and run_script parameter touched here is typed
// (action, asset_id, collection_id, name, command, source, wait_seconds) and
// admits one form; run_script's args, the one free-form object, is sent with
// string-valued members only, which is the one form these scripts read.

// scriptOutputs1588 writes one CSV output and, when told to, creates one
// collection, so a single run produces both kinds of file a transfer is
// about. The collection is created on the first run only: its name is unique
// within an owner, and a second creation would fail the run.
const scriptOutputs1588 = `
rows = [{"region": "north", "units": 41}]
platform.export(name=run.params["output"], rows=rows, format="csv")
if run.params["collection"] != "":
    platform.call("manage_asset", {"action": "create_collection", "name": run.params["collection"]})
`

// params1588 declares the two parameters the script reads.
func params1588() []any {
	return []any{
		map[string]any{
			"name": "output", "type": "string", "required": true,
			"description": "The output name this run writes.",
		},
		map[string]any{
			"name": "collection", "type": "string", "required": false, "default": "",
			"description": "A collection to create, or empty to create none.",
		},
	}
}

// produced1588 is one script with one asset and one collection its first run
// created, owned by the ordinary person who wrote it.
type produced1588 struct {
	owner, peer, admin *client
	scriptName         string
	scriptID           string
	outputName         string
	collectionName     string
	assetID            string
	collectionID       string
}

func setup1588(t *testing.T) produced1588 {
	t.Helper()
	id := unique1579()
	p := produced1588{
		owner:          connectAs(t, devOwnerAPIKey),
		peer:           connectAs(t, devPeerAPIKey),
		admin:          connect(t),
		scriptName:     "acceptance-1588-report-" + id,
		outputName:     "acceptance-1588-output-" + id,
		collectionName: "acceptance-1588-pack-" + id,
	}
	p.owner.call("manage_script", map[string]any{
		"command":     "create",
		"name":        p.scriptName,
		"description": "Acceptance #1588: a script whose outputs a transfer must account for.",
		"source":      scriptOutputs1588,
		"params":      params1588(),
	})
	// Whoever ends up with the script deletes it; the same command from the
	// other person is refused and ignored.
	t.Cleanup(func() {
		_, _, _ = p.owner.callRaw("manage_script", map[string]any{"command": "delete", "name": p.scriptName})
		_, _, _ = p.peer.callRaw("manage_script", map[string]any{"command": "delete", "name": p.scriptName})
	})
	mustSucceed1579(t, p.owner, p.scriptName, map[string]any{
		"output": p.outputName, "collection": p.collectionName,
	})
	p.scriptID = scriptIDOf1579(t, p.owner, p.scriptName)
	p.assetID = assetIDOf1579(t, p.owner, p.outputName)
	p.collectionID = collectionIDOf1588(t, p.owner, p.scriptID, p.collectionName)
	t.Cleanup(func() {
		for _, c := range []*client{p.owner, p.peer} {
			_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": p.assetID})
			_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": p.collectionID})
		}
	})
	return p
}

// collectionIDOf1588 returns the id of the collection the script's run created,
// read from the script's own inventory. A run's collection records the script
// principal as its owner id, and a person's collection listing is scoped on
// the owner id, so the listing the owner's Collections page reads does not
// hold it; the inventory is keyed on the producer and does.
func collectionIDOf1588(t *testing.T, c *client, scriptID, name string) string {
	t.Helper()
	for key, item := range producedOf1588(t, c, scriptID) {
		if got, _ := item["name"].(string); strings.HasPrefix(key, "collection:") && got == name {
			return strings.TrimPrefix(key, "collection:")
		}
	}
	t.Fatalf("the inventory of %s holds no collection named %q", scriptID, name)
	return ""
}

// transfer1588 moves the script to the peer, with the body given verbatim.
func (p produced1588) transfer(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	return p.admin.rest(http.MethodPut, "/api/v1/portal/scripts/"+p.scriptID+"/owner", strings.NewReader(body))
}

// canUpdateAsset reports whether c may write over the asset by hand, which is
// the ownership judgment every owner-only action on it shares.
func canUpdateAsset(c *client, assetID, description string) (bool, string) {
	_, text, err := c.callRaw("manage_asset", map[string]any{
		"action": "update", "asset_id": assetID, "description": description,
	})
	return err == nil && !strings.Contains(text, "your own assets"), text
}

// canUpdateCollection is canUpdateAsset for a collection.
func canUpdateCollection(c *client, collectionID, description string) (bool, string) {
	_, text, err := c.callRaw("manage_asset", map[string]any{
		"action": "update_collection", "collection_id": collectionID, "description": description,
	})
	return err == nil && !strings.Contains(text, "your own collections"), text
}

// produced1588 lists what the script has written, through the route the
// script's page reads, as the person given.
func producedOf1588(t *testing.T, c *client, scriptID string) map[string]map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/produced", nil)
	if status != http.StatusOK {
		t.Fatalf("reading what %s produced: status %d: %v", scriptID, status, body)
	}
	items, _ := body["data"].([]any)
	out := map[string]map[string]any{}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := item["target_kind"].(string)
		id, _ := item["target_id"].(string)
		out[kind+":"+id] = item
	}
	return out
}

// assetVersion1588 reads the asset's current version, which is what a run
// advances.
func assetVersion1588(t *testing.T, c *client, assetID string) float64 {
	t.Helper()
	out := c.call("manage_asset", map[string]any{"action": "get", "asset_id": assetID})
	v, _ := out["current_version"].(float64)
	return v
}

// TestIssue1588_ATransferOfAScriptWithOutputsMustSayWhatHappensToThem is
// criterion 1's sharp edge: the move is not made until the request states the
// disposition, and the refusal names what there is to decide about.
func TestIssue1588_ATransferOfAScriptWithOutputsMustSayWhatHappensToThem(t *testing.T) {
	p := setup1588(t)

	status, body := p.transfer(t, `{"owner_email":"`+devPeerEmailAddr+`"}`)

	if status != http.StatusBadRequest {
		t.Fatalf("a transfer that said nothing about the outputs was not refused: status %d: %v", status, body)
	}
	detail, _ := body["detail"].(string)
	for _, want := range []string{"1 asset and 1 collection", `"outputs": "move"`, `"outputs": "keep"`, devOwnerEmail} {
		if !strings.Contains(detail, want) {
			t.Fatalf("the refusal does not name %q: %q", want, detail)
		}
	}
	// Nothing moved: the script is still its owner's to read.
	out := p.owner.call("manage_script", map[string]any{"command": "get", "name": p.scriptName})
	if owner, _ := out["owner_email"].(string); owner != "" && owner != devOwnerEmail {
		t.Fatalf("the script moved despite the refusal: %v", out)
	}
}

// TestIssue1588_MovedOutputsBelongToTheNewOwner is criterion 2 and criterion
// 4: with outputs moved, the new owner can write over, and delete, the asset
// and the collection the script created; the previous owner cannot; a run by
// the new owner writes the next version into the SAME asset; and the script's
// inventory lists everything it produced, before and after.
func TestIssue1588_MovedOutputsBelongToTheNewOwner(t *testing.T) {
	p := setup1588(t)
	before := producedOf1588(t, p.owner, p.scriptID)
	if _, ok := before["asset:"+p.assetID]; !ok {
		t.Fatalf("the inventory does not list the asset the run wrote: %v", before)
	}
	if _, ok := before["collection:"+p.collectionID]; !ok {
		t.Fatalf("the inventory does not list the collection the run created: %v", before)
	}

	status, body := p.transfer(t, `{"owner_email":"`+devPeerEmailAddr+`","outputs":"MOVE"}`)

	if status != http.StatusOK {
		t.Fatalf("transferring with outputs moved: status %d: %v", status, body)
	}
	outputs, _ := body["outputs"].(map[string]any)
	if d, _ := outputs["disposition"].(string); d != "move" {
		t.Fatalf("the response does not state the outputs moved: %v", body)
	}
	if a, _ := outputs["assets"].(float64); a != 1 {
		t.Fatalf("the response counts %v assets moved, want 1: %v", a, body)
	}
	if c, _ := outputs["collections"].(float64); c != 1 {
		t.Fatalf("the response counts %v collections moved, want 1: %v", c, body)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "now belong to "+devPeerEmailAddr+" too") {
		t.Fatalf("the message does not say the files moved: %q", msg)
	}

	// Criterion 2: the new owner reaches both; the previous owner reaches
	// neither.
	if ok, text := canUpdateAsset(p.peer, p.assetID, "renamed by the new owner"); !ok {
		t.Fatalf("the new owner cannot write over the moved asset: %q", text)
	}
	if ok, text := canUpdateCollection(p.peer, p.collectionID, "described by the new owner"); !ok {
		t.Fatalf("the new owner cannot write over the moved collection: %q", text)
	}
	if ok, _ := canUpdateAsset(p.owner, p.assetID, "by the previous owner"); ok {
		t.Fatalf("the previous owner still owns the moved asset")
	}
	if ok, _ := canUpdateCollection(p.owner, p.collectionID, "by the previous owner"); ok {
		t.Fatalf("the previous owner still owns the moved collection")
	}
	asset := p.peer.call("manage_asset", map[string]any{"action": "get", "asset_id": p.assetID})
	if owner, _ := asset["owner_email"].(string); owner != devPeerEmailAddr {
		t.Fatalf("the moved asset records %q as its owner, want %q", owner, devPeerEmailAddr)
	}

	// A run by the new owner writes the next version into the same asset.
	was := assetVersion1588(t, p.peer, p.assetID)
	mustSucceed1579(t, p.peer, p.scriptName, map[string]any{"output": p.outputName, "collection": ""})
	if now := assetVersion1588(t, p.peer, p.assetID); now != was+1 {
		t.Fatalf("the new owner's run did not write the next version of the same asset: %v -> %v", was, now)
	}

	// Criterion 4: the inventory is unchanged by the move, and the address it
	// now reports is the new owner's.
	after := producedOf1588(t, p.peer, p.scriptID)
	for key := range before {
		if _, ok := after[key]; !ok {
			t.Fatalf("the inventory lost %s across the transfer: %v", key, after)
		}
	}
	if owner, _ := after["asset:"+p.assetID]["owner_email"].(string); owner != devPeerEmailAddr {
		t.Fatalf("the inventory reports the asset as %q's, want %q", owner, devPeerEmailAddr)
	}

	// Criterion 2, last clause: the new owner can delete it.
	p.peer.call("manage_asset", map[string]any{"action": "delete", "asset_id": p.assetID})
}

// TestIssue1588_KeptOutputsAreNamedAndStayOutOfTheNewOwnersReach is criterion
// 3 and criterion 4: with outputs kept, the response lists the files the new
// owner cannot reach, the new owner indeed cannot write over them, the runs go
// on writing new versions into them, and the script's inventory reports the
// same files with the previous owner's address, which is what its page marks.
func TestIssue1588_KeptOutputsAreNamedAndStayOutOfTheNewOwnersReach(t *testing.T) {
	p := setup1588(t)

	status, body := p.transfer(t, `{"owner_email":"`+devPeerEmailAddr+`","outputs":"keep"}`)

	if status != http.StatusOK {
		t.Fatalf("transferring with outputs kept: status %d: %v", status, body)
	}
	outputs, _ := body["outputs"].(map[string]any)
	if d, _ := outputs["disposition"].(string); d != "keep" {
		t.Fatalf("the response does not state the outputs were kept: %v", body)
	}
	kept, _ := outputs["kept"].([]any)
	named := map[string]string{}
	for _, raw := range kept {
		item, _ := raw.(map[string]any)
		kind, _ := item["target_kind"].(string)
		id, _ := item["target_id"].(string)
		owner, _ := item["owner_email"].(string)
		named[kind+":"+id] = owner
	}
	if named["asset:"+p.assetID] != devOwnerEmail || named["collection:"+p.collectionID] != devOwnerEmail {
		t.Fatalf("the response does not name both kept files as %s's: %v", devOwnerEmail, kept)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, devPeerEmailAddr+" cannot open, share or delete them") {
		t.Fatalf("the message does not say what the new owner cannot do: %q", msg)
	}

	// Criterion 3: the new owner cannot write over them; the previous owner
	// still can.
	if ok, _ := canUpdateAsset(p.peer, p.assetID, "by the new owner"); ok {
		t.Fatalf("the new owner can write over a kept asset")
	}
	if ok, _ := canUpdateCollection(p.peer, p.collectionID, "by the new owner"); ok {
		t.Fatalf("the new owner can write over a kept collection")
	}
	if ok, text := canUpdateAsset(p.owner, p.assetID, "still mine"); !ok {
		t.Fatalf("the previous owner lost a kept asset: %q", text)
	}

	// The new owner's run still writes the next version into the kept asset.
	was := assetVersion1588(t, p.owner, p.assetID)
	mustSucceed1579(t, p.peer, p.scriptName, map[string]any{"output": p.outputName, "collection": ""})
	if now := assetVersion1588(t, p.owner, p.assetID); now != was+1 {
		t.Fatalf("the run after the transfer did not write into the kept asset: %v -> %v", was, now)
	}

	// Criterion 4, and what the script's page marks: the inventory lists both
	// files, each with the previous owner's address.
	after := producedOf1588(t, p.peer, p.scriptID)
	for _, key := range []string{"asset:" + p.assetID, "collection:" + p.collectionID} {
		item, ok := after[key]
		if !ok {
			t.Fatalf("the inventory lost %s across the transfer: %v", key, after)
		}
		if owner, _ := item["owner_email"].(string); owner != devOwnerEmail {
			t.Fatalf("the inventory reports %s as %q's, want %q", key, owner, devOwnerEmail)
		}
	}
}

// TestIssue1588_AScriptWithNoOutputsMovesAsItAlwaysHas is criterion 5: a script
// whose runs created nothing needs no disposition and answers exactly as the
// transfer answered before.
func TestIssue1588_AScriptWithNoOutputsMovesAsItAlwaysHas(t *testing.T) {
	id := unique1579()
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	admin := connect(t)
	scriptName := "acceptance-1588-unrun-" + id
	owner.call("manage_script", map[string]any{
		"command": "create", "name": scriptName,
		"description": "Acceptance #1588: a script that has never run.",
		"source":      scriptOutputs1588, "params": params1588(),
	})
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": scriptName})
		_, _, _ = peer.callRaw("manage_script", map[string]any{"command": "delete", "name": scriptName})
	})

	status, body := admin.rest(http.MethodPut,
		"/api/v1/portal/scripts/"+scriptIDOf1579(t, owner, scriptName)+"/owner",
		strings.NewReader(`{"owner_email":"`+devPeerEmailAddr+`"}`))

	if status != http.StatusOK {
		t.Fatalf("transferring a script with no outputs: status %d: %v", status, body)
	}
	if _, present := body["outputs"]; present {
		t.Fatalf("a script with no outputs answered with an outputs account: %v", body)
	}
	want := scriptName + " now belongs to " + devPeerEmailAddr + " and runs with the access you hold, captured now."
	if msg, _ := body["message"].(string); msg != want {
		t.Fatalf("the message changed for a script with no outputs:\n got %q\nwant %q", msg, want)
	}
}
