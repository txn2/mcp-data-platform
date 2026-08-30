//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1551: an asset a managed script produces belongs to the person who
// owns the script. Before the fix it belonged to the script principal alone, so
// it was absent from that person's Assets page, absent from manage_asset
// action=list, absent from search, and openable only by an administrator.
//
// Every call here is made by an ordinary, non-administrator person: the owner
// of the script, and a second person who is neither the owner nor a share
// recipient.
//
// Wire forms: the parameters this exercises are all typed in their tool
// schemas, so each admits exactly one JSON form -- manage_asset's action,
// asset_id, name, query and search are strings, tags and sources are arrays of
// strings, limit is a number; search's intent and purpose are strings and
// sources an array of strings; fetch's reference is a string; manage_script's
// command, name and source are strings; run_script's name is a string,
// wait_seconds a number and args an object. Each is sent below as a literal
// tools/call parameter of that form. The one parameter whose schema admits a
// second form is run_script's args, which is an object of free-form values: it
// is sent both with a string-valued member and with a number-valued one, and
// both runs must produce the same ownership.

// scriptSource writes one CSV output whose rows come from the run's own
// arguments, so no query engine is needed to prove who the output belongs to.
const scriptSource1551 = `
rows = [{"region": "north", "units": 41}, {"region": "south", "units": 17}]
platform.export(name=run.params["output"], rows=rows, format="csv")
`

// scriptSourceSaveAsset makes the same point through save_asset called from
// inside the run, which stamps ownership on its own path.
const scriptSourceSaveAsset1551 = `
platform.call("save_asset", {
    "name": run.params["output"],
    "content": "# Written by save_asset from inside a run\n",
    "content_type": "text/markdown",
    "description": "Acceptance #1551: a save_asset write made inside a run.",
})
`

// unique names one run of this file, so a re-run does not collide with the
// scripts and assets the last one left.
func unique1551() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// createScript saves a script owned by the calling person and returns its name.
func createScript1551(t *testing.T, c *client, name, source string) string {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1551: a script whose output belongs to the person who owns it.",
		"source":      source,
		"params": []any{map[string]any{
			"name": "output", "type": "string", "required": true,
			"description": "The output name this run writes.",
		}},
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{
			"command": "delete", "name": name,
		})
	})
	return name
}

// runScript1551 runs the script and waits for it, failing on a run that did not
// succeed, and returns the run record so a test can read what the run printed.
func runScript1551(t *testing.T, c *client, name string, args map[string]any) map[string]any {
	t.Helper()
	out := c.call("run_script", map[string]any{
		"name": name, "args": args, "wait_seconds": 120,
	})
	if status, _ := out["status"].(string); status != "succeeded" {
		t.Fatalf("run of %s did not succeed: %v", name, out)
	}
	return out
}

// ownedAssets1551 returns the calling person's own assets, keyed by name.
func ownedAssets1551(t *testing.T, c *client) map[string]map[string]any {
	t.Helper()
	out := c.call("manage_asset", map[string]any{
		"action": "list", "limit": 200,
	})
	byName := map[string]map[string]any{}
	list, _ := out["assets"].([]any)
	for _, item := range list {
		a, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := a["name"].(string)
		byName[name] = a
	}
	return byName
}

// TestIssue1551_TheOwnerOfAScriptOwnsWhatItsRunsProduce is the whole criterion
// in the order a person meets it: the run happens, the output is theirs on
// every read path, and they change and remove it without an administrator.
func TestIssue1551_TheOwnerOfAScriptOwnsWhatItsRunsProduce(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1551()
	scriptName := "acceptance-1551-export-" + id
	outputName := "acceptance-1551-output-" + id

	createScript1551(t, owner, scriptName, scriptSource1551)
	runScript1551(t, owner, scriptName, map[string]any{"output": outputName})

	// manage_asset action=list holds it, beside anything else this person owns.
	assets := ownedAssets1551(t, owner)
	asset, ok := assets[outputName]
	if !ok {
		t.Fatalf("the owner's asset list does not hold %q: %v", outputName, keysOf1551(assets))
	}
	assetID, _ := asset["id"].(string)
	if assetID == "" {
		t.Fatalf("the listed asset carries no id: %v", asset)
	}
	if ownerID, _ := asset["owner_id"].(string); !strings.HasPrefix(ownerID, "script:") {
		t.Fatalf("owner_id = %q, want the script principal: the fix is on the read side, not the write", ownerID)
	}
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{
			"action": "delete", "asset_id": assetID,
		})
	})

	// The Assets page reads the REST listing, which must hold it too.
	status, body := owner.rest("GET", "/api/v1/portal/assets?limit=200", nil)
	if status != 200 {
		t.Fatalf("GET /api/v1/portal/assets: status %d", status)
	}
	if !restListHolds1551(body, assetID) {
		t.Fatalf("the owner's Assets page does not hold %s", assetID)
	}

	// Opening it reports them as its owner, which is what turns on every
	// owner-only affordance the page offers.
	status, one := owner.rest("GET", "/api/v1/portal/assets/"+assetID, nil)
	if status != 200 {
		t.Fatalf("GET /api/v1/portal/assets/%s: status %d", assetID, status)
	}
	if isOwner, _ := one["is_owner"].(bool); !isOwner {
		t.Fatalf("is_owner = false for the person the output was produced for: %v", one)
	}

	// And the tool surface opens it for them as well.
	owner.call("manage_asset", map[string]any{
		"action": "get", "asset_id": assetID,
	})

	// They rename it and tag it, with no administrator.
	renamed := outputName + " (renamed)"
	owner.call("manage_asset", map[string]any{
		"action": "update", "asset_id": assetID,
		"name": renamed, "tags": []any{"script", "acceptance-1551"},
	})

	// They share it.
	share := owner.call("manage_asset", map[string]any{
		"action": "share", "asset_id": assetID,
		"recipient": devPeerEmailAddr, "permission": "viewer",
	})
	shareID, _ := share["share_id"].(string)
	if shareID == "" {
		if s, ok := share["share"].(map[string]any); ok {
			shareID, _ = s["id"].(string)
		}
	}
	if shareID == "" {
		t.Fatalf("share returned no share id: %v", share)
	}
	owner.call("manage_asset", map[string]any{
		"action": "revoke_share", "asset_id": assetID, "share_id": shareID,
	})

	// They register a table over it. The output is a CSV, and the dev stack's
	// Trino carries the scratch catalog a registration lands in.
	reg := owner.call("manage_table", map[string]any{
		"action": "register", "reference": "mcp:asset:" + assetID, "connection": "acme-scratch",
	})
	registrationID, _ := reg["registration_id"].(string)
	if registrationID == "" {
		t.Fatalf("register returned no registration id: %v", reg)
	}
	owner.call("manage_table", map[string]any{
		"action": "unregister", "registration_id": registrationID,
	})

	// Deleting it is theirs too. The cleanup above tolerates the second
	// attempt; this is the criterion.
	owner.call("manage_asset", map[string]any{
		"action": "delete", "asset_id": assetID,
	})
}

// TestIssue1551_DiscoveryReturnsAScriptOutputToItsOwner covers the two
// discovery verbs: an intent matching the output finds it, and the reference
// the hit carries dereferences to its metadata.
func TestIssue1551_DiscoveryReturnsAScriptOutputToItsOwner(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1551()
	scriptName := "acceptance-1551-discovery-" + id
	outputName := "acceptance-1551-quarterly-inventory-variance-" + id

	createScript1551(t, owner, scriptName, scriptSource1551)
	runScript1551(t, owner, scriptName, map[string]any{"output": outputName})

	assets := ownedAssets1551(t, owner)
	asset, ok := assets[outputName]
	if !ok {
		t.Fatalf("the owner's asset list does not hold %q", outputName)
	}
	assetID, _ := asset["id"].(string)
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{
			"action": "delete", "asset_id": assetID,
		})
	})

	reference := "mcp:asset:" + assetID
	found := false
	for attempt := 0; attempt < 10 && !found; attempt++ {
		out := owner.call("search", map[string]any{
			"intent":  strings.ReplaceAll(outputName, "-", " "),
			"sources": []any{"assets"}, "limit": 25,
			"purpose": "Acceptance #1551: the owner finds the asset a run produced.",
		})
		found = searchHolds1551(out, reference)
		if !found {
			time.Sleep(2 * time.Second)
		}
	}
	if !found {
		t.Fatalf("search over assets did not return %s for the person it was produced for", reference)
	}

	doc := owner.call("fetch", map[string]any{
		"reference": reference,
		"purpose":   "Acceptance #1551: the owner dereferences the search hit for the asset a run produced.",
	})
	if found, _ := doc["found"].(bool); !found {
		t.Fatalf("fetch did not resolve %s for the person it was produced for: %v", reference, doc)
	}
}

// TestIssue1551_SomebodyElseIsRefused: the widening is one person's, and the
// refusal does not distinguish a script output from an asset that is not there.
func TestIssue1551_SomebodyElseIsRefused(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1551()
	scriptName := "acceptance-1551-private-" + id
	outputName := "acceptance-1551-private-output-" + id

	createScript1551(t, owner, scriptName, scriptSource1551)
	runScript1551(t, owner, scriptName, map[string]any{"output": outputName})

	assets := ownedAssets1551(t, owner)
	asset, ok := assets[outputName]
	if !ok {
		t.Fatalf("the owner's asset list does not hold %q", outputName)
	}
	assetID, _ := asset["id"].(string)
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{
			"action": "delete", "asset_id": assetID,
		})
	})

	peer := connectAs(t, devPeerAPIKey)
	if _, held := ownedAssets1551(t, peer)[outputName]; held {
		t.Fatalf("%q is in a second person's asset list", outputName)
	}

	status, _ := peer.rest("GET", "/api/v1/portal/assets/"+assetID, nil)
	if status != 403 && status != 404 {
		t.Fatalf("GET as somebody else: status %d, want 403 or 404", status)
	}

	res, text, err := peer.callRaw("manage_asset", map[string]any{
		"action": "update", "asset_id": assetID, "name": "taken",
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("a second person renamed an asset that is not theirs")
	}
	if strings.Contains(strings.ToLower(text), "script") {
		t.Fatalf("the refusal names a script, so it distinguishes a script output from a missing asset: %s", text)
	}

	denied := peer.call("fetch", map[string]any{
		"reference": "mcp:asset:" + assetID,
		"purpose":   "Acceptance #1551: somebody else attempts to dereference another person's script output.",
	})
	if found, _ := denied["found"].(bool); found {
		t.Fatalf("fetch returned another person's script output: %v", denied)
	}
	// The miss reads the same as a reference that never existed.
	if message, _ := denied["message"].(string); !strings.Contains(message, "no content found") {
		t.Fatalf("the miss does not read as an ordinary not-found: %v", denied)
	}
}

// TestIssue1551_ASecondRunIsAVersionNotASecondAsset: one asset per (script,
// output) survives the widening, because owner_id is untouched and the
// idempotency lookup still keys on it.
func TestIssue1551_ASecondRunIsAVersionNotASecondAsset(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1551()
	scriptName := "acceptance-1551-versions-" + id
	outputName := "acceptance-1551-versioned-output-" + id

	createScript1551(t, owner, scriptName, scriptSource1551)
	// The two runs bind the same free-form argument in the two JSON forms its
	// schema admits, and must produce one asset either way.
	runScript1551(t, owner, scriptName, map[string]any{"output": outputName})
	runScript1551(t, owner, scriptName, map[string]any{"output": outputName})

	held := 0
	var assetID string
	for name, a := range ownedAssets1551(t, owner) {
		if name == outputName {
			held++
			assetID, _ = a["id"].(string)
		}
	}
	if held != 1 {
		t.Fatalf("the owner's list holds %d assets named %q, want exactly 1", held, outputName)
	}
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{
			"action": "delete", "asset_id": assetID,
		})
	})

	versions := owner.call("manage_asset", map[string]any{
		"action": "list_versions", "asset_id": assetID,
	})
	list, _ := versions["versions"].([]any)
	if len(list) < 2 {
		t.Fatalf("version history holds %d versions after two runs: %v", len(list), versions)
	}
}

// TestIssue1551_ASaveAssetInsideARunLandsInTheOwnersList: the second write path
// stamps ownership itself, so it has to reach the same person.
func TestIssue1551_ASaveAssetInsideARunLandsInTheOwnersList(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1551()
	scriptName := "acceptance-1551-saveasset-" + id
	outputName := "acceptance-1551-saved-" + id

	createScript1551(t, owner, scriptName, scriptSourceSaveAsset1551)
	runScript1551(t, owner, scriptName, map[string]any{"output": outputName})

	asset, ok := ownedAssets1551(t, owner)[outputName]
	if !ok {
		t.Fatalf("a save_asset write made inside a run is absent from the owner's list")
	}
	assetID, _ := asset["id"].(string)
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{
			"action": "delete", "asset_id": assetID,
		})
	})
}

// TestIssue1551_TheResourcesPathIsUnchanged: managed resources never had this
// defect, and a run's create still files under the address it acts for, in a
// library its owner can reach and a second person cannot.
func TestIssue1551_TheResourcesPathIsUnchanged(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1551()
	scriptName := "acceptance-1551-resource-" + id
	resourceName := "acceptance-1551-reference-" + id

	source := `
res = platform.call("manage_resource", {
    "action": "create",
    "display_name": "` + resourceName + `",
    "filename": "` + resourceName + `.csv",
    "path": "acceptance",
    "content": "region,units\nnorth,41\n",
    "content_type": "text/csv",
    "description": "Acceptance #1551: a managed resource written from inside a run.",
})
print(res["resource_id"])
`
	createScript1551(t, owner, scriptName, source)
	run := runScript1551(t, owner, scriptName, map[string]any{"output": resourceName})

	resourceID := strings.TrimSpace(lastLine1551(run))
	if resourceID == "" {
		t.Fatalf("the run printed no resource id: %v", run["log"])
	}
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_resource", map[string]any{
			"action": "delete", "resource_id": resourceID,
		})
	})

	mine := owner.call("fetch", map[string]any{
		"reference": "mcp:resource:" + resourceID,
		"purpose":   "Acceptance #1551: the owner reads the resource a run filed for them.",
	})
	if found, _ := mine["found"].(bool); !found {
		t.Fatalf("the resource a run wrote is not in the owner's library: %v", mine)
	}
	if scope := resourceScope1551(mine); scope != devOwnerEmail {
		t.Fatalf("the resource is filed under %q, want the address the run acts for", scope)
	}

	peer := connectAs(t, devPeerAPIKey)
	denied := peer.call("fetch", map[string]any{
		"reference": "mcp:resource:" + resourceID,
		"purpose":   "Acceptance #1551: somebody else attempts to read it.",
	})
	if found, _ := denied["found"].(bool); found {
		t.Fatalf("a second person read a resource filed in the owner's library")
	}
}

// resourceScope1551 reads the scope id a fetched resource records.
func resourceScope1551(doc map[string]any) string {
	d, _ := doc["document"].(map[string]any)
	content, _ := d["content"].(map[string]any)
	scope, _ := content["scope_id"].(string)
	return scope
}

// lastLine1551 returns the last non-empty line a run printed.
func lastLine1551(run map[string]any) string {
	log, _ := run["log"].(string)
	lines := strings.Split(strings.TrimSpace(log), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// TestIssue1551_ARunsOwnListingStaysItsOutputs: the widening runs one way. A
// run's asset inventory is what that script produced, not the library of the
// person it acts for, which is the limit the security model states and this
// change must not cross.
func TestIssue1551_ARunsOwnListingStaysItsOutputs(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id := unique1551()
	handSaved := "acceptance-1551-hand-saved-" + id
	scriptName := "acceptance-1551-inventory-" + id
	outputName := "acceptance-1551-inventory-output-" + id

	saved := owner.call("save_asset", map[string]any{
		"name": handSaved, "content": "# Saved by hand\n", "content_type": "text/markdown",
		"description": "Acceptance #1551: an asset the person saved themselves.",
	})
	savedID, _ := saved["asset_id"].(string)
	if savedID == "" {
		t.Fatalf("save_asset returned no asset id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{
			"action": "delete", "asset_id": savedID,
		})
	})

	source := `
platform.export(name=run.params["output"], rows=[{"region": "north"}], format="csv")
listed = platform.call("manage_asset", {"action": "list", "limit": 200})
for asset in listed["assets"]:
    print(asset["name"])
`
	createScript1551(t, owner, scriptName, source)
	run := runScript1551(t, owner, scriptName, map[string]any{"output": outputName})

	asset, ok := ownedAssets1551(t, owner)[outputName]
	if !ok {
		t.Fatalf("the owner's asset list does not hold %q", outputName)
	}
	assetID, _ := asset["id"].(string)
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{
			"action": "delete", "asset_id": assetID,
		})
	})

	log, _ := run["log"].(string)
	if !strings.Contains(log, outputName) {
		t.Fatalf("the run's own listing does not hold the output it just wrote: %q", log)
	}
	if strings.Contains(log, handSaved) {
		t.Fatalf("the run's listing holds an asset its owner saved by hand: %q", log)
	}
}

func keysOf1551(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func restListHolds1551(body map[string]any, assetID string) bool {
	list, _ := body["data"].([]any)
	for _, item := range list {
		a, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := a["id"].(string); id == assetID {
			return true
		}
	}
	return false
}

// searchHolds1551 reports whether a search result carries the reference, in the
// shape search answers with: one group per source, each carrying its hits.
func searchHolds1551(out map[string]any, reference string) bool {
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			continue
		}
		hits, _ := group["hits"].([]any)
		for _, item := range hits {
			hit, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if ref, _ := hit["reference"].(string); ref == reference {
				return true
			}
		}
	}
	return false
}
