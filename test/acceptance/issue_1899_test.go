//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Issue #1899: the platform reports how full its database and buckets are and
// whether objects and rows agree. The table sampler reads each growing
// table's size, each HNSW index's size and the transaction id age; an upload
// moves its bucket's count without waiting for the next full listing; and the
// listing counts an object no row references and a row whose object is gone.
//
// Wire forms: the upload is the REST route (multipart, one form); s3_object's
// action, connection, bucket, key, content, content_type and purpose are typed
// strings, each sent as a literal tools/call parameter of that one form.
//
// Stack: `make dev`, whose dev/start.sh sets MCP_PLATFORM_CAPACITY_INTERVAL
// and MCP_PLATFORM_STORAGE_SCAN_INTERVAL to 30s and the orphan grace to 0s,
// with SeaweedFS as the object store.

const (
	issue1899Purpose = "Acceptance #1899: storage capacity and integrity."
	issue1899Bucket  = "managed-resources"
	// issue1899Resources is the connection the dev stack keeps managed
	// resources on: a store of its own, apart from the portal's SeaweedFS.
	issue1899Resources = "dev-resources"
)

// issue1899HNSWIndexes is every HNSW index the migrations leave in place
// (000178 dropped the scripts' own).
var issue1899HNSWIndexes = []string{
	"idx_call_records_embedding", "idx_catalog_datasets_embedding_hnsw", "idx_memory_records_embedding_hnsw",
	"idx_portal_assets_embedding_hnsw", "idx_portal_collections_embedding_hnsw",
	"idx_portal_knowledge_page_chunks_embedding_hnsw", "idx_prompts_embedding_hnsw", "idx_resources_embedding_hnsw",
}

// oneReplica is one replica's scrape: the capacity gauges carry the same value
// on every replica, so reading one is reading the shared row.
func oneReplica(t *testing.T) string {
	t.Helper()
	return scrapeOne(t, metricsURLs()[0])
}

// scrapeScalar is the value of a series with no labels in one scrape body,
// zero when it is absent.
func scrapeScalar(body, name string) float64 {
	for _, line := range strings.Split(body, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == name {
			v, _ := strconv.ParseFloat(fields[1], 64)
			return v
		}
	}
	return 0
}

// hasSeries reports whether the scrape body carries the named series.
func hasSeries(body, name string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, name+" ") || strings.HasPrefix(line, name+"{") {
			return true
		}
	}
	return false
}

// waitForFreshListing waits for a full bucket listing that began after since:
// one finished after since, then the next one. A listing skips objects
// modified after it began, so only one that began after since counts what
// was done before it.
func waitForFreshListing(t *testing.T, since time.Time) {
	t.Helper()
	first := waitForListing(t, float64(since.UnixNano())/1e9)
	waitForListing(t, first)
}

// waitForListing waits until a full bucket listing has finished after the
// given Unix time, on any replica (the listing loop's last success is newer),
// and returns that time.
func waitForListing(t *testing.T, after float64) float64 {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		var newest float64
		for _, line := range strings.Split(scrapeRaw(t), "\n") {
			if strings.HasPrefix(line, `background_loop_last_success_timestamp_seconds{loop="storage_scan"}`) {
				if f := strings.Fields(line); len(f) == 2 {
					v, _ := strconv.ParseFloat(f[1], 64)
					newest = max(newest, v)
				}
			}
		}
		if newest > after {
			return newest
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("no bucket listing finished after %s (MCP_PLATFORM_STORAGE_SCAN_INTERVAL is 30s on the dev stack)",
		time.Unix(int64(after), 0).Format(time.TimeOnly))
	return 0
}

// waitForGauge polls one replica until pred holds for the named series, and
// returns the last value read.
func waitForGauge(t *testing.T, name string, labels map[string]string, within time.Duration, pred func(float64) bool) float64 {
	t.Helper()
	deadline := time.Now().Add(within)
	var v float64
	for {
		v = metricSeries(oneReplica(t), name, labels)
		if pred(v) || time.Now().After(deadline) {
			return v
		}
		time.Sleep(time.Second)
	}
}

// TestIssue1899_TheTableSamplerReportsSizesAndTheTransactionAge: against the
// dev stack, the table sampler reports a size for audit_logs and for each HNSW
// index, and a transaction id age.
func TestIssue1899_TheTableSamplerReportsSizesAndTheTransactionAge(t *testing.T) {
	body := oneReplica(t)
	if v := metricSeries(body, "db_table_size_bytes", map[string]string{"table": "audit_logs"}); v <= 0 {
		t.Fatalf("db_table_size_bytes{table=\"audit_logs\"} = %v, want a size", v)
	}
	for _, idx := range issue1899HNSWIndexes {
		if v := metricSeries(body, "db_vector_index_size_bytes", map[string]string{"index": idx}); v <= 0 {
			t.Errorf("db_vector_index_size_bytes{index=%q} = %v, want a size", idx, v)
		}
	}
	if v := scrapeScalar(body, "db_transaction_id_age"); v <= 0 {
		t.Fatalf("db_transaction_id_age = %v, want an age", v)
	}
}

// TestIssue1899_AnUploadRaisesTheResourcesCountBeforeTheListing: uploading a
// resource raises the resources bytes and object count without waiting for the
// full scan. The scan reports the objects it read; an upload whose count rose
// while that figure stayed put was counted by the write, not the listing. A
// listing that lands in between is retried once.
func TestIssue1899_AnUploadRaisesTheResourcesCountBeforeTheListing(t *testing.T) {
	c := connect(t)
	labels := map[string]string{"bucket": issue1899Bucket, "purpose": "resources"}
	for attempt := 1; attempt <= 3; attempt++ {
		body := oneReplica(t)
		objects := metricSeries(body, "storage_bucket_objects", labels)
		bytes := metricSeries(body, "storage_bucket_bytes", labels)
		scanned := scrapeScalar(body, "storage_scan_objects")

		uploadResource1568(t, c, fmt.Sprintf("capacity-%d.csv", time.Now().UnixNano()), "text/csv", []byte("a,b\n1,2\n"))
		got := waitForGauge(t, "storage_bucket_objects", labels, 25*time.Second, func(v float64) bool { return v >= objects+1 })
		after := oneReplica(t)
		if scrapeScalar(after, "storage_scan_objects") != scanned {
			t.Logf("attempt %d: a listing ran during the upload; trying again", attempt)
			continue
		}
		if got < objects+1 {
			t.Fatalf("storage_bucket_objects{purpose=\"resources\"} stayed at %v after an upload", got)
		}
		if b := metricSeries(after, "storage_bucket_bytes", labels); b <= bytes {
			t.Fatalf("storage_bucket_bytes{purpose=\"resources\"} = %v, was %v: the upload's bytes were not added", b, bytes)
		}
		return
	}
	t.Fatal("every attempt overlapped a listing; raise DEV_STORAGE_SCAN_INTERVAL and run again")
}

// TestIssue1899_TheReconcileCountsOneOrphanAndOneDangling: with one object
// seeded under a platform prefix with no row, and one row pointing at a
// deleted object, the reconcile reports one orphan and one dangling reference.
// Both are made behind the platform's back through s3_object, which writes to
// the bucket directly.
func TestIssue1899_TheReconcileCountsOneOrphanAndOneDangling(t *testing.T) {
	c := connect(t)
	labels := map[string]string{"purpose": "resources"}
	// The baseline is read after a listing that began after this test did, so
	// an earlier test's seeds, cleaned up since, are not still in it.
	waitForFreshListing(t, time.Now())
	body := oneReplica(t)
	orphans := metricSeries(body, "storage_orphaned_objects", labels)
	dangling := metricSeries(body, "storage_dangling_references", labels)

	defer seedOrphanAndDangling1899(t, c)()
	waitForFreshListing(t, time.Now())

	body = oneReplica(t)
	gotOrphans := metricSeries(body, "storage_orphaned_objects", labels)
	gotDangling := metricSeries(body, "storage_dangling_references", labels)
	if gotOrphans != orphans+1 {
		t.Errorf("storage_orphaned_objects{purpose=\"resources\"} = %v, want %v", gotOrphans, orphans+1)
	}
	if gotDangling != dangling+1 {
		t.Errorf("storage_dangling_references{purpose=\"resources\"} = %v, want %v", gotDangling, dangling+1)
	}
}

// TestIssue1899_TheListingReportsItsOwnWork: the sampler's own duration and
// objects-scanned metrics are present, and each bucket is labeled with the
// store its own connection reaches: the dev portal's SeaweedFS, and for
// managed resources a TLS store that names itself as nothing the platform
// recognizes.
func TestIssue1899_TheListingReportsItsOwnWork(t *testing.T) {
	body := oneReplica(t)
	if v := scrapeScalar(body, "storage_scan_objects"); v <= 0 {
		t.Errorf("storage_scan_objects = %v, want the objects the last listing read", v)
	}
	if !hasSeries(body, "storage_scan_duration_seconds") {
		t.Error("storage_scan_duration_seconds is absent")
	}
	if v := metricSeries(body, "mcp_platform_storage_backend_info", map[string]string{"backend": "seaweedfs"}); v != 1 {
		t.Errorf("mcp_platform_storage_backend_info{backend=\"seaweedfs\"} = %v, want 1", v)
	}
	if v := metricSeries(body, "storage_bucket_objects", map[string]string{"bucket": "portal-assets", "backend": "seaweedfs"}); v <= 0 {
		t.Errorf("portal-assets is not labeled with its SeaweedFS store")
	}
	if v := metricSeries(body, "storage_bucket_objects", map[string]string{"bucket": issue1899Bucket, "backend": "other"}); v <= 0 {
		t.Errorf("%s is not labeled with its own connection's store", issue1899Bucket)
	}
}

// seedOrphanAndDangling1899 makes one object under the resources prefix that no
// row names, and one resource row whose object is then deleted, both behind
// the platform's back through s3_object on the bucket's own connection. It
// returns the cleanup, which the caller defers: it removes both through a
// fresh client (a client's session outlives a long criterion only so far) and
// fails the test when it cannot, so a later run's baseline is never another
// run's leftovers.
func seedOrphanAndDangling1899(t *testing.T, c *client) (cleanup func()) {
	t.Helper()
	stamp := time.Now().UnixNano()
	orphanKey := fmt.Sprintf("resources/global/global/acceptance-1899-%d/orphan.txt", stamp)
	c.call("s3_object", map[string]any{"action": "put", "connection": issue1899Resources, "bucket": issue1899Bucket,
		"key": orphanKey, "content": "no row names me", "content_type": "text/plain", "purpose": issue1899Purpose})

	id, _ := uploadResource1568(t, c, fmt.Sprintf("dangling-%d.txt", stamp), "text/plain", []byte("my object goes away"))
	status, res := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	key, _ := res["s3_key"].(string)
	if status != http.StatusOK || key == "" {
		t.Fatalf("reading the resource back: %d %v", status, res)
	}
	c.call("s3_object", map[string]any{"action": "delete", "connection": issue1899Resources, "bucket": issue1899Bucket,
		"key": key, "purpose": issue1899Purpose})

	return func() {
		fresh := connect(t)
		if _, text, err := fresh.callRaw("s3_object", map[string]any{"action": "delete", "connection": issue1899Resources,
			"bucket": issue1899Bucket, "key": orphanKey, "purpose": issue1899Purpose}); err != nil {
			t.Errorf("removing the seeded orphan %s: %v %s", orphanKey, err, text)
		}
		if status, body := fresh.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody); status != http.StatusNoContent && status != http.StatusNotFound {
			t.Errorf("removing the seeded resource %s: %d %v", id, status, body)
		}
	}
}
