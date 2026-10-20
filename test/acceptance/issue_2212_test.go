//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2212: a long export runs in the background. graphql_export
// and api_export answer a model client's page walk, or a call asking with
// async true, at once with a run id and an idempotency key; the same tool
// called again with the key reads the run's progress and, once done, the
// export's own answer with the asset; a key whose export is still running is
// joined rather than walked twice; and the caller's next platform_info names
// the export that finished.
//
// Wire forms: async is a typed boolean (true and false), idempotency_key a
// typed string, paginate a typed object; each is sent as a literal tools/call
// parameter.

const issue2212Document = `query Page($after: Cursor) {
  allDatasets(first: 5, after: $after) {
    pageInfo { endCursor hasNextPage }
    nodes { code }
  }
}`

// issue2212Await reads the export again by key until it answers with an
// asset, and returns that answer.
func issue2212Await(t *testing.T, c *client, tool string, args map[string]any, key string) map[string]any {
	t.Helper()
	read := map[string]any{}
	for k, v := range args {
		read[k] = v
	}
	read["idempotency_key"] = key
	deadline := time.Now().Add(90 * time.Second)
	for {
		out := c.call(tool, read)
		if _, ok := out["asset_id"]; ok {
			return out
		}
		if out["status"] == "failed" {
			t.Fatalf("the background export failed: %v", out)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the background export under %s did not finish: %v", key, out)
		}
		time.Sleep(time.Second)
	}
}

// TestIssue2212_AGraphQLWalkRunsInTheBackgroundAndIsReadAgainByItsKey.
func TestIssue2212_AGraphQLWalkRunsInTheBackgroundAndIsReadAgainByItsKey(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "bg2212", nil)
	args := map[string]any{
		"connection": name, "query": issue2212Document, "name": "acc-2212-datasets.json",
		"paginate":      map[string]any{"items": "allDatasets.nodes", "cursor_variable": "after", "max_pages": 50},
		"on_truncation": "warn",
		"purpose":       "Acceptance #2212: a page walk exported in the background.",
	}
	started := c.call("graphql_export", args)
	key, _ := started["idempotency_key"].(string)
	if started["status"] != "running" || started["run_id"] == nil || key == "" {
		t.Fatalf("a model client's walk did not start in the background: %v", started)
	}
	if !strings.Contains(fmt.Sprint(started["message"]), "idempotency_key="+key) {
		t.Fatalf("the answer does not say how to read it again: %v", started["message"])
	}
	done := issue2212Await(t, c, "graphql_export", args, key)
	pagination, _ := done["pagination"].(map[string]any)
	if pagination == nil || number(t, pagination, "items_merged") < 250 {
		t.Fatalf("the finished export did not walk every page: %v", done)
	}

	// The caller's next briefing names the export that finished: the
	// platform_info a session opens with, which delivers it once.
	later := connect(t)
	exports, _ := later.info["background_exports"].([]any)
	found := false
	for _, raw := range exports {
		e, _ := raw.(map[string]any)
		if e["idempotency_key"] == key && e["tool"] == "graphql_export" {
			found = true
		}
	}
	if !found {
		t.Fatalf("platform_info does not name the finished background export %s: %v", key, exports)
	}
}

// TestIssue2212_AnAPIExportAskedForAsyncIsJoinedNotWalkedTwice: with async
// true and a key of the caller's, a second call under the key while the walk
// runs joins it, and async false answers inline.
func TestIssue2212_AnAPIExportAskedForAsyncIsJoinedNotWalkedTwice(t *testing.T) {
	c := connect(t)
	key := fmt.Sprintf("acc-2212-%d", time.Now().UnixNano())
	args := map[string]any{
		"connection": apiTestConnection, "method": "GET", "path": "/v1/pagination/cursor",
		"name": "acc-2212-cursor.json", "async": true, "idempotency_key": key,
		"paginate": map[string]any{"items": "items", "cursor_param": "cursor"},
		"purpose":  "Acceptance #2212: an api export asked to run in the background.",
	}
	started := c.call("api_export", args)
	if started["status"] != "running" || started["idempotency_key"] != key {
		t.Fatalf("async true did not start in the background: %v", started)
	}
	again := c.call("api_export", args)
	if again["status"] != "running" && again["asset_id"] == nil {
		t.Fatalf("a call under a key in flight: %v", again)
	}
	if again["status"] == "running" && again["run_id"] != started["run_id"] {
		t.Fatalf("a call under a key in flight started a second walk: %v vs %v", again["run_id"], started["run_id"])
	}
	done := issue2212Await(t, c, "api_export", args, key)
	if number(t, done, "items_merged") != 100 {
		t.Fatalf("the finished export: %v", done)
	}

	inline := map[string]any{}
	for k, v := range args {
		inline[k] = v
	}
	inline["async"], inline["idempotency_key"], inline["name"] = false, key+"-inline", "acc-2212-inline.json"
	out := c.call("api_export", inline)
	if out["asset_id"] == nil {
		t.Fatalf("async false answers inline with the asset: %v", out)
	}
}
