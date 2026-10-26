//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2251: on a dev database carried across many runs, the
// admin endpoint list reaches every endpoint a page at a time and says how
// many there are, and a call just recorded is found by a word only it holds
// before it is embedded.
//
// Wire forms: the list takes page, per_page, access and status as query
// strings; manage_script endpoint_list takes limit as a JSON number (its
// schema types it integer); search takes sources as an array of strings.

// TestIssue2251_TheEndpointListReachesEveryEndpointAndCountsThem: read one
// endpoint a page, the pages hold every endpoint the total names, each once,
// the three bound here among them; a filter narrows the total; and
// endpoint_list cut at one says how many there are.
func TestIssue2251_TheEndpointListReachesEveryEndpointAndCountsThem(t *testing.T) {
	admin := connect(t)
	names := []string{endpointName("acc-2251-a"), endpointName("acc-2251-b"), endpointName("acc-2251-c")}
	for _, name := range names {
		saveEndpointScript(t, admin, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)
		bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "auth_mode": "authenticated", "callers": []string{"*"}})
	}

	status, first := admin.rest(http.MethodGet, "/api/v1/admin/endpoints?range=day", nil)
	if status != http.StatusOK {
		t.Fatalf("the list: %d %v", status, first)
	}
	total := int(number(t, first, "total"))
	if total < len(names) || number(t, first, "page") != 1 || number(t, first, "per_page") != 50 {
		t.Fatalf("the first page: total %v, page %v, per_page %v", first["total"], first["page"], first["per_page"])
	}

	// One a page, to the end: every endpoint, each once.
	seen := map[string]int{}
	for page := 1; page <= total+1; page++ {
		status, list := admin.rest(http.MethodGet, fmt.Sprintf("/api/v1/admin/endpoints?range=day&per_page=1&page=%d", page), nil)
		if status != http.StatusOK {
			t.Fatalf("page %d: %d %v", page, status, list)
		}
		rows, _ := list["endpoints"].([]any)
		if page > total {
			if len(rows) != 0 || int(number(t, list, "total")) != total {
				t.Errorf("past the last page: %d endpoints, total %v", len(rows), list["total"])
			}
			break
		}
		if len(rows) != 1 {
			t.Fatalf("page %d of %d holds %d endpoints", page, total, len(rows))
		}
		e, _ := rows[0].(map[string]any)
		seen[fmt.Sprint(e["name"])]++
	}
	if len(seen) != total {
		t.Errorf("%d pages named %d distinct endpoints", total, len(seen))
	}
	for _, name := range names {
		if seen[name] != 1 {
			t.Errorf("%s was listed %d times across the pages", name, seen[name])
		}
	}

	// A filter narrows the count, not only the page.
	_, authed := issue2208AllPages(t, admin, "range=day&access=authenticated", 200)
	_, public := issue2208AllPages(t, admin, "range=day&access=public", 200)
	_, checked := issue2208AllPages(t, admin, "range=day&access=bot_check", 200)
	if authed < len(names) || authed+public+checked != total {
		t.Errorf("authenticated %d, public %d and bot_check %d endpoints of %d", authed, public, checked, total)
	}
	if status, _ := admin.rest(http.MethodGet, "/api/v1/admin/endpoints?access=everyone", nil); status != http.StatusBadRequest {
		t.Errorf("an unknown access: %d, want 400", status)
	}

	// The MCP listing says what it left out.
	out := admin.call("manage_script", map[string]any{"command": "endpoint_list", "limit": 1})
	if number(t, out, "count") != 1 || int(number(t, out, "total")) != total {
		t.Fatalf("endpoint_list limit=1: count %v, total %v (want 1 of %d)", out["count"], out["total"], total)
	}
	if note, _ := out["note"].(string); !strings.Contains(note, fmt.Sprintf("1 of %d endpoints listed", total)) {
		t.Errorf("endpoint_list cut at one says %q", note)
	}
}

// TestIssue2251_AFreshCallIsFoundByAWordOnlyItHolds: once the call is
// recorded, one search for a word only it holds finds it, without waiting for
// the embedding service: the exact word ranks it above every recorded call
// that matches none of the query, however near in meaning.
func TestIssue2251_AFreshCallIsFoundByAWordOnlyItHolds(t *testing.T) {
	c := connect(t)
	admin := connect(t)
	marker := fmt.Sprintf("acceptance2251nonce%d", time.Now().UnixNano())
	invokeCarriesReference1624(c, "Acceptance #2251: a call found by the one word it holds, "+marker)
	awaitRecord(admin, c.sessionID)

	hits := callHits(c.call("search", map[string]any{
		"intent":  marker,
		"sources": []any{"calls"},
		"limit":   25,
		"purpose": "Acceptance #2251: find a call recorded a moment ago by its word.",
	}))
	if len(hits) == 0 {
		t.Fatal("the calls source returned nothing for the word")
	}
	if text, _ := hits[0]["text"].(string); !strings.Contains(text, marker) {
		t.Errorf("the first call hit is not the one holding the word: %.200s (of %d hits)", text, len(hits))
	}
}
