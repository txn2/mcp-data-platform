//go:build integration

package acceptance

import (
	"os"
	"testing"
)

// apiTestFixtureURL is where the util connection reaches the api-test
// fixture from the platform's own host: the loopback port dev/docker-compose
// maps it to, by address rather than by name, because the util guard refuses
// the name "localhost" outright and dev/platform.yaml exempts only the
// loopback prefix.
func apiTestFixtureURL() string {
	if v := os.Getenv("ACCEPTANCE_API_TEST_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:9282"
}

// Issue #1544: a fetch_url walk follows the fetched document's Link header.
// The util handler relays it, so a header-paginated document walks exactly as
// it does through a proxied connection, and a single fetch reports the signal.
func TestIssue1544_AFetchURLWalkFollowsTheDocumentsLinkHeader(t *testing.T) {
	c := connect(t)
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": "util",
		"method":     "POST",
		"path":       "/util/fetch",
		"body":       map[string]any{"url": apiTestFixtureURL() + "/v1/pagination/link"},
		"paginate":   map[string]any{"items": "items"},
		"purpose":    "Acceptance: walk a Link-header paginated document through fetch_url.",
	})
	assertWholeCollection(t, out)
}

func TestIssue1544_ASingleFetchReportsTheLinkSignal(t *testing.T) {
	c := connect(t)
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": "util",
		"method":     "POST",
		"path":       "/util/fetch",
		"body":       map[string]any{"url": apiTestFixtureURL() + "/v1/pagination/link"},
		"purpose":    "Acceptance: one page of a Link-header paginated document through fetch_url.",
	})
	pagination, _ := out["pagination"].(map[string]any)
	if pagination["source"] != "link_header" || pagination["next_url"] == nil {
		t.Fatalf("pagination = %v; want the Link header reported as the signal", out["pagination"])
	}
}
