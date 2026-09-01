//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1579: a managed script's run authenticates as script:<name>, and
// idx_scripts_name_owner makes a script name unique only within its OWNER, so
// two people who each keep a daily-sales present the same subject. Asset
// ownership matched on that subject, so a run of one person's script owned the
// outputs of the other person's same-named script: it could read them, write a
// new version over them, and reach them wherever ownsResource is the gate --
// while the person that run acts for could reach none of it.
//
// Both scripts here are created and run by ordinary, non-administrator people,
// and every criterion is executed through run_script, so what is exercised is a
// real run presenting its author's identity rather than a tool call the person
// makes. The two scripts carry the SAME name, which is what the platform allows
// and what the collision needs.
//
// Wire forms: every parameter touched here is typed in its tool schema and so
// admits exactly one JSON form. manage_asset's action, asset_id, name,
// description and query are strings and limit is a number; manage_script's
// command, name, description and source are strings and params an array of
// objects; run_script's name is a string and wait_seconds a number. The one
// parameter whose schema admits more than one form is run_script's args, an
// object of free-form values: it is sent below with string-valued members and,
// in TestIssue1579_TheCollisionIsIndependentOfTheArgumentForm, with a
// number-valued one, and both must produce the same ownership. Each is sent as
// a literal tools/call parameter of that form.

// scriptExport1579 writes one CSV output. Its rows come from the run itself, so
// proving who an output belongs to needs no query engine.
const scriptExport1579 = `
rows = [{"region": "north", "units": 41}]
platform.export(name=run.params["output"], rows=rows, format="csv")
`

// scriptInventory1579 prints the names of the assets this run's own listing
// holds, which is the enumeration the collision widened. The listing is scoped
// by the producer the platform recorded for this run's own writes, so it is
// this script's outputs whatever the script is named and whoever owns it.
const scriptInventory1579 = `
result = platform.call("manage_asset", {"action": "list", "limit": 200})
names = sorted([a["name"] for a in result["assets"]])
print("INVENTORY %s" % "|".join(names))
`

// scriptUpdate1579 writes a new description over a named asset. It is the
// ownsResource surface stated as a run: a refusal fails the run and its text is
// the run's error.
const scriptUpdate1579 = `
platform.call("manage_asset", {
    "action": "update",
    "asset_id": run.params["asset_id"],
    "description": "Acceptance #1579: written by a run.",
})
print("UPDATED")
`

// scriptSearch1579 prints the names the ranked search returns to this run.
const scriptSearch1579 = `
result = platform.call("manage_asset", {
    "action": "search", "query": run.params["query"], "limit": 50,
})
names = sorted([hit["asset"]["name"] for hit in result["assets"]])
print("SEARCH %s" % "|".join(names))
`

// unique1579 names one run of this file so a re-run collides with nothing the
// last one left.
func unique1579() string { return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000) }

// createScript1579 saves a script owned and authored by the calling person.
// Both people in these tests pass the SAME name, which is the premise.
func createScript1579(t *testing.T, c *client, name, source string, params []any) string {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1579: a script whose principal is shared with another owner's script of the same name.",
		"source":      source,
		"params":      params,
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	return name
}

// outputParam1579 is the one parameter an exporting script declares.
func outputParam1579() []any {
	return []any{map[string]any{
		"name": "output", "type": "string", "required": true,
		"description": "The output name this run writes.",
	}}
}

// run1579 runs a script and returns the finished run whatever its status: a
// criterion about a refusal reads the run's own error, which is where the
// person with the schedule reads it.
func run1579(t *testing.T, c *client, name string, args map[string]any) map[string]any {
	t.Helper()
	out := c.call("run_script", map[string]any{
		"name": name, "args": args, "wait_seconds": 120,
	})
	if status, _ := out["status"].(string); status == "" {
		t.Fatalf("run of %s reported no status: %v", name, out)
	}
	return out
}

// mustSucceed1579 runs a script and fails the test if the run did not succeed.
func mustSucceed1579(t *testing.T, c *client, name string, args map[string]any) map[string]any {
	t.Helper()
	out := run1579(t, c, name, args)
	if status, _ := out["status"].(string); status != "succeeded" {
		t.Fatalf("run of %s did not succeed: %v", name, out)
	}
	return out
}

// becomeUpdater1579 saves the update probe over an existing script, keeping its
// NAME and so keeping the principal it shares with the other owner's script of
// that name. A probe saved under a name of its own would present a principal
// nobody else has, and the collision this file is about would never arise.
func becomeUpdater1579(t *testing.T, c *client, scriptName string) {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command": "update", "name": scriptName, "source": scriptUpdate1579,
		"params": []any{map[string]any{
			"name": "asset_id", "type": "string", "required": true,
			"description": "The asset this run writes a description over.",
		}},
	})
}

// becomeInventory1579 saves the inventory probe over an existing script,
// keeping its name and its id, so what it enumerates is that same script's.
func becomeInventory1579(t *testing.T, c *client, scriptName string) {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command": "update", "name": scriptName, "source": scriptInventory1579,
	})
}

// scriptIDOf1579 returns the id of the calling person's script of this name,
// which is what the transfer route is addressed by.
func scriptIDOf1579(t *testing.T, c *client, name string) string {
	t.Helper()
	out := c.call("manage_script", map[string]any{"command": "get", "name": name})
	if id, _ := out["id"].(string); id != "" {
		return id
	}
	if sc, ok := out["script"].(map[string]any); ok {
		if id, _ := sc["id"].(string); id != "" {
			return id
		}
	}
	t.Fatalf("manage_script get returned no id for %q: %v", name, out)
	return ""
}

// printed1579 returns the line a probe script printed, identified by its prefix.
func printed1579(t *testing.T, run map[string]any, prefix string) string {
	t.Helper()
	log, _ := run["log"].(string)
	for _, line := range strings.Split(log, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	t.Fatalf("the run printed no %q line: %q", prefix, log)
	return ""
}

// assetIDOf1579 returns the id of the calling person's asset with this name,
// read through the listing that person's own Assets page reads.
func assetIDOf1579(t *testing.T, c *client, name string) string {
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
	t.Fatalf("%s owns no asset named %q", c.apiKey, name)
	return ""
}

// twoSameNamedScripts1579 is the premise both criteria rest on: two ordinary
// people who each keep a script of the same name, each having produced one
// output. It returns the shared script name, the two clients, and the two
// output names and asset ids.
type collision1579 struct {
	scriptName              string
	owner, peer             *client
	ownerOutput, peerOutput string
	ownerAsset, peerAsset   string
}

func twoSameNamedScripts1579(t *testing.T, id string) collision1579 {
	t.Helper()
	c := collision1579{
		scriptName:  "acceptance-1579-daily-sales-" + id,
		owner:       connectAs(t, devOwnerAPIKey),
		peer:        connectAs(t, devPeerAPIKey),
		ownerOutput: "acceptance-1579-owner-output-" + id,
		peerOutput:  "acceptance-1579-peer-output-" + id,
	}
	createScript1579(t, c.owner, c.scriptName, scriptExport1579, outputParam1579())
	createScript1579(t, c.peer, c.scriptName, scriptExport1579, outputParam1579())

	mustSucceed1579(t, c.owner, c.scriptName, map[string]any{"output": c.ownerOutput})
	mustSucceed1579(t, c.peer, c.scriptName, map[string]any{"output": c.peerOutput})

	c.ownerAsset = assetIDOf1579(t, c.owner, c.ownerOutput)
	c.peerAsset = assetIDOf1579(t, c.peer, c.peerOutput)
	return c
}

// TestIssue1579_ARunDoesNotOwnAnotherOwnersSameNamedScriptsOutput is criterion
// 1 and criterion 2 at the surface the ticket names first: a run of one
// person's daily-sales writes over the output of the other person's
// daily-sales. It must be refused, exactly as the person that run acts for is
// refused, and the author's own run must still write over its own output.
func TestIssue1579_ARunDoesNotOwnAnotherOwnersSameNamedScriptsOutput(t *testing.T) {
	id := unique1579()
	c := twoSameNamedScripts1579(t, id)

	// Criterion 4, checked here because it is the premise of everything below:
	// the two outputs are two rows, not one shared one.
	if c.ownerAsset == c.peerAsset || c.ownerAsset == "" || c.peerAsset == "" {
		t.Fatalf("the two scripts produced one asset row: owner %q, peer %q", c.ownerAsset, c.peerAsset)
	}

	// The write has to come from a run of the peer's SAME-NAMED script, or it
	// would present a principal of its own and the collision would never
	// arise. Saving a new source over that script keeps its name, and so keeps
	// the principal it shares with the owner's daily-sales.
	becomeUpdater1579(t, c.peer, c.scriptName)

	// The peer's run reaching for the owner's output.
	refused := run1579(t, c.peer, c.scriptName, map[string]any{"asset_id": c.ownerAsset})
	if status, _ := refused["status"].(string); status != "failed" {
		t.Fatalf("a run of the peer's daily-sales wrote over the owner's output: %v", refused)
	}
	if failure, _ := refused["error"].(string); !strings.Contains(failure, "your own assets") {
		t.Fatalf("the refusal did not come from the ownership check: %q", failure)
	}

	// The person that run acts for is refused the same write, which is the
	// property the run has to match.
	_, text, err := c.peer.callRaw("manage_asset", map[string]any{
		"action": "update", "asset_id": c.ownerAsset, "description": "by hand",
	})
	if err == nil && !strings.Contains(text, "your own assets") {
		t.Fatalf("the premise fails: the peer can write over the owner's asset by hand: %q", text)
	}

	// Criterion 3: the peer's own run still writes over its own script's output.
	ok := run1579(t, c.peer, c.scriptName, map[string]any{"asset_id": c.peerAsset})
	if status, _ := ok["status"].(string); status != "succeeded" {
		t.Fatalf("a run was refused its own script's output: %v", ok)
	}
}

// TestIssue1579_ARunsInventoryIsItsOwnScriptsOutputs is criterion 3's
// enumeration half and criterion 2's listing half. Before the fix the listing
// was scoped on the shared principal, so each run enumerated both people's
// outputs.
func TestIssue1579_ARunsInventoryIsItsOwnScriptsOutputs(t *testing.T) {
	id := unique1579()
	c := twoSameNamedScripts1579(t, id)

	// The inventory probe has to carry the SAME name as the exporting scripts,
	// or it would enumerate a different principal's rows. It is saved over the
	// same script name by each person, which is the edit funnel doing what a
	// person editing their own script does.
	for _, who := range []struct {
		name   string
		c      *client
		mine   string
		theirs string
	}{
		{"owner", c.owner, c.ownerOutput, c.peerOutput},
		{"peer", c.peer, c.peerOutput, c.ownerOutput},
	} {
		t.Run(who.name, func(t *testing.T) {
			// Saving a new version of the same-named script keeps the
			// principal, which is what makes this the inventory of the very
			// script whose outputs are in question.
			becomeInventory1579(t, who.c, c.scriptName)
			run := mustSucceed1579(t, who.c, c.scriptName, map[string]any{"output": "unused"})
			names := printed1579(t, run, "INVENTORY")
			if !strings.Contains(names, who.mine) {
				t.Fatalf("a run's inventory lost its own script's output: %q", names)
			}
			if strings.Contains(names, who.theirs) {
				t.Fatalf("a run's inventory holds another owner's same-named script's output: %q", names)
			}
		})
	}
}

// TestIssue1579_TheRankedSearchDoesNotCrossOwners is criterion 2's search half.
// The listing and the ranked search are two renderings of one judgment, and a
// fix in the listing alone would leave the collision reachable here.
func TestIssue1579_TheRankedSearchDoesNotCrossOwners(t *testing.T) {
	id := unique1579()
	c := twoSameNamedScripts1579(t, id)

	c.peer.call("manage_script", map[string]any{
		"command": "update", "name": c.scriptName, "source": scriptSearch1579,
		"params": []any{map[string]any{
			"name": "query", "type": "string", "required": true,
			"description": "What this run searches its own assets for.",
		}},
	})
	run := mustSucceed1579(t, c.peer, c.scriptName, map[string]any{"query": "acceptance-1579"})
	names := printed1579(t, run, "SEARCH")
	if strings.Contains(names, c.ownerOutput) {
		t.Fatalf("a run's search returned another owner's same-named script's output: %q", names)
	}
}

// TestIssue1579_TheOwnerStillReachesWhatTheirScriptWrote is criterion 5: an
// asset written under the shared principal keeps resolving for the person it
// was produced for, who is not an administrator, on the read and the write.
func TestIssue1579_TheOwnerStillReachesWhatTheirScriptWrote(t *testing.T) {
	id := unique1579()
	c := twoSameNamedScripts1579(t, id)

	got := c.owner.call("manage_asset", map[string]any{"action": "get", "asset_id": c.ownerAsset})
	if name, _ := got["name"].(string); name != c.ownerOutput {
		t.Fatalf("the owner cannot read what their own script produced: %v", got)
	}
	up := c.owner.call("manage_asset", map[string]any{
		"action": "update", "asset_id": c.ownerAsset,
		"description": "Acceptance #1579: renamed by the person the output was produced for.",
	})
	if up == nil {
		t.Fatal("the owner could not write over what their own script produced")
	}
}

// TestIssue1579_ATransferredScriptStillEnumeratesItsOwnOutputs is why the
// inventory is scoped by the producer rather than by anything on the row.
//
// An asset records the script owner's address at the moment it is inserted, and
// a transfer rewrites no asset row -- so after one, the row's owner_email is the
// PREVIOUS owner's while the run acts for the administrator who made the move.
// Neither identifier on the row names the script any more. The producer the
// platform recorded for the run's own writes does, and it is a script id, so it
// survives the transfer.
func TestIssue1579_ATransferredScriptStillEnumeratesItsOwnOutputs(t *testing.T) {
	id := unique1579()
	owner := connectAs(t, devOwnerAPIKey)
	admin := connect(t)
	scriptName := "acceptance-1579-transferred-" + id
	outputName := "acceptance-1579-transferred-output-" + id

	createScript1579(t, owner, scriptName, scriptExport1579, outputParam1579())
	mustSucceed1579(t, owner, scriptName, map[string]any{"output": outputName})

	// The move: to the second ordinary person, made by an administrator, which
	// is the only way a script changes hands.
	status, body := admin.rest(http.MethodPut,
		"/api/v1/portal/scripts/"+scriptIDOf1579(t, owner, scriptName)+"/owner",
		strings.NewReader(`{"owner_email":"`+devPeerEmailAddr+`"}`))
	if status != http.StatusOK {
		t.Fatalf("transferring %s: status %d: %v", scriptName, status, body)
	}

	// The peer now owns the script and may run it; the run acts for the
	// administrator who made the move, and the asset still records the original
	// owner's address.
	peer := connectAs(t, devPeerAPIKey)
	t.Cleanup(func() {
		_, _, _ = peer.callRaw("manage_script", map[string]any{"command": "delete", "name": scriptName})
	})
	becomeInventory1579(t, peer, scriptName)
	run := mustSucceed1579(t, peer, scriptName, map[string]any{"output": outputName})
	names := printed1579(t, run, "INVENTORY")
	if !strings.Contains(names, outputName) {
		t.Fatalf("a transferred script's run lost the outputs it produced: %q", names)
	}
}

// TestIssue1579_ADraftRunListsTheAuthorsOwnAssets guards the other side of the
// enumeration change. A draft is tagged as a script run for audit and for its
// own session identity, but it authenticates as the person at the keyboard: it
// carries no address it acts for and no script producer, so a scope keyed on
// the tag would refuse somebody the listing of their own library while they
// iterate on a script.
func TestIssue1579_ADraftRunListsTheAuthorsOwnAssets(t *testing.T) {
	id := unique1579()
	owner := connectAs(t, devOwnerAPIKey)
	assetName := "acceptance-1579-hand-saved-" + id

	owner.call("save_asset", map[string]any{
		"name": assetName, "content": "# Saved by hand\n", "content_type": "text/markdown",
		"description": "Acceptance #1579: an asset the person saved themselves.",
	})
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{
			"action": "delete", "asset_id": assetIDOf1579(t, owner, assetName),
		})
	})

	scriptName := "acceptance-1579-draft-" + id
	createScript1579(t, owner, scriptName, scriptInventory1579, []any{})

	out := owner.call("manage_script", map[string]any{
		"command": "run_draft", "name": scriptName, "source": scriptInventory1579,
	})
	log, _ := out["log"].(string)
	if !strings.Contains(log, assetName) {
		t.Fatalf("a draft run could not list the caller's own library: %q", log)
	}
}

// TestIssue1579_TheCollisionIsIndependentOfTheArgumentForm sends run_script's
// args -- the one parameter whose schema admits more than one JSON form -- with
// a number-valued member rather than a string-valued one. Ownership must not
// turn on how the run's arguments were encoded.
func TestIssue1579_TheCollisionIsIndependentOfTheArgumentForm(t *testing.T) {
	id := unique1579()
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	scriptName := "acceptance-1579-numeric-" + id

	// The output name is built from a NUMBER the run is handed, so args carries
	// a number-valued member on the wire.
	const numericExport = `
platform.export(name="acceptance-1579-numeric-%d" % run.params["suffix"], rows=[{"region": "north"}], format="csv")
`
	params := []any{map[string]any{
		"name": "suffix", "type": "int", "required": true,
		"description": "The numeric suffix of the output name this run writes.",
	}}
	createScript1579(t, owner, scriptName, numericExport, params)
	createScript1579(t, peer, scriptName, numericExport, params)

	mustSucceed1579(t, owner, scriptName, map[string]any{"suffix": 1})
	mustSucceed1579(t, peer, scriptName, map[string]any{"suffix": 2})

	ownerAsset := assetIDOf1579(t, owner, "acceptance-1579-numeric-1")
	peerAsset := assetIDOf1579(t, peer, "acceptance-1579-numeric-2")
	if ownerAsset == peerAsset {
		t.Fatalf("two owners' same-named scripts wrote one asset row: %q", ownerAsset)
	}

	becomeUpdater1579(t, peer, scriptName)
	refused := run1579(t, peer, scriptName, map[string]any{"asset_id": ownerAsset})
	if status, _ := refused["status"].(string); status != "failed" {
		t.Fatalf("a run reached another owner's same-named script's output: %v", refused)
	}
}
