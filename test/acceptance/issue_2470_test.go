//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #2470: the full bucket listing reclaims the orphans it finds. An
// object no row references, older than the reclaim age, is deleted and no
// longer counted as orphaned; an object younger than the reclaim age, and one
// a row names, is left; the deletes are counted per purpose.
//
// Wire forms: the upload is the REST route (multipart, one form); s3_object's
// action, connection, bucket, key, content, content_type and purpose are typed
// strings, each sent as a literal tools/call parameter of that one form.
//
// Stack: `make dev`, whose dev/start.sh lists the buckets every 30s, counts an
// object with no row as an orphan at once, and reclaims one older than
// DEV_STORAGE_RECLAIM_AGE (3m) with MCP_PLATFORM_STORAGE_RECLAIM left at its
// default, delete.

const (
	issue2470Purpose    = "Acceptance #2470: reclaiming orphaned objects."
	issue2470ReclaimAge = 3 * time.Minute
)

// objectExists2470 reports whether the managed-resources bucket holds key,
// asked through s3_object on the bucket's own connection.
func objectExists2470(t *testing.T, c *client, key string) bool {
	t.Helper()
	res, _, err := c.callRaw("s3_object", map[string]any{
		"action": "metadata", "connection": issue1899Resources, "bucket": issue1899Bucket,
		"key": key, "purpose": issue2470Purpose,
	})
	if err != nil {
		t.Fatalf("s3_object metadata %s: transport error: %v", key, err)
	}
	return !res.IsError
}

// putOrphan2470 writes an object under the resources prefix that no row names,
// behind the platform's back, and removes it when the test ends if the
// reclaim did not.
func putOrphan2470(t *testing.T, c *client, name string) string {
	t.Helper()
	key := fmt.Sprintf("resources/global/global/acceptance-2470-%d/%s", time.Now().UnixNano(), name)
	c.call("s3_object", map[string]any{
		"action": "put", "connection": issue1899Resources, "bucket": issue1899Bucket,
		"key": key, "content": "no row names me", "content_type": "text/plain", "purpose": issue2470Purpose,
	})
	t.Cleanup(func() {
		_, _, _ = connect(t).callRaw("s3_object", map[string]any{
			"action": "delete", "connection": issue1899Resources, "bucket": issue1899Bucket,
			"key": key, "purpose": issue2470Purpose,
		})
	})
	return key
}

// TestIssue2470_AnOldOrphanIsReclaimedAndAYoungOneAndAReferencedOneAreKept:
// an orphan older than the reclaim age is gone after the next listing and
// counted in storage_reclaimed_objects{purpose="resources"}; an orphan written
// just before that listing, and a resource uploaded through the platform
// before the old orphan, are both still in the bucket.
func TestIssue2470_AnOldOrphanIsReclaimedAndAYoungOneAndAReferencedOneAreKept(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	oldKey := putOrphan2470(t, c, "old.txt")
	written := time.Now()

	id, _ := uploadResource1568(t, c, fmt.Sprintf("kept-2470-%d.txt", written.UnixNano()), "text/plain", []byte("a row names me"))
	status, res := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	referencedKey, _ := res["s3_key"].(string)
	if status != http.StatusOK || referencedKey == "" {
		t.Fatalf("reading the uploaded resource back: %d %v", status, res)
	}
	t.Cleanup(func() { connect(t).rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })

	// Every listing until the old orphan is past the reclaim age counts it
	// and leaves it.
	waitForFreshListing(t, time.Now())
	if !objectExists2470(t, c, oldKey) {
		t.Fatal("the orphan was deleted before it was older than the reclaim age")
	}
	time.Sleep(time.Until(written.Add(issue2470ReclaimAge)))

	// storage_reclaimed_objects is what the last listing deleted, so it is
	// read on every poll from the moment the old orphan is eligible, before a
	// later listing (at least 27 seconds on) sets it again.
	youngKey := putOrphan2470(t, c, "young.txt")
	youngAt := time.Now()
	var reclaimed float64
	gone := false
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		reclaimed = max(reclaimed, metricSeries(oneReplica(t), "storage_reclaimed_objects", map[string]string{"purpose": "resources"}))
		gone = gone || !objectExists2470(t, c, oldKey)
		if gone && reclaimed >= 1 {
			break
		}
	}
	if !gone {
		t.Fatalf("the orphan older than the reclaim age is still in the bucket")
	}
	if reclaimed < 1 {
		t.Errorf("storage_reclaimed_objects{purpose=\"resources\"} never read at least 1 after the orphan was reclaimed")
	}

	// A listing that began after the young orphan was written met it and,
	// since it is younger than the reclaim age, left it.
	waitForFreshListing(t, youngAt)
	if !objectExists2470(t, c, youngKey) {
		t.Errorf("the orphan younger than the reclaim age was deleted")
	}
	if !objectExists2470(t, c, referencedKey) {
		t.Errorf("the object a resource row names was deleted")
	}
}
