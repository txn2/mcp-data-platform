//go:build integration

package acceptance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Acceptance for #2001: a burst of rate_limited rejections no longer evicts
// the unauthorized ones. Every request to /hooks/{source} is sent as raw HTTP
// with Content-Type application/json; the admin API is read as JSON.

// issue2001Signed signs body the way issue1996Source verifies: sha256= and
// the hex HMAC of the timestamp, a dot and the body.
func issue2001Signed(ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(issue1996Secret))
	_, _ = m.Write([]byte(ts + "."))
	_, _ = m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// issue2001Unauthorized are five refused requests, each for its own reason.
func issue2001Unauthorized(now time.Time) map[string]map[string]string {
	ts := strconv.FormatInt(now.Unix(), 10)
	stale := strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)
	body := []byte(`{"id":"x"}`)
	return map[string]map[string]string{
		"the signature header is missing":                             {"X-Timestamp": ts},
		"the signature does not match":                                {"X-Timestamp": ts, "X-Signature": "sha256=00"},
		"the timestamp header is missing":                             {"X-Signature": issue2001Signed(ts, body)},
		"the timestamp is not a Unix time in seconds or milliseconds": {"X-Timestamp": "soon", "X-Signature": issue2001Signed("soon", body)},
		"the timestamp is outside the tolerance window":               {"X-Timestamp": stale, "X-Signature": issue2001Signed(stale, body)},
	}
}

// issue2001Rows reads the rejections a list holds, by outcome.
func issue2001Rows(list any, source string) (unauthorized map[string]bool, limited int64) {
	unauthorized = map[string]bool{}
	rows, _ := list.([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if s, ok := row["source"]; ok && s != source {
			continue
		}
		switch row["outcome"] {
		case "unauthorized":
			unauthorized[fmt.Sprint(row["reason"])] = true
		case "rate_limited":
			n, _ := row["count"].(float64)
			limited += int64(n)
		}
	}
	return unauthorized, limited
}

// TestIssue2001_UnauthorizedRowsSurviveARateLimitBurst sends five
// unauthorized requests, then five hundred signed ones against a rate limit:
// the source's page and the overview still list all five unauthorized reasons,
// and the burst's refusals are counted on a row or two rather than fifty.
func TestIssue2001_UnauthorizedRowsSurviveARateLimitBurst(t *testing.T) {
	c := connect(t)
	source := issue1870Name("burst2001")
	issue1870Create(t, c, map[string]any{
		"name": source, "connection": issue1870Conn,
		"auth": map[string]any{
			"mode": "hmac", "secret": issue1996Secret, "signature_header": "X-Signature", "prefix": "sha256=",
			"timestamp_header": "X-Timestamp", "signed": "timestamp.body",
		},
		"config": map[string]any{"rate_limit_per_minute": 60, "rate_limit_burst": 1},
	})

	refused := issue2001Unauthorized(time.Now())
	for reason, header := range refused {
		res, body := issue1870Post(t, baseURL(), source, "application/json", []byte(`{"id":"x"}`), header)
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: answered %d %s, want 401", reason, res.StatusCode, body)
		}
	}

	var limited int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 25)
	for i := range 500 {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			body := []byte(fmt.Sprintf(`{"id":"b%d"}`, i))
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			res, _ := issue1870Post(t, baseURL(), source, "application/json", body,
				map[string]string{"X-Timestamp": ts, "X-Signature": issue2001Signed(ts, body)})
			if res.StatusCode == http.StatusTooManyRequests {
				mu.Lock()
				limited++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if limited == 0 {
		t.Fatal("the burst was never rate limited")
	}

	waitFor(t, "every rejection recorded", func() (bool, string) {
		_, got := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+source, http.NoBody)
		st, _ := got["status"].(map[string]any)
		reasons, counted := issue2001Rows(st["rejections"], source)
		return len(reasons) == len(refused) && counted == limited,
			fmt.Sprintf("%d of %d unauthorized reasons, %d of %d rate limited", len(reasons), len(refused), counted, limited)
	})
	_, got := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+source, http.NoBody)
	st, _ := got["status"].(map[string]any)
	rows, _ := st["rejections"].([]any)
	if len(rows) > len(refused)+2 {
		t.Errorf("the source keeps %d rows for %d unauthorized and one burst; a burst is a row or two", len(rows), len(refused))
	}

	_, ov := c.rest(http.MethodGet, "/api/v1/admin/webhooks/status?range=hour", http.NoBody)
	reasons, _ := issue2001Rows(ov["rejections"], source)
	for reason := range refused {
		if !reasons[reason] {
			t.Errorf("the overview lost the unauthorized row %q", reason)
		}
	}
}
