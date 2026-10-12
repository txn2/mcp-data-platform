//go:build integration

package acceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Acceptance for #2107: a sender that gives up before its answer is told
// apart from a failed write and can be traced to its events, the response
// time of accepted requests is reported per source, and the status route
// follows its source parameter. Every request to /hooks/{source} is raw HTTP,
// sent as JSON and as a form post; the admin API is read as JSON.

const issue2107Secret = "whsec_2107"

// issue2107Status reads the status route.
func issue2107Status(t *testing.T, c *client, query string) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodGet, "/api/v1/admin/webhooks/status?"+query, http.NoBody)
}

// issue2107SourceLine is a source's line on the overview.
func issue2107SourceLine(ov map[string]any, name string) map[string]any {
	list, _ := ov["sources"].([]any)
	for _, s := range list {
		line, _ := s.(map[string]any)
		if line["name"] == name {
			return line
		}
	}
	return nil
}

// issue2107Abandon posts body and gives up after wait, as a sender whose own
// timeout is shorter than the answer does.
func issue2107Abandon(t *testing.T, source, contentType string, body []byte, wait time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL()+"/hooks/"+source, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Signature", issue1870Sign(issue2107Secret, body))
	res, err := issue1870Client.Do(req)
	if err == nil {
		_ = res.Body.Close()
		t.Fatalf("the request was answered %d before the sender gave up", res.StatusCode)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the request failed other than by the sender giving up: %v", err)
	}
}

// TestIssue2107_ASenderThatGivesUpIsSenderGoneAndNamesItsEvents: a source
// that holds events three seconds before writing them, and a sender that
// waits half a second. Each delivery is counted sender_gone and never
// write_failed, its rejection carries the body's SHA-256 and its event id, and
// the write it left behind stores the event.
func TestIssue2107_ASenderThatGivesUpIsSenderGoneAndNamesItsEvents(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc2107-gone")
	issue1870Create(t, c, issue1870HMAC(name, issue2107Secret, map[string]any{
		"event_id_path": "$.id", "flush_max_interval_ms": 3000,
	}))

	hashes := map[string]string{}
	for _, f := range issue1870Forms {
		id := "gone-" + f.name
		body := f.encode(`{"id":"` + id + `"}`)
		sum := sha256.Sum256(body)
		hashes[id] = hex.EncodeToString(sum[:])
		issue2107Abandon(t, name, f.contentType, body, 500*time.Millisecond)
	}

	for id := range hashes {
		issue1870AwaitRows(t, c, name, "event_id = '"+id+"'", 1)
	}

	waitFor(t, "both deliveries counted and kept as sender_gone", func() (bool, string) {
		status, ov := issue2107Status(t, c, "range=hour&source="+name)
		if status != http.StatusOK {
			return false, fmt.Sprintf("status %d", status)
		}
		line := issue2107SourceLine(ov, name)
		hour, _ := line["last_hour"].(map[string]any)
		rows, _ := ov["rejections"].([]any)
		named := 0
		for _, r := range rows {
			row, _ := r.(map[string]any)
			ids, _ := row["event_ids"].([]any)
			if row["outcome"] != "sender_gone" || len(ids) != 1 {
				continue
			}
			id, _ := ids[0].(string)
			if want, ok := hashes[id]; ok && row["request_hash"] == want {
				named++
			}
		}
		return hour["sender_gone"] == float64(2) && hour["write_failed"] == nil && named == 2,
			fmt.Sprintf("last_hour %v, %d of 2 rejections name their event and hash", hour, named)
	})
}

// TestIssue2107_ResponseTimeIsReportedPerSource: two accepted deliveries,
// one in each form, are reported on the overview and the source's page with
// a count, percentiles, and the slowest request split into its stages.
func TestIssue2107_ResponseTimeIsReportedPerSource(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc2107-time")
	issue1870Create(t, c, issue1870HMAC(name, issue2107Secret, map[string]any{"event_id_path": "$.id"}))
	for _, f := range issue1870Forms {
		res, text := issue1870Signed(t, name, issue2107Secret, f.contentType, f.encode(`{"id":"time-`+f.name+`"}`))
		if res.StatusCode != http.StatusAccepted {
			t.Fatalf("%s delivery answered %d: %s", f.name, res.StatusCode, text)
		}
	}

	check := func(where string, ack map[string]any) (bool, string) {
		slow, _ := ack["slowest"].(map[string]any)
		if ack["count"] != float64(2) || slow == nil {
			return false, fmt.Sprintf("%s: %v", where, ack)
		}
		total, _ := slow["total_ms"].(float64)
		stages := 0.0
		for _, k := range []string{"buffer_ms", "mark_ms", "put_ms"} {
			v, ok := slow[k].(float64)
			if !ok {
				return false, fmt.Sprintf("%s: slowest has no %s: %v", where, k, slow)
			}
			stages += v
		}
		p50, _ := ack["p50_ms"].(float64)
		p99, _ := ack["p99_ms"].(float64)
		ok := ack["max_ms"] == total && total > 0 && stages <= total && p50 > 0 && p50 <= p99 && p99 <= total
		return ok, fmt.Sprintf("%s: %v", where, ack)
	}
	waitFor(t, "the response time on the overview and the source's page", func() (bool, string) {
		_, ov := issue2107Status(t, c, "range=hour")
		line := issue2107SourceLine(ov, name)
		hour, _ := line["ack_last_hour"].(map[string]any)
		if ok, detail := check("overview ack_last_hour", hour); !ok {
			return false, detail
		}
		_, detail := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
		st, _ := detail["status"].(map[string]any)
		day, _ := st["ack_last_day"].(map[string]any)
		return check("source page ack_last_day", day)
	})
}

// TestIssue2107_TheStatusRouteFollowsItsSourceParameter: with source, every
// part of the response is that source's, including the rejections, and a name
// no source has is 404.
func TestIssue2107_TheStatusRouteFollowsItsSourceParameter(t *testing.T) {
	c := connect(t)
	mine := issue1870Name("acc2107-mine")
	other := issue1870Name("acc2107-other")
	for _, n := range []string{mine, other} {
		issue1870Create(t, c, issue1870HMAC(n, issue2107Secret, map[string]any{"event_id_path": "$.id"}))
		forged := []byte(`{"id":"forged"}`)
		res, text := issue1870Post(t, baseURL(), n, "application/json", forged,
			map[string]string{"X-Signature": issue1870Sign("not-the-secret", forged)})
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a forged request to %s was answered %d: %s", n, res.StatusCode, text)
		}
	}

	waitFor(t, "each source's rejection on the overview", func() (bool, string) {
		_, ov := issue2107Status(t, c, "range=hour")
		seen := map[any]bool{}
		rows, _ := ov["rejections"].([]any)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			seen[row["source"]] = true
		}
		return seen[mine] && seen[other], fmt.Sprintf("sources with rejections: %v", seen)
	})

	for _, rng := range []string{"hour", "day"} {
		status, ov := issue2107Status(t, c, "range="+rng+"&source="+mine)
		if status != http.StatusOK {
			t.Fatalf("range=%s&source=%s answered %d: %v", rng, mine, status, ov)
		}
		sources, _ := ov["sources"].([]any)
		if len(sources) != 1 || issue2107SourceLine(ov, mine) == nil {
			t.Errorf("range=%s: the sources are %v, want %s alone", rng, sources, mine)
		}
		rows, _ := ov["rejections"].([]any)
		if len(rows) == 0 {
			t.Errorf("range=%s: %s's rejection is missing", rng, mine)
		}
		for _, r := range rows {
			if row, _ := r.(map[string]any); row["source"] != mine {
				t.Errorf("range=%s: a rejection of %v is in %s's status", rng, row["source"], mine)
			}
		}
		points, _ := ov["volume"].([]any)
		for _, p := range points {
			if point, _ := p.(map[string]any); point["source"] != mine {
				t.Errorf("range=%s: a volume point of %v is in %s's status", rng, point["source"], mine)
			}
		}
	}

	if status, body := issue2107Status(t, c, "range=hour&source=no-such-source-2107"); status != http.StatusNotFound {
		t.Errorf("a source no one has was answered %d: %v", status, body)
	}
}

// TestIssue2107_ANewSourcesFirstEventIsQueryableAfterItsAnswer: the first
// event of a new source is acknowledged without waiting on the query engine,
// and is queryable once its window's partition is registered after the
// answer.
func TestIssue2107_ANewSourcesFirstEventIsQueryableAfterItsAnswer(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc2107-first")
	issue1870Create(t, c, issue1870HMAC(name, issue2107Secret, map[string]any{"event_id_path": "$.id"}))
	for _, f := range issue1870Forms {
		res, text := issue1870Signed(t, name, issue2107Secret, f.contentType, f.encode(`{"id":"first-`+f.name+`"}`))
		if res.StatusCode != http.StatusAccepted {
			t.Fatalf("%s delivery answered %d: %s", f.name, res.StatusCode, text)
		}
		issue1870AwaitRows(t, c, name, "event_id = 'first-"+f.name+"'", 1)
	}
}
