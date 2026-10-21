//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2088: publishing an asset to a channel makes it openable by the
// channel's readers. notify action=publish (and platform.publish) takes access:
// signed_in by default, which creates or reuses a link share any signed-in
// user opens, public with an expiry, or existing, the asset page's link.
// Checked over a real MCP client against the Mattermost fixture from make dev.
//
// Wire forms: access is sent absent (the default), and as the strings
// "existing" and "public"; expires_in is a string; target is a string naming
// the fixture's chat channel on a bot channel with no target of its own.

const chan2088 = "acc-2088-mattermost"

// botChannel2088 creates the Mattermost channel standing for its bot.
func botChannel2088(t *testing.T, c *client) mattermostEnv {
	t.Helper()
	env := readMattermostEnv(t)
	putChannel(t, c, chan2088, map[string]any{"kind": "mattermost", "connection": "mattermost-dev"})
	t.Cleanup(func() { deleteChannel(t, c, chan2088) })
	return env
}

// TestIssue2088_PublishMakesASignedInLinkAndReusesIt: the default posts a
// share's link a signed-in stranger opens, and publishing the same asset to
// the same place again reuses that share.
func TestIssue2088_PublishMakesASignedInLinkAndReusesIt(t *testing.T) {
	c := connect(t)
	env := botChannel2088(t, c)
	assetID, marker := saveReport(t, c, "2088 default")

	first := c.call("notify", map[string]any{"action": "publish", "channel": chan2088, "target": env.channelID, "asset": assetID})
	if number(t, first, "queued") != 1 || first["access"] != "signed_in" {
		t.Fatalf("publish: %v", first)
	}
	link, _ := first["link"].(string)
	stranger := newIssue2241Member(t, c, "2088-stranger", nil)
	if status, body := rawGet(t, c.base, viewPath(t, link)+"/content", stranger.key); status != http.StatusOK || !strings.Contains(string(body), "west,812") {
		t.Errorf("a signed-in stranger opening the published link: %d %.200s", status, body)
	}
	waitFor(t, "the published post", func() (bool, string) {
		p, ok := mattermostPostWith(t, env, marker)
		return ok && strings.Contains(p.message, link), "waiting"
	})

	second := c.call("notify", map[string]any{"action": "publish", "channel": chan2088, "target": env.channelID, "asset": assetID})
	if second["share_id"] != first["share_id"] || second["link"] != link {
		t.Errorf("publishing again made a new share: %v then %v", first["share_id"], second["share_id"])
	}
}

// TestIssue2088_ExistingPostsTheAssetPageAndMakesNoShare: access=existing is
// today's behavior, the asset page's link, and creates no share.
func TestIssue2088_ExistingPostsTheAssetPageAndMakesNoShare(t *testing.T) {
	c := connect(t)
	env := botChannel2088(t, c)
	assetID, _ := saveReport(t, c, "2088 existing")
	before := len(assetShares(t, c, assetID))
	out := c.call("notify", map[string]any{
		"action": "publish", "channel": chan2088, "target": env.channelID, "asset": assetID, "access": "existing",
	})
	if link, _ := out["link"].(string); !strings.HasSuffix(link, "/portal/assets/"+assetID) {
		t.Errorf("link = %q, want the asset page", link)
	}
	if after := len(assetShares(t, c, assetID)); after != before {
		t.Errorf("access=existing created %d shares", after-before)
	}
}

// TestIssue2088_AScriptPublishesAPublicLink: platform.publish takes access and
// expires_in, and a run's publish creates the expiring public share. The
// asset is its author's, saved before the run.
func TestIssue2088_AScriptPublishesAPublicLink(t *testing.T) {
	c := connect(t)
	env := botChannel2088(t, c)
	name := fmt.Sprintf("acc-2088-publisher-%d", time.Now().UnixNano())
	assetID, report := saveReport(t, c, "2088 script")
	c.saveScript(map[string]any{
		"command": "create", "name": name, "display_name": "Acceptance 2088 publisher",
		"source": fmt.Sprintf(`
def main():
    """Publishes a report with a public link."""
    platform.publish(channel = %q, name = %q, target = %q, access = "public", expires_in = "24h")
`, chan2088, assetID, env.channelID),
	}, nil)
	t.Cleanup(func() { c.call("manage_script", map[string]any{"command": "delete", "name": name}) })

	run := c.call("run_script", map[string]any{"name": name})
	if s, _ := run["status"].(string); s != "succeeded" {
		t.Fatalf("run: %v", run)
	}
	var post mattermostPost
	waitFor(t, "the script's post", func() (bool, string) {
		p, ok := mattermostPostWith(t, env, report)
		post = p
		return ok, "waiting"
	})
	i := strings.Index(post.message, "/portal/view/")
	if i < 0 {
		t.Fatalf("the script's post carries no share link: %q", post.message)
	}
	path := strings.Fields(post.message[i:])[0]
	if status, _ := rawGet(t, c.base, path+"/content", ""); status != http.StatusOK {
		t.Errorf("the script's public link, signed out: %d", status)
	}
}
