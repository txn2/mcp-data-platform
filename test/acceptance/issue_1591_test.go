//go:build integration

package acceptance

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1591: the S3 toolkit registered eight tools for one subject. They are
// two now: s3_list is buckets when no bucket is named and objects when one is;
// s3_object is one action (get, metadata, put, copy, delete, presign) over a
// (bucket, key). The eight names are retired, not aliased. A connection
// configured read_only refuses the writing actions, naming the connection.
//
// Every criterion runs through the real surface against the dev stack (make
// dev), whose SeaweedFS backs two S3 connections: dev-s3, writable, and
// dev-s3-readonly, the same store bound read-only.

const (
	issue1591Bucket     = "portal-assets"
	issue1591Writable   = "dev-s3"
	issue1591ReadOnly   = "dev-s3-readonly"
	issue1591Purpose    = "Acceptance for #1591: the two S3 tools replace the eight."
	issue1591ObjectTool = "s3_object"
	issue1591ListTool   = "s3_list"
)

// issue1591Retired is the surface #1591 retired.
var issue1591Retired = []string{
	"s3_list_buckets", "s3_list_objects", "s3_get_object", "s3_get_object_metadata",
	"s3_presign_url", "s3_put_object", "s3_copy_object", "s3_delete_object",
}

// requireS3 fails, rather than skips, when the running platform has no S3
// connection: the dev stack always carries one.
func requireS3(t *testing.T, c *client) {
	t.Helper()
	tools, err := c.session.ListTools(c.ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == issue1591ObjectTool {
			return
		}
	}
	t.Fatalf("the running platform registers no %s; the dev stack (make dev) carries an S3 connection", issue1591ObjectTool)
}

// object issues one s3_object action and returns its result.
func (c *client) object(action, connection, key string, extra map[string]any) map[string]any {
	c.t.Helper()
	args := map[string]any{"action": action, "connection": connection, "bucket": issue1591Bucket, "key": key, "purpose": issue1591Purpose}
	for k, v := range extra {
		args[k] = v
	}
	return c.call(issue1591ObjectTool, args)
}

// objectErr issues one s3_object action expected to be refused and returns the
// refusal text.
func (c *client) objectErr(action, connection, key string, extra map[string]any) string {
	c.t.Helper()
	args := map[string]any{"action": action, "connection": connection, "bucket": issue1591Bucket, "key": key, "purpose": issue1591Purpose}
	for k, v := range extra {
		args[k] = v
	}
	res, text, err := c.callRaw(issue1591ObjectTool, args)
	if err != nil {
		c.t.Fatalf("%s %s: transport error: %v", issue1591ObjectTool, action, err)
	}
	if !res.IsError {
		c.t.Fatalf("%s %s on %s: expected a refusal, got %s", issue1591ObjectTool, action, connection, text)
	}
	return text
}

func (c *client) putText(connection, key, content string) {
	c.t.Helper()
	c.object("put", connection, key, map[string]any{"content": content, "content_type": "text/plain"})
	c.t.Cleanup(func() {
		_, _, _ = c.callRaw(issue1591ObjectTool, map[string]any{"action": "delete", "connection": issue1591Writable, "bucket": issue1591Bucket, "key": key, "purpose": issue1591Purpose})
	})
}

// TestIssue1591_TheTwoToolsReplaceTheEight (acceptance 5): the tool list
// carries s3_list and s3_object, none of the eight, and no other s3_ tool, so
// a persona that held the eight holds two.
func TestIssue1591_TheTwoToolsReplaceTheEight(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	tools, err := c.session.ListTools(c.ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var s3Tools []string
	for _, tool := range tools.Tools {
		if strings.HasPrefix(tool.Name, "s3_") {
			s3Tools = append(s3Tools, tool.Name)
		}
		for _, retired := range issue1591Retired {
			if tool.Name == retired {
				t.Errorf("retired tool %s is still registered", retired)
			}
		}
	}
	if len(s3Tools) != 2 {
		t.Fatalf("the S3 surface is %v; want exactly [s3_list s3_object]", s3Tools)
	}
	for _, retired := range issue1591Retired {
		res, text, err := c.callRaw(retired, map[string]any{"bucket": issue1591Bucket, "purpose": issue1591Purpose})
		if err == nil && !res.IsError {
			t.Errorf("%s still answers: %s", retired, text)
		}
	}
}

// TestIssue1591_ListBucketsAndObjects (acceptance 1, the listing half): one
// tool lists buckets with no bucket named and objects with one, keeping
// prefix, delimiter, max_keys and the continuation token.
func TestIssue1591_ListBucketsAndObjects(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	prefix := "acceptance-1591/" + stamp + "/"
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		c.putText(issue1591Writable, prefix+name, "content of "+name)
	}

	buckets := c.call(issue1591ListTool, map[string]any{"connection": issue1591Writable, "purpose": issue1591Purpose})
	found := false
	for _, b := range buckets["buckets"].([]any) {
		if b.(map[string]any)["name"] == issue1591Bucket {
			found = true
		}
	}
	if !found {
		t.Fatalf("s3_list with no bucket does not list %s: %v", issue1591Bucket, buckets)
	}

	page := c.call(issue1591ListTool, map[string]any{
		"connection": issue1591Writable, "bucket": issue1591Bucket, "prefix": prefix, "max_keys": 2, "purpose": issue1591Purpose,
	})
	if number(t, page, "count") != 2 || page["is_truncated"] != true {
		t.Fatalf("max_keys=2 over three objects: %v", page)
	}
	token, _ := page["next_continuation_token"].(string)
	if token == "" {
		t.Fatalf("a truncated page carries no continuation token: %v", page)
	}
	rest := c.call(issue1591ListTool, map[string]any{
		"connection": issue1591Writable, "bucket": issue1591Bucket, "prefix": prefix, "max_keys": 2, "continuation_token": token, "purpose": issue1591Purpose,
	})
	if number(t, rest, "count") != 1 || rest["is_truncated"] != false {
		t.Fatalf("the continuation page: %v", rest)
	}

	grouped := c.call(issue1591ListTool, map[string]any{
		"connection": issue1591Writable, "bucket": issue1591Bucket, "prefix": "acceptance-1591/", "delimiter": "/", "purpose": issue1591Purpose,
	})
	foundPrefix := false
	if cps, ok := grouped["common_prefixes"].([]any); ok {
		for _, cp := range cps {
			if cp == prefix {
				foundPrefix = true
			}
		}
	}
	if !foundPrefix {
		t.Fatalf("delimiter '/' does not group %s: %v", prefix, grouped)
	}
}

// TestIssue1591_ObjectActions (acceptance 1, the object half): every operation
// the six object tools performed is one action of s3_object against the real
// backend, the presigned URLs included.
func TestIssue1591_ObjectActions(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	key := "acceptance-1591/" + stamp + "/report.txt"
	body := "quarterly report " + stamp

	put := c.object("put", issue1591Writable, key, map[string]any{"content": body, "content_type": "text/plain", "metadata": map[string]any{"author": "acceptance"}})
	t.Cleanup(func() {
		_, _, _ = c.callRaw(issue1591ObjectTool, map[string]any{"action": "delete", "connection": issue1591Writable, "bucket": issue1591Bucket, "key": key, "purpose": issue1591Purpose})
	})
	if number(t, put, "size") != float64(len(body)) {
		t.Fatalf("put size: %v", put)
	}

	got := c.object("get", issue1591Writable, key, nil)
	if got["content"] != body || got["is_base64"] != false {
		t.Fatalf("get: %v", got)
	}

	meta := c.object("metadata", issue1591Writable, key, nil)
	if number(t, meta, "size") != float64(len(body)) || meta["content"] != nil {
		t.Fatalf("metadata: %v", meta)
	}
	if md, _ := meta["metadata"].(map[string]any); md["author"] != "acceptance" {
		t.Fatalf("metadata does not carry the custom metadata put attached: %v", meta)
	}

	binKey := "acceptance-1591/" + stamp + "/blob.bin"
	raw := []byte{0x00, 0xff, 0x10, 0x80}
	c.object("put", issue1591Writable, binKey, map[string]any{"content": base64.StdEncoding.EncodeToString(raw), "is_base64": true, "content_type": "application/octet-stream"})
	t.Cleanup(func() {
		_, _, _ = c.callRaw(issue1591ObjectTool, map[string]any{"action": "delete", "connection": issue1591Writable, "bucket": issue1591Bucket, "key": binKey, "purpose": issue1591Purpose})
	})
	gotBin := c.object("get", issue1591Writable, binKey, nil)
	decoded, err := base64.StdEncoding.DecodeString(gotBin["content"].(string))
	if err != nil || gotBin["is_base64"] != true || !bytes.Equal(decoded, raw) {
		t.Fatalf("binary get: %v (%v)", gotBin, err)
	}

	copyKey := "acceptance-1591/" + stamp + "/report-copy.txt"
	copied := c.object("copy", issue1591Writable, key, map[string]any{"dest_key": copyKey})
	t.Cleanup(func() {
		_, _, _ = c.callRaw(issue1591ObjectTool, map[string]any{"action": "delete", "connection": issue1591Writable, "bucket": issue1591Bucket, "key": copyKey, "purpose": issue1591Purpose})
	})
	if copied["dest_bucket"] != issue1591Bucket || copied["dest_key"] != copyKey {
		t.Fatalf("copy: %v", copied)
	}
	if c.object("get", issue1591Writable, copyKey, nil)["content"] != body {
		t.Fatal("the copy does not carry the source's content")
	}

	presigned := c.object("presign", issue1591Writable, key, map[string]any{"expires_in": 120})
	if presigned["method"] != "GET" || number(t, presigned, "expires_in_seconds") != 120 {
		t.Fatalf("presign: %v", presigned)
	}
	if fetched := httpBody(t, http.MethodGet, presigned["url"].(string), nil); fetched != body {
		t.Fatalf("the presigned GET URL serves %q, want %q", fetched, body)
	}

	upKey := "acceptance-1591/" + stamp + "/uploaded.txt"
	upload := c.object("presign", issue1591Writable, upKey, map[string]any{"method": "PUT", "expires_in": 120})
	t.Cleanup(func() {
		_, _, _ = c.callRaw(issue1591ObjectTool, map[string]any{"action": "delete", "connection": issue1591Writable, "bucket": issue1591Bucket, "key": upKey, "purpose": issue1591Purpose})
	})
	if upload["method"] != "PUT" {
		t.Fatalf("presign PUT: %v", upload)
	}
	httpBody(t, http.MethodPut, upload["url"].(string), strings.NewReader("uploaded through the link"))
	if c.object("get", issue1591Writable, upKey, nil)["content"] != "uploaded through the link" {
		t.Fatal("the presigned PUT URL did not write the object")
	}

	deleted := c.object("delete", issue1591Writable, copyKey, nil)
	if deleted["deleted"] != true {
		t.Fatalf("delete: %v", deleted)
	}
	if refusal := c.objectErr("get", issue1591Writable, copyKey, nil); !strings.Contains(refusal, "failed to get object") {
		t.Fatalf("a get after delete: %s", refusal)
	}
}

// TestIssue1591_ReadOnlyConnectionRefusesWritesNamingIt (acceptance 2): put,
// copy and delete are refused on the read-only connection, the refusal names
// the connection, and the reads still work on it.
func TestIssue1591_ReadOnlyConnectionRefusesWritesNamingIt(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	key := "acceptance-1591/" + stamp + "/frozen.txt"
	c.putText(issue1591Writable, key, "frozen")

	for action, extra := range map[string]map[string]any{
		"put":    {"content": "overwrite"},
		"copy":   {"dest_key": key + ".copy"},
		"delete": nil,
	} {
		refusal := c.objectErr(action, issue1591ReadOnly, key, extra)
		want := fmt.Sprintf(`connection "%s" is read-only`, issue1591ReadOnly)
		if !strings.Contains(refusal, want) || !strings.Contains(refusal, fmt.Sprintf(`action "%s"`, action)) {
			t.Errorf("%s on the read-only connection: %s; want it to name the connection and the action", action, refusal)
		}
	}
	if c.object("get", issue1591Writable, key, nil)["content"] != "frozen" {
		t.Fatal("a refused write changed the object")
	}
	if c.object("get", issue1591ReadOnly, key, nil)["content"] != "frozen" {
		t.Fatal("get on the read-only connection")
	}
	c.object("metadata", issue1591ReadOnly, key, nil)
	c.object("presign", issue1591ReadOnly, key, nil)
	listed := c.call(issue1591ListTool, map[string]any{"connection": issue1591ReadOnly, "bucket": issue1591Bucket, "prefix": "acceptance-1591/" + stamp + "/", "purpose": issue1591Purpose})
	if number(t, listed, "count") != 1 {
		t.Fatalf("s3_list on the read-only connection: %v", listed)
	}
}

// TestIssue1591_FindToolsReturnsTheConsolidatedTools (acceptance 4): the
// intents that used to return an individual tool return the consolidated one.
func TestIssue1591_FindToolsReturnsTheConsolidatedTools(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	for intent, want := range map[string]string{
		"upload a file to a bucket": issue1591ObjectTool,
		"get a download link":       issue1591ObjectTool,
		"browse a data lake":        issue1591ListTool,
	} {
		out := c.call("platform_find_tools", map[string]any{"query": intent})
		var names []string
		for _, tool := range out["tools"].([]any) {
			names = append(names, tool.(map[string]any)["name"].(string))
		}
		found := false
		for _, name := range names {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("platform_find_tools %q returned %v; want %s among them (note: %v)", intent, names, want, out["note"])
		}
	}
}

// httpBody performs one plain HTTP request against a presigned URL and returns
// the response body, failing on a non-2xx status.
func httpBody(t *testing.T, method, url string, body io.Reader) string {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		t.Fatalf("%s %s: %d %s", method, url, res.StatusCode, raw)
	}
	return string(raw)
}
