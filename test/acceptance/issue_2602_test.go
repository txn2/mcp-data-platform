//go:build integration

package acceptance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// Acceptance for #2602: each environment keeps its last N releases, and a
// sweep prunes every other release and the objects only it named, keeping
// every retained release serving and diffing exactly as before.
//
// Wire forms: manage_site set_environment and add_domain take keep_releases as
// an integer, a string of digits, or the string "default"; the portal route PUT
// /api/v1/portal/sites/{site}/environments/{name} takes keep_releases in a JSON
// body as an integer or the string "default". manage_site takes command, site,
// environment, hostname and note as strings, base and release as integers,
// public and requires_approval as booleans, and files as a list of objects whose
// content is utf-8 text and whose delete is a boolean. The admin route POST
// /api/v1/admin/sites/retention/run takes mode ("report" or "delete") and site
// as strings in a JSON body. GET /api/v1/portal/sites/{site}/compare takes from
// and to as integers in the query. GET .../releases, .../releases/{number},
// .../deployments and .../jobs take no body and no parameter beyond the path. A
// request to a site is an HTTP GET naming the hostname in Host.

const (
	retention2602    = "/api/v1/admin/sites/retention"
	portalBucket2602 = "portal-assets"
)

// keep2602 sets an environment's limit through manage_site as the holder of key.
func keep2602(t *testing.T, key string, s *site2604, env string, keep any) map[string]any {
	t.Helper()
	return connectAs(t, key).call("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": env, "keep_releases": keep})
}

// env2602 is one environment of a site as manage_site get reports it.
func env2602(t *testing.T, s *site2604, env string) map[string]any {
	t.Helper()
	got := connectAs(t, keyAdmin2604).call("manage_site", map[string]any{"command": "get", "site": s.name})
	st, _ := got["site"].(map[string]any)
	envs, _ := st["environments"].([]any)
	for _, e := range envs {
		if m, _ := e.(map[string]any); m["name"] == env {
			return m
		}
	}
	t.Fatalf("site %s has no environment %s: %v", s.name, env, got)
	return nil
}

func siteID2602(t *testing.T, s *site2604) string {
	t.Helper()
	got := connectAs(t, keyAdmin2604).call("manage_site", map[string]any{"command": "get", "site": s.name})
	st, _ := got["site"].(map[string]any)
	id, _ := st["id"].(string)
	if id == "" {
		t.Fatalf("no site id: %v", got)
	}
	return id
}

// sweep2602 runs one sweep of one site now, as an administrator.
func sweep2602(t *testing.T, s *site2604, mode string) map[string]any {
	t.Helper()
	status, got := rest2559(t, keyAdmin2604, http.MethodPost, retention2602+"/run", map[string]any{"mode": mode, "site": s.name})
	want2559(t, "running a "+mode+" sweep", status, http.StatusOK, got)
	return got
}

// prunedIn2602 is the release numbers a sweep's report names for the site.
func prunedIn2602(t *testing.T, report map[string]any, s *site2604) []int {
	t.Helper()
	sites, _ := report["sites"].([]any)
	for _, raw := range sites {
		m, _ := raw.(map[string]any)
		if m["site"] != s.name {
			continue
		}
		list, _ := m["releases"].([]any)
		out := make([]int, 0, len(list))
		for _, n := range list {
			out = append(out, int(n.(float64)))
		}
		sort.Ints(out)
		return out
	}
	return []int{}
}

func count2602(report map[string]any, field string) int {
	n, _ := report[field].(float64)
	return int(n)
}

// release2602 is one release as the portal reads it.
type release2602 struct {
	number   int
	manifest string
	pruned   bool
}

func releases2602(t *testing.T, s *site2604) map[int]release2602 {
	t.Helper()
	status, got := s.rest(t, keyAdmin2604, http.MethodGet, "/releases", nil)
	want2559(t, "listing releases", status, http.StatusOK, got)
	list, _ := got["releases"].([]any)
	out := map[int]release2602{}
	for _, raw := range list {
		m, _ := raw.(map[string]any)
		n := int(m["number"].(float64))
		pruned, _ := m["pruned"].(bool)
		manifest, _ := m["manifest_sha256"].(string)
		out[n] = release2602{number: n, manifest: manifest, pruned: pruned}
	}
	return out
}

// served2602 is the digest of the file set a release serves, as Digest computes
// it, and whether the portal reports it pruned.
func served2602(t *testing.T, s *site2604, number int) (digest string, files int, pruned bool) {
	t.Helper()
	status, got := s.rest(t, keyAdmin2604, http.MethodGet, "/releases/"+strconv.Itoa(number), nil)
	want2559(t, "reading release "+strconv.Itoa(number), status, http.StatusOK, got)
	list, _ := got["files"].([]any)
	type entry struct{ path, sum string }
	entries := make([]entry, 0, len(list))
	for _, raw := range list {
		m, _ := raw.(map[string]any)
		entries = append(entries, entry{m["path"].(string), m["sha256"].(string)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	h := sha256.New()
	for _, e := range entries {
		_, _ = io.WriteString(h, e.path+"\x00"+e.sum+"\n")
	}
	pruned, _ = got["pruned"].(bool)
	return hex.EncodeToString(h.Sum(nil)), len(entries), pruned
}

// note2602 is release n's unique file, which no other release names.
func note2602(n int) (path, body string) {
	return "notes/v" + strconv.Itoa(n) + ".txt", "unique bytes of release " + strconv.Itoa(n) + "\n"
}

// publishChain2602 publishes release 1 and then n-1 overlays on it, each
// replacing the page, adding its own note and deleting the one before, so each
// release names one object no other release names.
func publishChain2602(t *testing.T, s *site2604, n int) {
	t.Helper()
	c := connectAs(t, s.key)
	notePath, noteBody := note2602(1)
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "v1", "files": []map[string]any{
		{"path": "index.html", "content": "<h1>v1</h1>"},
		{"path": "about/index.html", "content": "<h1>about</h1>"},
		{"path": "shared.css", "content": strings.Repeat("body{color:#123}\n", 200)},
		{"path": notePath, "content": noteBody},
	}})
	for v := 2; v <= n; v++ {
		prev, _ := note2602(v - 1)
		path, body := note2602(v)
		c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "base": v - 1, "note": "v" + strconv.Itoa(v), "files": []map[string]any{
			{"path": "index.html", "content": "<h1>v" + strconv.Itoa(v) + "</h1>"},
			{"path": path, "content": body},
			{"path": prev, "delete": true},
		}})
	}
}

// promote2602 points env at release number, as an administrator.
func promote2602(t *testing.T, s *site2604, env string, number int) {
	t.Helper()
	status, got := s.rest(t, keyAdmin2604, http.MethodPost, "/promote", map[string]any{"release": number, "to": env, "reason": "acceptance 2602"})
	want2559(t, "promoting", status, http.StatusOK, got)
	if got["status"] != "promoted" {
		t.Fatalf("promoting release %d to %s: %v", number, env, got["_raw"])
	}
}

func bucket2602(t *testing.T) *s3client.Client {
	t.Helper()
	port := os.Getenv("DEV_S3_PORT")
	if port == "" {
		port = "9000"
	}
	c, err := s3client.New(t.Context(), &s3client.Config{
		Region: "us-east-1", Endpoint: "http://localhost:" + port, UsePathStyle: true,
		AccessKeyID: "dev-access-key", SecretAccessKey: "dev-secret-key", Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// stored2602 reports whether the site's object store holds key.
func stored2602(t *testing.T, c *s3client.Client, key string) bool {
	t.Helper()
	_, err := c.GetObjectMetadata(t.Context(), portalBucket2602, key)
	return err == nil
}

func objectKey2602(siteID, body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sites/" + siteID + "/objects/" + hex.EncodeToString(sum[:])
}

func expectRefused2602(t *testing.T, key, what string, args map[string]any, wantIn string) {
	t.Helper()
	res, text, err := connectAs(t, key).callRaw("manage_site", args)
	if err != nil {
		t.Fatalf("%s: transport error: %v", what, err)
	}
	if !res.IsError {
		t.Fatalf("%s: was not refused: %s", what, text)
	}
	if !strings.Contains(strings.ToLower(text), strings.ToLower(wantIn)) {
		t.Errorf("%s: refusal %q does not say %q", what, text, wantIn)
	}
}

// TestIssue2602_AnEnvironmentsLimitIsSetAndRead is criterion 1: keep_releases
// is set, read and changed per environment through the tool and the portal
// route, and get shows each environment's limit and how many releases it keeps.
func TestIssue2602_AnEnvironmentsLimitIsSetAndRead(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2602a-")
	publishChain2602(t, s, 3)

	keep2602(t, keyOwner2604, s, "dev", 2)
	dev := env2602(t, s, "dev")
	if dev["keep_releases"] != float64(2) || dev["keep_releases_default"] != false {
		t.Errorf("after keep_releases 2: %v", dev)
	}
	if dev["releases_kept"] != float64(2) {
		t.Errorf("dev keeps the last 2 of 3 releases, reported %v", dev["releases_kept"])
	}
	keep2602(t, keyOwner2604, s, "dev", "7")
	if got := env2602(t, s, "dev")["keep_releases"]; got != float64(7) {
		t.Errorf("after keep_releases \"7\": %v", got)
	}
	keep2602(t, keyOwner2604, s, "dev", "default")
	dev = env2602(t, s, "dev")
	status, settings := rest2559(t, keyAdmin2604, http.MethodGet, retention2602, nil)
	want2559(t, "reading the retention settings", status, http.StatusOK, settings)
	if dev["keep_releases_default"] != true || dev["keep_releases"] != settings["keep_default"] {
		t.Errorf("after keep_releases default: %v, deployment default %v", dev, settings["keep_default"])
	}
	if dev["releases_kept"] != float64(3) {
		t.Errorf("under the default dev keeps all 3 releases, reported %v", dev["releases_kept"])
	}

	s.setEnv(t, keyOwner2604, "dev", map[string]any{"keep_releases": 1})
	if got := env2602(t, s, "dev")["keep_releases"]; got != float64(1) {
		t.Errorf("after the portal route set 1: %v", got)
	}
	s.setEnv(t, keyOwner2604, "dev", map[string]any{"keep_releases": "default"})
	if got := env2602(t, s, "dev")["keep_releases_default"]; got != true {
		t.Errorf("after the portal route set default: %v", got)
	}
	keep2602(t, keyOwner2604, s, "dev", 0)
	if got := env2602(t, s, "dev")["keep_releases"]; got != float64(0) {
		t.Errorf("after keep_releases 0: %v", got)
	}

	connectAs(t, keyAdmin2604).call("manage_site", map[string]any{
		"command": "add_domain", "hostname": "stage-" + s.name + ".example.test", "site": s.name, "environment": "stage", "keep_releases": 4,
	})
	if got := env2602(t, s, "stage")["keep_releases"]; got != float64(4) {
		t.Errorf("add_domain creating stage with keep_releases 4: %v", got)
	}

	for _, bad := range []any{-1, "ten", "1.5"} {
		expectRefused2602(t, keyOwner2604, "keep_releases "+strings.TrimSpace(toString2602(bad)),
			map[string]any{"command": "set_environment", "site": s.name, "environment": "dev", "keep_releases": bad}, "keep_releases")
	}
}

func toString2602(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	}
	return ""
}

// TestIssue2602_OnlyTheOwnerOrAnAdministratorChangesTheLimit is the user's
// decision: lowering a limit deletes rollback history, so someone who manages
// the site without owning it sees the limit and cannot change it.
func TestIssue2602_OnlyTheOwnerOrAnAdministratorChangesTheLimit(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2602b-")
	s.share(t, keyOwner2604, map[string]any{"email": emailPeer2604, "level": "manage"})

	expectRefused2602(t, keyPeer2604, "a manager changing keep_releases",
		map[string]any{"command": "set_environment", "site": s.name, "environment": "dev", "keep_releases": 1}, "owner")
	status, got := s.rest(t, keyPeer2604, http.MethodPut, "/environments/dev", map[string]any{"keep_releases": 1})
	if status != http.StatusForbidden {
		t.Errorf("a manager changing keep_releases through the portal: %d %v", status, got["_raw"])
	}
	if env2602(t, s, "dev")["keep_releases_default"] != true {
		t.Error("a refused change changed the limit")
	}
	// The same manager still changes what managing allows.
	connectAs(t, keyPeer2604).call("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "stage"})
	keep2602(t, keyAdmin2604, s, "dev", 3)
	if got := env2602(t, s, "dev")["keep_releases"]; got != float64(3) {
		t.Errorf("an administrator setting 3: %v", got)
	}
}

// TestIssue2602_ASweepKeepsEveryRetainedReleaseIntact is criteria 2, 3 and 6: a
// chain of 40 derived releases (past the full snapshot every 32 links) is
// pruned to what dev, stage and prod keep; report mode deletes nothing and
// counts what delete mode then removes; every retained release serves the file
// set its manifest_sha256 names; the objects only pruned releases named are
// gone and the rest are not; a rollback to a pruned release is refused naming
// the limit.
func TestIssue2602_ASweepKeepsEveryRetainedReleaseIntact(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2602c-")
	publishChain2602(t, s, 40)
	for _, env := range []string{"stage", "prod"} {
		s.setEnv(t, keyOwner2604, env, map[string]any{"public": true})
	}
	promote2602(t, s, "stage", 10)
	promote2602(t, s, "prod", 20)
	keep2602(t, keyOwner2604, s, "dev", 5)
	keep2602(t, keyOwner2604, s, "stage", 1)
	keep2602(t, keyOwner2604, s, "prod", 1)

	siteID := siteID2602(t, s)
	bucket := bucket2602(t)
	before := releases2602(t, s)
	if len(before) != 40 {
		t.Fatalf("expected 40 releases, got %d", len(before))
	}
	kept := []int{10, 20, 36, 37, 38, 39, 40}
	var want []int
	for n := 1; n <= 40; n++ {
		if !slices.Contains(kept, n) {
			want = append(want, n)
		}
	}

	report := sweep2602(t, s, "report")
	if got := prunedIn2602(t, report, s); !slices.Equal(got, want) {
		t.Fatalf("report mode would prune %v, want %v", got, want)
	}
	for n, r := range releases2602(t, s) {
		if r.pruned {
			t.Errorf("report mode pruned release %d", n)
		}
	}
	_, onlyPruned := note2602(5)
	if !stored2602(t, bucket, objectKey2602(siteID, onlyPruned)) {
		t.Fatal("release 5's note was not stored before the sweep")
	}

	deleted := sweep2602(t, s, "delete")
	if got := prunedIn2602(t, deleted, s); !slices.Equal(got, want) {
		t.Errorf("delete mode pruned %v, want %v", got, want)
	}
	for _, field := range []string{"releases", "files", "bytes"} {
		if count2602(report, field) != count2602(deleted, field) {
			t.Errorf("%s: report said %d, delete removed %d", field, count2602(report, field), count2602(deleted, field))
		}
	}
	if count2602(deleted, "files") < len(want) {
		t.Errorf("each pruned release named one object of its own, removed %d files for %d releases", count2602(deleted, "files"), len(want))
	}

	after := releases2602(t, s)
	for n := 1; n <= 40; n++ {
		r := after[n]
		if r.manifest != before[n].manifest {
			t.Errorf("release %d manifest_sha256 changed", n)
		}
		if r.pruned != slices.Contains(want, n) {
			t.Errorf("release %d pruned = %v", n, r.pruned)
		}
		digest, files, pruned := served2602(t, s, n)
		switch {
		case slices.Contains(kept, n) && digest != r.manifest:
			t.Errorf("retained release %d serves a file set (%d files) whose digest is not its manifest_sha256", n, files)
		case slices.Contains(want, n) && (!pruned || files != 0):
			t.Errorf("pruned release %d reads as pruned=%v with %d files", n, pruned, files)
		}
	}
	for _, n := range want {
		_, body := note2602(n)
		if stored2602(t, bucket, objectKey2602(siteID, body)) {
			t.Errorf("the object only pruned release %d named is still stored", n)
		}
	}
	for _, n := range []int{10, 20, 38, 40} {
		_, body := note2602(n)
		if !stored2602(t, bucket, objectKey2602(siteID, body)) {
			t.Errorf("the object retained release %d names was deleted", n)
		}
	}
	if !stored2602(t, bucket, objectKey2602(siteID, strings.Repeat("body{color:#123}\n", 200))) {
		t.Error("the object every release shares was deleted")
	}

	served2518(t, s.host("stage"), "v10")
	served2518(t, s.host("prod"), "v20")
	expectRefused2602(t, keyOwner2604, "rolling dev back to a pruned release",
		map[string]any{"command": "rollback", "site": s.name, "environment": "dev", "release": 30}, "limit")
	connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "rollback", "site": s.name, "environment": "dev", "release": 38})
	promote2602(t, s, "stage", 38)
	served2518(t, s.host("stage"), "v38")
	status, got := s.rest(t, keyAdmin2604, http.MethodPost, "/promote", map[string]any{"release": 12, "to": "stage"})
	if status == http.StatusOK || !strings.Contains(strings.ToLower(got["_raw"].(string)), "limit") {
		t.Errorf("promoting a pruned release: %d %v", status, got["_raw"])
	}
	// A comparison answers more people than promote does, so its refusal
	// names the release and none of the site's environments or their limits.
	status, got = s.rest(t, keyOwner2604, http.MethodGet, "/compare?from=12&to=38", nil)
	raw, _ := got["_raw"].(string)
	if status != http.StatusUnprocessableEntity || !strings.Contains(raw, "release 12 was removed by the release limit, so it cannot be compared") {
		t.Errorf("comparing with a pruned release: %d %s", status, raw)
	}
	if strings.Contains(raw, "keeps the last") || strings.Contains(raw, "stage") || strings.Contains(raw, "prod") {
		t.Errorf("the comparison's refusal names the site's environments: %s", raw)
	}
	// stage moved from 10 to 38, so 10 is in no window any more and the next
	// sweep would remove it, and nothing else.
	if again := prunedIn2602(t, sweep2602(t, s, "report"), s); !slices.Equal(again, []int{10}) {
		t.Errorf("after stage moved on, the next sweep would prune %v, want [10]", again)
	}
}

// TestIssue2602_FilesStoredBeforeContentAddressingStillServe is criterion 4: a
// file stored under its release's own prefix before v1.157.0 keeps serving for
// every retained release that names it after that release is pruned.
func TestIssue2602_FilesStoredBeforeContentAddressingStillServe(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2602d-")
	const legacyBody = "bytes stored the way releases before v1.157.0 stored them\n"
	c := connectAs(t, keyOwner2604)
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "v1", "files": []map[string]any{
		{"path": "index.html", "content": "<h1>v1</h1>"},
		{"path": "legacy.txt", "content": legacyBody},
	}})
	siteID := siteID2602(t, s)
	db := issue1904DB(t)
	var releaseID string
	if err := db.QueryRowContext(t.Context(), `SELECT id FROM site_releases WHERE site_id = $1 AND number = 1`, siteID).Scan(&releaseID); err != nil {
		t.Fatal(err)
	}
	// Move the file to where a release before content addressing kept it.
	bucket := bucket2602(t)
	cas := objectKey2602(siteID, legacyBody)
	legacy := "sites/" + siteID + "/releases/" + releaseID + "/files/legacy.txt"
	if _, err := bucket.PutObject(t.Context(), &s3client.PutObjectInput{Bucket: portalBucket2602, Key: legacy, Body: []byte(legacyBody), ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE site_release_files SET stored_in = release_id WHERE release_id = $1 AND path = 'legacy.txt'`, releaseID); err != nil {
		t.Fatal(err)
	}
	if err := bucket.DeleteObject(t.Context(), portalBucket2602, cas); err != nil {
		t.Fatal(err)
	}
	for v := 2; v <= 4; v++ {
		c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "base": v - 1, "note": "v" + strconv.Itoa(v), "files": []map[string]any{
			{"path": "index.html", "content": "<h1>v" + strconv.Itoa(v) + "</h1>"},
		}})
	}
	s.setEnv(t, keyOwner2604, "stage", map[string]any{"public": true})
	promote2602(t, s, "stage", 4)
	keep2602(t, keyOwner2604, s, "dev", 1)
	keep2602(t, keyOwner2604, s, "stage", 1)

	if got := prunedIn2602(t, sweep2602(t, s, "delete"), s); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("pruned %v, want [1 2 3]", got)
	}
	if !stored2602(t, bucket, legacy) {
		t.Fatal("the object a retained release names under a pruned release's prefix was deleted")
	}
	r := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.host("stage"), http.MethodGet, "/legacy.txt", nil, "")
		return r, r.status == http.StatusOK && r.body == legacyBody
	})
	if r.body != legacyBody {
		t.Errorf("stage serves %d %q", r.status, r.body)
	}
}

// TestIssue2602_PinnedAndPendingReleasesAreKept is criterion 5: a release an
// application release pins and one an open approval request names are never
// pruned, whatever the limits say.
func TestIssue2602_PinnedAndPendingReleasesAreKept(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2602e-")
	publishChain2602(t, s, 3)
	admin := connectAs(t, keyAdmin2604)
	s.setEnv(t, keyAdmin2604, "prod", map[string]any{"requires_approval": true})
	s.setEnv(t, keyOwner2604, "stage", map[string]any{})

	// An application release pins a site part at the site's newest release
	// and points the environment at it: stage serves 3, pinned.
	app := "acc2602app-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": app, "description": "pins a site environment"})
	t.Cleanup(func() {
		_, _, _ = connectAs(t, keyAdmin2604).callRaw("manage_application", map[string]any{"command": "delete", "application": app})
	})
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "site", "part_id": s.name + "/stage"})
	admin.call("manage_application", map[string]any{"command": "release", "application": app, "note": "pins stage at 3"})

	c := connectAs(t, keyOwner2604)
	for v := 4; v <= 6; v++ {
		prev, _ := note2602(v - 1)
		path, body := note2602(v)
		c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "base": v - 1, "note": "v" + strconv.Itoa(v), "files": []map[string]any{
			{"path": "index.html", "content": "<h1>v" + strconv.Itoa(v) + "</h1>"}, {"path": path, "content": body}, {"path": prev, "delete": true},
		}})
	}
	promote2602(t, s, "stage", 4)

	status, got := s.rest(t, keyOwner2604, http.MethodPost, "/promote", map[string]any{"release": 5, "to": "prod", "reason": "acceptance 2602"})
	want2559(t, "asking to promote", status, http.StatusOK, got)
	if got["status"] != "awaiting_approval" {
		t.Fatalf("the owner's promote to prod did not wait on Approvals: %v", got["_raw"])
	}
	for _, env := range []string{"dev", "stage", "prod"} {
		keep2602(t, keyAdmin2604, s, env, 1)
	}

	if pruned := prunedIn2602(t, sweep2602(t, s, "delete"), s); !slices.Equal(pruned, []int{1, 2}) {
		t.Fatalf("pruned %v, want [1 2]: 3 is pinned, 4 is stage's, 5 waits on an approval, 6 is dev's", pruned)
	}
	approve2604(t, got)
	status, got = s.rest(t, keyOwner2604, http.MethodPost, "/promote", map[string]any{"release": 5, "to": "prod", "reason": "acceptance 2602"})
	want2559(t, "finishing the promote", status, http.StatusOK, got)
	if got["status"] != "promoted" {
		t.Errorf("finishing the approved promote: %v", got["_raw"])
	}
}

// TestIssue2602_WhatKeepsAReleaseIsTheManagersToKnow: someone who does not
// manage a site reads its releases, a release, its history and its running
// jobs without the name of an environment they cannot open, without the
// application whose pin keeps a release, and without where a release came
// from; the owner reads all of it. It is #2604's rule for the site itself,
// held on what this ticket adds to the release routes.
func TestIssue2602_WhatKeepsAReleaseIsTheManagersToKnow(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2602p-")
	publishChain2602(t, s, 2)
	const hidden = "quiet2602"
	s.setEnv(t, keyOwner2604, hidden, map[string]any{})
	promote2602(t, s, hidden, 1)
	admin := connectAs(t, keyAdmin2604)
	app := "acc2602pin-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": app, "description": "pins a private site environment"})
	t.Cleanup(func() {
		_, _, _ = connectAs(t, keyAdmin2604).callRaw("manage_application", map[string]any{"command": "delete", "application": app})
	})
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "site", "part_id": s.name + "/" + hidden})
	admin.call("manage_application", map[string]any{"command": "release", "application": app, "note": "pins the private environment"})

	status, got := s.rest(t, keyOwner2604, http.MethodGet, "/releases", nil)
	want2559(t, "the owner reading the releases", status, http.StatusOK, got)
	if raw, _ := got["_raw"].(string); !strings.Contains(raw, `"`+hidden+`"`) || !strings.Contains(raw, "application "+app) {
		t.Fatalf("the owner does not read what keeps the releases: %s", raw)
	}

	for _, path := range []string{"/releases", "/releases/1", "/releases/2", "/deployments", "/jobs"} {
		status, got := s.rest(t, keyPeer2604, http.MethodGet, path, nil)
		want2559(t, "someone with no access reading "+path, status, http.StatusOK, got)
		raw, _ := got["_raw"].(string)
		for _, secret := range []string{hidden, app, `"dev"`, `"source"`} {
			if strings.Contains(raw, secret) {
				t.Errorf("%s names %s to someone who does not manage the site: %s", path, secret, raw)
			}
		}
	}
}

// TestIssue2602_LoweringALimitTakesEffectAtTheNextSweep is criterion 7: 0 keeps
// every release, and a lower limit prunes at the next sweep.
func TestIssue2602_LoweringALimitTakesEffectAtTheNextSweep(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2602f-")
	publishChain2602(t, s, 5)
	keep2602(t, keyOwner2604, s, "dev", 0)
	if got := prunedIn2602(t, sweep2602(t, s, "delete"), s); len(got) != 0 {
		t.Fatalf("keep_releases 0 pruned %v", got)
	}
	keep2602(t, keyOwner2604, s, "dev", 2)
	if got := prunedIn2602(t, sweep2602(t, s, "delete"), s); !slices.Equal(got, []int{1, 2, 3}) {
		t.Errorf("keep_releases 2 pruned %v, want [1 2 3]", got)
	}
	if got := env2602(t, s, "dev")["releases_kept"]; got != float64(2) {
		t.Errorf("dev keeps %v releases after the sweep", got)
	}
}

// TestIssue2602_OrphansPastTheGraceAreRemoved is criterion 3's second half: an
// object under a site that no release names is deleted once it is older than
// the grace (sites.releases.orphan_grace, 30s on the dev stack), never before,
// and a key outside the layout the platform writes is never touched.
func TestIssue2602_OrphansPastTheGraceAreRemoved(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2602g-")
	publishChain2602(t, s, 1)
	s.setEnv(t, keyOwner2604, "stage", map[string]any{"public": true})
	promote2602(t, s, "stage", 1)
	siteID := siteID2602(t, s)
	bucket := bucket2602(t)
	orphan := objectKey2602(siteID, "left behind by a render that failed")
	stray := "sites/" + siteID + "/not-a-layout-the-platform-writes.txt"
	for _, key := range []string{orphan, stray} {
		if _, err := bucket.PutObject(t.Context(), &s3client.PutObjectInput{Bucket: portalBucket2602, Key: key, Body: []byte("x"), ContentType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = bucket.DeleteObject(context.Background(), portalBucket2602, stray) })

	if got := sweep2602(t, s, "delete"); count2602(got, "orphans") != 0 || !stored2602(t, bucket, orphan) {
		t.Fatalf("an orphan inside the grace was removed: %v", got["_raw"])
	}
	deadline := time.Now().Add(2 * time.Minute)
	for stored2602(t, bucket, orphan) && time.Now().Before(deadline) {
		report := sweep2602(t, s, "report")
		if count2602(report, "orphans") > 0 {
			if !stored2602(t, bucket, orphan) {
				t.Fatal("report mode deleted the orphan")
			}
			sweep2602(t, s, "delete")
			break
		}
		time.Sleep(3 * time.Second)
	}
	if stored2602(t, bucket, orphan) {
		t.Error("the orphan past the grace is still stored")
	}
	if !stored2602(t, bucket, stray) {
		t.Error("a key outside the platform's layout was deleted")
	}
	served2518(t, s.host("stage"), "v1")
}

// TestIssue2602_TheDeploymentDefaultIsSavedInSettings is the deployment
// default: Admin > Settings > Sites reads and saves the releases each
// environment keeps and the sweep's mode.
func TestIssue2602_TheDeploymentDefaultIsSavedInSettings(t *testing.T) {
	const path = "/api/v1/admin/settings/sites"
	status, current := rest2559(t, keyAdmin2604, http.MethodGet, path, nil)
	want2559(t, "reading the sites settings", status, http.StatusOK, current)
	if current["releases_keep"] == nil || current["releases_mode"] == nil {
		t.Fatalf("the settings carry no release retention: %v", current["_raw"])
	}
	saved := map[string]any{}
	for k, v := range current {
		if k != "_raw" {
			saved[k] = v
		}
	}
	t.Cleanup(func() { _, _ = rest2559(t, keyAdmin2604, http.MethodPut, path, saved) })

	next := map[string]any{}
	for k, v := range saved {
		next[k] = v
	}
	next["releases_keep"], next["releases_mode"] = 40, "report"
	status, got := rest2559(t, keyAdmin2604, http.MethodPut, path, next)
	want2559(t, "saving the sites settings", status, http.StatusOK, got)
	// Every replica reads the saved section within its refresh interval.
	var settings map[string]any
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		_, settings = rest2559(t, keyAdmin2604, http.MethodGet, retention2602, nil)
		if settings["keep_default"] == float64(40) && settings["mode"] == "report" {
			break
		}
	}
	if settings["keep_default"] != float64(40) || settings["mode"] != "report" {
		t.Errorf("after saving 40 and report: %v", settings["_raw"])
	}
	next["releases_mode"] = "sometimes"
	if status, _ := rest2559(t, keyAdmin2604, http.MethodPut, path, next); status != http.StatusUnprocessableEntity {
		t.Errorf("an unknown mode was saved: %d", status)
	}
}
