//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2593: a site's source tree and a media folder arrive from a
// bucket prefix on an S3 connection the caller may use, without a tool call
// carrying their bytes: manage_site publish and the portal publish route take
// from_bucket, and manage_resource extract takes it in place of an archive.
// The connection's bucket_prefix (#2584) holds: dev-s3-prefixed reaches
// portal-* buckets only, so portal-assets is inside it and dev-scratch outside.
//
// Reading a bucket takes the tools that read one: a persona allowed the
// connection but not s3_list and s3_object reads nothing through a publish or
// an extraction either.
//
// Wire forms: from_bucket is a JSON object whose connection, bucket and prefix
// are strings, sent to manage_site publish (with command and site as strings),
// to manage_resource extract (with action, path, scope and scope_id as
// strings), and in the JSON body of POST /api/v1/portal/sites/{site}/publish.
// Objects are seeded with s3_object put (action, connection, bucket, key,
// content, content_type and purpose as strings). The persona without
// s3_object is created through POST /api/v1/admin/personas (name,
// display_name as strings, roles, allow_tools, deny_tools and
// allow_connections as lists of strings) and its key through POST
// /api/v1/admin/auth/keys (name, email, description as strings, roles a list
// of strings). A request to a site is an HTTP GET naming the hostname in
// Host, after the platform's sign-in.

const (
	issue2593Bucket  = "portal-assets"
	issue2593Purpose = "Acceptance for #2593: a site and a media folder from a bucket prefix."
)

// seed2593 puts objects under a fresh prefix through dev-s3 and returns the
// prefix; they are deleted when the test ends.
func seed2593(t *testing.T, objects map[string][2]string) string {
	t.Helper()
	c := connect(t)
	requireS3(t, c)
	prefix := fmt.Sprintf("acceptance-2593/%d/", time.Now().UnixNano())
	for key, v := range objects {
		c.call(issue1591ObjectTool, map[string]any{
			"action": "put", "connection": issue1591Writable, "bucket": issue2593Bucket,
			"key": prefix + key, "content": v[0], "content_type": v[1], "purpose": issue2593Purpose,
		})
	}
	t.Cleanup(func() {
		for key := range objects {
			_, _, _ = connect(t).callRaw(issue1591ObjectTool, map[string]any{
				"action": "delete", "connection": issue1591Writable, "bucket": issue2593Bucket, "key": prefix + key, "purpose": issue2593Purpose,
			})
		}
	})
	return prefix
}

func siteTree2593(marker string) map[string][2]string {
	return map[string][2]string{
		"index.html":       {"<h1>" + marker + "</h1>", "text/html"},
		"about/index.html": {"<h1>about</h1>", "text/html"},
		"css/site.css":     {"body{color:#123}", "text/css"},
	}
}

// TestIssue2593_APublishFromABucketPrefix: manage_site publish from_bucket
// writes a release of the objects under the prefix, each at its path under
// it, and the release says where it came from.
func TestIssue2593_APublishFromABucketPrefix(t *testing.T) {
	marker := "bucket-2593-" + unique1579()
	prefix := seed2593(t, siteTree2593(marker))
	s := newSite2604(t, keyOwner2604, "acc2593a-")
	from := map[string]any{"connection": "dev-s3-prefixed", "bucket": issue2593Bucket, "prefix": prefix}
	got := connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "publish", "site": s.name, "from_bucket": from})
	rel, _ := got["release"].(map[string]any)
	if rel["number"] != float64(1) || rel["file_count"] != float64(3) {
		t.Fatalf("the publish from the bucket: %v", got)
	}
	if src, _ := rel["source"].(string); !strings.Contains(src, "s3://"+issue2593Bucket+"/"+prefix) || !strings.Contains(src, "dev-s3-prefixed") {
		t.Errorf("the release says it came from %q", src)
	}
	served2518For(t, s, "dev", marker)
}

// TestIssue2593_ThePortalPublishesFromABucketAsAJob: the portal route runs the
// same publish as a job, read as the caller.
func TestIssue2593_ThePortalPublishesFromABucketAsAJob(t *testing.T) {
	marker := "portal-bucket-2593-" + unique1579()
	prefix := seed2593(t, siteTree2593(marker))
	s := newSite2604(t, keyOwner2604, "acc2593b-")
	status, job := s.rest(t, keyOwner2604, http.MethodPost, "/publish", map[string]any{
		"from_bucket": map[string]any{"connection": "dev-s3", "bucket": issue2593Bucket, "prefix": prefix}, "note": "from the bucket",
	})
	want2559(t, "starting the publish", status, http.StatusAccepted, job)
	id, _ := job["id"].(string)
	deadline := time.Now().Add(2 * time.Minute)
	var done map[string]any
	for time.Now().Before(deadline) {
		_, done = s.rest(t, keyOwner2604, http.MethodGet, "/jobs/"+id, nil)
		if done["status"] != "running" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if done["status"] != "done" || done["release"] != float64(1) {
		t.Fatalf("the publish job ended %v", done["_raw"])
	}
	served2518For(t, s, "dev", marker)
}

// TestIssue2593_TheConnectionsLimitHolds: a bucket outside the connection's
// bucket_prefix, and a connection left unnamed, are refused through the tool
// and the portal route, and nothing is published.
func TestIssue2593_TheConnectionsLimitHolds(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2593c-")
	outside := map[string]any{"connection": "dev-s3-prefixed", "bucket": "dev-scratch", "prefix": "anything/"}
	res, text, err := connectAs(t, keyOwner2604).callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "from_bucket": outside})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, `limited to buckets whose name starts with "portal-"`) || !strings.Contains(text, "nothing was published") {
		t.Errorf("a bucket outside the prefix through the tool: %s", text)
	}
	if strings.Contains(text, "dev-secret-key") || strings.Contains(text, "localhost:") {
		t.Errorf("the refusal carries the connection's configuration: %s", text)
	}
	status, got := s.rest(t, keyOwner2604, http.MethodPost, "/publish", map[string]any{"from_bucket": outside})
	if status != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(got["_raw"]), "limited to buckets") {
		t.Errorf("a bucket outside the prefix through the portal: %d %v", status, got["_raw"])
	}
	res, text, err = connectAs(t, keyOwner2604).callRaw("manage_site", map[string]any{
		"command": "publish", "site": s.name,
		"from_bucket": map[string]any{"bucket": issue2593Bucket},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The tool's schema requires connection, so the refusal may come from it.
	if !res.IsError || !strings.Contains(text, "connection") {
		t.Errorf("an unnamed connection: %s", text)
	}
	status, got = s.rest(t, keyOwner2604, http.MethodPost, "/publish", map[string]any{"from_bucket": map[string]any{"bucket": issue2593Bucket}})
	if status != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(got["_raw"]), "name the S3 connection") {
		t.Errorf("an unnamed connection through the portal: %d %v", status, got["_raw"])
	}
	status, rels := s.rest(t, keyAdmin2604, http.MethodGet, "/releases", nil)
	if list, _ := rels["releases"].([]any); status != http.StatusOK || len(list) != 0 {
		t.Errorf("a refused publish left %v", rels["_raw"])
	}
}

// TestIssue2593_AMediaFolderFromABucketPrefix: manage_resource extract with
// from_bucket writes each object under the prefix as a managed resource of the
// folder, its own folders beneath it.
func TestIssue2593_AMediaFolderFromABucketPrefix(t *testing.T) {
	prefix := seed2593(t, map[string][2]string{
		"logo.svg":          {`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`, "image/svg+xml"},
		"2026/q1/chart.csv": {"a,b\n1,2\n", "text/csv"},
	})
	_, me := rest2559(t, keyOwner2604, http.MethodGet, "/api/v1/portal/me", nil)
	folder := "media2593-" + unique1579()
	got := connectAs(t, keyOwner2604).call("manage_resource", map[string]any{
		"action": "extract", "path": folder, "scope": "user", "scope_id": fmt.Sprint(me["user_id"]),
		"from_bucket": map[string]any{"connection": "dev-s3", "bucket": issue2593Bucket, "prefix": prefix},
	})
	if got["count"] != float64(2) {
		t.Fatalf("the extraction from the bucket: %v", got)
	}
	members, _ := got["members"].([]any)
	byPath := map[string]map[string]any{}
	for _, raw := range members {
		m, _ := raw.(map[string]any)
		byPath[fmt.Sprint(m["member"])] = m
	}
	if m := byPath["logo.svg"]; m == nil || m["content_type"] != "image/svg+xml" {
		t.Errorf("the SVG is filed as an image, as it was stored: %v", m)
	}
	if m := byPath["2026/q1/chart.csv"]; m == nil || m["path"] != folder+"/2026/q1" {
		t.Errorf("an object's own folders are created beneath the target: %v", m)
	}

	res, text, err := connectAs(t, keyOwner2604).callRaw("manage_resource", map[string]any{
		"action": "extract", "path": folder + "-outside", "scope": "user", "scope_id": fmt.Sprint(me["user_id"]),
		"from_bucket": map[string]any{"connection": "dev-s3-prefixed", "bucket": "dev-scratch"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, `limited to buckets whose name starts with "portal-"`) || !strings.Contains(text, "Nothing was written") {
		t.Errorf("an extraction from a bucket outside the prefix: %s", text)
	}
}

// withoutS3Object2593 creates a database persona allowed every tool but
// s3_object and every connection, and a key with an address of its own whose
// one role maps to it, so its holder can own a site; both are deleted when the
// test ends.
func withoutS3Object2593(t *testing.T) string {
	t.Helper()
	admin := connect(t)
	name := fmt.Sprintf("acc-2593-no-s3-%d", time.Now().UnixNano())
	role := strings.ReplaceAll(name, "-", "_")
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": name, "display_name": "Acceptance 2593 without s3_object", "roles": []any{role},
		"allow_tools": []any{"*"}, "deny_tools": []any{"s3_object"}, "allow_connections": []any{"*"},
	}))
	if status != http.StatusCreated {
		t.Fatalf("creating persona %s: %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody) })
	status, out = admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": name, "email": name + "@example.com", "roles": []any{role}, "description": "Acceptance 2593 without s3_object",
	}))
	key, _ := out["key"].(string)
	if (status != http.StatusCreated && status != http.StatusOK) || key == "" {
		t.Fatalf("creating key %s: %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, http.NoBody) })
	return key
}

// TestIssue2593_APersonaWithoutS3ObjectReadsNoBucket: a persona allowed the
// connection and every tool but s3_object owns a site and still reads no
// bucket: manage_site publish, the portal publish route and manage_resource
// extract each refuse naming s3_object, and nothing is published or written.
// The same prefix publishes for a persona that holds the tools.
func TestIssue2593_APersonaWithoutS3ObjectReadsNoBucket(t *testing.T) {
	marker := "denied-2593-" + unique1579()
	prefix := seed2593(t, siteTree2593(marker))
	from := map[string]any{"connection": "dev-s3", "bucket": issue2593Bucket, "prefix": prefix}
	key := withoutS3Object2593(t)
	s := newSite2604(t, key, "acc2593d-")
	c := connectAs(t, key)

	res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "from_bucket": from})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "may not use s3_object") || !strings.Contains(text, "nothing was published") {
		t.Errorf("a publish from the bucket through the tool: %s", text)
	}
	status, got := s.rest(t, key, http.MethodPost, "/publish", map[string]any{"from_bucket": from})
	if status != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(got["_raw"]), "may not use s3_object") {
		t.Errorf("a publish from the bucket through the portal: %d %v", status, got["_raw"])
	}
	status, rels := s.rest(t, keyAdmin2604, http.MethodGet, "/releases", nil)
	if list, _ := rels["releases"].([]any); status != http.StatusOK || len(list) != 0 {
		t.Errorf("a refused publish left %v", rels["_raw"])
	}

	_, me := rest2559(t, key, http.MethodGet, "/api/v1/portal/me", nil)
	folder := "denied2593-" + unique1579()
	res, text, err = c.callRaw("manage_resource", map[string]any{
		"action": "extract", "path": folder, "scope": "user", "scope_id": fmt.Sprint(me["user_id"]), "from_bucket": from,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "may not use s3_object") || !strings.Contains(text, "Nothing was written") {
		t.Errorf("an extraction from the bucket: %s", text)
	}
	listed := c.call("manage_resource", map[string]any{"action": "list", "path": folder, "scope": "user", "scope_id": fmt.Sprint(me["user_id"])})
	if strings.Contains(fmt.Sprint(listed), "index.html") {
		t.Errorf("a refused extraction wrote %v", listed)
	}

	owned := newSite2604(t, keyOwner2604, "acc2593e-")
	pub := connectAs(t, keyOwner2604).call("manage_site", map[string]any{"command": "publish", "site": owned.name, "from_bucket": from})
	if rel, _ := pub["release"].(map[string]any); rel["file_count"] != float64(3) {
		t.Errorf("the same prefix for a persona holding the tools: %v", pub)
	}
}
