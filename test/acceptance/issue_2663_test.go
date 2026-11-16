//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// Acceptance for #2663: a reference whose row and object are removed while
// the storage listing walks the buckets is not counted as dangling. Resources
// are uploaded and deleted through the REST routes, one a second,
// across three full listings; storage_dangling_references{purpose="resources"}
// read after each listing never rises above the baseline read before. A row
// whose object is really gone is still counted (TestIssue1899_
// TheReconcileCountsOneOrphanAndOneDangling, unchanged).
//
// Wire forms: the upload is the REST route (multipart, one form) and the
// delete is DELETE /api/v1/resources/{id}; no tool parameter is sent.
//
// Stack: `make dev`, whose dev/start.sh runs the storage listing every 30s.

// TestIssue2663_ReferencesRemovedDuringTheListingAreNotDangling: resources
// written and removed while three listings run leave the dangling count where
// it was.
func TestIssue2663_ReferencesRemovedDuringTheListingAreNotDangling(t *testing.T) {
	c := connectFor(t, 6*time.Minute)
	labels := map[string]string{"purpose": "resources"}
	waitForFreshListing(t, time.Now())
	baseline := metricSeries(oneReplica(t), "storage_dangling_references", labels)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var churned int
	wg.Add(1)
	go func() {
		defer wg.Done()
		admin := connect(t)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(time.Second):
			}
			id, _ := uploadResource1568(t, c, fmt.Sprintf("churn-2663-%d-%d.txt", time.Now().UnixNano(), i), "text/plain", []byte("replaced at once"))
			if status, body := admin.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody); status != http.StatusNoContent {
				t.Errorf("deleting churned resource %s: %d %v", id, status, body)
				return
			}
			churned++
		}
	}()

	after := float64(time.Now().UnixNano()) / 1e9
	for pass := 1; pass <= 3; pass++ {
		after = waitForListing(t, after)
		if got := metricSeries(oneReplica(t), "storage_dangling_references", labels); got > baseline {
			t.Errorf("after listing %d, storage_dangling_references{purpose=\"resources\"} = %v, above the baseline %v", pass, got, baseline)
		}
	}
	close(stop)
	wg.Wait()
	if churned < 10 {
		t.Fatalf("only %d resources were written and removed during the listings; the criterion needs the listings to overlap removals", churned)
	}
}
