//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2584: an S3 connection's bucket_prefix was described as restricting
// the connection to matching buckets, but only the bucket listing read it:
// s3_list with a named bucket and every s3_object action reached any bucket the
// credential could. A connection that sets the prefix now refuses every call
// naming a bucket outside it, a copy's dest_bucket included, naming the
// connection and the prefix.
//
// Every criterion runs against the dev stack (make dev), whose SeaweedFS backs
// dev-s3 (no prefix) and dev-s3-prefixed (bucket_prefix "portal-"): the bucket
// portal-lake is inside the limit and dev-scratch outside it.
//
// Wire forms: bucket, dest_bucket, key, dest_key, action and method are JSON
// strings, the only form their schemas admit; method is sent as "PUT" and
// omitted (GET); expires_in is a JSON number. Each refusal is checked for
// every action s3_object takes and for s3_list with a bucket.

const (
	issue2584Prefixed = "dev-s3-prefixed"
	issue2584Outside  = "dev-scratch"
	issue2584Purpose  = "Acceptance for #2584: bucket_prefix limits a connection."
)

func issue2584Refusal(bucket string) string {
	return fmt.Sprintf(`connection "%s" is limited to buckets whose name starts with "portal-": bucket "%s" is outside it`,
		issue2584Prefixed, bucket)
}

// TestIssue2584_EveryActionIsRefusedOutsideThePrefix: each s3_object action
// and s3_list with a bucket, against a bucket outside the prefix that exists
// and that dev-s3 reaches, is refused with the connection and prefix named and
// carries no URL.
func TestIssue2584_EveryActionIsRefusedOutsideThePrefix(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	key := fmt.Sprintf("acceptance-2584/%d/outside.txt", time.Now().UnixNano())
	// The object exists, so a refusal is the limit and not a missing key.
	c.call(issue1591ObjectTool, map[string]any{
		"action": "put", "connection": issue1591Writable, "bucket": issue2584Outside,
		"key": key, "content": "outside the prefix", "content_type": "text/plain", "purpose": issue2584Purpose,
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw(issue1591ObjectTool, map[string]any{
			"action": "delete", "connection": issue1591Writable,
			"bucket": issue2584Outside, "key": key, "purpose": issue2584Purpose,
		})
	})
	want := issue2584Refusal(issue2584Outside)

	calls := []map[string]any{
		{"action": "get"},
		{"action": "metadata"},
		{"action": "put", "content": "overwritten", "content_type": "text/plain"},
		{"action": "copy", "dest_key": key + ".copy"},
		{"action": "delete"},
		{"action": "presign", "expires_in": 60},
		{"action": "presign", "method": "PUT", "expires_in": 60},
	}
	for _, args := range calls {
		args["connection"], args["bucket"], args["key"], args["purpose"] = issue2584Prefixed, issue2584Outside, key, issue2584Purpose
		res, text, err := c.callRaw(issue1591ObjectTool, args)
		if err != nil {
			t.Fatalf("s3_object %v: %v", args["action"], err)
		}
		if !res.IsError || !strings.Contains(text, want) {
			t.Errorf("s3_object %v %v outside the prefix: %q; want %q", args["action"], args["method"], text, want)
		}
		if strings.Contains(text, "X-Amz-") {
			t.Errorf("the refusal carries a signed URL: %q", text)
		}
	}
	res, text, err := c.callRaw(issue1591ListTool, map[string]any{"connection": issue2584Prefixed, "bucket": issue2584Outside, "purpose": issue2584Purpose})
	if err != nil {
		t.Fatalf("s3_list: %v", err)
	}
	if !res.IsError || !strings.Contains(text, want) {
		t.Errorf("s3_list with a bucket outside the prefix: %q; want %q", text, want)
	}

	got := c.call(issue1591ObjectTool, map[string]any{"action": "get", "connection": issue1591Writable, "bucket": issue2584Outside, "key": key, "purpose": issue2584Purpose})
	if got["content"] != "outside the prefix" {
		t.Errorf("the object outside the prefix changed: %v", got["content"])
	}
	if refusal := c.objectErr("metadata", issue1591Writable, key+".copy", map[string]any{"bucket": issue2584Outside}); !strings.Contains(refusal, "failed to get object metadata") {
		t.Errorf("a refused copy wrote its destination: %s", refusal)
	}
}

// TestIssue2584_ACopyCannotLeaveThePrefix: a copy from a bucket inside the
// prefix to dest_bucket outside it is refused, and nothing lands there.
func TestIssue2584_ACopyCannotLeaveThePrefix(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	key := fmt.Sprintf("acceptance-2584/%d/inside.txt", time.Now().UnixNano())
	c.putText(issue1591Writable, key, "inside the prefix")

	refusal := c.objectErr("copy", issue2584Prefixed, key, map[string]any{"dest_bucket": issue2584Outside, "dest_key": key})
	if want := issue2584Refusal(issue2584Outside); !strings.Contains(refusal, want) {
		t.Errorf("copy out of the prefix: %q; want %q", refusal, want)
	}
	if missing := c.objectErr("metadata", issue1591Writable, key, map[string]any{"bucket": issue2584Outside}); !strings.Contains(missing, "failed to get object metadata") {
		t.Errorf("the refused copy landed in %s: %s", issue2584Outside, missing)
	}
}

// TestIssue2584_InsideThePrefixIsServed: on the prefixed connection a bucket
// inside the prefix is listed, read, copied within the prefix and presigned.
func TestIssue2584_InsideThePrefixIsServed(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	key := fmt.Sprintf("acceptance-2584/%d/served.txt", time.Now().UnixNano())
	c.putText(issue1591Writable, key, "served")

	if got := c.object("get", issue2584Prefixed, key, nil)["content"]; got != "served" {
		t.Errorf("get inside the prefix: %v", got)
	}
	listed := c.call(issue1591ListTool, map[string]any{"connection": issue2584Prefixed, "bucket": issue1591Bucket, "prefix": key, "purpose": issue2584Purpose})
	if n, _ := listed["count"].(float64); n != 1 {
		t.Errorf("s3_list inside the prefix: %v", listed)
	}
	c.object("copy", issue2584Prefixed, key, map[string]any{"dest_bucket": issue1591Bucket, "dest_key": key + ".copy"})
	t.Cleanup(func() {
		_, _, _ = c.callRaw(issue1591ObjectTool, map[string]any{
			"action": "delete", "connection": issue1591Writable,
			"bucket": issue1591Bucket, "key": key + ".copy", "purpose": issue2584Purpose,
		})
	})
	if link := c.object("presign", issue2584Prefixed, key, map[string]any{"expires_in": 60}); link["method"] != "GET" {
		t.Errorf("presign inside the prefix: %v", link)
	}

	buckets := c.call(issue1591ListTool, map[string]any{"connection": issue2584Prefixed, "purpose": issue2584Purpose})
	list, _ := buckets["buckets"].([]any)
	for _, b := range list {
		if name, _ := b.(map[string]any)["name"].(string); !strings.HasPrefix(name, "portal-") {
			t.Errorf("the bucket listing shows %q outside the prefix", name)
		}
	}
}

// TestIssue2584_NoPrefixLimitsNothing: dev-s3 sets no prefix, and the same
// bucket the prefixed connection refuses is served on it.
func TestIssue2584_NoPrefixLimitsNothing(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	listed := c.call(issue1591ListTool, map[string]any{"connection": issue1591Writable, "bucket": issue2584Outside, "max_keys": 1, "purpose": issue2584Purpose})
	if listed["bucket"] != issue2584Outside {
		t.Errorf("s3_list on dev-s3: %v", listed)
	}
}
