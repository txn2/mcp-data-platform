//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
)

// Acceptance for #2591: a site serves a managed-resource folder at a URL
// prefix, with no release holding its files.
//
// Wire forms: manage_site update takes media as a list of objects whose
// prefix, scope, scope_id and folder are strings; the portal route PATCH
// /api/v1/portal/sites/{site} takes the same list as media in a JSON body.
// manage_resource takes action and reference and content as strings and force
// as a boolean. A file is uploaded to POST /api/v1/resources as multipart form
// fields, and moved by PATCH /api/v1/resources/{id} with path as a string in a
// JSON body. A request to a site is an HTTP GET naming the hostname in Host,
// with and without Range, and with a path holding percent-encoded bytes that
// are not UTF-8 and a NUL.

// library2591 is a folder of the owner's own library.
type library2591 struct {
	scopeID string
	folder  string
}

func newLibrary2591(t *testing.T, key string) library2591 {
	t.Helper()
	_, me := rest2559(t, key, http.MethodGet, "/api/v1/portal/me", nil)
	return library2591{scopeID: fmt.Sprint(me["user_id"]), folder: "media2591-" + unique1579()}
}

// upload puts a file at folder/sub/name and returns its reference.
func (l library2591) upload(t *testing.T, key, sub, name, contentType string, body []byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	folder := l.folder
	if sub != "" {
		folder += "/" + sub
	}
	for k, v := range map[string]string{"scope": "user", "scope_id": l.scopeID, "path": folder, "display_name": name, "description": "Acceptance 2591", "category": "other", "content_type": contentType} {
		_ = w.WriteField(k, v)
	}
	// The part declares the file's type, as a browser's upload does.
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	header.Set("Content-Type", contentType)
	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(body)
	_ = w.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL()+"/api/v1/resources", &buf) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	got := map[string]any{}
	_ = json.Unmarshal(raw, &got)
	id, _ := got["id"].(string)
	if res.StatusCode >= http.StatusBadRequest || id == "" {
		t.Fatalf("uploading %s: %d %s", name, res.StatusCode, raw)
	}
	return id
}

func (l library2591) mount(prefix string) map[string]any {
	return map[string]any{"prefix": prefix, "scope": "user", "scope_id": l.scopeID, "folder": l.folder}
}

// mediaSite is a site with a release served publicly at stage's domain.
func mediaSite2591(t *testing.T) *site2604 {
	t.Helper()
	s := newSite2604(t, keyOwner2604, "acc2591-")
	connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "v1", "files": []map[string]any{
		{"path": "index.html", "content": "<h1>v1</h1>"},
		{"path": "media/index.html", "content": "<h1>the release's own file</h1>"},
		{"path": "404.html", "content": "<h1>not here</h1>"},
	}})
	s.setEnv(t, keyOwner2604, "stage", map[string]any{})
	promote2602(t, s, "stage", 1)
	host := s.publicDomain(t, "stage")
	settled2619(t, host, nil, func(r pageResponse) bool { return r.status == http.StatusOK && strings.Contains(r.body, "<h1>v1</h1>") })
	return s
}

// getMedia2591 reads stage through its public domain: every pages address
// asks for a sign-in (#2619).
func getMedia2591(t *testing.T, s *site2604, path string, headers map[string]string) pageResponse {
	t.Helper()
	return onHost(t, publicHost2619(s, "stage"), http.MethodGet, path, headers, "")
}

// TestIssue2591_ASiteServesAMountedFolderWithNoRelease is criteria 1, 2 and 3:
// a mounted folder's files answer at the prefix by range, a rollback leaves
// them, and a replaced file shows with no publish.
func TestIssue2591_ASiteServesAMountedFolderWithNoRelease(t *testing.T) {
	s := mediaSite2591(t)
	lib := newLibrary2591(t, keyOwner2604)
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("image-bytes-", 400))
	ref := lib.upload(t, keyOwner2604, "", "logo.png", "image/png", png)
	lib.upload(t, keyOwner2604, "2026/q1", "chart.png", "image/png", []byte("\x89PNG\r\n\x1a\nnested"))

	connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "update", "site": s.name, "media": []map[string]any{lib.mount("/media/")}})

	r := pollUntil2518(t, func() (pageResponse, bool) {
		r := getMedia2591(t, s, "/media/logo.png", nil)
		return r, r.status == http.StatusOK
	})
	if r.status != http.StatusOK || r.body != string(png) {
		t.Fatalf("the mounted file answers %d with %d bytes", r.status, len(r.body))
	}
	if got := getMedia2591(t, s, "/media/2026/q1/chart.png", nil); got.status != http.StatusOK || !strings.HasSuffix(got.body, "nested") {
		t.Errorf("a file in a subfolder of the mount: %d", got.status)
	}
	if got := getMedia2591(t, s, "/media/logo.png", map[string]string{"Range": "bytes=0-7"}); got.status != http.StatusPartialContent || got.body != string(png[:8]) {
		t.Errorf("a range of a mounted file: %d %q", got.status, got.body)
	}
	if got := getMedia2591(t, s, "/media/index.html", nil); !strings.Contains(got.body, "the release's own file") {
		t.Errorf("a release file at the same path must win over the mount: %d %q", got.status, got.body)
	}
	if got := getMedia2591(t, s, "/media/missing.png", nil); got.status != http.StatusNotFound || !strings.Contains(got.body, "not here") {
		t.Errorf("a path the mount does not hold answers the site's 404: %d", got.status)
	}

	var releases int
	if err := issue1904DB(t).QueryRowContext(t.Context(), `SELECT COUNT(*) FROM site_release_files f JOIN site_releases r ON r.id = f.release_id JOIN sites s ON s.id = r.site_id
		WHERE s.name = $1 AND f.path LIKE '%logo.png'`, s.name).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if releases != 0 {
		t.Errorf("a release holds the mounted file %d times", releases)
	}

	// A new release and a rollback leave the media alone.
	connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "publish", "site": s.name, "base": 1, "note": "v2", "files": []map[string]any{{"path": "index.html", "content": "<h1>v2</h1>"}}})
	promote2602(t, s, "stage", 2)
	connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "rollback", "site": s.name, "environment": "stage", "release": 1})
	if got := getMedia2591(t, s, "/media/logo.png", nil); got.status != http.StatusOK {
		t.Errorf("after a rollback the mounted file answers %d", got.status)
	}

	// Replacing the file shows with no publish.
	connectAs(t, keyOwner2604).call("manage_resource", map[string]any{"action": "replace_content", "reference": "mcp:resource:" + ref, "content": "replaced bytes", "content_type": "image/png"})
	got := pollUntil2518(t, func() (pageResponse, bool) {
		r := getMedia2591(t, s, "/media/logo.png", nil)
		return r, r.body == "replaced bytes"
	})
	if got.body != "replaced bytes" {
		t.Errorf("a replaced file still serves %d bytes of the old content", len(got.body))
	}
}

// TestIssue2591_AMediaFolderIsNeverASecondSite is the decision that active
// types in a mount are downloads.
func TestIssue2591_AMediaFolderIsNeverASecondSite(t *testing.T) {
	s := mediaSite2591(t)
	lib := newLibrary2591(t, keyOwner2604)
	lib.upload(t, keyOwner2604, "", "page.html", "text/html", []byte("<script>alert(1)</script>"))
	lib.upload(t, keyOwner2604, "", "icon.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	s.rest(t, keyOwner2604, http.MethodPatch, "", map[string]any{"media": []map[string]any{lib.mount("/files/")}})
	for _, p := range []string{"/files/page.html", "/files/icon.svg"} {
		r := pollUntil2518(t, func() (pageResponse, bool) {
			r := getMedia2591(t, s, p, nil)
			return r, r.status == http.StatusOK
		})
		if !strings.Contains(r.header.Get("Content-Disposition"), "attachment") || r.header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s is served inline: %v", p, r.header)
		}
		if !strings.Contains(r.header.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s carries no sandbox policy: %v", p, r.header)
		}
	}
}

// TestIssue2591_MountingNeedsTheFolderAndTheSite is criterion 4 and the rule
// that mounting is a publishing act.
func TestIssue2591_MountingNeedsTheFolderAndTheSite(t *testing.T) {
	s := mediaSite2591(t)
	theirs := newLibrary2591(t, keyPeer2604)
	theirs.upload(t, keyPeer2604, "", "private.png", "image/png", []byte("theirs"))

	expect := func(key string, mount map[string]any, want string) {
		t.Helper()
		res, text, err := connectAs(t, key).callRaw("manage_site", map[string]any{"command": "update", "site": s.name, "media": []map[string]any{mount}})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || !strings.Contains(strings.ToLower(text), want) {
			t.Errorf("mount %v by %s: %s", mount, key, text)
		}
	}
	expect(keyOwner2604, theirs.mount("/theirs/"), "that you can read")
	expect(keyOwner2604, map[string]any{"prefix": "/", "scope": "user", "scope_id": theirs.scopeID, "folder": theirs.folder}, "never its root")
	expect(keyOwner2604, map[string]any{"prefix": "/_viewer/", "scope": "global", "folder": "x"}, "answers itself")
	mine := newLibrary2591(t, keyPeer2604)
	mine.upload(t, keyPeer2604, "", "a.png", "image/png", []byte("a"))
	expect(keyPeer2604, mine.mount("/m/"), "not allowed")
	status, _ := s.rest(t, keyOwner2604, http.MethodPatch, "", map[string]any{"media": []map[string]any{theirs.mount("/theirs/")}})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("the portal route mounting a folder the caller cannot read: %d", status)
	}
	if got := getMedia2591(t, s, "/theirs/private.png", nil); got.status == http.StatusOK {
		t.Error("a refused mount serves the folder")
	}
}

// TestIssue2591_AMountedFolderIsProtected is the decision that a file in a
// mounted folder is refused deletion unless forced.
func TestIssue2591_AMountedFolderIsProtected(t *testing.T) {
	s := mediaSite2591(t)
	lib := newLibrary2591(t, keyOwner2604)
	ref := lib.upload(t, keyOwner2604, "", "keep.png", "image/png", []byte("keep"))
	other := lib.upload(t, keyOwner2604, "", "rest.png", "image/png", []byte("rest"))
	connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "update", "site": s.name, "media": []map[string]any{lib.mount("/media/")}})

	res, text, err := connectAs(t, keyOwner2604).callRaw("manage_resource", map[string]any{"action": "delete", "reference": "mcp:resource:" + ref})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(text, "site serves this file") || strings.Contains(text, `"deleted":true`) {
		t.Errorf("deleting a mounted file through the tool: %s", text)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, baseURL()+"/api/v1/resources/"+other, http.NoBody) // #nosec G704 -- the platform under test
	req.Header.Set("X-API-Key", keyOwner2604)
	rr, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	_ = rr.Body.Close()
	if rr.StatusCode != http.StatusConflict {
		t.Errorf("deleting a mounted file through the API: %d", rr.StatusCode)
	}
	if got := getMedia2591(t, s, "/media/keep.png", nil); got.status != http.StatusOK {
		t.Errorf("the refused deletes left the file serving: %d", got.status)
	}
	connectAs(t, keyOwner2604).call("manage_resource", map[string]any{"action": "delete", "reference": "mcp:resource:" + ref, "force": true})
	got := pollUntil2518(t, func() (pageResponse, bool) {
		r := getMedia2591(t, s, "/media/keep.png", nil)
		return r, r.status == http.StatusNotFound
	})
	if got.status != http.StatusNotFound {
		t.Errorf("a forced delete still serves: %d", got.status)
	}
}

// TestIssue2591_AMountServesOnlyWhatItsFolderHoldsNow: a file moved out of a
// mounted folder stops answering at its old address at once, though the
// library still answers that address for the file's other readers; a file
// moved within the folder answers at its new path; and a request path no file
// can have answers the site's 404, not an error.
func TestIssue2591_AMountServesOnlyWhatItsFolderHoldsNow(t *testing.T) {
	s := mediaSite2591(t)
	lib := newLibrary2591(t, keyOwner2604)
	out := lib.upload(t, keyOwner2604, "", "moving-out.png", "image/png", []byte("MOVED-OUT-2591"))
	within := lib.upload(t, keyOwner2604, "", "moving-within.png", "image/png", []byte("MOVED-WITHIN-2591"))
	connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "update", "site": s.name, "media": []map[string]any{lib.mount("/media/")}})
	pollUntil2518(t, func() (pageResponse, bool) {
		r := getMedia2591(t, s, "/media/moving-out.png", nil)
		return r, r.status == http.StatusOK && r.body == "MOVED-OUT-2591"
	})

	elsewhere := lib.folder + "-private"
	if status, got := rest2559(t, keyOwner2604, http.MethodPatch, "/api/v1/resources/"+out, map[string]any{"path": elsewhere}); status != http.StatusOK {
		t.Fatalf("moving the file out of the mounted folder: %d %v", status, got["_raw"])
	}
	if status, got := rest2559(t, keyOwner2604, http.MethodPatch, "/api/v1/resources/"+within, map[string]any{"path": lib.folder + "/sub"}); status != http.StatusOK {
		t.Fatalf("moving the file within the mounted folder: %d %v", status, got["_raw"])
	}
	if r := getMedia2591(t, s, "/media/moving-out.png", nil); r.status != http.StatusNotFound || strings.Contains(r.body, "MOVED-OUT-2591") {
		t.Errorf("a file moved out of the mount still answers at its old address: %d %q", r.status, r.body)
	}
	if r := getMedia2591(t, s, "/media/moving-within.png", nil); r.status != http.StatusNotFound {
		t.Errorf("a file moved within the mount still answers at its old path: %d", r.status)
	}
	if r := getMedia2591(t, s, "/media/sub/moving-within.png", nil); r.status != http.StatusOK || r.body != "MOVED-WITHIN-2591" {
		t.Errorf("a file moved within the mount answers %d at its new path", r.status)
	}

	// The file moved out is no longer one a site serves: deleting it needs no force.
	res, text, err := connectAs(t, keyOwner2604).callRaw("manage_resource", map[string]any{"action": "delete", "reference": "mcp:resource:" + out})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || strings.Contains(text, "site serves this file") {
		t.Errorf("deleting a file moved out of the mount: %s", text)
	}

	// Bytes no file name can hold answer not found, never a failure to read.
	if r := getMedia2591(t, s, "/media/%ff.png", nil); r.status != http.StatusNotFound || !strings.Contains(r.body, "not here") {
		t.Errorf("/media/%%ff.png answers %d, not the site's 404: %q", r.status, r.body)
	}
	// A NUL byte is refused by the proxy in front of the platform (400) before
	// the platform sees it; whatever answers, it is not a server error and
	// serves nothing.
	if r := getMedia2591(t, s, "/media/a%00b.png", nil); r.status >= http.StatusInternalServerError || r.status == http.StatusOK {
		t.Errorf("/media/a%%00b.png answers %d: %q", r.status, r.body)
	}
}

// TestIssue2591_APrivateEnvironmentsMediaIsPrivate keeps a mount behind the
// environment's sign-in.
func TestIssue2591_APrivateEnvironmentsMediaIsPrivate(t *testing.T) {
	s := mediaSite2591(t)
	lib := newLibrary2591(t, keyOwner2604)
	lib.upload(t, keyOwner2604, "", "secret.png", "image/png", []byte("SECRET-2591"))
	connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "update", "site": s.name, "media": []map[string]any{lib.mount("/media/")}})
	r := onHost(t, s.host("dev"), http.MethodGet, "/media/secret.png", nil, "")
	if r.status == http.StatusOK || strings.Contains(r.body, "SECRET-2591") {
		t.Errorf("dev is private, and an anonymous request read its media: %d", r.status)
	}
}
