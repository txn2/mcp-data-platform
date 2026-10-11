//go:build integration

package acceptance

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #1824: the slack kind, proved against a real workspace. An
// administrator creates an api connection holding the bot token and a slack
// channel over it through the admin routes a person uses, tests it, and sends
// a document through the notify tool over a real MCP client. What arrived is
// read back from Slack's own conversations.history and conversations.replies,
// never from the platform's record of having sent it.
//
// Upstream. A developer's own workspace, named in dev/.slack-env
// (SLACK_TEST_BOT_TOKEN, SLACK_TEST_CHANNEL_ID; gitignored). The bot needs
// chat:write, files:write, channels:history and channels:read, and must be
// in the channel. Every criterion fails rather than skipping without it.
//
// Wire forms. The connection PUT's config is an object of strings; the
// channel PUT's kind, connection and target are strings. notify's title and
// body are strings, and attachments is sent as a list of strings (a resource's
// mcp:resource: reference).

const (
	chanSlack = "acc-1824-slack"
	connSlack = "acc-1824-slack"
)

// slackEnv is the developer's workspace.
type slackEnv struct{ token, channel string }

// readSlackEnv reads dev/.slack-env, failing when it is absent.
func readSlackEnv(t *testing.T) slackEnv {
	t.Helper()
	data, err := os.ReadFile("../../dev/.slack-env")
	if err != nil {
		t.Fatalf("no Slack workspace is configured (%v). Put SLACK_TEST_BOT_TOKEN and SLACK_TEST_CHANNEL_ID in dev/.slack-env", err)
	}
	env := slackEnv{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "=")
		if !ok {
			continue
		}
		switch key {
		case "SLACK_TEST_BOT_TOKEN":
			env.token = value
		case "SLACK_TEST_CHANNEL_ID":
			env.channel = value
		}
	}
	if env.token == "" || env.channel == "" {
		t.Fatal("dev/.slack-env lacks SLACK_TEST_BOT_TOKEN or SLACK_TEST_CHANNEL_ID")
	}
	return env
}

// slackChannelSetup creates the connection and the channel through the admin
// routes, removing both after.
func slackChannelSetup(t *testing.T, c *client, env slackEnv) {
	t.Helper()
	path := "/api/v1/admin/connection-instances/api/" + connSlack
	code, out := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"base_url": "https://slack.com/api", "auth_mode": "bearer", "credential": env.token,
			"connection_name": connSlack, "call_timeout": "20s",
		},
		"description": "Acceptance #1824: a Slack workspace's bot",
	}))
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("saving the Slack connection: %d %v", code, out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, path, http.NoBody) })
	view := putChannel(t, c, chanSlack, map[string]any{
		"kind": "slack", "connection": connSlack, "target": env.channel,
		"description": "Acceptance #1824: a channel in a real workspace",
	})
	t.Cleanup(func() { deleteChannel(t, c, chanSlack) })
	if warnings, ok := view["warnings"].([]any); ok && len(warnings) > 0 {
		t.Fatalf("the channel cannot deliver as configured: %v", warnings)
	}
}

// slackMessages reads the channel's latest messages.
func slackMessages(t *testing.T, env slackEnv, path string, params url.Values) []map[string]any {
	t.Helper()
	params.Set("channel", env.channel)
	out := getJSON(t, "https://slack.com/api/"+path+"?"+params.Encode(), env.token)
	if ok, _ := out["ok"].(bool); !ok {
		t.Fatalf("%s: %v", path, out["error"])
	}
	raw, _ := out["messages"].([]any)
	msgs := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		if mm, ok := m.(map[string]any); ok {
			msgs = append(msgs, mm)
		}
	}
	return msgs
}

// slackMessageWith returns the latest channel message whose text holds marker.
func slackMessageWith(t *testing.T, env slackEnv, marker string) (map[string]any, bool) {
	t.Helper()
	for _, m := range slackMessages(t, env, "conversations.history", url.Values{"limit": {"30"}}) {
		if text, _ := m["text"].(string); strings.Contains(text, marker) {
			return m, true
		}
	}
	return nil, false
}

// TestIssue1824_ATestSendReachesTheChannel: the editor's test send goes
// through the channel's own transport to the real workspace.
func TestIssue1824_ATestSendReachesTheChannel(t *testing.T) {
	env := readSlackEnv(t)
	c := connect(t)
	slackChannelSetup(t, c, env)

	code, out := testChannel(t, c, chanSlack)
	if code != http.StatusOK {
		t.Fatalf("test send: %d %v", code, out)
	}
	waitFor(t, "the test message in Slack", func() (bool, string) {
		_, ok := slackMessageWith(t, env, "Test message from the data platform")
		return ok, "reading conversations.history"
	})
}

// TestIssue1824_ADocumentArrivesRenderedWithItsFileInTheThread: the markdown
// renders as Slack's own blocks, the link is a context block, and the file is
// shared into the message's thread.
func TestIssue1824_ADocumentArrivesRenderedWithItsFileInTheThread(t *testing.T) {
	env := readSlackEnv(t)
	c := connect(t)
	slackChannelSetup(t, c, env)

	res := c.call("manage_resource", map[string]any{
		"action": "create", "filename": "chart-1824.png", "display_name": "acc-1824 chart",
		"path": "acceptance-1824", "description": "Acceptance #1824: a PNG posted to Slack",
		"content_base64": base64.StdEncoding.EncodeToString(pngBytes(t)), "content_type": "image/png",
		"if_exists": "replace",
	})
	resourceID, _ := res["resource_id"].(string)
	if resourceID == "" {
		t.Fatalf("manage_resource: %v", res)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody) })

	marker := fmt.Sprintf("acc-1824 weekly %d", time.Now().UnixNano())
	sent := c.call("notify", map[string]any{
		"action": "send", "channel": chanSlack, "title": marker, "body": reportMarkdown,
		"link": "https://example.com/runs/1824", "attachments": []any{"mcp:resource:" + resourceID},
	})
	if number(t, sent, "queued") != 1 {
		t.Fatalf("send: %v", sent)
	}

	var msg map[string]any
	waitFor(t, "the document in Slack", func() (bool, string) {
		m, ok := slackMessageWith(t, env, marker)
		msg = m
		return ok, "reading conversations.history"
	})
	types := []string{}
	blocks, _ := msg["blocks"].([]any)
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		ty, _ := bm["type"].(string)
		types = append(types, ty)
	}
	joined := strings.Join(types, ",")
	for _, want := range []string{"header", "table", "context"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the message's blocks %v lack %q: the markdown did not render", types, want)
		}
	}
	ts, _ := msg["ts"].(string)
	var files []any
	waitFor(t, "the file in the message's thread", func() (bool, string) {
		for _, r := range slackMessages(t, env, "conversations.replies", url.Values{"ts": {ts}}) {
			if f, ok := r["files"].([]any); ok && len(f) > 0 {
				files = f
				return true, ""
			}
		}
		return false, "no reply with files yet"
	})
	f, _ := files[0].(map[string]any)
	if name, _ := f["name"].(string); name != "chart-1824.png" {
		t.Errorf("file name = %q, want the resource's", name)
	}
}

// TestIssue1824_AReportIsPublishedToSlack is the epic's second driving use
// case: a report saved as an asset and published to the channel, its rows
// rendered as Slack's own table and its link back to the asset.
func TestIssue1824_AReportIsPublishedToSlack(t *testing.T) {
	env := readSlackEnv(t)
	c := connect(t)
	slackChannelSetup(t, c, env)

	marker := fmt.Sprintf("Acc 1824 weekly report %d", time.Now().UnixNano())
	saved := c.call("save_asset", map[string]any{
		"name": marker, "content": "region,total\nwest,812\neast,640\n", "content_type": "text/csv",
		"description": "Acceptance #1824: a report published to Slack",
	})
	assetID, _ := saved["asset_id"].(string)
	out := c.call("notify", map[string]any{
		"action": "publish", "channel": chanSlack, "asset": assetID, "message": "This week's numbers.",
	})
	if number(t, out, "queued") != 1 {
		t.Fatalf("publish: %v", out)
	}
	var msg map[string]any
	waitFor(t, "the published report in Slack", func() (bool, string) {
		m, ok := slackMessageWith(t, env, marker)
		msg = m
		return ok, "reading conversations.history"
	})
	raw := fmt.Sprint(msg["blocks"])
	if !strings.Contains(raw, "table") || !strings.Contains(raw, "/portal/assets/"+assetID) {
		t.Errorf("the published report lacks its table or its link: %s", raw)
	}
}
