//go:build integration

package acceptance

import (
	"net/http"
	"testing"
	"time"
)

// Acceptance for #2444: a deleted connection leaves every replica's
// mcp_platform_connections state count, and a connection saved again under its
// name starts with no state, on the replica that probed it as well as the one
// that served the delete. Two replicas of the dev stack over one database.
//
// Wire forms: the admin connection route takes a JSON object of config
// strings; the connection test route takes no body.

const reload2444 = 15 * time.Second

func unreachable2444(t *testing.T) []float64 {
	t.Helper()
	labels := map[string]string{"kind": "api", "state": "unreachable"}
	var out []float64
	for _, target := range metricsURLs() {
		out = append(out, metricSeries(scrapeOne(t, target), "mcp_platform_connections", labels))
	}
	return out
}

func save2444(t *testing.T, c *client, name, base string) {
	t.Helper()
	cfg := map[string]any{
		"base_url": base, "connection_name": name, "auth_mode": "none",
		"connect_timeout": "1s", "call_timeout": "2s", "trust_level": "untrusted",
	}
	if status, body := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name,
		jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance #2444"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d: %v", status, body)
	}
}

// waitBaseline waits until every replica's unreachable count is back to want.
func waitBaseline(t *testing.T, want []float64, what string) {
	t.Helper()
	deadline := time.Now().Add(reload2444)
	for {
		got := unreachable2444(t)
		same := len(got) == len(want)
		for i := range got {
			same = same && got[i] == want[i]
		}
		if same {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: replicas report unreachable %v, want %v within %s", what, got, want, reload2444)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestIssue2444_ADeletedUnreachableConnectionLeavesEveryReplicasStateCount(t *testing.T) {
	found := replicas(t)
	a, b := connectAt(t, found[0].base, devAPIKey()), connectAt(t, found[1].base, devAPIKey())
	if len(metricsURLs()) < 2 {
		t.Fatal("this criterion reads each replica's metrics; run the dev stack with DEV_REPLICAS=2")
	}
	baseline := unreachable2444(t)
	name := "acc-2444-" + unique1579()
	save2444(t, a, name, "http://127.0.0.1:1")
	t.Cleanup(func() { a.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })

	// The other replica learns of the connection over the reload bus, then
	// finds it unreachable.
	deadline := time.Now().Add(reload2444)
	for {
		status, out := b.rest(http.MethodPost, "/api/v1/admin/connection-instances/api/"+name+"/test", http.NoBody)
		if out["ok"] == false && out["kind"] == "api" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the connection test on the other replica answered %d: %v", status, out)
		}
		time.Sleep(250 * time.Millisecond)
	}
	sum := func(v []float64) (s float64) {
		for _, x := range v {
			s += x
		}
		return s
	}
	if after := unreachable2444(t); sum(after) != sum(baseline)+1 {
		t.Fatalf("after the probe replicas report unreachable %v, baseline %v", after, baseline)
	}

	if status, _ := a.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody); status != http.StatusNoContent {
		t.Fatalf("delete answered %d", status)
	}
	waitBaseline(t, baseline, "after the delete")

	// Saved again under the name, at an upstream that answers: no replica
	// reads it as the deleted connection's unreachable before a call uses it.
	configured := func() float64 {
		return issue1898Count(t, "mcp_platform_connections", map[string]string{"kind": "api", "state": "configured"})
	}
	before := configured()
	save2444(t, a, name, baseURL())
	for deadline := time.Now().Add(reload2444); configured() < before+float64(len(found)); {
		if time.Now().After(deadline) {
			t.Fatalf("the connection saved again was not listed by every replica within %s", reload2444)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if got := unreachable2444(t); sum(got) != sum(baseline) {
		t.Errorf("a connection saved again under a deleted one's name reads unreachable %v, baseline %v", got, baseline)
	}
}
