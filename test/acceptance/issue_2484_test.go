//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2484 (epic #2469): an uploaded or pasted spec names an
// upstream_url it is compared with and never replaced by. Refreshing it
// records a report of how the upstream differs, while the stored content and
// what api_discover returns stay as they were; a later check reports only what
// the upstream changed since the last copy read; the URL is fetched through
// the same guard as a URL-sourced spec.
//
// Wire forms: the spec is pasted (source_kind inline, content a JSON string);
// PUT .../watch takes upstream_url as a string, sent set and as "".

// TestIssue2484_AnUploadedSpecIsComparedAndNeverReplaced.
func TestIssue2484_AnUploadedSpecIsComparedAndNeverReplaced(t *testing.T) {
	admin := connect(t)
	up := newIssue2483Upstream(t, admin, "acc-2484-up", issue2483V2, `"v2"`)
	catalog := issue2483Catalog(t, admin, "acc-2484", map[string]any{"source_kind": "inline", "content": issue2483V1})
	issue2483AwaitOps(t, admin, catalog, []string{"deletePet"}, nil)

	if status, out := admin.rest(http.MethodPost, issue2483SpecPath(catalog)+"/refresh", http.NoBody); status != http.StatusBadRequest {
		t.Fatalf("a pasted spec with no upstream_url has nothing to check: %d %v", status, out)
	}
	watch := issue2483Watch(t, admin, catalog, map[string]any{"refresh_interval": "", "hold_changes": false, "upstream_url": up.url})
	if watch["upstream_url"] != up.url {
		t.Fatalf("the watch: %v", watch)
	}

	out := issue2483Refresh(t, admin, catalog)
	change, _ := out["change"].(map[string]any)
	if change == nil || change["status"] != "reported" {
		t.Fatalf("the upstream differs: want a reported change, got %v", out)
	}
	if !strings.Contains(fmt.Sprint(change["summary"]), "1 added, 1 removed, 1 changed, 1 deprecated") {
		t.Fatalf("the report: %v", change["summary"])
	}
	status, spec := admin.rest(http.MethodGet, issue2483SpecPath(catalog), http.NoBody)
	if status != http.StatusOK || spec["content"] != issue2483V1 {
		t.Fatalf("the stored content changed: %d %v", status, spec["content"])
	}
	issue2483AwaitOps(t, admin, catalog, []string{"deletePet"}, []string{"createPet"})

	if again := issue2483Refresh(t, admin, catalog); again["not_modified"] != true || again["change"] != nil {
		t.Fatalf("the same upstream again: %v", again)
	}
	up.publish(issue2483V3, `"v3"`)
	next, _ := issue2483Refresh(t, admin, catalog)["change"].(map[string]any)
	if next == nil || next["summary"] != "1 added, 0 removed, 0 changed, 0 deprecated; version 2.0 to 3.0" {
		t.Fatalf("a later check reports what changed since the last copy read: %v", next)
	}

	cleared := issue2483Watch(t, admin, catalog, map[string]any{"refresh_interval": "", "hold_changes": false, "upstream_url": ""})
	if cleared["upstream_url"] != "" {
		t.Fatalf("an empty upstream_url clears it: %v", cleared)
	}
}

// TestIssue2484_TheUpstreamIsFetchedThroughTheGuard: an upstream_url on a
// private address the operator did not list is refused, as a URL-sourced
// spec's would be, and a URL that is not http(s) is refused when set.
func TestIssue2484_TheUpstreamIsFetchedThroughTheGuard(t *testing.T) {
	admin := connect(t)
	catalog := issue2483Catalog(t, admin, "acc-2484-guard", map[string]any{"source_kind": "inline", "content": issue2483V1})
	if status, out := admin.rest(http.MethodPut, issue2483SpecPath(catalog)+"/watch",
		jsonBody(t, map[string]any{"refresh_interval": "", "hold_changes": false, "upstream_url": "file:///etc/passwd"})); status != http.StatusBadRequest {
		t.Fatalf("a file URL: %d %v", status, out)
	}
	issue2483Watch(t, admin, catalog, map[string]any{"refresh_interval": "", "hold_changes": false, "upstream_url": "http://169.254.169.254/latest/meta-data/"})
	status, out := admin.rest(http.MethodPost, issue2483SpecPath(catalog)+"/refresh", http.NoBody)
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out), "SSRF") {
		t.Fatalf("the metadata address: want 400 naming the guard, got %d %v", status, out)
	}
	if strings.Contains(fmt.Sprint(out), "169.254.169.254/latest") {
		t.Fatalf("the refusal repeats the URL: %v", out)
	}
}
