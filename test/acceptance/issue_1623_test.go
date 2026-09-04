//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1623: every asset read carried the asset's whole provenance, and a
// capture is appended on every content write with nothing bounding it. On a
// deployment where a scheduled script refreshes a dashboard hourly, the asset
// record became the largest thing the platform served and `manage_asset list`
// stopped fitting in what an MCP client accepts.
//
// Executed here through the surfaces a user calls: the MCP tool, the portal's
// own REST routes, and the admin twin.
//
// Wire forms. Every parameter this ticket touches is declared with a concrete
// type in manage_asset's schema, which the SDK validates against on a generic
// AddTool registration, so each admits exactly one JSON form: `action` and
// `asset_id` a string, `offset` and `limit` a number. TestIssue1623_WireForms
// sends the number form and the string form of `offset` and `limit` as literal
// params and pins that a string is refused rather than silently read as the
// default -- a silently-defaulted page would hand a caller the newest captures
// again and read as though there were no more.
//
// The REST twins take the same two as query parameters, where the wire form is
// always a string; both routes are exercised below.

const (
	// issue1623Cap is the version cap the pruned asset carries: five versions
	// kept, so the writes below push most of its history past the cap.
	issue1623Cap = 5
	// issue1623Writes is how many content writes each asset takes after its
	// first, so the unbounded one ends with more captures than a single read
	// carries.
	issue1623Writes = 25
	// issue1623InlineCaptures is how many captures a single asset read carries.
	issue1623InlineCaptures = 20
	// issue1623ListBudget is the size a listing of the caller's assets must
	// come in under. The measured listing on the reporting deployment was
	// 1,473,539 characters.
	issue1623ListBudget = 64 * 1024
	// issue1623GalleryBudget is the size the Assets gallery request must come
	// in under. It was 1,049,973 bytes.
	issue1623GalleryBudget = 200 * 1024
)

// issue1623Body is the asset's content: one line the patch below rewrites, so
// every write is an anchored edit through the funnel a real edit takes.
func issue1623Body(n int) string {
	return fmt.Sprintf("# Weather Watch\n\nReading: %d\n", n)
}

// issue1623Cite makes one API call and returns the `mcp:call:` reference it
// stamps on its own result.
//
// A write with no calls behind it records no capture at all -- the platform
// does not write an empty one -- so every write below cites a real call, which
// is what a refreshing script does through its own query.
func issue1623Cite(c *client) string {
	c.t.Helper()
	var (
		res  *mcp.CallToolResult
		text string
		err  error
	)
	// The same wait the shared call helper performs, repeated here because this
	// needs every content block and not only the first.
	for attempt := 0; attempt <= rateLimitRetries; attempt++ {
		res, text, err = c.callRaw("api_invoke_endpoint", map[string]any{
			"connection": apiTestConnection,
			"method":     "GET",
			"path":       "/v1/pagination/link",
			"purpose":    "Reading the fixture the acceptance asset is refreshed from.",
		})
		if err != nil {
			c.t.Fatalf("api_invoke_endpoint: transport error: %v", err)
		}
		if !res.IsError || !strings.Contains(text, "RATE_LIMITED") {
			break
		}
		time.Sleep(retryAfter(text))
	}
	if res.IsError {
		c.t.Fatalf("api_invoke_endpoint: tool error: %s", text)
	}
	// The reference rides in a content block of its own beside the upstream's
	// answer, which is where a caller reads it from (#1589).
	for _, block := range res.Content {
		tc, ok := block.(*mcp.TextContent)
		if !ok {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(tc.Text), &obj) != nil {
			continue
		}
		ref, ok := obj["call_reference"].(map[string]any)
		if !ok {
			continue
		}
		if id, _ := ref["call_id"].(string); id != "" {
			return id
		}
	}
	c.t.Fatalf("api_invoke_endpoint stamped no call reference: %v", res.Content)
	return ""
}

// issue1623Asset saves an asset, sets its version cap, and rewrites its content
// writes times, each write citing a call of its own. It returns the asset id.
func issue1623Asset(c *client, name string, maxVersions int, writes int) string {
	c.t.Helper()
	saved := c.call("save_asset", map[string]any{
		"name":         name,
		"content":      issue1623Body(0),
		"content_type": "text/markdown",
		"sources":      []any{issue1623Cite(c)},
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		c.t.Fatalf("save_asset returned no asset_id: %v", saved)
	}

	c.call("manage_asset", map[string]any{
		"action": "update", "asset_id": id, "max_versions": maxVersions,
	})

	for n := 1; n <= writes; n++ {
		c.call("manage_asset", map[string]any{
			"action": "patch", "asset_id": id,
			"edits": []any{map[string]any{
				"find": fmt.Sprintf("Reading: %d", n-1), "replace": fmt.Sprintf("Reading: %d", n),
			}},
			"sources":        []any{issue1623Cite(c)},
			"change_summary": fmt.Sprintf("refresh %d", n),
		})
	}
	return id
}

// issue1623Provenance is the provenance block of a single asset read.
type issue1623Provenance struct {
	Captures      []map[string]any `json:"captures"`
	CapturesTotal int              `json:"captures_total"`
}

func issue1623ReadProvenance(t *testing.T, row map[string]any) issue1623Provenance {
	t.Helper()
	raw, err := json.Marshal(row["provenance"])
	if err != nil {
		t.Fatalf("re-encoding provenance: %v", err)
	}
	var out issue1623Provenance
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding provenance: %v", err)
	}
	return out
}

// captureVersions reads the version each capture names, in the order given.
func issue1623CaptureVersions(captures []map[string]any) []int {
	out := make([]int, 0, len(captures))
	for _, c := range captures {
		v, _ := c["version"].(float64)
		out = append(out, int(v))
	}
	return out
}

// issue1623Raw calls manage_asset and returns the raw result, waiting out a
// rate-limit refusal the way the refusal itself instructs. A criterion about
// which wire forms a parameter admits has to see the platform's own verdict,
// so it cannot go through the helper that fails the test on a tool error.
func issue1623Raw(c *client, args map[string]any) (*mcp.CallToolResult, string, error) {
	c.t.Helper()
	var (
		res  *mcp.CallToolResult
		text string
		err  error
	)
	for attempt := 0; attempt <= rateLimitRetries; attempt++ {
		res, text, err = c.callRaw("manage_asset", args)
		if err != nil || !res.IsError || !strings.Contains(text, "RATE_LIMITED") {
			break
		}
		time.Sleep(retryAfter(text))
	}
	return res, text, err
}

// Criterion 1: a listing comes in under the budget and every row carries a
// provenance summary rather than the captures.
func TestIssue1623_ListingCarriesASummaryAndFits(t *testing.T) {
	c := connect(t)
	pruned := issue1623Asset(c, "issue-1623 pruned", issue1623Cap, issue1623Writes)

	res, text, err := issue1623Raw(c, map[string]any{
		"action": "list", "limit": 50,
	})
	if err != nil || res.IsError {
		t.Fatalf("manage_asset list: %v %s", err, text)
	}
	t.Logf("manage_asset list limit=50: %d characters (budget %d)", len(text), issue1623ListBudget)
	if len(text) >= issue1623ListBudget {
		t.Fatalf("manage_asset list is %d characters, over the %d budget", len(text), issue1623ListBudget)
	}
	if strings.Contains(text, `"provenance"`) {
		t.Fatalf("the listing carries provenance rather than a summary:\n%s", text)
	}

	var out struct {
		Assets []map[string]any `json:"assets"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("manage_asset list: %v", err)
	}
	if len(out.Assets) == 0 {
		t.Fatal("manage_asset list returned no assets")
	}

	var found bool
	for _, row := range out.Assets {
		if _, ok := row["provenance"]; ok {
			t.Fatalf("asset %v carries provenance in a listing row", row["id"])
		}
		summary, ok := row["provenance_summary"].(map[string]any)
		if !ok {
			t.Fatalf("asset %v carries no provenance_summary: %v", row["id"], row)
		}
		if row["id"] != pruned {
			continue
		}
		found = true
		// The summary's count is what the asset actually holds, which the
		// single read below reports independently.
		read := c.call("manage_asset", map[string]any{
			"action": "get", "asset_id": pruned,
		})
		prov := issue1623ReadProvenance(t, read)
		kept := len(prov.Captures)
		if prov.CapturesTotal > 0 {
			kept = prov.CapturesTotal
		}
		if got, _ := summary["captures"].(float64); int(got) != kept {
			t.Fatalf("provenance_summary.captures is %v, the asset holds %d", got, kept)
		}
		if _, ok := summary["last_tool"]; !ok {
			t.Fatalf("provenance_summary names no last_tool: %v", summary)
		}
	}
	if !found {
		t.Fatalf("asset %s is absent from its owner's listing", pruned)
	}
}

// Criterion 3: the Assets gallery request comes in under its budget, and
// carries the summary rather than the captures.
func TestIssue1623_GalleryRequestFits(t *testing.T) {
	c := connect(t)
	issue1623Asset(c, "issue-1623 gallery", issue1623Cap, issue1623Writes)

	status, body := c.rest("GET", "/api/v1/portal/assets?limit=50", nil)
	if status != 200 {
		t.Fatalf("GET /portal/assets: status %d", status)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-encoding the gallery response: %v", err)
	}
	t.Logf("GET /portal/assets?limit=50: %d bytes (budget %d)", len(raw), issue1623GalleryBudget)
	if len(raw) >= issue1623GalleryBudget {
		t.Fatalf("the gallery response is %d bytes, over the %d budget", len(raw), issue1623GalleryBudget)
	}
	rows, _ := body["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("the gallery returned no assets")
	}
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if _, ok := row["provenance"]; ok {
			t.Fatalf("gallery row %v carries provenance", row["id"])
		}
		if _, ok := row["provenance_summary"]; !ok {
			t.Fatalf("gallery row %v carries no provenance_summary", row["id"])
		}
	}
}

// Criterion 2: a get carries the newest twenty captures and the count of what
// the asset holds, and action=provenance pages the rest with no overlap and no
// gap. Read on an asset keeping every version, since that is where captures
// accumulate past the bound.
func TestIssue1623_GetIsBoundedAndTheRestArePaged(t *testing.T) {
	c := connect(t)
	id := issue1623Asset(c, "issue-1623 unbounded", 0, issue1623Writes)

	read := c.call("manage_asset", map[string]any{
		"action": "get", "asset_id": id,
	})
	prov := issue1623ReadProvenance(t, read)
	if len(prov.Captures) != issue1623InlineCaptures {
		t.Fatalf("get carries %d captures, expected %d", len(prov.Captures), issue1623InlineCaptures)
	}
	total := prov.CapturesTotal
	if total != issue1623Writes+1 {
		t.Fatalf("captures_total is %d, expected %d", total, issue1623Writes+1)
	}
	inlineVersions := issue1623CaptureVersions(prov.Captures)

	page := c.call("manage_asset", map[string]any{
		"action": "provenance", "asset_id": id, "offset": 20, "limit": 20,
	})
	if got, _ := page["total"].(float64); int(got) != total {
		t.Fatalf("the page reports %v captures, the get reported %d", got, total)
	}
	rows, _ := page["captures"].([]any)
	want := total - issue1623InlineCaptures
	if len(rows) != want {
		t.Fatalf("the page carries %d captures, expected the remaining %d", len(rows), want)
	}

	paged := make([]int, 0, len(rows))
	for _, r := range rows {
		row, _ := r.(map[string]any)
		v, _ := row["version"].(float64)
		paged = append(paged, int(v))
	}
	// Newest first, and picking up exactly where the read stopped: the newest
	// paged capture is one older than the oldest the read carried.
	for i := 1; i < len(paged); i++ {
		if paged[i] >= paged[i-1] {
			t.Fatalf("the page is not newest first: %v", paged)
		}
	}
	oldestInline := inlineVersions[0]
	if len(paged) > 0 && paged[0] != oldestInline-1 {
		t.Fatalf("the page starts at version %d, the read's oldest was %d: overlap or gap", paged[0], oldestInline-1)
	}
	for _, v := range paged {
		for _, inline := range inlineVersions {
			if v == inline {
				t.Fatalf("version %d appears in both the read and the page", v)
			}
		}
	}
}

// The same read through the portal's own route and the admin twin, which the
// asset viewer and the admin asset page call.
func TestIssue1623_RESTReadsAreBoundedAndPaged(t *testing.T) {
	c := connect(t)
	id := issue1623Asset(c, "issue-1623 rest", 0, issue1623Writes)

	for _, prefix := range []string{"/api/v1/portal/assets/", "/api/v1/admin/assets/"} {
		status, body := c.rest("GET", prefix+id, nil)
		if status != 200 {
			t.Fatalf("GET %s%s: status %d", prefix, id, status)
		}
		prov := issue1623ReadProvenance(t, body)
		if len(prov.Captures) != issue1623InlineCaptures {
			t.Fatalf("GET %s%s carries %d captures, expected %d", prefix, id, len(prov.Captures), issue1623InlineCaptures)
		}
		if prov.CapturesTotal != issue1623Writes+1 {
			t.Fatalf("GET %s%s reports captures_total %d, expected %d", prefix, id, prov.CapturesTotal, issue1623Writes+1)
		}

		status, page := c.rest("GET", prefix+id+"/provenance?offset=20&limit=20", nil)
		if status != 200 {
			t.Fatalf("GET %s%s/provenance: status %d", prefix, id, status)
		}
		rows, _ := page["captures"].([]any)
		if len(rows) != issue1623Writes+1-issue1623InlineCaptures {
			t.Fatalf("GET %s%s/provenance carries %d captures, expected %d",
				prefix, id, len(rows), issue1623Writes+1-issue1623InlineCaptures)
		}
		if got, _ := page["total"].(float64); int(got) != issue1623Writes+1 {
			t.Fatalf("GET %s%s/provenance reports total %v", prefix, id, got)
		}
	}
}

// Criterion 4: the captures follow the versions. After the writes push history
// past the cap, the captures for the pruned versions are gone, the capture for
// version 1 remains, and list_versions and captures_total agree on what is
// kept.
func TestIssue1623_CapturesFollowTheirVersions(t *testing.T) {
	c := connect(t)
	id := issue1623Asset(c, "issue-1623 pruning", issue1623Cap, issue1623Writes)

	read := c.call("manage_asset", map[string]any{
		"action": "get", "asset_id": id,
	})
	prov := issue1623ReadProvenance(t, read)
	kept := issue1623CaptureVersions(prov.Captures)
	if prov.CapturesTotal != 0 && prov.CapturesTotal != len(kept) {
		t.Fatalf("the pruned asset holds %d captures but the read carried %d", prov.CapturesTotal, len(kept))
	}

	versions := c.call("manage_asset", map[string]any{
		"action": "list_versions", "asset_id": id, "limit": 50,
	})
	rows, _ := versions["versions"].([]any)
	live := map[int]bool{}
	for _, r := range rows {
		row, _ := r.(map[string]any)
		v, _ := row["version"].(float64)
		live[int(v)] = true
	}
	if len(live) != issue1623Cap {
		t.Fatalf("the asset kept %d versions, expected the cap of %d", len(live), issue1623Cap)
	}

	var hasOrigin bool
	for _, v := range kept {
		if v == 1 {
			hasOrigin = true
			continue
		}
		if !live[v] {
			t.Fatalf("a capture for version %d survives its pruned version: kept %v, live %v", v, kept, live)
		}
	}
	if !hasOrigin {
		t.Fatalf("the capture for version 1 was pruned: %v", kept)
	}
	// Every kept capture but the origin names a live version, so what the
	// asset says about itself and what it still holds agree.
	if len(kept) != len(live)+1 {
		t.Fatalf("the asset holds %d captures for %d live versions plus the origin: %v", len(kept), len(live), kept)
	}
}

// Criterion 6: an asset with a single capture reads exactly as it did before
// the bound existed.
func TestIssue1623_ASingleCaptureReadsUnchanged(t *testing.T) {
	c := connect(t)
	id := issue1623Asset(c, "issue-1623 single", 0, 0)

	read := c.call("manage_asset", map[string]any{
		"action": "get", "asset_id": id,
	})
	prov := issue1623ReadProvenance(t, read)
	if len(prov.Captures) != 1 {
		t.Fatalf("a single-capture asset carries %d captures", len(prov.Captures))
	}
	if prov.CapturesTotal != 0 {
		t.Fatalf("nothing was cut, so captures_total should be absent, got %d", prov.CapturesTotal)
	}
}

// The wire forms `offset` and `limit` admit. Both are declared `type: integer`,
// so the number form is what the page reads; the string form is refused rather
// than silently read as the default, which would hand the caller the newest
// captures again under an offset that says otherwise.
func TestIssue1623_WireForms(t *testing.T) {
	c := connect(t)
	id := issue1623Asset(c, "issue-1623 wire forms", 0, issue1623Writes)

	res, text, err := issue1623Raw(c, map[string]any{
		"action": "provenance", "asset_id": id, "offset": 20, "limit": 20,
	})
	if err != nil || res.IsError {
		t.Fatalf("the number form was refused: %v %s", err, text)
	}
	var numberForm map[string]any
	if err := json.Unmarshal([]byte(text), &numberForm); err != nil {
		t.Fatalf("decoding the number form's page: %v", err)
	}

	res, text, err = issue1623Raw(c, map[string]any{
		"action": "provenance", "asset_id": id, "offset": "20", "limit": "20",
	})
	if err == nil && !res.IsError {
		var stringForm map[string]any
		if uErr := json.Unmarshal([]byte(text), &stringForm); uErr != nil {
			t.Fatalf("decoding the string form's page: %v", uErr)
		}
		if fmt.Sprint(stringForm["offset"]) != fmt.Sprint(numberForm["offset"]) ||
			fmt.Sprint(stringForm["limit"]) != fmt.Sprint(numberForm["limit"]) {
			t.Fatalf("the string form was accepted and read differently: %v vs %v", stringForm, numberForm)
		}
		return
	}
	t.Logf("the string form of offset/limit was refused: %s", text)
	// Refused is the correct answer for a typed parameter; what must not happen
	// is a silent fall back to the default page.
	if !strings.Contains(strings.ToLower(text), "offset") && !strings.Contains(strings.ToLower(text), "limit") &&
		!strings.Contains(strings.ToLower(text), "invalid") && err == nil {
		t.Fatalf("the string form was refused without saying why: %s", text)
	}
}
