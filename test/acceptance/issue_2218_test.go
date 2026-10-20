//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// Issue #2218: the whole acceptance suite, run nightly, failed criteria that
// pass alone. Two of those failures were the platform answering differently
// depending on which replica a request reached a moment after a write to the
// other one. Each criterion here makes the write on one replica and asks the
// other at once, through the dev stack's two replicas directly, so the result
// no longer depends on the proxy's round robin.
//
// Wire forms: the connection save takes a JSON object whose config is an
// object; the webhook source create takes a JSON object with auth and config
// objects; api_invoke_endpoint is sent with connection, method, path and
// purpose strings; save_asset with name, content, content_type and purpose
// strings and sources as a list of call reference strings; manage_asset get
// with action and asset_id strings. Each admits that one form.

// TestIssue2218_ATrinoConnectionSavedOnOneReplicaHoldsATableOnTheOther: a
// webhook source on a Trino connection saved through the other replica a
// moment before is created, not refused for a scratch catalog the connection
// has. The second replica read the connection from the store instead of
// waiting for the save's announcement.
func TestIssue2218_ATrinoConnectionSavedOnOneReplicaHoldsATableOnTheOther(t *testing.T) {
	a, b := connectReplicaPair(t)
	conn := "acc2218-scratch-" + unique1805()
	issue1888Connection(t, a, conn, "mcp-scratch")

	source := issue1870Name("acc2218-src")
	status, body := b.rest(http.MethodPost, "/api/v1/admin/webhooks/sources", jsonBody(t, map[string]any{
		"name": source, "connection": conn,
		"auth":   map[string]any{"mode": "hmac", "secret": "whsec_2218", "signature_header": "X-Signature", "prefix": "sha256="},
		"config": map[string]any{"split": "$", "persona": "admin"},
	}))
	if status == http.StatusCreated {
		t.Cleanup(func() { _, _ = b.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+source, http.NoBody) })
		return
	}
	detail, _ := body["detail"].(string)
	if strings.Contains(detail, "no scratch catalog") {
		t.Fatalf("the replica that did not save the connection answered as if it had no scratch target: %v", body)
	}
	t.Fatalf("creating the source on the other replica answered %d: %v", status, body)
}

// TestIssue2218_ACallServedByOneReplicaIsCitedThroughTheOther: an asset saved
// through one replica, citing a call the other replica served a moment
// before, records that call as its source. The saving replica's flush drains
// only its own audit writer, so it reads the log again for a call it does not
// find yet rather than recording the capture without it.
func TestIssue2218_ACallServedByOneReplicaIsCitedThroughTheOther(t *testing.T) {
	a, b := connectReplicaPair(t)
	for attempt := range 5 {
		callID := issue1623Cite(a)
		saved := b.call("save_asset", map[string]any{
			"name":         fmt.Sprintf("acceptance-2218-cited-%d-%s", attempt, unique1805()),
			"content":      "# Cited\n\nRead from the fixture.\n",
			"content_type": "text/markdown",
			"sources":      []any{"mcp:call:" + callID},
			"purpose":      "Acceptance #2218: an asset citing a call another replica served.",
		})
		id, _ := saved["asset_id"].(string)
		if id == "" {
			t.Fatalf("save_asset returned no asset_id: %v", saved)
		}
		t.Cleanup(func() { _, _, _ = b.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })

		read := b.call("manage_asset", map[string]any{"action": "get", "asset_id": id})
		raw, _ := json.Marshal(read["provenance"])
		var prov struct {
			Captures []struct {
				EventIDs []string `json:"event_ids"`
			} `json:"captures"`
		}
		_ = json.Unmarshal(raw, &prov)
		if len(prov.Captures) == 0 || !slices.Contains(prov.Captures[0].EventIDs, callID) {
			t.Fatalf("attempt %d: the asset does not record the cited call %s: %s", attempt, callID, raw)
		}
	}
}
