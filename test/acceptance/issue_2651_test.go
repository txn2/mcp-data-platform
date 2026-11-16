//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Acceptance for #2651: a delivery's acknowledgement is counted in
// webhook_ack_seconds with bucket edges at 1.5, 2 and 3 seconds, the status
// route reads the span from and to name without range=custom, and the
// acknowledgement history it answers carries p99_ms estimated within its
// bucket (never above max_ms) beside max_ms.
//
// Wire forms: the webhook source is created with a JSON object; the delivery
// is one signed JSON body; GET /api/v1/admin/webhooks/status takes source,
// from and to (RFC 3339) as query parameters, with and without range=custom.

// TestIssue2651_TheAckHistogramHasEdgesAroundSenderTimeouts: after one
// delivery the scrape carries webhook_ack_seconds buckets at 1.5, 2 and 3.
func TestIssue2651_TheAckHistogramHasEdgesAroundSenderTimeouts(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc2651")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_2651", map[string]any{"event_id_path": "$.id"}))
	body := []byte(`{"id":"ack-2651"}`)
	if res, text := issue1870Signed(t, name, "whsec_2651", "application/json", body); res.StatusCode != http.StatusAccepted {
		t.Fatalf("the delivery answered %d: %s", res.StatusCode, text)
	}
	scrape := scrapeRaw(t)
	for _, le := range []string{"1.5", "2", "3"} {
		if metricSeries(scrape, "webhook_ack_seconds_bucket", map[string]string{"source": name, "le": le}) < 1 {
			t.Errorf("webhook_ack_seconds has no bucket le=%q counting the delivery", le)
		}
	}
}

// TestIssue2651_TheStatusRouteReadsFromAndTo: from and to without range read
// that span and say custom; each acknowledgement point's p99_ms is no more
// than its max_ms.
func TestIssue2651_TheStatusRouteReadsFromAndTo(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc2651-span")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_2651", map[string]any{"event_id_path": "$.id"}))
	for i := range 3 {
		body := []byte(`{"id":"span-2651-` + string(rune('a'+i)) + `"}`)
		if res, text := issue1870Signed(t, name, "whsec_2651", "application/json", body); res.StatusCode != http.StatusAccepted {
			t.Fatalf("delivery %d answered %d: %s", i, res.StatusCode, text)
		}
	}
	from := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Minute)
	to := time.Now().UTC().Add(time.Minute).Truncate(time.Minute)
	q := url.Values{"source": {name}, "from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
	var out map[string]any
	deadline := time.Now().Add(90 * time.Second)
	for {
		status, got := c.rest(http.MethodGet, "/api/v1/admin/webhooks/status?"+q.Encode(), http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("the status route answered %d: %v", status, got)
		}
		out = got
		hist, _ := got["history"].(map[string]any)
		if ack, _ := hist["ack"].([]any); len(ack) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if out["range"] != "custom" {
		t.Errorf("from and to without range read range %v, not custom", out["range"])
	}
	hist, _ := out["history"].(map[string]any)
	ack, _ := hist["ack"].([]any)
	if len(ack) == 0 {
		t.Fatalf("the span holds no acknowledgement point: %v", hist)
	}
	for _, raw := range ack {
		p, _ := raw.(map[string]any)
		p99, _ := p["p99_ms"].(float64)
		most, _ := p["max_ms"].(float64)
		if p99 > most {
			t.Errorf("a point's p99_ms %v is past its max_ms %v", p99, most)
		}
	}
}
