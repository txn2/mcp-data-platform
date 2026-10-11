//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1724: the platform's own alerts reach the channels an operator names,
// beside the email recipients they already name: a scheduled run that failed,
// a connection whose credential was revoked, and a review queue past its
// threshold. Each is read back from the Mattermost fixture.
//
// The review-queue check runs hourly; the dev stack must be started with
// MCP_PLATFORM_REVIEW_ALERT_INTERVAL=5s for its criterion, which fails rather
// than skips without it.
//
// Wire forms. Each alert route's channels field is a list of strings: sent
// present (["acc-1724-mattermost"]), empty ([]) and absent, the last of which
// keeps the stored list.

const chanAlerts = "acc-1724-mattermost"

// alertChannel creates the Mattermost channel the alerts post to.
func alertChannel(t *testing.T, c *client) mattermostEnv {
	t.Helper()
	env := readMattermostEnv(t)
	putChannel(t, c, chanAlerts, map[string]any{
		"kind": "mattermost", "connection": "mattermost-dev", "target": env.channelID, "repeat_after": "1s",
	})
	t.Cleanup(func() { deleteChannel(t, c, chanAlerts) })
	return env
}

// putSettings writes an alert's settings, restoring what was there after.
func putSettings(t *testing.T, c *client, path string, body map[string]any) map[string]any {
	t.Helper()
	code, before := c.rest(http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("reading %s: %d %v", path, code, before)
	}
	t.Cleanup(func() {
		restore := map[string]any{}
		for k, v := range before {
			if k != "updated_by" && k != "updated_at" && k != "warnings" {
				restore[k] = v
			}
		}
		c.rest(http.MethodPut, path, jsonBody(t, restore))
	})
	code, out := c.rest(http.MethodPut, path, jsonBody(t, body))
	if code != http.StatusOK {
		t.Fatalf("PUT %s: %d %v", path, code, out)
	}
	return out
}

// TestIssue1724_ChannelsPresentEmptyAndAbsent covers the wire forms on the
// new route, and the refusal of a name no channel is stored under.
func TestIssue1724_ChannelsPresentEmptyAndAbsent(t *testing.T) {
	c := connect(t)
	alertChannel(t, c)
	const path = "/api/v1/admin/settings/script-run-alert"
	out := putSettings(t, c, path, map[string]any{"channels": []string{chanAlerts}})
	if fmt.Sprint(out["channels"]) != "["+chanAlerts+"]" {
		t.Fatalf("present: %v", out)
	}
	if _, out = c.rest(http.MethodPut, path, jsonBody(t, map[string]any{})); fmt.Sprint(out["channels"]) != "["+chanAlerts+"]" {
		t.Errorf("absent did not keep the stored channels: %v", out)
	}
	if _, out = c.rest(http.MethodPut, path, jsonBody(t, map[string]any{"channels": []string{}})); fmt.Sprint(out["channels"]) != "[]" {
		t.Errorf("empty did not clear the channels: %v", out)
	}
	if code, out := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{"channels": []string{"acc-1724-missing"}})); code != http.StatusBadRequest {
		t.Errorf("an unknown channel: %d %v", code, out)
	}
	for _, p := range []string{"/api/v1/admin/settings/review-queue-alert", "/api/v1/admin/settings/connection-alert"} {
		code, out := c.rest(http.MethodGet, p, nil)
		if _, ok := out["channels"].([]any); code != http.StatusOK || !ok {
			t.Errorf("%s does not carry channels: %d %v", p, code, out)
		}
	}
}

// TestIssue1724_AFailedScheduledRunPostsWithALinkToTheRun: the operations
// channel hears about a nightly automation failing, and can open the run.
func TestIssue1724_AFailedScheduledRunPostsWithALinkToTheRun(t *testing.T) {
	c := connect(t)
	env := alertChannel(t, c)
	putSettings(t, c, "/api/v1/admin/settings/script-run-alert", map[string]any{"channels": []string{chanAlerts}})

	name := fmt.Sprintf("acc-1724-failing-%d", time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name, "display_name": "Acceptance 1724 failing",
		"source": `
def main():
    """Fails the way a nightly load does when the warehouse is short."""
    fail("the warehouse returned 0 rows for yesterday", retryable = False)
`,
	}, nil)
	t.Cleanup(func() { c.call("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	scriptID, _ := got["id"].(string)

	c.call("manage_script", map[string]any{"command": "schedule_set", "name": name, "cron": "* * * * *", "timezone": "UTC"})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "schedule_disable", "name": name})
	})
	fireNow1986(t, scriptID)

	link := "/portal/automations/" + scriptID + "/runs/"
	waitFor(t, "the failure post", func() (bool, string) {
		return postsContain(t, env, link), "waiting for a post linking to a run of " + scriptID
	})
}

// TestIssue1724_ARevokedConnectionPostsAtOnce: the channel hears the moment
// the credential is discarded, not only when it escalates.
func TestIssue1724_ARevokedConnectionPostsAtOnce(t *testing.T) {
	c := connect(t)
	env := alertChannel(t, c)
	putSettings(t, c, "/api/v1/admin/settings/connection-alert", map[string]any{
		"enabled": true, "escalate_after_hours": 24, "recipients": []string{}, "channels": []string{chanAlerts},
	})
	up := newFakeUpstream(t)
	name := issue1694Connect(t, c, up, "1724")
	up.rejects.Store(true)
	issue1694Revoke(t, c, name)

	waitFor(t, "the revocation post", func() (bool, string) {
		return postsContain(t, env, name), "waiting for a post naming " + name
	})
	if !postsContain(t, env, "needs to be reauthorized") {
		t.Error("the post does not say the connection needs to be reauthorized")
	}
}

// TestIssue1724_ACrossedReviewQueuePostsToTheChannel: the review-queue alert
// crossing its threshold posts to the channel with a link to the queue.
func TestIssue1724_ACrossedReviewQueuePostsToTheChannel(t *testing.T) {
	c := connect(t)
	env := alertChannel(t, c)
	captureMemory1625(t, c, fmt.Sprintf("Acceptance #1724: a pending insight (%d).", time.Now().UnixNano()), "business_knowledge", nil)
	putSettings(t, c, "/api/v1/admin/settings/review-queue-alert", map[string]any{
		"enabled": true, "pending_threshold": 1, "oldest_pending_days": 0, "cooldown_hours": 1,
		"recipients": []string{}, "channels": []string{chanAlerts},
	})
	// The cooldown marker is the one piece of state no surface resets: a
	// queue alerted within the hour would not alert again.
	if _, err := devDB(t).ExecContext(t.Context(), `DELETE FROM review_alert_state WHERE queue = 'knowledge_review'`); err != nil {
		t.Fatalf("clearing the cooldown marker: %v", err)
	}

	waitFor(t, "the review-queue post (the stack needs MCP_PLATFORM_REVIEW_ALERT_INTERVAL set short)", func() (bool, string) {
		for _, m := range mattermostPosts(t, env) {
			if strings.Contains(m, "/portal/knowledge#review") {
				return true, ""
			}
		}
		return false, "no post linking to the review queue"
	})
}
