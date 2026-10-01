//go:build integration

package acceptance

import (
	"net/http"
	"testing"
	"time"
)

// Acceptance for #2000: PUT /api/v1/admin/webhooks/sources/{name} answers with
// the updated_at the update stamped, the one a following GET returns. The
// admin API takes one JSON object per route.

// TestIssue2000_APutAnswersWithTheUpdatedAtItStamped changes a source's
// compaction window and retention, and compares the PUT's updated_at with the
// source's created_at and with what a GET returns.
func TestIssue2000_APutAnswersWithTheUpdatedAtItStamped(t *testing.T) {
	c := connect(t)
	name := issue1870Name("upd2000")
	issue1870Create(t, c, issue1870HMAC(name, "acceptance-2000", map[string]any{"compact_every_minutes": 5}))

	status, put := c.rest(http.MethodPut, "/api/v1/admin/webhooks/sources/"+name, jsonBody(t, map[string]any{
		"auth":   map[string]any{"mode": "hmac", "signature_header": "X-Signature", "prefix": "sha256="},
		"config": map[string]any{"compact_every_minutes": 60, "compacted_retention_days": 90},
	}))
	if status != http.StatusOK {
		t.Fatalf("PUT: %d %v", status, put)
	}
	created, _ := time.Parse(time.RFC3339Nano, put["created_at"].(string))
	updated, _ := time.Parse(time.RFC3339Nano, put["updated_at"].(string))
	if !updated.After(created) {
		t.Fatalf("the PUT answered updated_at %s, not after created_at %s", updated, created)
	}

	_, got := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
	src, _ := got["source"].(map[string]any)
	if src["updated_at"] != put["updated_at"] {
		t.Errorf("the PUT answered updated_at %v, a GET returns %v", put["updated_at"], src["updated_at"])
	}
}
