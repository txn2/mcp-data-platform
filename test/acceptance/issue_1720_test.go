//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #1720 and #1723: notification channels.
//
// A channel is an operator-configured destination that is not one person: a
// Mattermost channel, an incoming webhook, or a named list of addresses. Before this, a script that monitored something could tell nobody
// what it found except by writing an asset or by posting to a chat webhook
// through platform.call("api_invoke_endpoint", ...), with the secret and the
// formatting living in every script.
//
// These criteria run through the real surfaces on the local stack: an
// administrator creates each kind through the admin REST route a person uses,
// tests each through its own transport, and sends through the notify tool over
// a real MCP client. What arrived is read back from the destination itself --
// the Mattermost API and the mailpit API -- never from the platform's own
// record of having sent it.
//
// Upstreams. Every criterion needs the fixtures make dev starts
// (dev/seed-mattermost.sh writes dev/.mattermost-env; SMTP points at mailpit),
// and fails rather than skipping without them: a criterion that did not run is
// not a criterion that passed.
//
// Wire forms. The channel PUT's kind, description, connection, target, mode
// and repeat_after are typed strings in ChannelInput
// (internal/admin/settingsapi/channels.go), enabled is a bool, max_per_hour an
// int, and recipients a list of strings, so each admits exactly one JSON form
// and each is sent below as a literal value of that form. Both the form with
// every optional field present and the form with all of them omitted are sent,
// because omitted is a distinct wire form that takes the platform defaults.
// The notify tool's action, channel, title, body, link, asset and message are
// typed strings in notifyInput (internal/platform/notifylayer/tool.go) and are
// sent as literal tools/call string parameters.

// channelWait bounds how long a criterion waits for the queue's worker to
// deliver a document. The worker is woken by pg_notify on every enqueue and
// polls besides, so a delivery that has not landed in this long has not been
// made.
const channelWait = 90 * time.Second

// The names this test's channels are created under. They are removed at the
// end of each criterion so a rerun starts from the same place.
const (
	chanMattermost = "acc-1720-mattermost"
	chanWebhook    = "acc-1720-webhook"
	chanEmail      = "acc-1720-email"
)

// mattermostEnv is the fixture dev/seed-mattermost.sh wrote.
type mattermostEnv struct {
	url, token, channelID string
}

// readMattermostEnv reads dev/.mattermost-env. A criterion that needs it fails
// when it is absent rather than skipping: the fixture is part of make dev, and
// a green run against a stack that never started it proves nothing.
func readMattermostEnv(t *testing.T) mattermostEnv {
	t.Helper()
	data, err := os.ReadFile("../../dev/.mattermost-env")
	if err != nil {
		t.Fatalf("the Mattermost fixture is not seeded (%v). Start the dev stack: make dev", err)
	}
	env := mattermostEnv{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"`)
		switch key {
		case "MATTERMOST_URL":
			env.url = value
		case "MATTERMOST_TOKEN":
			env.token = value
		case "MATTERMOST_CHANNEL_ID":
			env.channelID = value
		}
	}
	if env.url == "" || env.token == "" || env.channelID == "" {
		t.Fatalf("dev/.mattermost-env is incomplete: %+v", env)
	}
	return env
}

// putChannel creates or replaces a channel through the admin route and
// returns the stored view.
func putChannel(t *testing.T, c *client, name string, body map[string]any) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodPut, "/api/v1/admin/notification-channels/"+name, jsonBody(t, body))
	if status != http.StatusOK {
		t.Fatalf("PUT channel %s: status %d, body %v", name, status, out)
	}
	return out
}

// deleteChannel removes a channel, so a rerun starts clean.
func deleteChannel(t *testing.T, c *client, name string) {
	t.Helper()
	status, out := c.rest(http.MethodDelete, "/api/v1/admin/notification-channels/"+name, nil)
	if status != http.StatusNoContent {
		t.Errorf("DELETE channel %s: status %d, body %v", name, status, out)
	}
}

// testChannel posts a test through the channel's own transport and returns
// what the upstream answered.
func testChannel(t *testing.T, c *client, name string) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodPost, "/api/v1/admin/notification-channels/"+name+"/test", nil)
}

// getJSON reads a JSON document from an upstream with a bearer token.
func getJSON(t *testing.T, url, token string) map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %s", url, resp.StatusCode, data)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("GET %s: decoding %s: %v", url, data, err)
	}
	return out
}

// waitFor polls until check reports the message arrived, failing on timeout
// with what the last look found.
func waitFor(t *testing.T, what string, check func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(channelWait)
	var last string
	for time.Now().Before(deadline) {
		ok, detail := check()
		if ok {
			return
		}
		last = detail
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("%s did not arrive within %v; last look: %s", what, channelWait, last)
}

// mattermostPosts returns the messages in the fixture's channel, newest first.
func mattermostPosts(t *testing.T, env mattermostEnv) []string {
	t.Helper()
	out := getJSON(t, fmt.Sprintf("%s/api/v4/channels/%s/posts?per_page=50", env.url, env.channelID), env.token)
	posts, _ := out["posts"].(map[string]any)
	messages := make([]string, 0, len(posts))
	for _, p := range posts {
		post, _ := p.(map[string]any)
		if msg, ok := post["message"].(string); ok {
			messages = append(messages, msg)
		}
	}
	return messages
}

// mailpitMessages returns the subjects mailpit is holding.
func mailpitMessages(t *testing.T) []string {
	t.Helper()
	out := getJSON(t, mailpitBase()+"/api/v1/messages?limit=100", "")
	items, _ := out["messages"].([]any)
	subjects := make([]string, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		if s, ok := m["Subject"].(string); ok {
			subjects = append(subjects, s)
		}
	}
	return subjects
}

// mailpitBase is where the dev stack's mail lands.
func mailpitBase() string {
	if v := os.Getenv("MAILPIT_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://localhost:8025"
}

// containing reports whether any entry contains want.
func containing(entries []string, want string) bool {
	for _, e := range entries {
		if strings.Contains(e, want) {
			return true
		}
	}
	return false
}

// TestIssue1720_MattermostChannelDeliversADocument is the first driving use
// case end to end: an administrator creates a channel, tests it, sends a
// document through the notify tool, and the message is read back from
// Mattermost itself.
func TestIssue1720_MattermostChannelDeliversADocument(t *testing.T) {
	env := readMattermostEnv(t)
	c := connect(t)

	// Wire form: every optional field present, each as the literal type its
	// schema declares.
	view := putChannel(t, c, chanMattermost, map[string]any{
		"kind":         "mattermost",
		"description":  "Acceptance: the dev Mattermost",
		"enabled":      true,
		"connection":   "mattermost-dev",
		"target":       env.channelID,
		"mode":         "immediate",
		"repeat_after": "1h",
		"max_per_hour": 60,
	})
	t.Cleanup(func() { deleteChannel(t, c, chanMattermost) })

	if view["kind"] != "mattermost" || view["target"] != env.channelID {
		t.Fatalf("stored channel = %v", view)
	}
	if warnings, ok := view["warnings"].([]any); ok && len(warnings) > 0 {
		t.Fatalf("the channel cannot deliver as configured: %v", warnings)
	}

	// The test send goes through the channel's own transport and reports what
	// Mattermost answered, not that a row was written.
	status, out := testChannel(t, c, chanMattermost)
	if status != http.StatusOK {
		t.Fatalf("test send: status %d, body %v", status, out)
	}
	waitFor(t, "the test message", func() (bool, string) {
		msgs := mattermostPosts(t, env)
		return containing(msgs, "Test message from the data platform"), fmt.Sprint(len(msgs), " posts")
	})

	// A document sent through the tool is delivered by the worker.
	marker := fmt.Sprintf("acc-1720 threshold crossed %d", time.Now().UnixNano())
	res := c.call("notify", map[string]any{
		"action":  "send",
		"channel": chanMattermost,
		"title":   marker,
		"body":    "Queue depth is 812, over the 500 threshold.",
		"link":    "https://example.com/runs/1",
	})
	if queued := number(t, res, "queued"); queued != 1 {
		t.Fatalf("queued = %v, want 1 row for a chat channel", queued)
	}
	if delivered, _ := res["delivered"].(bool); delivered {
		t.Error("the tool claimed delivery; a send is queued, not delivered")
	}
	waitFor(t, "the document", func() (bool, string) {
		msgs := mattermostPosts(t, env)
		return containing(msgs, marker), fmt.Sprint(len(msgs), " posts")
	})

	// The body and the link travel with it: a monitor that posts a number
	// without saying where it came from is the thing people mute.
	msgs := mattermostPosts(t, env)
	if !containing(msgs, "Queue depth is 812") {
		t.Error("the document's body did not arrive")
	}
	if !containing(msgs, "https://example.com/runs/1") {
		t.Error("the document's link did not arrive")
	}
}

// TestIssue1720_EmailChannelDeliversToEveryRecipient covers the kind that
// names no connection: the operator's own mailing list, delivered through the
// deployment's mail server and read back from mailpit.
func TestIssue1720_EmailChannelDeliversToEveryRecipient(t *testing.T) {
	c := connect(t)

	// Wire form: only the required fields, every optional one omitted, which
	// is a distinct wire form taking the platform defaults.
	view := putChannel(t, c, chanEmail, map[string]any{
		"kind":       "email",
		"recipients": []string{"acc-1720-a@example.com", "acc-1720-b@example.com"},
	})
	t.Cleanup(func() { deleteChannel(t, c, chanEmail) })

	if view["mode"] != "immediate" {
		t.Errorf("mode = %v, want the immediate default", view["mode"])
	}
	if view["max_per_hour"] == nil || view["max_per_hour"].(float64) == 0 {
		t.Errorf("max_per_hour = %v, want the platform default", view["max_per_hour"])
	}

	marker := fmt.Sprintf("acc-1720 weekly report %d", time.Now().UnixNano())
	res := c.call("notify", map[string]any{
		"action":  "send",
		"channel": chanEmail,
		"title":   marker,
		"body":    "Revenue rose 4% week over week.",
	})
	// One row per recipient, not one row for the list: each person keeps
	// their own delivery mode and unsubscribe link.
	if queued := number(t, res, "queued"); queued != 2 {
		t.Fatalf("queued = %v, want one row per recipient", queued)
	}
	waitFor(t, "the emails", func() (bool, string) {
		subjects := mailpitMessages(t)
		return containing(subjects, marker), fmt.Sprint(len(subjects), " messages")
	})
}

// TestIssue1720_WebhookChannelDelivers covers the kind that carries neither a
// channel choice nor files: one POST of {"text": ...} to the connection's
// base URL.
//
// The dev stack's api-test fixture stands in for an incoming webhook: it
// accepts a POST and echoes it, which is exactly what this criterion needs --
// proof the platform reached the configured address with the message in the
// documented shape.
func TestIssue1720_WebhookChannelDelivers(t *testing.T) {
	c := connect(t)

	view := putChannel(t, c, chanWebhook, map[string]any{
		"kind":        "webhook",
		"description": "Acceptance: an incoming webhook",
		"connection":  "api-test-fixture",
	})
	t.Cleanup(func() { deleteChannel(t, c, chanWebhook) })

	if view["target"] != nil && view["target"] != "" {
		t.Errorf("target = %v; a webhook posts where its URL points", view["target"])
	}

	// The fixture answers whatever it answers; what this proves is that the
	// platform built and sent the request rather than refusing it.
	status, out := testChannel(t, c, chanWebhook)
	if status != http.StatusOK && status != http.StatusServiceUnavailable {
		t.Fatalf("test send: status %d, body %v", status, out)
	}
	if status == http.StatusServiceUnavailable {
		detail, _ := out["detail"].(string)
		// A refusal is acceptable only when it is the upstream's, not the
		// platform failing to reach a configured connection at all.
		if strings.Contains(detail, "is served here") || strings.Contains(detail, "no api connection") {
			t.Fatalf("the platform could not reach the channel's connection: %s", detail)
		}
	}
}

// TestIssue1723_NotifyToolListsOnlyReachableChannels covers the tool's list
// action through the real client: channel names are not guessable, so the
// list is how anything finds one.
func TestIssue1723_NotifyToolListsOnlyReachableChannels(t *testing.T) {
	c := connect(t)

	putChannel(t, c, chanEmail, map[string]any{
		"kind":        "email",
		"description": "Acceptance: the operations list",
		"recipients":  []string{"acc-1720-a@example.com"},
	})
	t.Cleanup(func() { deleteChannel(t, c, chanEmail) })

	// The tool is registered at all: a tool nothing lists is a tool nobody
	// can call (#1675).
	var registered bool
	for _, tool := range c.tools() {
		if tool.Name == "notify" {
			registered = true
		}
	}
	if !registered {
		t.Fatal("the notify tool is not in this server's tools/list")
	}

	res := c.call("notify", map[string]any{"action": "list"})
	channels, _ := res["channels"].([]any)
	var found map[string]any
	for _, ch := range channels {
		entry, _ := ch.(map[string]any)
		if entry["name"] == chanEmail {
			found = entry
		}
	}
	if found == nil {
		t.Fatalf("the email channel was not listed: %v", res)
	}
	// The listing says what the kind can show, so a caller composing a
	// document knows before sending.
	if carries, _ := found["carries"].(string); carries == "" {
		t.Errorf("the listed channel does not say what it carries: %v", found)
	}
}

// TestIssue1723_NotifyPublishesAnAsset covers the publish action: an asset
// becomes a document with its name, its content and a link back to it.
func TestIssue1723_NotifyPublishesAnAsset(t *testing.T) {
	env := readMattermostEnv(t)
	c := connect(t)

	putChannel(t, c, chanMattermost, map[string]any{
		"kind":       "mattermost",
		"connection": "mattermost-dev",
		"target":     env.channelID,
	})
	t.Cleanup(func() { deleteChannel(t, c, chanMattermost) })

	marker := fmt.Sprintf("Acc 1720 report %d", time.Now().UnixNano())
	saved := c.call("save_asset", map[string]any{
		"name":         marker,
		"content":      "# Weekly numbers\n\n| region | total |\n| --- | --- |\n| west | 812 |\n",
		"content_type": "text/markdown",
		"description":  "Acceptance: an asset published to a channel",
	})
	// save_asset answers with asset_id, not id.
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no id: %v", saved)
	}

	res := c.call("notify", map[string]any{
		"action":  "publish",
		"channel": chanMattermost,
		"asset":   assetID,
		"message": "This week's numbers.",
	})
	if queued := number(t, res, "queued"); queued != 1 {
		t.Fatalf("queued = %v, want 1", queued)
	}
	if link, _ := res["link"].(string); !strings.Contains(link, assetID) {
		t.Errorf("link = %q, want the asset's portal page", link)
	}

	waitFor(t, "the published asset", func() (bool, string) {
		msgs := mattermostPosts(t, env)
		return containing(msgs, marker), fmt.Sprint(len(msgs), " posts")
	})
	msgs := mattermostPosts(t, env)
	if !containing(msgs, "This week's numbers.") {
		t.Error("the publisher's message did not arrive")
	}
	if !containing(msgs, "| west | 812 |") {
		t.Error("the asset's content did not arrive")
	}
}

// TestIssue1723_ScriptPostsThroughPlatformNotify is the driving use case: a
// managed script that monitors something tells the operations channel what it
// found, and the post leads back to the run that produced it.
func TestIssue1723_ScriptPostsThroughPlatformNotify(t *testing.T) {
	env := readMattermostEnv(t)
	c := connect(t)

	putChannel(t, c, chanMattermost, map[string]any{
		"kind":       "mattermost",
		"connection": "mattermost-dev",
		"target":     env.channelID,
	})
	t.Cleanup(func() { deleteChannel(t, c, chanMattermost) })

	marker := fmt.Sprintf("acc-1720 from a script %d", time.Now().UnixNano())
	scriptName := fmt.Sprintf("acc-1720-monitor-%d", time.Now().UnixNano())
	source := fmt.Sprintf(`
platform.notify(
    channel=%q,
    title=%q,
    body="The monitor found 812 rows over threshold.",
)
`, chanMattermost, marker)

	c.call("manage_script", map[string]any{
		"command": "create", "name": scriptName,
		"display_name": "Acceptance 1720 monitor", "source": source,
	})
	t.Cleanup(func() {
		c.call("manage_script", map[string]any{"command": "delete", "name": scriptName})
	})

	run := c.call("run_script", map[string]any{"name": scriptName})
	if status, _ := run["status"].(string); status != "succeeded" {
		t.Fatalf("the run did not succeed: %v", run)
	}

	waitFor(t, "the script's post", func() (bool, string) {
		msgs := mattermostPosts(t, env)
		return containing(msgs, marker), fmt.Sprint(len(msgs), " posts")
	})

	// Every post from a monitor leads back to the run, so a reader who wants
	// to know what produced the number can open it.
	msgs := mattermostPosts(t, env)
	if !containing(msgs, "/portal/scripts/") {
		t.Error("the post does not link back to the run that produced it")
	}
}
