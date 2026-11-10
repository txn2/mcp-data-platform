//go:build integration

package acceptance

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2588: an agent reads and edits a site's files through
// manage_asset, and publishes an overlay with manage_site, with no new tool.
//
// Wire forms: manage_asset takes action, asset_id, find, pattern,
// change_summary as strings, base_version, from_version, to_version, limit and
// offset as integers, dry_run as a boolean and edits as a list of objects whose
// find and replace are strings. manage_site takes command, site, note, from and
// to as strings, base and release as integers, and files as a list of objects
// whose content is utf-8 or base64 text and whose delete is a boolean. A request
// to a site is an HTTP GET naming the hostname in Host.

const (
	home2588   = "<html><body><h1>Pricing from $99</h1><p>marker-2588</p></body></html>\n"
	about2588  = "<html><body><p>about</p></body></html>\n"
	marker2588 = "marker-2588"
)

// site2588 is a site whose default environment serves one release.
func site2588(t *testing.T, c *client) *siteClient {
	t.Helper()
	s := newSite2518(t, c)
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "first", "files": []map[string]any{
		{"path": "index.html", "content": home2588},
		{"path": "about/index.html", "content": about2588},
		{"path": "logo.bin", "content": base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 3}), "encoding": "base64"},
		{"path": "_redirects", "content": "/old /new 302\n"},
	}})
	return s
}

func (s *siteClient) ref(path string) string {
	if path == "" {
		return "mcp:site:" + s.name + "/default"
	}
	return "mcp:site:" + s.name + "/default/" + path
}

func editOf(find, replace string) []map[string]any {
	return []map[string]any{{"find": find, "replace": replace}}
}

// refused2588 is a manage_asset call the platform refuses, and its text.
func refused2588(t *testing.T, c *client, args map[string]any) string {
	t.Helper()
	res, text, err := c.callRaw("manage_asset", args)
	if err != nil {
		t.Fatalf("manage_asset: transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("manage_asset %v was not refused: %s", args["action"], text)
	}
	return text
}

// releases2588 is how many releases the site holds.
func releases2588(t *testing.T, c *client, s *siteClient) []any {
	t.Helper()
	got, _ := c.call("manage_site", map[string]any{"command": "releases", "site": s.name})["releases"].([]any)
	return got
}

func number2588(v any) int {
	n, _ := v.(float64)
	return int(n)
}

func TestIssue2588_AnAgentFindsTheLineChangesItAndPromotes(t *testing.T) {
	c := connect(t)
	s := site2588(t, c)

	tree := c.call("manage_asset", map[string]any{"action": "outline", "asset_id": s.ref("")})
	if number2588(tree["total"]) != 4 {
		t.Fatalf("outline of the tree: %v", tree)
	}
	found := c.call("manage_asset", map[string]any{"action": "locate", "asset_id": s.ref(""), "find": "$99"})
	files, _ := found["files"].([]any)
	if len(files) != 1 || !strings.Contains(fmt.Sprint(files[0]), "index.html") {
		t.Fatalf("locate across the tree: %v", found)
	}
	if number2588(found["files_scanned"]) != 3 || number2588(found["files_skipped"]) != 1 {
		t.Errorf("locate must say what it did not search: %v", found)
	}
	content := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("index.html")})
	if content["content"] != home2588 || number2588(content["version"]) != 1 {
		t.Fatalf("get_content: %v", content)
	}

	patched := c.call("manage_asset", map[string]any{
		"action": "patch", "asset_id": s.ref("index.html"), "base_version": 1,
		"change_summary": "Fix the pricing headline", "edits": editOf("$99", "$89"),
	})
	if number2588(patched["version"]) != 2 {
		t.Fatalf("patch: %v", patched)
	}
	if len(fmt.Sprint(patched)) > 2048 {
		t.Errorf("a one-word patch answered with %d bytes", len(fmt.Sprint(patched)))
	}
	if strings.Contains(fmt.Sprint(patched), "marker-2588</p></body>") && !strings.Contains(fmt.Sprint(patched["diff"]), marker2588) {
		t.Errorf("the answer carries the body: %v", patched)
	}

	diff := c.call("manage_asset", map[string]any{"action": "diff", "asset_id": s.ref("")})
	changed, _ := diff["changed"].([]any)
	if len(changed) != 1 || changed[0] != "index.html" {
		t.Errorf("the tree's diff must name only the file changed: %v", diff)
	}
	fileDiff := c.call("manage_asset", map[string]any{"action": "diff", "asset_id": s.ref("index.html"), "from_version": 1, "to_version": 2})
	if !strings.Contains(fmt.Sprint(fileDiff["diff"]), "+<html><body><h1>Pricing from $89") {
		t.Errorf("the file's diff: %v", fileDiff)
	}

	c.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "default", "to": "dev"})
	got := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.preview("dev"), http.MethodGet, "/", nil, "")
		return r, r.status == http.StatusOK && strings.Contains(r.body, "$89")
	})
	if !strings.Contains(got.body, marker2588) {
		t.Errorf("the rest of the page must survive the edit: %s", got.body)
	}
	if about := onHost(t, s.preview("dev"), http.MethodGet, "/about/", nil, ""); about.status != http.StatusOK || !strings.Contains(about.body, "about") {
		t.Errorf("a file the patch left alone: %d %s", about.status, about.body)
	}
	rels := releases2588(t, c, s)
	if len(rels) != 2 || !strings.Contains(fmt.Sprint(rels[0]), "Fix the pricing headline") {
		t.Errorf("the summary is the release note: %v", rels)
	}
	newest, _ := rels[0].(map[string]any)
	if number2588(newest["base"]) != 1 {
		t.Errorf("a derived release records its base: %v", newest)
	}
}

func TestIssue2588_AFailedAnchorAStaleBaseAndADryRunMoveNothing(t *testing.T) {
	c := connect(t)
	s := site2588(t, c)
	if msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": s.ref("index.html"), "edits": editOf("no such words", "x")}); !strings.Contains(msg, "PATCH_NO_MATCH") {
		t.Errorf("a failed anchor: %s", msg)
	}
	if msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": s.ref("index.html"), "base_version": 9, "edits": editOf("$99", "$89")}); !strings.Contains(msg, "PATCH_STALE_BASE") {
		t.Errorf("a stale base: %s", msg)
	}
	dry := c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("index.html"), "dry_run": true, "edits": editOf("$99", "$89")})
	if dry["dry_run"] != true || !strings.Contains(fmt.Sprint(dry["diff"]), "$89") || number2588(dry["version"]) != 1 {
		t.Errorf("a dry run reports the change: %v", dry)
	}
	if same := c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("index.html"), "edits": editOf("$99", "$99")}); same["unchanged"] != true {
		t.Errorf("an edit that changes nothing: %v", same)
	}
	if rels := releases2588(t, c, s); len(rels) != 1 {
		t.Errorf("none of those may write a release: %v", rels)
	}
}

func TestIssue2588_TwoEditsFromOneBaseCannotBothLand(t *testing.T) {
	c := connect(t)
	s := site2588(t, c)
	first := c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("index.html"), "base_version": 1, "edits": editOf("$99", "$89")})
	if number2588(first["version"]) != 2 {
		t.Fatalf("the first edit: %v", first)
	}
	msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": s.ref("about/index.html"), "base_version": 1, "edits": editOf("about", "About")})
	if !strings.Contains(msg, "PATCH_STALE_BASE") {
		t.Errorf("the second edit read an older release: %s", msg)
	}
}

func TestIssue2588_APublishWithABaseOverlaysAndRollbackRestoresTheManifest(t *testing.T) {
	c := connect(t)
	s := site2588(t, c)
	before := releases2588(t, c, s)[0].(map[string]any)["manifest_sha256"]

	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "base": 1, "note": "overlay", "files": []map[string]any{
		{"path": "index.html", "content": "<h1>replaced</h1>\n"},
		{"path": "added.html", "content": "<p>added</p>\n"},
		{"path": "added.bin", "content": base64.StdEncoding.EncodeToString([]byte{9, 8, 7}), "encoding": "base64"},
		{"path": "about/index.html", "delete": true},
	}})
	tree := c.call("manage_asset", map[string]any{"action": "outline", "asset_id": s.ref("")})
	listed := fmt.Sprint(tree["files"])
	for _, want := range []string{"added.html", "added.bin", "index.html", "logo.bin"} {
		if !strings.Contains(listed, want) {
			t.Errorf("after the overlay %s is missing: %s", want, listed)
		}
	}
	if strings.Contains(listed, "about/index.html") {
		t.Errorf("a deleted file is still there: %s", listed)
	}
	c.call("manage_site", map[string]any{"command": "rollback", "site": s.name, "environment": "default", "release": 1})
	again := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("about/index.html")})
	if again["content"] != about2588 || number2588(again["version"]) != 1 {
		t.Errorf("rolling back must restore the deleted file: %v", again)
	}
	if after := releases2588(t, c, s)[1].(map[string]any)["manifest_sha256"]; after != before {
		t.Errorf("the release's digest moved: %v %v", before, after)
	}
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "base": 1, "files": []map[string]any{{"path": "index.html", "content": "<h1>again</h1>\n"}}})

	res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "base": 1, "files": []map[string]any{{"path": "index.html", "content": "<h1>stale</h1>\n"}}})
	if err != nil || !res.IsError || !strings.Contains(text, "does not match the current version") {
		t.Errorf("an overlay on a release default moved past: %v %s", err, text)
	}
	res, text, err = c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "base": 3, "files": []map[string]any{{"path": "a.html"}}})
	if err != nil || !res.IsError || !strings.Contains(text, "content is required") {
		t.Errorf("an entry with no content must not blank a page: %v %s", err, text)
	}
}

func TestIssue2588_OnlyTheWorkingEnvironmentTakesEdits(t *testing.T) {
	c := connect(t)
	s := site2588(t, c)
	c.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "default", "to": "dev"})
	dev := "mcp:site:" + s.name + "/dev/index.html"
	prod := "mcp:site:" + s.name + "/prod/index.html"
	if got := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": dev}); got["content"] != home2588 {
		t.Errorf("another environment still reads: %v", got)
	}
	if msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": dev, "edits": editOf("$99", "$1")}); !strings.Contains(msg, "read-only") {
		t.Errorf("a public environment: %s", msg)
	}
	if msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": prod, "edits": editOf("$99", "$1")}); !strings.Contains(msg, "read-only") && !strings.Contains(msg, "requires approval") {
		t.Errorf("an environment that requires approval: %s", msg)
	}
	res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "environment": "dev", "base": 1, "files": []map[string]any{{"path": "index.html", "content": "<h1>x</h1>"}}})
	if err != nil || !res.IsError || !strings.Contains(text, "read-only") {
		t.Errorf("an overlay on a public environment: %v %s", err, text)
	}
	if rel := onHost(t, s.preview("dev"), http.MethodGet, "/", nil, ""); !strings.Contains(rel.body, "$99") {
		t.Errorf("the public environment moved: %s", rel.body)
	}
}

func TestIssue2588_WhoMayReadAndWhoMayChange(t *testing.T) {
	owner := connectAs(t, keyEngineer2559)
	s := site2588(t, owner)
	peer := connectAs(t, keyDirector2559)

	// With no access: refused the same for a file that exists and one that does
	// not, and the refusal holds none of the file.
	exists := refused2588(t, peer, map[string]any{"action": "get_content", "asset_id": s.ref("index.html")})
	missing := refused2588(t, peer, map[string]any{"action": "get_content", "asset_id": s.ref("nothing-here.html")})
	if exists != missing {
		t.Errorf("a refusal must not say whether a file exists:\n%s\n%s", exists, missing)
	}
	for _, action := range []map[string]any{
		{"action": "outline", "asset_id": s.ref("")},
		{"action": "locate", "asset_id": s.ref(""), "find": marker2588},
		{"action": "stats", "asset_id": s.ref("index.html")},
		{"action": "diff", "asset_id": s.ref(""), "from_version": 1, "to_version": 1},
		{"action": "patch", "asset_id": s.ref("index.html"), "edits": editOf("$99", "$1")},
	} {
		if msg := refused2588(t, peer, action); strings.Contains(msg, marker2588) || strings.Contains(msg, "index.html") {
			t.Errorf("%v repeats the site's content in its refusal: %s", action["action"], msg)
		}
	}

	// With view: current files read; history and writes stay with those who
	// manage.
	owner.call("manage_site", map[string]any{"command": "grant", "site": s.name, "email": emailDirector2559, "level": "view"})
	if got := peer.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("index.html")}); got["content"] != home2588 {
		t.Errorf("view reads the current file: %v", got)
	}
	refused2588(t, peer, map[string]any{"action": "diff", "asset_id": s.ref("index.html"), "from_version": 1, "to_version": 1})
	refused2588(t, peer, map[string]any{"action": "patch", "asset_id": s.ref("index.html"), "edits": editOf("$99", "$1")})

	// With manage: a change, attributed to them.
	owner.call("manage_site", map[string]any{"command": "grant", "site": s.name, "email": emailDirector2559, "level": "manage"})
	peer.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("index.html"), "edits": editOf("$99", "$89")})
	newest, _ := releases2588(t, owner, s)[0].(map[string]any)
	if newest["created_by"] != emailDirector2559 {
		t.Errorf("the release must be attributed to whoever made it: %v", newest)
	}
}

func TestIssue2588_APersonaDeniedManageSiteCannotChangeASiteThroughManageAsset(t *testing.T) {
	admin := connect(t)
	s := site2588(t, admin)
	persona, role := "acc-2588-"+unique1579(), "acc_2588_role"
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": persona, "display_name": "Acceptance 2588", "roles": []any{role}, "priority": 90,
		"allow_tools": []any{"*"}, "deny_tools": []any{"manage_site"}, "allow_connections": []any{"*"},
	}))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the persona: %d %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+persona, http.NoBody) })
	denied := issue2089Key(t, admin, "acc-2588-"+unique1579(), role)
	msg := refused2588(t, denied, map[string]any{"action": "patch", "asset_id": s.ref("index.html"), "edits": editOf("$99", "$1")})
	if !strings.Contains(msg, "manage_site") {
		t.Errorf("the refusal must name the tool the persona lacks: %s", msg)
	}
	if rels := releases2588(t, admin, s); len(rels) != 1 {
		t.Errorf("no release may result: %v", rels)
	}
}

func TestIssue2588_AHiddenPathAndAFolderReferenceAreRefusedOrScoped(t *testing.T) {
	c := connect(t)
	s := site2588(t, c)
	for _, path := range []string{".env", "a/../../b", ".git/config"} {
		msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": s.ref(path), "edits": editOf("a", "b")})
		if !strings.Contains(msg, "dotted segment") && !strings.Contains(msg, "relative and clean") {
			t.Errorf("%s: %s", path, msg)
		}
	}
	folder := c.call("manage_asset", map[string]any{"action": "outline", "asset_id": s.ref("about/")})
	if number2588(folder["total"]) != 1 {
		t.Errorf("a folder reference lists only that folder: %v", folder)
	}
	if msg := refused2588(t, c, map[string]any{"action": "get_content", "asset_id": s.ref("")}); !strings.Contains(msg, "outline") {
		t.Errorf("get_content on a tree says what to do instead: %s", msg)
	}
	if msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": s.ref("logo.bin"), "edits": editOf("a", "b")}); !strings.Contains(msg, "PATCH_NOT_TEXT") {
		t.Errorf("a binary file: %s", msg)
	}
}

func TestIssue2588_TheRedirectMapReadsAndPatchesAsAFile(t *testing.T) {
	c := connect(t)
	s := site2588(t, c)
	got := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("_redirects")})
	if got["content"] != "/old /new 302\n" {
		t.Fatalf("the rendered map: %v", got)
	}
	c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("_redirects"), "edits": editOf("/new", "/newer")})
	if msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": s.ref("_redirects"), "edits": editOf("302", "999")}); !strings.Contains(msg, "_redirects") {
		t.Errorf("a map that does not parse: %s", msg)
	}
	c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("index.html"), "edits": editOf("$99", "$89")})
	c.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "default", "to": "dev"})
	r := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.preview("dev"), http.MethodGet, "/old", nil, "")
		return r, r.status == http.StatusFound
	})
	if loc := r.header.Get("Location"); loc != "/newer" {
		t.Errorf("a later release must carry the patched map: %q", loc)
	}
}

func TestIssue2588_ActionsWithNoMeaningOnASiteFileSaySo(t *testing.T) {
	c := connect(t)
	s := site2588(t, c)
	for _, action := range []string{"share", "delete", "update", "revert", "get", "list_versions"} {
		if msg := refused2588(t, c, map[string]any{"action": action, "asset_id": s.ref("index.html")}); !strings.Contains(msg, "does not apply to a site file") {
			t.Errorf("%s: %s", action, msg)
		}
	}
}

func TestIssue2588_APatchOnALargeSiteIsTheSizeOfTheEdit(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	const pages = 3000
	files := make([]map[string]any, 0, pages)
	for i := range pages {
		files = append(files, map[string]any{"path": fmt.Sprintf("p/%d/index.html", i), "content": fmt.Sprintf("<h1>page %d</h1>\n", i)})
	}
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "files": files})

	patched := c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("p/1500/index.html"), "edits": editOf("page 1500", "page fifteen hundred")})
	if len(fmt.Sprint(patched)) > 2048 {
		t.Errorf("the answer to a one-line patch is %d bytes", len(fmt.Sprint(patched)))
	}
	if rels := releases2588(t, c, s); len(rels) != 2 || number2588(rels[0].(map[string]any)["file_count"]) != pages {
		t.Fatalf("the derived release still holds every file: %v", rels[0])
	}
	if got := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("p/2999/index.html")}); got["content"] != "<h1>page 2999</h1>\n" {
		t.Errorf("a file the patch left alone: %v", got)
	}
}

func TestIssue2588_TheToolsTeachIt(t *testing.T) {
	c := connect(t)
	var site, asset string
	for _, tool := range c.tools() {
		switch tool.Name {
		case "manage_site":
			site = tool.Description
		case "manage_asset":
			asset = tool.Description + fmt.Sprint(tool.InputSchema)
		}
	}
	if !strings.Contains(site, "mcp:site:") || !strings.Contains(site, "base") {
		t.Errorf("manage_site must teach the edit path: %s", site)
	}
	if !strings.Contains(asset, "mcp:site:") {
		t.Errorf("manage_asset must name the reference form: %s", asset)
	}
	page := c.call("fetch", map[string]any{"reference": "mcp:knowledge_page:platform-pages-and-sites", "purpose": "Reading how a site is changed in place to check the guidance teaches it."})
	if text := fmt.Sprint(page); !strings.Contains(text, "mcp:site:") || !strings.Contains(text, "manage_asset") {
		t.Errorf("the knowledge page must teach it: %.400s", text)
	}
}
