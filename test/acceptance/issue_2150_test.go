//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Acceptance for #2150: the webhook page's history. A burst sent now is read
// a week later, once its per-minute rows have been moved six days back and
// rolled up into hours; a spooling replica's received and written events
// diverge while its object store is stopped and converge once it returns;
// and every request is counted by the HTTP status its sender was answered
// with.
//
// Wire forms: every event is sent as JSON and as a form post; the status
// route takes range=week, and range=custom with from and to in RFC 3339.

// issue2150Status reads the status route with q.
func issue2150Status(t *testing.T, c *client, q url.Values) map[string]any {
	t.Helper()
	status, ov := c.rest(http.MethodGet, "/api/v1/admin/webhooks/status?"+q.Encode(), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("status route %s: %d %v", q.Encode(), status, ov)
	}
	return ov
}

// issue2150Statuses sums the history's requests by status for source.
func issue2150Statuses(ov map[string]any, source string) map[int]int {
	out := map[int]int{}
	h, _ := ov["history"].(map[string]any)
	points, _ := h["statuses"].([]any)
	for _, p := range points {
		pt, _ := p.(map[string]any)
		if pt["source"] == source {
			out[int(pt["http_status"].(float64))] += int(pt["count"].(float64))
		}
	}
	return out
}

// TestIssue2150_ABurstShowsAWeekLater: a burst of deliveries in both forms
// and a few with a bad signature are counted by status; their rows moved six
// days back are rolled up into hours by the compactor's retention pass and
// read the same through range=week and through a custom range of that day.
func TestIssue2150_ABurstShowsAWeekLater(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	db := devDB(t)
	name := issue2121Source(t, c, "acc2150-burst")
	for i := range 12 {
		f := issue1870Forms[i%2]
		body := f.encode(fmt.Sprintf(`{"id":"burst-%d","type":"order.created"}`, i))
		if res, out := issue1870Signed(t, name, issue2121Secret, f.contentType, body); res.StatusCode != http.StatusAccepted {
			t.Fatalf("%s delivery %d: %d %s", f.name, i, res.StatusCode, out)
		}
	}
	for i := range 3 {
		res, _ := issue1870Signed(t, name, "not-the-secret", "application/json", []byte(fmt.Sprintf(`{"id":"bad-%d"}`, i)))
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a bad signature was answered %d", res.StatusCode)
		}
	}
	q := url.Values{"range": {"hour"}, "source": {name}}
	waitFor(t, "the burst counted by status", func() (bool, string) {
		got := issue2150Statuses(issue2150Status(t, c, q), name)
		return got[202] == 12 && got[401] == 3, fmt.Sprintf("%v", got)
	})

	// A week passes for these rows: they move six days back, past the 48
	// hours the history is kept per minute.
	for _, table := range []string{"webhook_request_counts", "webhook_status_counts", "webhook_ack_latency"} {
		if _, err := db.Exec(`UPDATE `+table+` SET minute = minute - INTERVAL '6 days' WHERE source = $1`, name); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "the rows rolled up into hours", func() (bool, string) {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM webhook_request_counts
			WHERE source = $1 AND minute <> date_bin('1 hour', minute, TIMESTAMPTZ 'epoch')`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 0, fmt.Sprintf("%d per-minute rows left", n)
	})

	week := issue2150Statuses(issue2150Status(t, c, url.Values{"range": {"week"}, "source": {name}}), name)
	if week[202] != 12 || week[401] != 3 {
		t.Fatalf("a week later the burst reads %v", week)
	}
	day := time.Now().UTC().Add(-6 * 24 * time.Hour)
	custom := issue2150Status(t, c, url.Values{
		"range": {"custom"}, "source": {name},
		"from": {day.Add(-3 * time.Hour).Format(time.RFC3339)}, "to": {day.Add(3 * time.Hour).Format(time.RFC3339)},
	})
	if got := issue2150Statuses(custom, name); got[202] != 12 || got[401] != 3 {
		t.Fatalf("a custom range around that day reads %v", got)
	}
	if custom["bucket_seconds"] != float64(3600) {
		t.Fatalf("a range past 48 hours is drawn in hours, not %v", custom["bucket_seconds"])
	}
}

// issue2150Flow sums one replica's received and written events.
func issue2150Flow(ov map[string]any, replica string) (received, written int) {
	h, _ := ov["history"].(map[string]any)
	rows, _ := h["replicas"].([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if m["replica"] == replica {
			received += int(m["received_events"].(float64))
			written += int(m["drained_events"].(float64))
		}
	}
	return received, written
}

// TestIssue2150_ReceiveAndDrainDivergeThenConverge: with a replica's object
// store stopped its received events run ahead of those it wrote; once the
// store returns, written catches up.
func TestIssue2150_ReceiveAndDrainDivergeThenConverge(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	name := issue2121Source(t, c, "acc2150-flow")
	store := issue2121NewStore(t)
	root, _ := filepath.Abs("../..")
	r := issue2121Start(t, filepath.Join(root, "build", "2150", "spool-flow-"+strconv.FormatInt(time.Now().UnixNano(), 36)), store)

	store.pause()
	var ids []string
	for i := range 6 {
		f := issue1870Forms[i%2]
		id := fmt.Sprintf("flow-%s-%d", f.name, i)
		if status, _, b, err := r.post(name, f.name, id, 10*time.Second); err != nil || status != http.StatusAccepted {
			t.Fatalf("%s delivery: %d %s %v", f.name, status, b, err)
		}
		ids = append(ids, id)
	}
	q := url.Values{"range": {"hour"}}
	waitFor(t, "received ahead of written", func() (bool, string) {
		rcv, wr := issue2150Flow(issue2150Status(t, c, q), r.name)
		return rcv >= 6 && wr < rcv, fmt.Sprintf("received %d written %d", rcv, wr)
	})
	store.resume()
	issue2121AwaitExactlyOnce(t, c, name, ids)
	waitFor(t, "written caught up", func() (bool, string) {
		rcv, wr := issue2150Flow(issue2150Status(t, c, q), r.name)
		return rcv >= 6 && wr >= rcv, fmt.Sprintf("received %d written %d", rcv, wr)
	})
}
