//go:build integration

package acceptance

import "testing"

// apiTestConnection is the api-test fixture `make dev` registers
// (dev/start.sh), the same image the demo deployments run.
const apiTestConnection = "api-test-fixture"

// walkAPITest walks one of the fixture's paginated operations through
// api_invoke_endpoint and returns the result.
func walkAPITest(c *client, path string, paginate map[string]any) map[string]any {
	c.t.Helper()
	return c.call("api_invoke_endpoint", map[string]any{
		"connection": apiTestConnection,
		"method":     "GET",
		"path":       path,
		"paginate":   paginate,
		"purpose":    "Acceptance: walk a paginated collection of the api-test fixture in one call.",
	})
}

// assertWholeCollection checks that a walk of the fixture's 100-item
// collection, served 10 to a page, fetched every page and merged every item.
func assertWholeCollection(t *testing.T, out map[string]any) {
	t.Helper()
	if got := number(t, out, "pages_fetched"); got != 10 {
		t.Errorf("pages_fetched = %v; want 10", got)
	}
	if got := number(t, out, "items_merged"); got != 100 {
		t.Errorf("items_merged = %v; want 100", got)
	}
	if got := out["stopped_by"]; got != "end" {
		t.Errorf("stopped_by = %v; want end", got)
	}
	body, _ := out["body"].([]any)
	if len(body) != 100 {
		t.Errorf("merged body holds %d items; want 100", len(body))
	}
}

// Issue #1543: a next link that differs from the connection only in scheme is
// followed. The fixture builds its Link and @odata.nextLink values from the
// request it sees, so behind a TLS-terminating proxy they are http:// on an
// https:// connection; the page is requested through the connection's
// base_url either way. On the dev stack the schemes agree, so what this pins
// is that the Link-header and OData walks complete against the real fixture;
// the scheme rule itself is pinned in internal/pagewalk.
func TestIssue1543_ALinkHeaderWalkFetchesEveryPage(t *testing.T) {
	c := connect(t)
	assertWholeCollection(t, walkAPITest(c, "/v1/pagination/link", map[string]any{"items": "items"}))
}

func TestIssue1543_AnODataWalkFetchesEveryPage(t *testing.T) {
	c := connect(t)
	assertWholeCollection(t, walkAPITest(c, "/v1/pagination/odata", map[string]any{"items": "value"}))
}

func TestIssue1543_ACursorWalkFetchesEveryPage(t *testing.T) {
	c := connect(t)
	assertWholeCollection(t, walkAPITest(c, "/v1/pagination/cursor", map[string]any{"items": "items", "cursor_param": "cursor"}))
}

// Without paginate the signal is reported and not followed, which is the
// contract the walk was added beside, not instead of.
func TestIssue1543_WithoutPaginateTheSignalIsReportedNotFollowed(t *testing.T) {
	c := connect(t)
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": apiTestConnection, "method": "GET", "path": "/v1/pagination/link",
		"purpose": "Acceptance: one page of a paginated collection with its signal reported.",
	})
	pagination, _ := out["pagination"].(map[string]any)
	if pagination["has_more"] != true || pagination["next_url"] == nil {
		t.Fatalf("pagination = %v; want has_more with a next_url", out["pagination"])
	}
	if _, walked := out["pages_fetched"]; walked {
		t.Fatalf("a call without paginate must not walk: %v", out)
	}
}
