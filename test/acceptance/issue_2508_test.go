//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Issue #2508: tag management and near-duplicate hints at save. save_asset
// returns existing_tags (tags on assets the caller can open that are a case or
// spelling variant of one given) and similar_assets (assets the caller can
// open whose embedding is very close to the new one), and is never refused for
// either. Admin > Tags lists every tag with its count and renames and merges
// them, recorded in the audit log. Nothing the caller cannot open is returned
// or counted.
//
// Wire forms: save_asset's name, description, content and content_type as
// strings and tags as a JSON array of strings; GET /api/v1/portal/tags's prefix
// and GET /api/v1/admin/tags's q as query strings; POST
// /api/v1/admin/tags/merge's from as a JSON array of strings and into as a
// string; POST /api/v1/admin/tags/rename's from and to as strings.

// embedWait2508 bounds the wait for a saved asset to be embedded: the nightly
// runner embeds on CPU behind whatever the index queue holds (#2516).
const embedWait2508 = 4 * time.Minute

type save2508 struct {
	name, description string
	tags              []string
	content           string
}

func (s save2508) args() map[string]any {
	tags := make([]any, len(s.tags))
	for i, t := range s.tags {
		tags[i] = t
	}
	content := s.content
	if content == "" {
		content = "# " + s.name
	}
	return map[string]any{
		"name": s.name, "description": s.description, "content_type": "text/markdown",
		"content": content, "tags": tags,
	}
}

// saveTracked saves an asset and deletes it when the test ends, returning its
// id and the save's whole answer.
func saveTracked2508(t *testing.T, c *client, s save2508) (string, map[string]any) {
	t.Helper()
	out := c.call("save_asset", s.args())
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("saving %s: %v", s.name, out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id, out
}

// similarIDs is the ids a save reported in similar_assets.
func similarIDs2508(out map[string]any) []string {
	list, _ := out["similar_assets"].([]any)
	ids := make([]string, 0, len(list))
	for _, m := range list {
		if row, ok := m.(map[string]any); ok {
			id, _ := row["asset_id"].(string)
			ids = append(ids, id)
		}
	}
	return ids
}

// awaitSimilar saves a near-duplicate of target as c until the save reports
// target in similar_assets (the platform embeds in the background), deleting
// each attempt that does not. It returns false at the deadline.
func awaitSimilar2508(t *testing.T, c *client, dup save2508, target string) bool {
	t.Helper()
	deadline := time.Now().Add(embedWait2508)
	for {
		out := c.call("save_asset", dup.args())
		id, _ := out["asset_id"].(string)
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
		if contains2507(similarIDs2508(out), target) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Second)
	}
}

func TestIssue2508_ASaveWithACaseVariantReturnsTheExistingTag(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	stamp := time.Now().UnixNano()
	existing := fmt.Sprintf("acc2508finance%d", stamp)
	given := fmt.Sprintf("Acc2508Finance%d", stamp)
	saveTracked2508(t, owner, save2508{name: fmt.Sprintf("acc2508 first %d", stamp), tags: []string{existing}})
	_, out := saveTracked2508(t, owner, save2508{name: fmt.Sprintf("acc2508 second %d", stamp), tags: []string{given}})
	raw, _ := json.Marshal(out["existing_tags"])
	var variants []struct {
		Tag     string `json:"tag"`
		Count   int    `json:"asset_count"`
		CloseTo string `json:"close_to"`
	}
	_ = json.Unmarshal(raw, &variants)
	found := false
	for _, v := range variants {
		if v.Tag == existing && v.CloseTo == given && v.Count >= 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("a save tagged %s returns %s in existing_tags: %s", given, existing, raw)
	}
}

func TestIssue2508_ANearDuplicateTheCallerCanOpenIsReportedAndOneTheyCannotIsNot(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	stamp := time.Now().UnixNano()

	mine := save2508{
		name:        fmt.Sprintf("Coastal orchard rainfall forecast %d", stamp),
		description: "Weekly rainfall forecast for the coastal orchards, by grove and irrigation zone.",
		tags:        []string{fmt.Sprintf("acc2508orchard%d", stamp)},
	}
	mineID, _ := saveTracked2508(t, owner, mine)
	dup := mine
	dup.content = "# " + mine.name + "\n\nRevised."
	if !awaitSimilar2508(t, owner, dup, mineID) {
		t.Fatalf("a save almost the same as an asset the caller owns returns it in similar_assets within %s", embedWait2508)
	}

	theirs := save2508{
		name:        fmt.Sprintf("Harbor freight tonnage ledger %d", stamp),
		description: "Monthly freight tonnage through the harbor, by berth and carrier.",
		tags:        []string{fmt.Sprintf("acc2508harbor%d", stamp)},
	}
	theirsID, _ := saveTracked2508(t, peer, theirs)
	// The peer's own near-duplicate finds it once it is embedded, so the
	// owner's save below is compared against a vector that exists.
	if !awaitSimilar2508(t, peer, theirs, theirsID) {
		t.Fatalf("the peer's asset was not embedded within %s", embedWait2508)
	}
	_, out := saveTracked2508(t, owner, theirs)
	raw, _ := json.Marshal(out)
	if contains2507(similarIDs2508(out), theirsID) || strings.Contains(string(raw), devPeerEmailAddr) {
		t.Errorf("an asset the caller cannot open is never in similar_assets: %s", raw)
	}
}

func TestIssue2508_MergingATagMovesEveryAssetAndIsAudited(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	stamp := time.Now().UnixNano()
	fin := fmt.Sprintf("acc2508fin%d", stamp)
	finance := fmt.Sprintf("acc2508finance%d", stamp)
	for i := range 3 {
		saveTracked2508(t, owner, save2508{name: fmt.Sprintf("acc2508 fin %d %d", stamp, i), tags: []string{fin}})
	}
	for i := range 2 {
		saveTracked2508(t, owner, save2508{name: fmt.Sprintf("acc2508 finance %d %d", stamp, i), tags: []string{finance}})
	}
	counts := func() map[string]int {
		status, out := admin.rest(http.MethodGet, "/api/v1/admin/tags?q="+"acc2508fin"+"&limit=500", http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("listing the tags: %d %v", status, out)
		}
		raw, _ := json.Marshal(out["tags"])
		var rows []struct {
			Tag   string `json:"tag"`
			Count int    `json:"count"`
		}
		_ = json.Unmarshal(raw, &rows)
		got := map[string]int{}
		for _, r := range rows {
			got[r.Tag] = r.Count
		}
		return got
	}
	before := counts()
	if before[fin] != 3 || before[finance] != 2 {
		t.Fatalf("the admin list counts each tag's assets: fin=%d finance=%d", before[fin], before[finance])
	}
	if status, _ := owner.rest(http.MethodPost, "/api/v1/admin/tags/merge", jsonBody(t, map[string]any{
		"from": []any{fin}, "into": finance,
	})); status != http.StatusForbidden && status != http.StatusUnauthorized {
		t.Errorf("a person who is not an administrator cannot merge tags: %d", status)
	}
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/tags/merge", jsonBody(t, map[string]any{
		"from": []any{fin}, "into": finance,
	}))
	if status != http.StatusOK {
		t.Fatalf("merging %s into %s: %d %v", fin, finance, status, out)
	}
	moved, _ := out["moved"].(float64)
	if int(moved) != 3 {
		t.Errorf("the merge moves the three assets carrying %s: %v", fin, out)
	}
	after := counts()
	if _, ok := after[fin]; ok {
		t.Errorf("no asset carries %s after the merge: %v", fin, after)
	}
	if after[finance] != before[finance]+int(moved) {
		t.Errorf("%s rises by the number moved: %d -> %d, moved %d", finance, before[finance], after[finance], int(moved))
	}
	if !issue2508Audited(t, admin, finance) {
		t.Errorf("the merge is recorded in the audit log")
	}
}

func TestIssue2508_TagSuggestionsCoverOnlyWhatTheCallerCanOpen(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	stamp := time.Now().UnixNano()
	prefix := fmt.Sprintf("acc2508sug%d", stamp)
	saveTracked2508(t, owner, save2508{name: "acc2508 suggest mine", tags: []string{prefix + "mine"}})
	saveTracked2508(t, peer, save2508{name: "acc2508 suggest theirs", tags: []string{prefix + "secret"}})
	status, out := owner.rest(http.MethodGet, "/api/v1/portal/tags?prefix="+url.QueryEscape(strings.ToUpper(prefix)), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("listing tags by prefix: %d %v", status, out)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), prefix+"mine") {
		t.Errorf("the caller's own tag is suggested, matched without case: %s", raw)
	}
	if strings.Contains(string(raw), prefix+"secret") {
		t.Errorf("a tag only on an asset the caller cannot open is never suggested: %s", raw)
	}
}

// issue2508Audited polls the audit log for the merge into target: the write
// is asynchronous.
func issue2508Audited(t *testing.T, admin *client, target string) bool {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, body := admin.rest(http.MethodGet, "/api/v1/admin/audit/events?tool_name=admin_tags_merge&per_page=50", http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("GET the audit events: status %d", status)
		}
		if raw, _ := json.Marshal(body["data"]); strings.Contains(string(raw), target) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
}
