//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// Issue #1987: memory_capture embedded its text before it answered, so a slow
// embedder held a committed write open for minutes and the client reported a
// timeout for a capture that had been stored. A capture is now stored at once
// and its embedding queued; the recall-first check runs when the index job
// writes the vector, and the response says so.
//
// The embedder is made slow the way it was on the deployment that reported
// this: the dev stack's own Ollama, paused, so a request to it is accepted and
// never answered. Every criterion runs through the MCP tools a client calls,
// and the stored record is read back through the administrator's memory route.
//
// Wire forms: memory_capture's type and content are typed strings, and
// memory_manage's command, id and content are typed strings, each admitting
// one JSON form, sent as literal tools/call parameters.

// issue1987Embedder is the dev stack's embedding server.
const issue1987Embedder = "acme-dev-ollama"

// issue1987Answer is how long a capture may take: far under the provider's
// 30-second request timeout the old path waited on, and under what an MCP
// client waits before it gives up.
const issue1987Answer = 10 * time.Second

// issue1987Embedded is how long a queued embed may take once the embedder
// answers again, backlog included.
const issue1987Embedded = 4 * time.Minute

// issue1987Pause stalls the embedder until the returned function is called,
// and in any case when the test ends. Pausing freezes the process with its
// socket open, so a request to it waits rather than failing fast.
func issue1987Pause(t *testing.T) func() {
	t.Helper()
	container := os.Getenv("ISSUE_1987_EMBEDDER_CONTAINER")
	if container == "" {
		container = issue1987Embedder
	}
	docker := func(action string) {
		if out, err := exec.Command("docker", action, container).CombinedOutput(); err != nil { // #nosec G204 -- fixed verbs on the dev stack's own container
			t.Fatalf("docker %s %s: %v\n%s", action, container, err, out)
		}
	}
	docker("pause")
	var once sync.Once
	resume := func() { once.Do(func() { docker("unpause") }) }
	t.Cleanup(resume)
	return resume
}

// issue1987Capture captures content as c, timing the call, and forgets the
// record when the test ends.
func issue1987Capture(t *testing.T, c *client, content string) (id string, out map[string]any, took time.Duration) {
	t.Helper()
	start := time.Now()
	out = c.call("memory_capture", map[string]any{"type": "personal_preference", "content": content})
	took = time.Since(start)
	id, _ = out["id"].(string)
	if id == "" {
		t.Fatalf("memory_capture returned no id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("memory_manage", map[string]any{"command": "forget", "id": id})
	})
	return id, out, took
}

// issue1987Record reads one of the caller's records through the
// administrator's memory route, which returns each record's status and
// metadata.
func issue1987Record(t *testing.T, c *client, owner, id string) map[string]any {
	t.Helper()
	q := url.Values{"created_by": {owner}, "limit": {"200"}}
	status, body := c.rest(http.MethodGet, "/api/v1/admin/memory/records?"+q.Encode(), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/memory/records: status %d: %v", status, body)
	}
	data, _ := body["data"].([]any)
	for _, r := range data {
		rec, _ := r.(map[string]any)
		if rec["id"] == id {
			return rec
		}
	}
	t.Fatalf("record %s is not among %s's records", id, owner)
	return nil
}

// issue1987AwaitChecked waits until the recall check has run on each record,
// which it does only after the index job has written the record's vector.
func issue1987AwaitChecked(t *testing.T, c *client, owner string, ids ...string) map[string]map[string]any {
	t.Helper()
	deadline := time.Now().Add(issue1987Embedded)
	got := map[string]map[string]any{}
	for _, id := range ids {
		for {
			rec := issue1987Record(t, c, owner, id)
			meta, _ := rec["metadata"].(map[string]any)
			if meta["recall_check"] == "done" {
				got[id] = rec
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("record %s was never embedded and checked (metadata %v)", id, meta)
			}
			time.Sleep(time.Second)
		}
	}
	return got
}

// issue1987Owner is the address the suite's identity captures under.
func issue1987Owner(t *testing.T, c *client) string {
	t.Helper()
	status, me := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/portal/me: status %d", status)
	}
	email, _ := me["email"].(string)
	if email == "" {
		t.Fatalf("/portal/me names no email: %v", me)
	}
	return email
}

// TestIssue1987_ACaptureAnswersWhileTheEmbedderIsStalled is the ticket's
// first criterion: with the embedder not answering, memory_capture returns in
// well under 30 seconds with the record stored, says the recall check is to
// come, and once the embedder answers the record is embedded and checked. An
// update to the record's content returns the same way.
func TestIssue1987_ACaptureAnswersWhileTheEmbedderIsStalled(t *testing.T) {
	c := connectFor(t, issue1987Embedded+2*time.Minute)
	owner := issue1987Owner(t, c)
	resume := issue1987Pause(t)

	content := fmt.Sprintf("Acceptance #1987 %d: I read quarterly revenue in constant dollars.", time.Now().UnixNano())
	id, out, took := issue1987Capture(t, c, content)
	t.Logf("memory_capture answered in %s with the embedder paused", took)
	if took > issue1987Answer {
		t.Errorf("memory_capture took %s with the embedder stalled; want under %s", took, issue1987Answer)
	}
	if out["recall_check"] != "pending" {
		t.Errorf("recall_check = %v, want pending: the response must not claim a check that has not run", out["recall_check"])
	}
	for _, gone := range []string{"superseded", "superseded_ids", "similar_existing"} {
		if _, ok := out[gone]; ok {
			t.Errorf("the response reports %s, which the check has not produced yet: %v", gone, out)
		}
	}
	rec := issue1987Record(t, c, owner, id)
	if rec["content"] != content {
		t.Errorf("the stored record reads %v, want the capture's content", rec["content"])
	}

	start := time.Now()
	updated := content + " Updated."
	c.call("memory_manage", map[string]any{"command": "update", "id": id, "content": updated})
	if took := time.Since(start); took > issue1987Answer {
		t.Errorf("memory_manage update took %s with the embedder stalled; want under %s", took, issue1987Answer)
	}

	resume()
	issue1987AwaitChecked(t, c, owner, id)
}

// TestIssue1987_TheNewerRestatementSupersedesTheOlder is the recall-first
// check the capture no longer runs in the request: two captures of one fact,
// stored before either is embedded, consolidate to the newer once both are.
func TestIssue1987_TheNewerRestatementSupersedesTheOlder(t *testing.T) {
	c := connectFor(t, issue1987Embedded+2*time.Minute)
	owner := issue1987Owner(t, c)
	fact := fmt.Sprintf("Acceptance #1987 %d: the finance team closes the books on the third business day of each month.",
		time.Now().UnixNano())

	resume := issue1987Pause(t)
	older, _, _ := issue1987Capture(t, c, fact)
	newer, _, _ := issue1987Capture(t, c, fact)
	resume()

	recs := issue1987AwaitChecked(t, c, owner, older, newer)
	if got := recs[newer]["status"]; got != "active" {
		t.Errorf("the newer restatement is %v, want active", got)
	}
	final := issue1987Record(t, c, owner, older)
	if final["status"] != "superseded" {
		t.Errorf("the older restatement is %v, want superseded by the newer", final["status"])
	}
}
