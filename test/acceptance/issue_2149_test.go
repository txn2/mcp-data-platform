//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Acceptance for #2149: the webhook receiver's conditions posted to a
// notification channel at a warning and a critical level. A source whose
// object store is stopped walks its spool's oldest event from warning to
// critical and resolves when the store returns, each step posted once to the
// Mattermost fixture; a replica stopped with events in its spool is posted
// critical; and the thresholds round-trip through the settings route, which
// refuses a channel that does not exist.
//
// Wire forms: the settings route takes one JSON object, every threshold as a
// JSON number and the channels as a list; a threshold sent as a string is
// refused. Each event is sent to the receiver as JSON and as a form post.

const issue2149Path = "/api/v1/admin/settings/webhook-alert"

// issue2149Quiet is every condition off, so a criterion turns on only the one
// it watches and the fixture is not filled with the dev stack's other sources.
func issue2149Quiet(channel string) map[string]any {
	off := map[string]any{"warning": 0, "critical": 0}
	return map[string]any{
		"channels":           []string{channel},
		"spool_fill_percent": off, "spool_age_percent": off, "drain_failures": off,
		"drain_failing_minutes": 0, "not_reporting_seconds": 0, "spool_volume_free_percent": off,
		"defaults": map[string]any{
			"refusal_minutes": 0, "unauthorized_percent": off, "ack_p99_ms": off,
			"compaction_overdue_minutes": off, "silent_after_minutes": 0,
		},
		"sources": map[string]any{},
	}
}

// TestIssue2149_ThresholdsRoundTripAndAMissingChannelIsRefused: what is
// written is read back, a field left out keeps its value, a source's
// override is kept, and a channel no channel is stored under is refused.
func TestIssue2149_ThresholdsRoundTripAndAMissingChannelIsRefused(t *testing.T) {
	c := connect(t)
	channel := "acc-2149-rt-" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	issue2148Channel(t, c, channel)
	body := issue2149Quiet(channel)
	body["spool_age_percent"] = map[string]any{"warning": 20, "critical": 40}
	body["sources"] = map[string]any{"email-events": map[string]any{"silent_after_minutes": 90}}
	putSettings(t, c, issue2149Path, body)

	status, got := c.rest(http.MethodGet, issue2149Path, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET: %d %v", status, got)
	}
	age, _ := got["spool_age_percent"].(map[string]any)
	sources, _ := got["sources"].(map[string]any)
	esp, _ := sources["email-events"].(map[string]any)
	if age["warning"] != float64(20) || age["critical"] != float64(40) || esp["silent_after_minutes"] != float64(90) {
		t.Fatalf("the thresholds did not round-trip: %v", got)
	}
	_, kept := c.rest(http.MethodPut, issue2149Path, jsonBody(t, map[string]any{"not_reporting_seconds": 300}))
	if kept["not_reporting_seconds"] != float64(300) || kept["spool_age_percent"].(map[string]any)["warning"] != float64(20) {
		t.Fatalf("a write of one field did not keep the rest: %v", kept)
	}
	if status, out := c.rest(http.MethodPut, issue2149Path, jsonBody(t, map[string]any{"channels": []string{"acc-2149-no-such-channel"}})); status != http.StatusBadRequest {
		t.Fatalf("a missing channel was accepted: %d %v", status, out)
	}
	if status, out := c.rest(http.MethodPut, issue2149Path, jsonBody(t, map[string]any{"not_reporting_seconds": "60"})); status != http.StatusBadRequest {
		t.Fatalf("a threshold as a string was accepted: %d %v", status, out)
	}
	if status, out := c.rest(http.MethodPut, issue2149Path, jsonBody(t, map[string]any{
		"spool_fill_percent": map[string]any{"warning": 90, "critical": 50},
	})); status != http.StatusBadRequest {
		t.Fatalf("a warning above its critical was accepted: %d %v", status, out)
	}
}

// TestIssue2149_SpoolAgeWalksWarningToCriticalAndResolves: a replica's object
// store stops; the oldest event waiting crosses the warning and then the
// critical share of the spool's max_age (1h); the store returns, the spool
// drains, and the condition resolves. Each step is posted once.
func TestIssue2149_SpoolAgeWalksWarningToCriticalAndResolves(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	channel := "acc-2149-age-" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	env := issue2148Channel(t, c, channel)
	body := issue2149Quiet(channel)
	// 0.5% and 1.5% of an hour: 18 and 54 seconds.
	body["spool_age_percent"] = map[string]any{"warning": 0.5, "critical": 1.5}
	putSettings(t, c, issue2149Path, body)

	name := issue2121Source(t, c, "acc2149-age")
	store := issue2121NewStore(t)
	root, _ := filepath.Abs("../..")
	r := issue2121Start(t, filepath.Join(root, "build", "2149", "spool-age-"+strconv.FormatInt(time.Now().UnixNano(), 36)), store,
		"MCP_PLATFORM_WEBHOOK_ALERT_INTERVAL=2s")
	title := "Oldest webhook event on " + r.name

	store.pause()
	var ids []string
	for i, f := range issue1870Forms {
		id := fmt.Sprintf("age-%s-%d", f.name, i)
		if status, _, b, err := r.post(name, f.name, id, 10*time.Second); err != nil || status != http.StatusAccepted {
			t.Fatalf("%s delivery with the store stopped: %d %s %v", f.name, status, b, err)
		}
		ids = append(ids, id)
	}
	waitFor(t, "the warning", func() (bool, string) {
		n := issue2148Count(t, env, "WARNING: "+title)
		return n == 1, fmt.Sprintf("%d warning posts", n)
	})
	waitFor(t, "the critical", func() (bool, string) {
		n := issue2148Count(t, env, "CRITICAL: "+title)
		return n == 1, fmt.Sprintf("%d critical posts", n)
	})
	store.resume()
	issue2121AwaitExactlyOnce(t, c, name, ids)
	waitFor(t, "the resolution", func() (bool, string) {
		n := issue2148Count(t, env, "Resolved: "+title)
		return n == 1, fmt.Sprintf("%d resolved posts", n)
	})
	for _, level := range []string{"WARNING: ", "CRITICAL: "} {
		if n := issue2148Count(t, env, level+title); n != 1 {
			t.Fatalf("%s%s was posted %d times; each step posts once", level, title, n)
		}
	}
}

// TestIssue2149_AStoppedReplicaHoldingEventsIsCritical: a replica is killed
// with events in its spool, so its report stops; another replica's evaluation
// posts it critical. Started again on its spool with its store answering, it
// drains and the condition resolves.
func TestIssue2149_AStoppedReplicaHoldingEventsIsCritical(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	channel := "acc-2149-stop-" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	env := issue2148Channel(t, c, channel)
	body := issue2149Quiet(channel)
	body["not_reporting_seconds"] = 15
	putSettings(t, c, issue2149Path, body)

	name := issue2121Source(t, c, "acc2149-stop")
	root, _ := filepath.Abs("../..")
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	// The evaluation runs on a replica that keeps reporting; the one stopped
	// cannot judge itself.
	issue2121Start(t, filepath.Join(root, "build", "2149", "spool-eval-"+stamp), issue2121NewStore(t),
		"MCP_PLATFORM_WEBHOOK_ALERT_INTERVAL=2s")
	store := issue2121NewStore(t)
	dir := filepath.Join(root, "build", "2149", "spool-stopped-"+stamp)
	r := issue2121Start(t, dir, store)

	store.pause()
	if status, _, b, err := r.post(name, "json", "stopped-1", 10*time.Second); err != nil || status != http.StatusAccepted {
		t.Fatalf("delivery with the store stopped: %d %s %v", status, b, err)
	}
	waitFor(t, "the spool reporting its event", func() (bool, string) {
		line := issue2121Spool(t, c, r.name)
		return line != nil && line["events"].(float64) >= 1, fmt.Sprintf("%v", line)
	})
	stopped := r.name
	r.kill()
	title := "Replica " + stopped + " holds"
	waitFor(t, "the critical", func() (bool, string) {
		n := issue2148Count(t, env, "CRITICAL: "+title)
		return n == 1, fmt.Sprintf("%d critical posts", n)
	})

	store.resume()
	r.start(t, mustBin(t))
	issue2121AwaitExactlyOnce(t, c, name, []string{"stopped-1"})
	waitFor(t, "the resolution", func() (bool, string) {
		n := issue2148Count(t, env, "Resolved: "+title)
		return n == 1, fmt.Sprintf("%d resolved posts", n)
	})
}

// mustBin is the platform binary the replicas run.
func mustBin(t *testing.T) string {
	t.Helper()
	bin, err := issue2121Bin()
	if err != nil {
		t.Fatal(err)
	}
	return bin
}
