//go:build integration

package acceptance

import "testing"

// Issue #1548: a fetch_url walk whose body is sent as a string of JSON, the
// form a single fetch already accepts, walks the url it names. Before the fix
// the string fell through to the path address, pinned to the util connection's
// synthetic host, and the first next link was refused as another host.
func TestIssue1548_AFetchURLWalkAcceptsTheBodyAsAJSONString(t *testing.T) {
	c := connect(t)
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": "util",
		"method":     "POST",
		"path":       "/util/fetch",
		"body":       `{"url": "` + apiTestFixtureURL() + `/v1/pagination/link"}`,
		"paginate":   map[string]any{"items": "items"},
		"purpose":    "Acceptance: walk a Link-header paginated document through fetch_url with the body sent as a JSON string.",
	})
	assertWholeCollection(t, out)
}
