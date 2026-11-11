//go:build integration

package acceptance

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// Acceptance for #2590: a person changes one of a site's files in the portal,
// and the save is an ordinary release of dev attributed to them, derived from
// the release they read. Two people editing one file cannot overwrite each
// other: the second save is refused with the conflict and nothing is written.
// Someone who does not manage the site reads the files and the API refuses
// their writes.
//
// Wire forms: GET /api/v1/portal/sites/{site}/files takes path, offset and
// limit in the query; GET .../file takes path in the query; PUT .../file takes
// a JSON object whose path and note are strings, content a string of utf-8
// text or, with encoding "base64", of base64, base an integer and dry_run a
// boolean (left out, and true); DELETE .../file takes path, base and note in
// the query; GET .../file/diff takes path, from and to in the query.
// manage_site takes command, site and note as strings and files as a list of
// objects whose path and content are utf-8 text.

const (
	pricing2590      = "pricing/index.html"
	pricingBody2590  = "<h1>Plans</h1>\n<p class=\"price\">Starter $12 per user, per month</p>\n"
	marker2590       = "Starter $12"
	changedPrice2590 = "Starter $15"
)

// files2590 is a site of the holder of keyOwner2604 whose dev serves release 1.
func files2590(t *testing.T, prefix string) *site2604 {
	t.Helper()
	s := newSite2604(t, keyOwner2604, prefix)
	connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "v1", "files": []map[string]any{
		{"path": "index.html", "content": "<h1>Home</h1>\n"},
		{"path": pricing2590, "content": pricingBody2590},
		{"path": "old.html", "content": "<h1>Old</h1>\n"},
	}})
	return s
}

// read2590 is one file's text and the release it was read from.
func read2590(t *testing.T, s *site2604, key, path string) (body string, release int) {
	t.Helper()
	status, got := s.rest(t, key, http.MethodGet, "/file?path="+url.QueryEscape(path), nil)
	want2559(t, "reading "+path, status, http.StatusOK, got)
	body, _ = got["body"].(string)
	n, _ := got["release"].(float64)
	return body, int(n)
}

func save2590(t *testing.T, s *site2604, key string, body map[string]any) (int, map[string]any) {
	t.Helper()
	return s.rest(t, key, http.MethodPut, "/file", body)
}

func devRelease2590(t *testing.T, s *site2604) int {
	t.Helper()
	status, got := s.rest(t, keyOwner2604, http.MethodGet, "", nil)
	want2559(t, "reading the site", status, http.StatusOK, got)
	envs, _ := got["environments"].([]any)
	for _, raw := range envs {
		if e, _ := raw.(map[string]any); e["name"] == "dev" {
			n, _ := e["release"].(float64)
			return int(n)
		}
	}
	t.Fatalf("no dev in %v", got["_raw"])
	return 0
}

// TestIssue2590_AManagerChangesALineAndDevGetsANewReleaseAttributedToThem is
// done-when 1.
func TestIssue2590_AManagerChangesALineAndDevGetsANewReleaseAttributedToThem(t *testing.T) {
	s := files2590(t, "acc2590a-")
	s.share(t, keyOwner2604, map[string]any{"email": emailPeer2604, "level": "manage"})

	status, listing := s.rest(t, keyPeer2604, http.MethodGet, "/files?path=pricing%2F&offset=0&limit=50", nil)
	want2559(t, "listing pricing/", status, http.StatusOK, listing)
	if !strings.Contains(listing["_raw"].(string), pricing2590) {
		t.Fatalf("the listing does not hold %s: %v", pricing2590, listing["_raw"])
	}

	body, release := read2590(t, s, keyPeer2604, pricing2590)
	if release != 1 || !strings.Contains(body, marker2590) {
		t.Fatalf("read release %d: %q", release, body)
	}
	edited := strings.Replace(body, marker2590, changedPrice2590, 1)

	status, dry := save2590(t, s, keyPeer2604, map[string]any{"path": pricing2590, "content": edited, "base": release, "dry_run": true})
	want2559(t, "a dry run", status, http.StatusOK, dry)
	if dry["outputs_changed"] != float64(1) || devRelease2590(t, s) != 1 {
		t.Fatalf("a dry run reported %v and dev serves %d; it must store nothing", dry["outputs_changed"], devRelease2590(t, s))
	}

	status, saved := save2590(t, s, keyPeer2604, map[string]any{"path": pricing2590, "content": edited, "base": release, "note": "Starter price to $15"})
	want2559(t, "saving", status, http.StatusOK, saved)
	rel, _ := saved["release"].(map[string]any)
	if rel["number"] != float64(2) || rel["base"] != float64(1) || rel["created_by"] != emailPeer2604 || rel["source"] != "edit "+pricing2590 {
		t.Fatalf("the saved release: %v", saved["_raw"])
	}
	if got := devRelease2590(t, s); got != 2 {
		t.Fatalf("dev serves %d after the save", got)
	}
	if now, at := read2590(t, s, keyOwner2604, pricing2590); at != 2 || !strings.Contains(now, changedPrice2590) {
		t.Fatalf("dev serves release %d: %q", at, now)
	}
	status, diff := s.rest(t, keyOwner2604, http.MethodGet, "/file/diff?path="+url.QueryEscape(pricing2590)+"&from=1&to=2", nil)
	want2559(t, "the file's change", status, http.StatusOK, diff)
	if d, _ := diff["diff"].(string); !strings.Contains(d, "-<p class=\"price\">"+marker2590) || !strings.Contains(d, "+<p class=\"price\">"+changedPrice2590) {
		t.Errorf("the diff: %q", d)
	}

	// The other forms a save and a delete take: base64 content, and a delete
	// that is a release without the file.
	logo := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a}
	status, png := save2590(t, s, keyPeer2604, map[string]any{"path": "logo.png", "content": base64.StdEncoding.EncodeToString(logo), "encoding": "base64", "base": 2})
	want2559(t, "saving base64 content", status, http.StatusOK, png)
	status, gone := s.rest(t, keyPeer2604, http.MethodDelete, "/file?path=old.html&base=3&note="+url.QueryEscape("drop the old page"), nil)
	want2559(t, "deleting a file", status, http.StatusOK, gone)
	rel, _ = gone["release"].(map[string]any)
	if rel["number"] != float64(4) || rel["source"] != "delete old.html" {
		t.Errorf("the delete: %v", gone["_raw"])
	}
	if status, _ := s.rest(t, keyOwner2604, http.MethodGet, "/file?path=old.html", nil); status == http.StatusOK {
		t.Error("dev still serves the deleted file")
	}
}

// TestIssue2590_TwoEditorsOnOneFileCannotOverwriteEachOther is done-when 2.
func TestIssue2590_TwoEditorsOnOneFileCannotOverwriteEachOther(t *testing.T) {
	s := files2590(t, "acc2590b-")
	s.share(t, keyOwner2604, map[string]any{"email": emailPeer2604, "level": "manage"})

	ownerBody, ownerBase := read2590(t, s, keyOwner2604, pricing2590)
	peerBody, peerBase := read2590(t, s, keyPeer2604, pricing2590)
	peerText := strings.Replace(peerBody, marker2590, "Starter $14", 1)
	status, first := save2590(t, s, keyPeer2604, map[string]any{"path": pricing2590, "content": peerText, "base": peerBase, "note": "peer's price"})
	want2559(t, "the first save", status, http.StatusOK, first)

	status, second := save2590(t, s, keyOwner2604, map[string]any{"path": pricing2590, "content": strings.Replace(ownerBody, marker2590, "Starter $16", 1), "base": ownerBase})
	if status != http.StatusConflict {
		t.Fatalf("the second save against release %d: got %d, want 409: %v", ownerBase, status, second["_raw"])
	}
	if !strings.Contains(second["_raw"].(string), "current version "+strconv.Itoa(ownerBase+1)) {
		t.Errorf("the conflict does not name the release dev serves: %v", second["_raw"])
	}
	if now, at := read2590(t, s, keyOwner2604, pricing2590); at != ownerBase+1 || now != peerText {
		t.Fatalf("after the refused save dev serves release %d: %q; the first save must stand", at, now)
	}
	if got := devRelease2590(t, s); got != 2 {
		t.Errorf("dev serves %d; the refused save wrote a release", got)
	}
}

// TestIssue2590_SomeoneWhoDoesNotManageTheSiteIsRefusedEveryWrite is
// done-when 3: dev shared with them, they read its files and the API refuses
// a save, a delete and a diff (an earlier release can hold what a later one
// took out).
func TestIssue2590_SomeoneWhoDoesNotManageTheSiteIsRefusedEveryWrite(t *testing.T) {
	s := files2590(t, "acc2590c-")
	s.share(t, keyOwner2604, map[string]any{"email": emailPeer2604, "level": "view", "environments": []string{"dev"}})

	if body, _ := read2590(t, s, keyPeer2604, pricing2590); !strings.Contains(body, marker2590) {
		t.Fatalf("someone dev is shared with reads %q", body)
	}
	refusals := []struct {
		what   string
		method string
		path   string
		body   any
	}{
		{"a save", http.MethodPut, "/file", map[string]any{"path": pricing2590, "content": "<h1>mine</h1>", "base": 1}},
		{"a dry run", http.MethodPut, "/file", map[string]any{"path": pricing2590, "content": "<h1>mine</h1>", "base": 1, "dry_run": true}},
		{"a delete", http.MethodDelete, "/file?path=old.html&base=1", nil},
		{"a diff", http.MethodGet, "/file/diff?path=" + url.QueryEscape(pricing2590) + "&from=1&to=1", nil},
	}
	for _, r := range refusals {
		if status, got := s.rest(t, keyPeer2604, r.method, r.path, r.body); status != http.StatusForbidden {
			t.Errorf("%s by someone who does not manage the site: got %d, want 403: %v", r.what, status, got["_raw"])
		}
	}
	if got := devRelease2590(t, s); got != 1 {
		t.Errorf("dev serves %d; a refused write wrote a release", got)
	}
}
