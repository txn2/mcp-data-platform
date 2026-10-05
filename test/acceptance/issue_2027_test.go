//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2027: one read rule for a script's definition on every surface.
//
// #1866 made a script's definition readable by everyone signed in on the
// direct read surfaces, while search, fetch, prompt references and knowledge
// page citations still treated a script as private to its owner. What this
// holds, against the running platform:
//
//   - a caller who owns no scripts finds another person's script with search,
//     and fetch returns its contract and its source;
//   - a knowledge page citing another person's script resolves the citation
//     for every reader, and the knowledge graph draws the script for them;
//   - a prompt referencing another person's script embeds its contract when
//     served to a reader who does not own it;
//   - search finds a script by a word that appears only in a comment in its
//     source;
//   - a persona whose tool rules do not allow manage_script finds and fetches
//     scripts;
//   - fetch of a script names the outputs the reader can open and counts the
//     rest without naming them, each named output's reference fetches for that
//     reader (an asset shared with them included), and the graph draws only the
//     edges to outputs the reader can open;
//   - running, editing, reading the runs and the state of another person's
//     script are refused exactly as before.
//
// The criterion that the stale comments and docs state the rule the code
// applies is a review criterion and is held by make doc-check and review, not
// executed here.
//
// The script's owner is the dev stack's owner key; the reader who owns no part
// of it is the peer key (both collaborators, whose persona allows every tool),
// and the inventory analyst key, whose persona allows search and fetch and not
// manage_script. Knowledge pages are promoted by the administrator key, since
// apply_knowledge is the administrator's.
//
// Wire forms: search's `intent`, `purpose` are strings, `sources` an array of
// strings and `limit` a number; fetch's `reference` and `purpose` are strings;
// manage_script's `command`, `name`, `description`, `source` are strings and
// `params` an array of objects; run_script's `name` is a string, `args` an
// object, `wait_seconds` a number; manage_prompt's `command`, `name`,
// `content`, `scope`, `script` are strings; apply_knowledge's `page` is an
// object whose `references` is an array of strings. Each parameter is typed in
// its schema and admits that one form, sent as a literal tools/call param.

// script2027 is a script that exports two reports, and whose source carries a
// word found nowhere on its card.
const script2027 = `
# The quokkafence window: a customer with no order in ninety days has churned.
def main():
    """Writes the shared report and the private one."""
    platform.export(name = run.params["shared"], rows = [{"region": "north", "churned": 4}], format = "csv")
    platform.export(name = run.params["private"], rows = [{"region": "south", "churned": 9}], format = "csv")
`

type fixture2027 struct {
	name, id, shared, private, sharedID, privateID string
}

// setup2027 saves the owner's script and runs it once, so it has produced
// two assets, and shares the first with the peer.
func setup2027(t *testing.T, owner *client) fixture2027 {
	t.Helper()
	n := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	f := fixture2027{name: "acc-2027-" + n, shared: "acc-2027-shared-" + n, private: "acc-2027-private-" + n}
	params := []any{
		map[string]any{"name": "shared", "type": "string", "required": true, "description": "The report shared with the peer."},
		map[string]any{"name": "private", "type": "string", "required": true, "description": "The report nobody else is shown."},
	}
	owner.saveScript(map[string]any{
		"command": "create", "name": f.name,
		"description": "Acceptance #2027: weekly churn by region.",
		"source":      script2027, "params": params,
	}, map[string]any{"shared": f.shared + "-draft", "private": f.private + "-draft"})
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": f.name}) })
	f.id = scriptID1569(t, owner, f.name)

	runScript1569(t, owner, f.name, map[string]any{"shared": f.shared, "private": f.private})
	assets := ownedAssets1551(t, owner)
	for name, dest := range map[string]*string{f.shared: &f.sharedID, f.private: &f.privateID} {
		a, ok := assets[name]
		if !ok {
			t.Fatalf("the run did not produce %q", name)
		}
		*dest, _ = a["id"].(string)
		id := *dest
		t.Cleanup(func() { _, _, _ = owner.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	}
	status, share := owner.rest(http.MethodPost, "/api/v1/portal/assets/"+f.sharedID+"/shares",
		jsonBody(t, map[string]any{"shared_with_email": devPeerEmailAddr, "permission": "viewer"}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("share the report with the peer: HTTP %d: %v", status, share)
	}
	return f
}

// fetch2027 dereferences the script as the reader and returns the document.
func fetch2027(t *testing.T, reader *client, id string) (body string, content map[string]any) {
	t.Helper()
	out := reader.call("fetch", map[string]any{
		"reference": "mcp:script:" + id,
		"purpose":   "Acceptance #2027: a reader reads a script's definition.",
	})
	if found, _ := out["found"].(bool); !found {
		t.Fatalf("fetch did not return the script to this reader: %v", out)
	}
	doc, _ := out["document"].(map[string]any)
	body, _ = doc["body"].(string)
	content, _ = doc["content"].(map[string]any)
	return body, content
}

// searches2027 reports whether search returns the script to the reader.
func searches2027(t *testing.T, reader *client, intent, id string) bool {
	t.Helper()
	out := reader.call("search", map[string]any{
		"intent": intent, "sources": []any{"scripts"}, "limit": 25,
		"purpose": "Acceptance #2027: a reader looks for the script that does this.",
	})
	return searchHolds1551(out, "mcp:script:"+id)
}

func TestIssue2027_ACallerWhoOwnsNoScriptsFindsAndFetchesAnothersScript(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	f := setup2027(t, owner)

	if !searches2027(t, peer, "weekly churn by region", f.id) {
		t.Fatalf("search did not return another person's script to the peer")
	}
	body, content := fetch2027(t, peer, f.id)
	for _, want := range []string{"Acceptance #2027: weekly churn by region.", "Source (version 1):", "quokkafence window", "platform.export"} {
		if !strings.Contains(body, want) {
			t.Errorf("the fetched document does not carry %q:\n%s", want, body)
		}
	}
	if content["runs_withheld"] != true {
		t.Errorf("the last run reached a reader who does not own the script: %v", content)
	}
	if _, present := content["last_successful_run"]; present {
		t.Errorf("the last run reached a reader who does not own the script: %v", content["last_successful_run"])
	}
}

func TestIssue2027_SearchFindsAPhraseOnlyInTheSource(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	f := setup2027(t, owner)

	if !searches2027(t, peer, "quokkafence", f.id) {
		t.Fatalf("search did not find the script by a word that appears only in a comment in its source")
	}
}

func TestIssue2027_APersonaWithoutManageScriptFindsAndFetches(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	analyst := connectAs(t, "acme-analyst-key")
	for _, tool := range analyst.tools() {
		if tool.Name == "manage_script" {
			t.Fatalf("the analyst's persona was expected to deny manage_script")
		}
	}
	f := setup2027(t, owner)

	if !searches2027(t, analyst, "weekly churn by region", f.id) {
		t.Fatalf("search did not return the script to a persona without manage_script")
	}
	body, _ := fetch2027(t, analyst, f.id)
	if !strings.Contains(body, "quokkafence window") {
		t.Errorf("fetch did not carry the source to a persona without manage_script:\n%s", body)
	}
}

func TestIssue2027_FetchListsOpenableOutputsAndCountsTheRest(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	f := setup2027(t, owner)

	body, content := fetch2027(t, peer, f.id)
	if !strings.Contains(body, f.shared+" (mcp:asset:"+f.sharedID+")") {
		t.Errorf("the output shared with the peer is not named:\n%s", body)
	}
	if !strings.Contains(body, "1 more you cannot open") {
		t.Errorf("the output not shared with the peer is not counted:\n%s", body)
	}
	if content["outputs_hidden"] != float64(1) {
		t.Errorf("outputs_hidden = %v; want 1", content["outputs_hidden"])
	}
	all := fmt.Sprint(content) + body
	if strings.Contains(all, f.private) || strings.Contains(all, f.privateID) {
		t.Errorf("the output not shared with the peer is named to them")
	}

	ownerBody, _ := fetch2027(t, owner, f.id)
	if !strings.Contains(ownerBody, f.private) {
		t.Errorf("the owner is not shown their own output:\n%s", ownerBody)
	}

	// The named output's reference is one the peer can follow: fetch opens an
	// asset shared with a person, as the portal does; the other stays closed.
	for id, want := range map[string]bool{f.sharedID: true, f.privateID: false} {
		out := peer.call("fetch", map[string]any{
			"reference": "mcp:asset:" + id,
			"purpose":   "Acceptance #2027: a reader follows an output the script listed.",
		})
		if found, _ := out["found"].(bool); found != want {
			t.Errorf("fetch mcp:asset:%s as the peer: found=%v, want %v", id, found, want)
		}
	}
}

func TestIssue2027_APageCitingAnothersScriptResolvesForEveryReader(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	f := setup2027(t, owner)
	ref := "mcp:script:" + f.id

	pageID := promote1855(t, admin, "Weekly churn is computed by [the churn script]("+ref+").", []any{ref})

	got, ok := pageRefs1855(t, peer, pageID)[ref]
	if !ok {
		t.Fatalf("the script citation is not listed for a reader who does not own the script")
	}
	if got["label"] != f.name {
		t.Errorf("the citation is labeled %v; want %q", got["label"], f.name)
	}
	status, resolved := peer.rest(http.MethodPost, "/api/v1/portal/knowledge-pages/refs/resolve",
		strings.NewReader(`{"urns":["`+ref+`"]}`))
	if status != http.StatusOK {
		t.Fatalf("resolve as the peer: %d %v", status, resolved)
	}
	list, _ := resolved["refs"].([]any)
	if r, _ := list[0].(map[string]any); r["accessible"] != true {
		t.Errorf("the citation does not resolve for the peer: %v", r)
	}

	g := graph1985(t, peer)
	if _, ok := g.nodes[ref]; !ok {
		t.Fatalf("the knowledge graph does not draw the script for the peer")
	}
	if !g.hasEdge("mcp:knowledge_page:"+pageID, ref) {
		t.Errorf("the graph does not draw the page's citation of the script")
	}
	if !g.hasEdge(ref, "mcp:asset:"+f.sharedID) {
		t.Errorf("the graph does not draw the output shared with the peer")
	}
	if g.hasEdge(ref, "mcp:asset:"+f.privateID) {
		t.Errorf("the graph draws an output not shared with the peer")
	}
}

func TestIssue2027_APromptServesAnothersScriptContract(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	f := setup2027(t, owner)

	name := "acc-2027-prompt-" + f.id[:8]
	created := peer.call("manage_prompt", map[string]any{
		"command": "create", "name": name, "scope": "personal",
		"content": "Read the weekly churn report before answering.",
	})
	if promptID1586(created) == "" {
		t.Fatalf("manage_prompt create: %v", created)
	}
	t.Cleanup(func() { _, _, _ = peer.callRaw("manage_prompt", map[string]any{"command": "delete", "name": name}) })

	attached := peer.call("manage_prompt", map[string]any{
		"command": "attach_script", "name": name, "script": "mcp:script:" + f.id,
	})
	if attached["status"] != "attached" {
		t.Fatalf("a reader could not reference another person's script: %v", attached)
	}

	used := peer.call("manage_prompt", map[string]any{"command": "use", "name": name})
	scripts, _ := used["scripts"].([]any)
	if len(scripts) != 1 {
		t.Fatalf("manage_prompt use lists %d scripts; want 1: %v", len(scripts), used)
	}
	entry, _ := scripts[0].(map[string]any)
	if entry["availability"] != "embedded" {
		t.Fatalf("the script was not delivered to the peer: %v", entry)
	}
	contract, _ := entry["contract"].(map[string]any)
	if contract["name"] != f.name {
		t.Errorf("the delivered contract names %v; want %q", contract["name"], f.name)
	}
	if _, present := contract["last_successful_run"]; present {
		t.Errorf("the last run reached a reader who does not own the script: %v", contract)
	}
}

func TestIssue2027_ActingStaysTheOwners(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	f := setup2027(t, owner)

	res, text, err := peer.callRaw("run_script", map[string]any{
		"name": f.name, "owner_email": devOwnerEmail, "args": map[string]any{"shared": "x", "private": "y"}, "wait_seconds": 5,
	})
	if err != nil || !res.IsError {
		t.Errorf("run_script of another person's script was not refused: %v %s", err, text)
	}
	for _, args := range []map[string]any{
		{"command": "runs", "name": f.name, "owner_email": devOwnerEmail},
		{"command": "state", "name": f.name, "owner_email": devOwnerEmail},
		{"command": "update", "name": f.name, "owner_email": devOwnerEmail, "description": "taken over"},
	} {
		res, text, err := peer.callRaw("manage_script", args)
		if err != nil || !res.IsError || !strings.Contains(text, "only a script's owner or an administrator") {
			t.Errorf("manage_script %v on another person's script was not refused in those words: %v %s", args["command"], err, text)
		}
	}
	for _, path := range []string{"/runs", "/produced"} {
		status, _ := peer.rest(http.MethodGet, "/api/v1/portal/scripts/"+f.id+path, http.NoBody)
		if status != http.StatusNotFound {
			t.Errorf("GET %s of another person's script: HTTP %d; want 404", path, status)
		}
	}
}
