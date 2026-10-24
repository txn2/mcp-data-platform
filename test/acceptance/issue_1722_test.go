//go:build integration

package acceptance

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1722: what turns a monitor into something people do not mute. The
// same alert inside the channel's repeat window is counted and not posted, the
// next post says how many were held back, a channel cannot be sent past its
// hourly cap, and an administrator reads all of it on the channel's status.
//
// The monitor here is a managed script run through run_script rather than
// waited for on a schedule: the post goes through the same platform.notify
// and the same notify tool either way, and the channel's window is twenty
// seconds: long enough that three runs on a slow runner all fall inside it, short
// enough that waiting it out costs seconds rather than minutes.
//
// Wire forms. notify's key is a typed string, sent present (by the script)
// and absent (the plain send that follows). repeat_after is a duration
// string, sent as "30s" -- longer than the channel's 1s window -- by the second
// criterion, and the channel's own repeat_after and max_per_hour are sent as
// the string and integer the admin route declares.

const chanSuppress = "acc-1722-mattermost"

// suppressWindow1722 is the channel's repeat_after in the central criterion.
const suppressWindow1722 = 20 * time.Second

// monitorScript posts one alert under a fixed key.
func monitorScript(channel, marker string) string {
	return fmt.Sprintf(`
def main():
    """Posts the feed's lag to the operations channel."""
    platform.notify(
        channel = %q,
        title = %q,
        body = "The orders feed is 41 minutes behind.",
        key = "feed:orders",
    )
`, channel, marker)
}

// TestIssue1722_ARepeatingAlertPostsOnceThenSaysWhatItHeldBack is the
// ticket's central criterion through a script.
func TestIssue1722_ARepeatingAlertPostsOnceThenSaysWhatItHeldBack(t *testing.T) {
	env := readMattermostEnv(t)
	c := connect(t)
	putChannel(t, c, chanSuppress, map[string]any{
		"kind": "mattermost", "connection": "mattermost-dev", "target": env.channelID,
		"repeat_after": suppressWindow1722.String(), "max_per_hour": 60,
	})
	t.Cleanup(func() { deleteChannel(t, c, chanSuppress) })

	marker := fmt.Sprintf("acc-1722 feed lag %d", time.Now().UnixNano())
	name := fmt.Sprintf("acc-1722-monitor-%d", time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name, "display_name": "Acceptance 1722 monitor",
		"source": monitorScript(chanSuppress, marker),
	}, nil)
	t.Cleanup(func() { c.call("manage_script", map[string]any{"command": "delete", "name": name}) })
	// Saving drafts the script with writes allowed, and the draft posts under
	// the key. Its post is already in the channel; its row and the key are
	// cleared so the runs below start a fresh window.
	waitFor(t, "the draft's post", func() (bool, string) {
		_, ok := mattermostPostWith(t, env, marker)
		return ok, "waiting"
	})
	purgeChannelRows(t, chanSuppress)
	before := countPostsWith(t, env, marker)

	// Three runs inside the window: the first posts, the next two are held.
	// The window opened during the first run, so it closes no later than
	// suppressWindow1722 after that run returned.
	var windowEnd time.Time
	for i := range 3 {
		run := c.call("run_script", map[string]any{"name": name})
		if status, _ := run["status"].(string); status != "succeeded" {
			t.Fatalf("run %d: %v", i+1, run)
		}
		if i == 0 {
			windowEnd = time.Now().Add(suppressWindow1722)
		}
	}
	waitFor(t, "the first run's post", func() (bool, string) {
		n := countPostsWith(t, env, marker)
		return n == before+1, fmt.Sprint(n-before, " new posts")
	})
	if n := countPostsWith(t, env, marker); n != before+1 {
		t.Fatalf("%d new posts inside the window, want 1", n-before)
	}

	// The status route and the Notifications tab filter count the same thing.
	status := channelStatus(t, c, chanSuppress)
	if number(t, status, "suppressed") != 2 {
		t.Errorf("status suppressed = %v, want the two held runs", status["suppressed"])
	}
	if number(t, status, "sent_last_hour") != 1 || number(t, status, "max_per_hour") != 60 {
		t.Errorf("status = %v, want one send against a cap of 60", status)
	}
	code, rows := c.rest(http.MethodGet, "/api/v1/admin/notifications?channel="+chanSuppress, nil)
	if code != http.StatusOK || number(t, rows, "total") != 1 {
		t.Errorf("the Notifications tab's channel filter: %d %v", code, rows)
	}

	// Past the window the next run posts, saying how many were held back.
	time.Sleep(time.Until(windowEnd) + time.Second)
	run := c.call("run_script", map[string]any{"name": name})
	if s, _ := run["status"].(string); s != "succeeded" {
		t.Fatalf("the run after the window: %v", run)
	}
	waitFor(t, "the post after the window", func() (bool, string) {
		n := countPostsWith(t, env, marker)
		return n == before+2, fmt.Sprint(n-before, " new posts")
	})
	if !postsContain(t, env, "2 repeats of this alert since") {
		t.Error("the post after the window does not say how many repeats were held back")
	}
}

// TestIssue1722_ALongerRepeatAfterHoldsTheKeyAndAnUnkeyedSendIsNeverHeld
// covers the two remaining forms: repeat_after lengthens the window for its
// key, and a send with no key is delivered every time.
func TestIssue1722_ALongerRepeatAfterHoldsTheKeyAndAnUnkeyedSendIsNeverHeld(t *testing.T) {
	env := readMattermostEnv(t)
	c := connect(t)
	putChannel(t, c, chanSuppress, map[string]any{
		"kind": "mattermost", "connection": "mattermost-dev", "target": env.channelID, "repeat_after": "1s",
	})
	t.Cleanup(func() { deleteChannel(t, c, chanSuppress) })

	key := fmt.Sprintf("acc-1722-k-%d", time.Now().UnixNano())
	first := c.call("notify", map[string]any{"action": "send", "channel": chanSuppress, "title": "t", "key": key, "repeat_after": "30s"})
	if number(t, first, "queued") != 1 {
		t.Fatalf("first keyed send: %v", first)
	}
	time.Sleep(1500 * time.Millisecond)
	held := c.call("notify", map[string]any{"action": "send", "channel": chanSuppress, "title": "t", "key": key})
	if s, _ := held["suppressed"].(bool); !s {
		t.Errorf("a send 1.5s into a 30s window went through: %v", held)
	}
	for range 2 {
		plain := c.call("notify", map[string]any{"action": "send", "channel": chanSuppress, "title": "no key"})
		if number(t, plain, "queued") != 1 {
			t.Errorf("an unkeyed send was not queued: %v", plain)
		}
	}
}

// TestIssue1722_TheHourlyCapRefusesNamingTheCap: a channel cannot be flooded,
// and the sender is told which cap it met.
func TestIssue1722_TheHourlyCapRefusesNamingTheCap(t *testing.T) {
	env := readMattermostEnv(t)
	c := connect(t)
	putChannel(t, c, chanSuppress, map[string]any{
		"kind": "mattermost", "connection": "mattermost-dev", "target": env.channelID, "max_per_hour": 2,
	})
	t.Cleanup(func() { deleteChannel(t, c, chanSuppress) })
	// Rows from an earlier criterion count against the hour.
	purgeChannelRows(t, chanSuppress)

	for i := range 2 {
		out := c.call("notify", map[string]any{"action": "send", "channel": chanSuppress, "title": fmt.Sprintf("cap %d", i)})
		if number(t, out, "queued") != 1 {
			t.Fatalf("send %d under the cap: %v", i+1, out)
		}
	}
	res, text, err := c.callRaw("notify", map[string]any{"action": "send", "channel": chanSuppress, "title": "over"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "cap of 2 messages in the last hour") {
		t.Errorf("the third send: error=%v %q", res.IsError, text)
	}
}

// channelStatus reads the admin status route.
func channelStatus(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	code, out := c.rest(http.MethodGet, "/api/v1/admin/notification-channels/"+name+"/status", nil)
	if code != http.StatusOK {
		t.Fatalf("status route: %d %v", code, out)
	}
	return out
}

// countPostsWith counts the fixture channel's posts containing marker.
func countPostsWith(t *testing.T, env mattermostEnv, marker string) int {
	t.Helper()
	n := 0
	for _, m := range mattermostPosts(t, env) {
		if strings.Contains(m, marker) {
			n++
		}
	}
	return n
}

// postsContain reports whether any of the fixture channel's posts contains s.
func postsContain(t *testing.T, env mattermostEnv, s string) bool {
	t.Helper()
	return containing(mattermostPosts(t, env), s)
}

// devDB opens the dev stack's database, for the state no surface resets.
func devDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// purgeChannelRows deletes a channel's queue rows and keys from the dev
// database, so a criterion counting this hour's sends starts from none.
func purgeChannelRows(t *testing.T, channel string) {
	t.Helper()
	db := devDB(t)
	for _, q := range []string{
		`DELETE FROM notifications WHERE channel = $1`,
		`DELETE FROM notification_channel_keys WHERE channel = $1`,
	} {
		if _, err := db.ExecContext(t.Context(), q, channel); err != nil {
			t.Fatalf("clearing channel %s: %v", channel, err)
		}
	}
}
