//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1585: search reported its per-source candidate fetch cap as if it were
// a match count. Every provider was asked for at most 25 candidates and the
// coverage summary published that 25 as `matched`, so a source that matched
// thousands and a source that matched exactly 25 were the same response. The
// `limit` parameter documented a display budget of up to 50 that a single-source
// search could never reach, because the fetch depth was 25 whatever the caller
// asked for.
//
// What these hold: a source with more matching candidates than the fetch depth
// says so on the wire; the tool description states what bounds a single-source
// result; a single-source search with limit above 25 really returns more than
// 25; browse mode's total is untouched; and a source whose matches fit inside
// the depth reports an exact count carrying no flag.
//
// Wire forms: every parameter these calls touch is typed in the search tool's
// schema, so each admits exactly one JSON form and is sent in it. `limit` and
// `offset` are `{"type":"integer"}` and are sent as JSON numbers; `sources` is
// `{"type":"array","items":{"type":"string"}}` and is sent as a JSON array of
// strings; `intent` and `purpose` are `{"type":"string"}` and are sent as JSON
// strings. The handler is registered through the typed mcp.AddTool form, which
// validates arguments against that schema, so no other form is admitted (a
// string "50" for limit is rejected before the handler runs) and there is no
// second form to send.

// assetsSeed1585 is how many assets the fixture files. It is one more than the
// per-source candidate fetch depth (25), which is the smallest set that proves
// the difference between "matched 25" and "matched at least 25": at limit 10
// the depth binds and the count is capped, at limit 50 the depth is the display
// budget and the same set is counted exactly.
const assetsSeed1585 = 26

// intent1585 is the fixture's query text. Assets rank hybrid on the dev stack
// (an embedder is configured), so the vector arm returns the caller's top-k
// whatever the words are; the token is here to make the fixture's own rows the
// most relevant ones rather than to be the only thing that matches.
const intent1585 = "acceptance issue 1585 capped coverage fixture"

// seedAssets1585 files assetsSeed1585 assets owned by the caller and removes
// them when the test ends, so the assets source has more candidates than the
// fetch depth.
func seedAssets1585(t *testing.T, c *client) {
	t.Helper()
	for i := 0; i < assetsSeed1585; i++ {
		name := "Acceptance 1585 fixture " + string(rune('a'+i%26)) + "-" + itoa1585(i)
		out := c.call("save_asset", map[string]any{
			"name":         name,
			"content_type": "text/markdown",
			"content":      "# " + name + "\n\n" + intent1585 + "\n",
		})
		id, _ := out["asset_id"].(string)
		if id == "" {
			t.Fatalf("save_asset returned no asset: %v", out)
		}
		t.Cleanup(func() {
			_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
		})
	}
}

// itoa1585 renders a small non-negative int without pulling strconv into the
// fixture's name construction.
func itoa1585(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// connectionsIntent1585 is a query the connections source can match. Connections
// carry no embeddings and are ranked by substring overlap over name, kind, and
// description, so the query has to share a token with one; the word is in the
// built-in platform-admin connection's own description, which every deployment
// carries. The source is chosen for this criterion because it is genuinely
// filtered rather than ranked top-k: a deployment has far fewer connections than
// the fetch depth, so its count can only be the exact one.
const connectionsIntent1585 = "connection"

// searchOne1585 runs one single-source relevance search and returns the decoded
// response.
func searchOne1585(c *client, source, intent string, limit int) map[string]any {
	return c.call("search", map[string]any{
		"intent":  intent,
		"sources": []string{source},
		"limit":   limit,
		"purpose": "Acceptance #1585: read the per-source coverage summary.",
	})
}

// coverageFor1585 returns the coverage entry for one source, failing when the
// response carries none: a source that returned candidates must be reported.
func coverageFor1585(t *testing.T, out map[string]any, source string) map[string]any {
	t.Helper()
	list, _ := out["coverage"].([]any)
	for _, raw := range list {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if entry["source"] == source {
			return entry
		}
	}
	t.Fatalf("no coverage entry for %q in %v", source, out)
	return nil
}

// TestIssue1585_ACappedSourceSaysSoOnTheWire is criterion 1: a search whose
// source has more matching candidates than the per-source fetch limit is
// distinguishable in the response from one that matched exactly the limit.
func TestIssue1585_ACappedSourceSaysSoOnTheWire(t *testing.T) {
	c := connect(t)
	seedAssets1585(t, c)

	out := searchOne1585(c, "assets", intent1585, 10)
	cov := coverageFor1585(t, out, "assets")

	matched, _ := cov["matched"].(float64)
	if int(matched) != 25 {
		t.Fatalf("assets matched %v, want the fetch depth 25 with more behind it: %v", cov["matched"], cov)
	}
	capped, _ := cov["matched_capped"].(bool)
	if !capped {
		t.Fatalf("assets reported matched=25 with no matched_capped flag, which reads as an exact count: %v", cov)
	}
}

// TestIssue1585_TheToolDescriptionStatesTheBound is criterion 2: the search
// tool description states what bounds a single-source result, so an agent
// reading only the description does not read `matched` as a corpus count.
func TestIssue1585_TheToolDescriptionStatesTheBound(t *testing.T) {
	c := connect(t)

	res, err := c.session.ListTools(c.ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var tool *mcp.Tool
	for _, candidate := range res.Tools {
		if candidate.Name == "search" {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatal("tools/list carries no search tool")
	}
	if !strings.Contains(tool.Description, "matched_capped") {
		t.Fatalf("the search description never names matched_capped, so an agent reading it takes matched for a corpus count:\n%s", tool.Description)
	}

	// InputSchema arrives as an any: the SDK carries whatever the server
	// declared, so it is re-marshaled before being read as the schema it is.
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("search input schema does not marshal: %v", err)
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("search input schema is not JSON: %v", err)
	}
	limitDesc := schema.Properties["limit"].Description
	if !strings.Contains(limitDesc, "source") {
		t.Fatalf("the limit description says nothing about what a single source can return:\n%s", limitDesc)
	}
}

// TestIssue1585_ASingleSourceHonoursLimitAboveTheFetchDepth is criterion 3: a
// single-source search with limit above the old per-source fetch cap returns
// more than that cap.
func TestIssue1585_ASingleSourceHonoursLimitAboveTheFetchDepth(t *testing.T) {
	c := connect(t)
	seedAssets1585(t, c)

	out := searchOne1585(c, "assets", intent1585, 50)
	count := int(number(t, out, "count"))
	if count <= 25 {
		t.Fatalf("limit=50 against one source displayed %d results, still bounded by the old fetch cap: %v", count, out["coverage"])
	}
	if count > 50 {
		t.Fatalf("limit=50 displayed %d results, above the budget the caller asked for", count)
	}
}

// TestIssue1585_BrowseTotalIsUnchanged is criterion 4: browse mode's total
// still reports the true count and carries no capped flag, since it is the way
// to enumerate a source.
func TestIssue1585_BrowseTotalIsUnchanged(t *testing.T) {
	c := connect(t)

	const page = 5
	first := browse1585(c, 0, page)
	total := int(number(t, first, "total"))
	if total <= page {
		t.Fatalf("browse reported total %d, at or below one page, so a capped total would be indistinguishable from a true one here; the dev stack should carry more knowledge pages than that: %v", total, first)
	}

	// The claim is that `total` is the source's real member count, so the way to
	// hold it is to walk the source by that count and land exactly on it. A
	// number that were really a fetch bound would run out early or keep going.
	seen := 0
	for offset := 0; offset < total; offset += page {
		out := first
		if offset > 0 {
			out = browse1585(c, offset, page)
		}
		if got := int(number(t, out, "total")); got != total {
			t.Fatalf("browse total changed mid-walk: %d at offset %d, %d at offset 0", got, offset, total)
		}
		count := int(number(t, out, "count"))
		if count > page {
			t.Fatalf("browse returned %d items for a page size of %d", count, page)
		}
		seen += count
	}
	if seen != total {
		t.Fatalf("walking the source by its own total yielded %d items, not the %d it reported", seen, total)
	}
}

// browse1585 reads one page of a browsed source.
func browse1585(c *client, offset, limit int) map[string]any {
	return c.call("search", map[string]any{
		"sources": []string{"knowledge_pages"},
		"offset":  offset,
		"limit":   limit,
		"purpose": "Acceptance #1585: enumerate a source and read its true total.",
	})
}

// TestIssue1585_AnUncappedSourceReportsAnExactCount is criterion 5: a source
// whose true match count is below the fetch depth reports it exactly, with no
// capped flag. Connections are that source on any deployment: the fixture's
// stack carries fewer connections than the depth, so the count can only be the
// real one.
func TestIssue1585_AnUncappedSourceReportsAnExactCount(t *testing.T) {
	c := connect(t)

	out := searchOne1585(c, "connections", connectionsIntent1585, 10)
	cov := coverageFor1585(t, out, "connections")

	matched, _ := cov["matched"].(float64)
	if int(matched) >= 25 {
		t.Fatalf("connections matched %v, at or above the fetch depth; this criterion needs a source that fits inside it: %v", cov["matched"], cov)
	}
	if _, present := cov["matched_capped"]; present {
		t.Fatalf("a source that fits inside the fetch depth carries a capped flag: %v", cov)
	}
}
