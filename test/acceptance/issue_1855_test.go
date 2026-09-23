//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1855: apply_knowledge refused a knowledge page citing a managed script
// (mcp:script:<id>), so a page whose point is "table X is kept in sync by
// script Z" could not cite the script at all.
//
// What this holds, against the running platform: an apply whose
// page.references includes an existing script succeeds and the reference is
// listed on the page; a script named in the page body is picked up the same
// way; a reader who cannot open the script (a script is personal) still reads
// the page, with the script reference withheld, and fetch answers that reader
// found=false for it; and a script that does not exist is still refused.
//
// The script's owner is the dev stack's owner key, the reader who cannot open
// it is the peer key (both collaborators), and the page is promoted by the
// default administrator key, since apply_knowledge is the administrator's.
//
// Wire forms: apply_knowledge's `action`, `sink` and `confirm` are typed
// string/string/boolean and `page` is an object whose `references` is an
// array of strings; manage_script's `command`, `name`, `description` and
// `source` are strings; fetch's `reference` is a string. Each is sent in that
// one form, as literal tools/call params.

// script1855 creates a personal script as the owner key and returns its id.
func script1855(t *testing.T, owner *client) string {
	t.Helper()
	name := fmt.Sprintf("acc-1855-%d", time.Now().UnixNano())
	owner.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1855: the script a knowledge page cites.",
		"source":      "print(\"orders synced\")\n",
	})
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := owner.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("manage_script get names no id: %v", got)
	}
	return id
}

// promote1855 applies a knowledge page and registers its rollback, returning
// the page id.
func promote1855(t *testing.T, admin *client, body string, references []any) string {
	t.Helper()
	out := admin.call("apply_knowledge", map[string]any{
		"action": "apply", "sink": "knowledge_page", "confirm": true,
		"page": map[string]any{
			"slug":       fmt.Sprintf("acceptance-1855-%d", time.Now().UnixNano()),
			"title":      "Acceptance 1855 orders sync",
			"summary":    "Written by the #1855 acceptance run.",
			"body":       body,
			"references": references,
			"force_new":  true,
		},
	})
	if csID, _ := out["changeset_id"].(string); csID != "" {
		rollback1696(t, admin, csID)
	}
	pageID, _ := out["page_id"].(string)
	if pageID == "" {
		t.Fatalf("the promotion names no page: %v", out)
	}
	return pageID
}

// pageRefs1855 reads a page's references as the given reader, by URN.
func pageRefs1855(t *testing.T, reader *client, pageID string) map[string]map[string]any {
	t.Helper()
	status, out := reader.rest(http.MethodGet, "/api/v1/portal/knowledge-pages/"+pageID+"/refs", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET refs: status %d: %v", status, out)
	}
	refs := map[string]map[string]any{}
	list, _ := out["refs"].([]any)
	for _, item := range list {
		r, _ := item.(map[string]any)
		urn, _ := r["urn"].(string)
		refs[urn] = r
	}
	return refs
}

func TestIssue1855_AnExistingScriptIsCitedAndListed(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	ref := "mcp:script:" + script1855(t, owner)

	pageID := promote1855(t, admin, "The orders table is kept in sync daily.", []any{ref})

	for who, reader := range map[string]*client{"the owner": owner, "an administrator": admin} {
		got, ok := pageRefs1855(t, reader, pageID)[ref]
		if !ok {
			t.Fatalf("the script reference is not listed for %s", who)
		}
		if got["type"] != "script" || got["source"] != "promoted" {
			t.Errorf("the listed script reference = %v; want type script, source promoted", got)
		}
		if label, _ := got["label"].(string); !strings.HasPrefix(label, "acc-1855-") {
			t.Errorf("the script reference is labeled %q; want the script's name", label)
		}
	}
}

func TestIssue1855_AScriptNamedInTheBodyIsPickedUp(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	ref := "mcp:script:" + script1855(t, owner)

	pageID := promote1855(t, admin, "The orders table is kept in sync by [the sync script]("+ref+").", []any{})

	got, ok := pageRefs1855(t, owner, pageID)[ref]
	if !ok {
		t.Fatalf("the script named in the body is not listed on the page")
	}
	if got["source"] != "inline" {
		t.Errorf("the body reference's source = %v; want inline", got["source"])
	}
}

func TestIssue1855_AReaderWhoCannotOpenTheScriptStillReadsThePage(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	ref := "mcp:script:" + script1855(t, owner)
	const dataset = "urn:li:dataset:(urn:li:dataPlatform:trino,acceptance.orders.daily,PROD)"

	pageID := promote1855(t, admin, "The orders table is kept in sync daily.", []any{ref, dataset})

	status, page := peer.rest(http.MethodGet, "/api/v1/portal/knowledge-pages/"+pageID, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the page is not readable by a reader who cannot open the script: %d %v", status, page)
	}
	refs := pageRefs1855(t, peer, pageID)
	if _, listed := refs[ref]; listed {
		t.Errorf("the script reference is listed for a reader who cannot open the script: %v", refs[ref])
	}
	if _, listed := refs[dataset]; !listed {
		t.Errorf("the page's other reference is not listed for that reader: %v", refs)
	}

	status, resolved := peer.rest(http.MethodPost, "/api/v1/portal/knowledge-pages/refs/resolve",
		strings.NewReader(`{"urns":["`+ref+`"]}`))
	if status != http.StatusOK {
		t.Fatalf("resolve as the peer: %d %v", status, resolved)
	}
	list, _ := resolved["refs"].([]any)
	if len(list) != 1 {
		t.Fatalf("resolve returned %v", resolved)
	}
	if r, _ := list[0].(map[string]any); r["accessible"] != false {
		t.Errorf("the script resolves as accessible for a reader who cannot open it: %v", r)
	}

	fetched := peer.call("fetch", map[string]any{
		"reference": ref,
		"purpose":   "Acceptance #1855: a reader who cannot open the script follows the page's citation.",
	})
	if found, _ := fetched["found"].(bool); found {
		t.Errorf("fetch returned the script to a reader who does not own it: %v", fetched)
	}
}

func TestIssue1855_AMissingScriptIsRefused(t *testing.T) {
	admin := connect(t)
	res, text, err := admin.callRaw("apply_knowledge", map[string]any{
		"action": "apply", "sink": "knowledge_page", "confirm": true,
		"page": map[string]any{
			"slug":       fmt.Sprintf("acceptance-1855-missing-%d", time.Now().UnixNano()),
			"title":      "Acceptance 1855 missing script",
			"body":       "Cites a script that does not exist.",
			"references": []any{"mcp:script:0b7e2f0c-1111-4a4a-8b8b-000000000000"},
			"force_new":  true,
		},
	})
	if err != nil {
		t.Fatalf("apply_knowledge: transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("a citation of a script that does not exist was accepted: %s", text)
	}
	if !strings.Contains(text, "does not exist") {
		t.Errorf("the refusal does not say the script does not exist: %s", text)
	}
}
