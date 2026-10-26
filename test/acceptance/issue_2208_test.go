//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Acceptance for #2208: an administrator reads every endpoint binding on the
// deployment with its automation, owner and traffic from one route, a public
// endpoint is told apart from a signed-in one, the requests an endpoint
// answered and the ones it refused at the bot check are counted by status
// class and by reason, the endpoint's page carries them, and a non-administrator
// is refused as every admin route refuses one.
//
// Wire forms: the routes take range as a query parameter (day, week); the
// endpoints are sent requests with and without a credential and a public
// one without a bot check answer.

// issue2208Endpoint finds name among the list route's endpoints.
func issue2208Endpoint(list map[string]any, name string) map[string]any {
	rows, _ := list["endpoints"].([]any)
	for _, raw := range rows {
		if e, _ := raw.(map[string]any); e["name"] == name {
			return e
		}
	}
	return nil
}

// issue2208AllPages reads every page of the list route with the query given,
// per_page at a time, and returns the endpoints of all of them as one list
// and the total the first page reported. The list is paged on the server
// (#2251), so a deployment past one page is read to its end.
func issue2208AllPages(t *testing.T, c *client, query string, perPage int) (all map[string]any, total int) {
	t.Helper()
	var rows []any
	for page := 1; ; page++ {
		path := fmt.Sprintf("/api/v1/admin/endpoints?%s&per_page=%d&page=%d", query, perPage, page)
		status, list := c.rest(http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s: %d %v", path, status, list)
		}
		if page == 1 {
			total = int(number(t, list, "total"))
		}
		got, _ := list["endpoints"].([]any)
		rows = append(rows, got...)
		if len(got) < perPage || len(rows) >= total {
			break
		}
	}
	return map[string]any{"endpoints": rows}, total
}

// TestIssue2208_TheListAndThePageCountAnsweredAndRefusedRequests.
func TestIssue2208_TheListAndThePageCountAnsweredAndRefusedRequests(t *testing.T) {
	admin := connect(t)
	public, signed := endpointName("acc-2208-public"), endpointName("acc-2208-signed")
	saveEndpointScript(t, admin, public, issue2171EchoHandler, `{"params": {}, "caller": None}`)
	saveEndpointScript(t, admin, signed, issue2171EchoHandler, `{"params": {}, "caller": None}`)
	check := putBotCheck(t, admin, issue2173Quick)
	bindEndpoint(t, admin, public, "", map[string]any{"endpoint": public, "auth_mode": "bot_check", "bot_check": check, "methods": []any{"POST"}})
	bindEndpoint(t, admin, signed, "", map[string]any{"endpoint": signed, "auth_mode": "authenticated", "callers": []string{"*"}})

	for range 3 { // refused at the bot check
		if got := endpointRequest(t, admin.base, http.MethodPost, "/endpoints/"+public, nil, "{}"); got.status != http.StatusForbidden {
			t.Fatalf("POST without a bot check answer: %d %s", got.status, got.body)
		}
	}
	for range 2 {
		if got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+signed, bearer(admin.apiKey), ""); got.status != http.StatusOK {
			t.Fatalf("GET signed in: %d %s", got.status, got.body)
		}
	}
	if got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+signed, nil, ""); got.status != http.StatusUnauthorized {
		t.Fatalf("GET with no credential: %d", got.status)
	}

	// Refusals are added every few seconds by the replica that counted them.
	var pub, sig map[string]any
	deadline := time.Now().Add(45 * time.Second)
	for {
		list, _ := issue2208AllPages(t, admin, "range=day", 200)
		pub, sig = issue2208Endpoint(list, public), issue2208Endpoint(list, signed)
		if pub != nil && sig != nil && number(t, pub, "refused") >= 3 && number(t, sig, "refused") >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the list never counted the refusals: public %v signed %v", pub, sig)
		}
		time.Sleep(2 * time.Second)
	}
	if pub["auth_mode"] != "bot_check" || pub["bot_check"] != check || pub["bot_check_provider"] != "pow" || sig["auth_mode"] != "authenticated" {
		t.Fatalf("access: %v / %v", pub["auth_mode"], sig["auth_mode"])
	}
	if script, _ := pub["script"].(map[string]any); script["name"] != public || script["owner"] == "" {
		t.Fatalf("the endpoint's automation and owner: %v", pub["script"])
	}
	reasons, _ := pub["refused_by_reason"].(map[string]any)
	if fmt.Sprint(reasons["bot_check"]) != "3" {
		t.Fatalf("refused_by_reason: %v", reasons)
	}
	traffic, _ := sig["traffic"].(map[string]any)
	classes, _ := traffic["by_status_class"].(map[string]any)
	if number(t, traffic, "requests") != 2 || fmt.Sprint(classes["2xx"]) != "2" {
		t.Fatalf("the signed-in endpoint's traffic: %v", traffic)
	}

	status, page := admin.rest(http.MethodGet, "/api/v1/admin/endpoints/"+public+"?range=week", nil)
	if status != http.StatusOK || number(t, page, "bucket_seconds") != 6*3600 {
		t.Fatalf("the endpoint's page: %d %v", status, page)
	}
	series, _ := page["series"].([]any)
	var refused float64
	for _, raw := range series {
		b, _ := raw.(map[string]any)
		refused += number(t, b, "refused")
	}
	if refused != 3 {
		t.Fatalf("the page's chart carries %v refusals, want 3", refused)
	}
}

// TestIssue2208_ANonAdministratorIsRefused: the route refuses an analyst as
// every admin route does, with the status the admin authentication answers a
// key that is not an administrator's (401), read here off an existing admin
// route so the two cannot disagree.
func TestIssue2208_ANonAdministratorIsRefused(t *testing.T) {
	analyst := connectAs(t, analystKey1892)
	want, _ := analyst.rest(http.MethodGet, botChecksPath, nil)
	if want != http.StatusUnauthorized && want != http.StatusForbidden {
		t.Fatalf("an existing admin route answered an analyst %d", want)
	}
	if status, body := analyst.rest(http.MethodGet, "/api/v1/admin/endpoints", nil); status != want {
		t.Fatalf("an analyst reading /api/v1/admin/endpoints: HTTP %d %v, want %d as every admin route answers", status, body, want)
	}
}
