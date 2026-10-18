//go:build integration

package acceptance

import (
	"net/http"
	"slices"
	"testing"
)

// Acceptance for #2181: the storage report names the buckets whose endpoint
// answers as SeaweedFS, which is what the Storage settings offer the cluster's
// address on and what the Storage page names the cluster by. The dev stack's
// object store is SeaweedFS, so its portal bucket is named.
//
// Wire forms: GET /api/v1/admin/storage takes no parameters and no body.

// TestIssue2181_TheReportNamesTheBucketsOnSeaweedFS: the report carries
// seaweedfs_buckets as a JSON list, and the portal-assets bucket, on the dev
// stack's SeaweedFS, is in it.
func TestIssue2181_TheReportNamesTheBucketsOnSeaweedFS(t *testing.T) {
	c := connect(t)
	status, rep := c.rest(http.MethodGet, "/api/v1/admin/storage", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/storage: %d %v", status, rep)
	}
	raw, ok := rep["seaweedfs_buckets"].([]any)
	if !ok {
		t.Fatalf("seaweedfs_buckets is not a list: %#v", rep["seaweedfs_buckets"])
	}
	names := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		names = append(names, s)
	}
	if !slices.Contains(names, "portal-assets") {
		t.Fatalf("seaweedfs_buckets %v does not name portal-assets, which is on the dev SeaweedFS", names)
	}
}
