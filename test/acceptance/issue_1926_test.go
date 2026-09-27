//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// Issue #1926: GET /api/v1/portal/memory/records lists only the caller's own
// records, and no admin route listed memory records at all, so an
// administrator could not see, count or audit the memory on their deployment.
// GET /api/v1/admin/memory/records lists every record, with the portal
// route's filters and limit/offset paging plus created_by.
//
// Every criterion runs against the running platform: records are written by
// two different users through memory_capture, and the administrator lists
// them through api_invoke_endpoint on the built-in platform-admin connection.
//
// Wire forms: api_invoke_endpoint's query_params values are untyped, so limit
// and offset are sent as a number and as a string. created_by is a string.
// memory_capture's type, content, category and confidence are strings only.

const (
	issue1926AdminConn = "platform-admin"
	issue1926Route     = "/api/v1/admin/memory/records"
	issue1926Purpose   = "Acceptance #1926: an administrator lists every user's memory records."
)

// issue1926Capture writes one memory record as c and forgets it when the test
// ends.
func issue1926Capture(t *testing.T, c *client, content string) string {
	t.Helper()
	out := c.call("memory_capture", map[string]any{
		"type":       "business_knowledge",
		"content":    content,
		"category":   "business_context",
		"confidence": "high",
	})
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("memory_capture returned no id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("memory_manage", map[string]any{"command": "forget", "id": id})
	})
	return id
}

// issue1926List lists one page of the admin memory route through
// api_invoke_endpoint as c.
func issue1926List(t *testing.T, c *client, query map[string]any) (ids []string, total float64) {
	t.Helper()
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection":   issue1926AdminConn,
		"method":       "GET",
		"path":         issue1926Route,
		"query_params": query,
		"purpose":      issue1926Purpose,
	})
	if status := number(t, out, "status"); status != http.StatusOK {
		t.Fatalf("GET %s: upstream status %v: %v", issue1926Route, status, out["body"])
	}
	body, _ := out["body"].(map[string]any)
	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("GET %s: body = %v; want a data list", issue1926Route, out["body"])
	}
	for _, r := range data {
		rec, _ := r.(map[string]any)
		id, _ := rec["id"].(string)
		ids = append(ids, id)
	}
	return ids, number(t, body, "total")
}

// TestIssue1926_AdminListsEveryUsersRecords: an administrator lists records
// written by two different users, each narrowed by created_by, and neither
// user's record is hidden from the unnarrowed list.
func TestIssue1926_AdminListsEveryUsersRecords(t *testing.T) {
	owner, peer := connectAs(t, devOwnerAPIKey), connectAs(t, devPeerAPIKey)
	stamp := time.Now().UnixNano()
	ownerID := issue1926Capture(t, owner, fmt.Sprintf("Acceptance 1926 owner note %d: the fiscal calendar closes on the last Friday of each quarter.", stamp))
	peerID := issue1926Capture(t, peer, fmt.Sprintf("Acceptance 1926 peer note %d: warehouse bin codes are three letters followed by two digits.", stamp))

	for form, limit := range map[string]any{"number": 100, "string": "100"} {
		t.Run("limit_as_"+form, func(t *testing.T) {
			admin := connect(t)
			for author, want := range map[string]string{devOwnerEmail: ownerID, devPeerEmailAddr: peerID} {
				ids, total := issue1926List(t, admin, map[string]any{"created_by": author, "limit": limit, "status": "active"})
				if !slices.Contains(ids, want) {
					t.Errorf("created_by=%s: ids %v lack the record %s that user wrote", author, ids, want)
				}
				if total < 1 {
					t.Errorf("created_by=%s: total = %v", author, total)
				}
			}
			// The unnarrowed list is every author's: walked by offset, it
			// holds both records.
			seen := map[string]bool{}
			offset := map[string]any{"number": 0, "string": "0"}[form]
			for page := 0; page < 200; page++ {
				ids, total := issue1926List(t, admin, map[string]any{"limit": limit, "offset": offset, "status": "active"})
				for _, id := range ids {
					seen[id] = true
				}
				next := page*100 + len(ids)
				if len(ids) == 0 || float64(next) >= total {
					break
				}
				offset = map[string]any{"number": next, "string": fmt.Sprint(next)}[form]
			}
			if !seen[ownerID] || !seen[peerID] {
				t.Errorf("the unnarrowed list holds owner=%v peer=%v; want both users' records", seen[ownerID], seen[peerID])
			}
		})
	}
}

// TestIssue1926_ANonAdminIsRefused: the route is behind the admin gate, so a
// caller without the admin persona is refused and handed no records.
func TestIssue1926_ANonAdminIsRefused(t *testing.T) {
	peer := connectAs(t, devPeerAPIKey)
	status, body := peer.rest(http.MethodGet, issue1926Route+"?limit=100", http.NoBody)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("a non-admin's GET: HTTP %d %v; want it refused", status, body)
	}
	if body["data"] != nil {
		t.Errorf("a refused call carried records: %v", body["data"])
	}
	// The same caller reaches its own records through the portal route, so
	// the refusal is the admin gate and not a missing identity.
	status, body = peer.rest(http.MethodGet, "/api/v1/portal/memory/records?limit=1", http.NoBody)
	if status != http.StatusOK || !strings.Contains(fmt.Sprint(body), "data") {
		t.Errorf("the portal route for the same caller: HTTP %d %v; want its own records", status, body)
	}
}
