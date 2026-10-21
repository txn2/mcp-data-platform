//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Issue #2241: sharing an asset to a channel from Share. The owner chooses one
// of the chat channels a Slack or Mattermost bot is in, or another channel
// they reach, and who opens the posted link; posting creates a link share in
// that mode and posts the share's URL. The checks go through the routes the
// Share dialog calls, read what arrived from Mattermost's and Slack's own
// APIs, and open the posted link as another signed-in person and as nobody.
//
// Upstreams. Mattermost from make dev (dev/.mattermost-env); a developer's own
// Slack workspace (dev/.slack-env). Both channels are created with no target
// of their own, so the chat channels offered are the bots' own lists.
//
// Wire forms: the channel-share body's access_mode is sent as each of its two
// strings, "authenticated" and "public"; target is a string, present for the
// bot channels and absent for the email channel; expires_in is a string,
// present for public and absent for authenticated. allow_sharing on the
// admin persona body is sent as the JSON boolean false.

const (
	chan2241Mattermost = "acc-2241-mattermost"
	chan2241Slack      = "acc-2241-slack"
	conn2241Slack      = "acc-2241-slack"
	chan2241Email      = "acc-2241-email"
)

// issue2241Member is a database persona and an API key reaching it.
type issue2241Member struct {
	persona, key, userID string
	client               *client
}

// newIssue2241Member creates a persona with extra fields on its body and a key
// whose one role maps to it.
func newIssue2241Member(t *testing.T, admin *client, label string, extra map[string]any) issue2241Member {
	t.Helper()
	suffix := time.Now().UnixNano()
	name := fmt.Sprintf("acc-%s-%d", label, suffix)
	role := strings.ReplaceAll(name, "-", "_")
	body := map[string]any{
		"name": name, "display_name": "Acceptance " + label, "roles": []any{role},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
	}
	for k, v := range extra {
		body[k] = v
	}
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, body)); status != http.StatusCreated {
		t.Fatalf("creating persona %s: %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody) })
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": name, "roles": []any{role}, "description": "Acceptance " + label,
	}))
	key, _ := out["key"].(string)
	if (status != http.StatusCreated && status != http.StatusOK) || key == "" {
		t.Fatalf("creating key %s: %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, http.NoBody) })
	m := issue2241Member{persona: name, key: key, client: connectAs(t, key)}
	_, me := m.client.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	m.userID, _ = me["user_id"].(string)
	return m
}

// rawGet reads a path with the given key, or with none.
func rawGet(t *testing.T, base, path, key string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	httpClient := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test read
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, body
}

// viewPath is the path part of a posted share URL.
func viewPath(t *testing.T, link string) string {
	t.Helper()
	i := strings.Index(link, "/portal/view/")
	if i < 0 {
		t.Fatalf("the posted link %q is not a share link", link)
	}
	return link[i:]
}

// assetShares reads an asset's share list as its owner's Share dialog does.
func assetShares(t *testing.T, c *client, assetID string) []map[string]any {
	t.Helper()
	status, raw := rawGet(t, c.base, "/api/v1/portal/assets/"+assetID+"/shares", c.apiKey)
	if status != http.StatusOK {
		t.Fatalf("listing shares: %d %s", status, raw)
	}
	var shares []map[string]any
	if err := json.Unmarshal(raw, &shares); err != nil {
		t.Fatalf("shares: %v %s", err, raw)
	}
	return shares
}

// saveReport saves a CSV asset as c, removed after.
func saveReport(t *testing.T, c *client, label string) (assetID, marker string) {
	t.Helper()
	marker = fmt.Sprintf("Acc %s report %d", label, time.Now().UnixNano())
	saved := c.call("save_asset", map[string]any{
		"name": marker, "content": "region,total\nwest,812\neast,640\n", "content_type": "text/csv",
		"description": "Acceptance " + label + ": an asset shared to a channel",
	})
	assetID, _ = saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID}) })
	return assetID, marker
}

// waitForThumbnail waits for the renderer to draw the asset's tile, which is
// the preview a post carries.
func waitForThumbnail(t *testing.T, c *client, assetID string) {
	t.Helper()
	waitFor(t, "the asset's tile", func() (bool, string) {
		_, a := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody)
		key, _ := a["thumbnail_s3_key"].(string)
		return key != "", "no tile yet"
	})
}

// channelTargets returns the chat channels the portal lists under a channel.
func channelTargets(t *testing.T, c *client, channel string) map[string]string {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/portal/channels", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("listing channels: %d %v", status, out)
	}
	list, _ := out["channels"].([]any)
	for _, raw := range list {
		ch, _ := raw.(map[string]any)
		if ch["name"] != channel {
			continue
		}
		if e, _ := ch["targets_error"].(string); e != "" {
			t.Fatalf("the chat channels of %s could not be read: %s", channel, e)
		}
		targets := map[string]string{}
		ts, _ := ch["targets"].([]any)
		for _, tr := range ts {
			tm, _ := tr.(map[string]any)
			id, _ := tm["id"].(string)
			name, _ := tm["name"].(string)
			targets[id] = name
		}
		return targets
	}
	t.Fatalf("channel %s is not listed: %v", channel, out)
	return nil
}

// mattermostBotChannel creates the Mattermost channel with no target of its
// own, and returns the fixture channel's id, which the bot is in.
func mattermostBotChannel(t *testing.T, c *client) mattermostEnv {
	t.Helper()
	env := readMattermostEnv(t)
	putChannel(t, c, chan2241Mattermost, map[string]any{"kind": "mattermost", "connection": "mattermost-dev"})
	t.Cleanup(func() { deleteChannel(t, c, chan2241Mattermost) })
	return env
}

// shareToChannel posts an asset through the Share dialog's route.
func shareToChannel(t *testing.T, c *client, assetID string, body map[string]any) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/channel-shares", jsonBody(t, body))
}

// TestIssue2241_TheChannelListIsTheBotsChannels: one channel per bot, with no
// target of its own, offers every chat channel the bot is in.
func TestIssue2241_TheChannelListIsTheBotsChannels(t *testing.T) {
	c := connect(t)
	env := mattermostBotChannel(t, c)
	slack := readSlackEnv(t)
	slackBotChannel(t, c, slack)

	if _, ok := channelTargets(t, c, chan2241Mattermost)[env.channelID]; !ok {
		t.Errorf("the Mattermost bot's channel %s is not offered", env.channelID)
	}
	if _, ok := channelTargets(t, c, chan2241Slack)[slack.channel]; !ok {
		t.Errorf("the Slack bot's channel %s is not offered", slack.channel)
	}
}

// slackBotChannel creates the Slack connection and a channel over it with no
// target of its own.
func slackBotChannel(t *testing.T, c *client, env slackEnv) {
	t.Helper()
	path := "/api/v1/admin/connection-instances/api/" + conn2241Slack
	code, out := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"base_url": "https://slack.com/api", "auth_mode": "bearer", "credential": env.token,
			"connection_name": conn2241Slack, "call_timeout": "20s",
		},
		"description": "Acceptance #2241: a Slack workspace's bot",
	}))
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("saving the Slack connection: %d %v", code, out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, path, http.NoBody) })
	putChannel(t, c, chan2241Slack, map[string]any{"kind": "slack", "connection": conn2241Slack})
	t.Cleanup(func() { deleteChannel(t, c, chan2241Slack) })
}

// TestIssue2241_ASignedInPostOpensForSomeoneWithNoShare: posted with Signed-in
// users, the link opens for a signed-in person who holds no share, the post
// carries the preview as a file and the share URL, and the share is listed.
func TestIssue2241_ASignedInPostOpensForSomeoneWithNoShare(t *testing.T) {
	c := connect(t)
	env := mattermostBotChannel(t, c)
	assetID, marker := saveReport(t, c, "2241 signed-in")
	waitForThumbnail(t, c, assetID)
	stranger := newIssue2241Member(t, c, "2241-stranger", nil)
	if status, _ := rawGet(t, c.base, "/api/v1/portal/assets/"+assetID, stranger.key); status == http.StatusOK {
		t.Fatal("the stranger could open the asset before it was shared")
	}

	status, out := shareToChannel(t, c, assetID, map[string]any{
		"channel": chan2241Mattermost, "target": env.channelID, "access_mode": "authenticated", "message": "From Share.",
	})
	if status != http.StatusOK || number(t, out, "queued") != 1 {
		t.Fatalf("channel share: %d %v", status, out)
	}
	link, _ := out["link"].(string)
	path := viewPath(t, link)

	var post mattermostPost
	waitFor(t, "the post in Mattermost", func() (bool, string) {
		p, ok := mattermostPostWith(t, env, marker)
		post = p
		return ok, "waiting"
	})
	if !strings.Contains(post.message, link) {
		t.Errorf("the post does not carry the share URL %q: %q", link, post.message)
	}
	if len(post.fileIDs) == 0 {
		t.Error("the post carries no preview file")
	}

	if status, body := rawGet(t, c.base, path+"/content", stranger.key); status != http.StatusOK || !strings.Contains(string(body), "west,812") {
		t.Errorf("the signed-in stranger opening the posted link: %d %.200s", status, body)
	}
	found := false
	for _, s := range assetShares(t, c, assetID) {
		if s["origin"] == "channel_post" && s["access_mode"] == "authenticated" && strings.Contains(fmt.Sprint(s["posted_to"]), chan2241Mattermost) {
			found = true
		}
	}
	if !found {
		t.Error("the asset's share list has no channel_post share naming the channel")
	}
}

// TestIssue2241_APublicPostOpensSignedOutUntilItExpires: posted with Anyone
// with the link, a signed-out browser opens it; past its expiration it does
// not. The expiration is moved into the past in the database rather than
// waited for.
func TestIssue2241_APublicPostOpensSignedOutUntilItExpires(t *testing.T) {
	c := connect(t)
	putChannel(t, c, chan2241Email, map[string]any{"kind": "email", "recipients": []string{"acc-2241@example.com"}})
	t.Cleanup(func() { deleteChannel(t, c, chan2241Email) })
	assetID, _ := saveReport(t, c, "2241 public")

	status, out := shareToChannel(t, c, assetID, map[string]any{
		"channel": chan2241Email, "access_mode": "public", "expires_in": "168h",
	})
	if status != http.StatusOK || number(t, out, "queued") < 1 {
		t.Fatalf("channel share: %d %v", status, out)
	}
	if out["expires_at"] == nil {
		t.Error("the answer does not say when the link expires")
	}
	path := viewPath(t, fmt.Sprint(out["link"]))
	if status, body := rawGet(t, c.base, path+"/content", ""); status != http.StatusOK || !strings.Contains(string(body), "west,812") {
		t.Fatalf("signed out, before expiry: %d %.200s", status, body)
	}

	shareID, _ := out["share_id"].(string)
	if _, err := devDB(t).ExecContext(t.Context(),
		`UPDATE portal_shares SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, shareID); err != nil {
		t.Fatalf("expiring the share: %v", err)
	}
	if status, _ := rawGet(t, c.base, path+"/content", ""); status == http.StatusOK {
		t.Error("the posted public link still opens after it expired")
	}
}

// TestIssue2241_RevokingThePostsShareStopsTheLink: revoking the share a post
// created makes the posted link stop opening.
func TestIssue2241_RevokingThePostsShareStopsTheLink(t *testing.T) {
	c := connect(t)
	env := mattermostBotChannel(t, c)
	assetID, _ := saveReport(t, c, "2241 revoke")
	stranger := newIssue2241Member(t, c, "2241-revoke", nil)

	status, out := shareToChannel(t, c, assetID, map[string]any{
		"channel": chan2241Mattermost, "target": env.channelID, "access_mode": "authenticated",
	})
	if status != http.StatusOK {
		t.Fatalf("channel share: %d %v", status, out)
	}
	path := viewPath(t, fmt.Sprint(out["link"]))
	if status, _ := rawGet(t, c.base, path+"/content", stranger.key); status != http.StatusOK {
		t.Fatalf("before the revoke the link answered %d", status)
	}
	shareID, _ := out["share_id"].(string)
	if status, body := c.rest(http.MethodDelete, "/api/v1/portal/shares/"+shareID, http.NoBody); status >= 300 {
		t.Fatalf("revoke: %d %v", status, body)
	}
	other := newIssue2241Member(t, c, "2241-revoke-late", nil)
	if status, _ := rawGet(t, c.base, path+"/content", other.key); status == http.StatusOK {
		t.Error("the posted link still opens after its share was revoked")
	}
}

// TestIssue2241_SlackShowsThePreviewInTheChannelAndTheShareURL: the post to
// Slack carries the share URL, and the preview image is a file shared into
// the channel itself rather than into the message's thread.
func TestIssue2241_SlackShowsThePreviewInTheChannelAndTheShareURL(t *testing.T) {
	c := connect(t)
	env := readSlackEnv(t)
	slackBotChannel(t, c, env)
	assetID, marker := saveReport(t, c, "2241 slack")
	waitForThumbnail(t, c, assetID)

	status, out := shareToChannel(t, c, assetID, map[string]any{
		"channel": chan2241Slack, "target": env.channel, "access_mode": "authenticated",
	})
	if status != http.StatusOK || number(t, out, "queued") != 1 {
		t.Fatalf("channel share: %d %v", status, out)
	}
	link, _ := out["link"].(string)
	var msg map[string]any
	waitFor(t, "the post in Slack", func() (bool, string) {
		m, ok := slackMessageWith(t, env, marker)
		msg = m
		return ok, "reading conversations.history"
	})
	if !strings.Contains(fmt.Sprint(msg["blocks"]), link) {
		t.Errorf("the Slack post does not carry the share URL %q: %v", link, msg["blocks"])
	}
	ts, _ := msg["ts"].(string)
	waitFor(t, "the preview in the channel", func() (bool, string) {
		for _, m := range slackMessages(t, env, "conversations.history", url.Values{"oldest": {ts}, "limit": {"20"}}) {
			if files, ok := m["files"].([]any); ok && len(files) > 0 && m["thread_ts"] == nil {
				return true, ""
			}
		}
		return false, "no channel message with a file after the post"
	})
}

// TestIssue2241_APersonaWithoutSharingGetsNoShareAndNoChannels: a persona
// denied sharing is told so on /me, and the channel list, the channel share
// and the person share refuse it.
func TestIssue2241_APersonaWithoutSharingGetsNoShareAndNoChannels(t *testing.T) {
	admin := connect(t)
	member := newIssue2241Member(t, admin, "2241-noshare", map[string]any{"allow_sharing": false})
	_, me := member.client.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if me["sharing"] != false {
		t.Errorf("/me sharing = %v, want false", me["sharing"])
	}
	assetID, _ := saveReport(t, member.client, "2241 noshare")
	if status, _ := member.client.rest(http.MethodGet, "/api/v1/portal/channels", http.NoBody); status != http.StatusForbidden {
		t.Errorf("channel list: %d, want 403", status)
	}
	if status, _ := shareToChannel(t, member.client, assetID, map[string]any{"channel": chan2241Email, "access_mode": "authenticated"}); status != http.StatusForbidden {
		t.Errorf("channel share: %d, want 403", status)
	}
	if status, _ := member.client.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares",
		jsonBody(t, map[string]any{"access_mode": "authenticated"})); status != http.StatusForbidden {
		t.Errorf("link share: %d, want 403", status)
	}
	allowed := newIssue2241Member(t, admin, "2241-share", nil)
	_, me = allowed.client.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if me["sharing"] != true {
		t.Errorf("/me sharing for an allowed persona = %v", me["sharing"])
	}
}

// TestIssue2241_AViewerRecipientCannotShareToAChannel: someone the asset is
// shared with as a viewer cannot post it to a channel.
func TestIssue2241_AViewerRecipientCannotShareToAChannel(t *testing.T) {
	c := connect(t)
	putChannel(t, c, chan2241Email, map[string]any{"kind": "email", "recipients": []string{"acc-2241@example.com"}})
	t.Cleanup(func() { deleteChannel(t, c, chan2241Email) })
	assetID, _ := saveReport(t, c, "2241 viewer")
	viewer := newIssue2241Member(t, c, "2241-viewer", nil)
	if status, out := c.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares", jsonBody(t, map[string]any{
		"shared_with_user_id": viewer.userID, "permission": "viewer", "notify": false,
	})); status != http.StatusCreated {
		t.Fatalf("sharing with the viewer: %d %v", status, out)
	}
	before := len(assetShares(t, c, assetID))
	status, out := shareToChannel(t, viewer.client, assetID, map[string]any{"channel": chan2241Email, "access_mode": "authenticated"})
	if status == http.StatusOK {
		t.Fatalf("the viewer posted the asset: %v", out)
	}
	if after := len(assetShares(t, c, assetID)); after != before {
		t.Errorf("the refused post left %d shares, want %d", after, before)
	}
}

// TestIssue2241_TheSeparatePostRouteIsGone: the portal posts from Share only;
// the old route answers no post. The asset page's button is gone as well,
// which ui/src/components/AssetViewer.test.tsx checks.
func TestIssue2241_TheSeparatePostRouteIsGone(t *testing.T) {
	c := connect(t)
	assetID, _ := saveReport(t, c, "2241 route")
	status, _ := c.rest(http.MethodPost, "/api/v1/portal/channels/"+chan2241Email+"/publish",
		jsonBody(t, map[string]any{"asset_id": assetID}))
	if status == http.StatusOK {
		t.Error("POST /api/v1/portal/channels/{name}/publish still posts")
	}
}
