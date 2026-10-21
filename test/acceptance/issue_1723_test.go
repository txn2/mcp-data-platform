//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #1723's portal half: posting an asset to a channel from the portal,
// now the Share dialog's channel destination (#2241), and the run page's list
// of what a run posted, through the routes the portal calls. The tool half is
// in issue_1720_test.go.
//
// Wire forms. The channel-share body declares channel, access_mode and message
// as strings; message is sent present by one request and absent by the other.

// TestIssue1723_ThePortalPostsAnAssetToAChannel is the asset page's dialog:
// the channels the signed-in person reaches, then the asset posted to one.
func TestIssue1723_ThePortalPostsAnAssetToAChannel(t *testing.T) {
	env := readMattermostEnv(t)
	c := connect(t)
	putChannel(t, c, chanMattermost, map[string]any{
		"kind": "mattermost", "connection": "mattermost-dev", "target": env.channelID,
	})
	t.Cleanup(func() { deleteChannel(t, c, chanMattermost) })

	code, list := c.rest(http.MethodGet, "/api/v1/portal/channels", nil)
	if code != http.StatusOK {
		t.Fatalf("listing channels: %d %v", code, list)
	}
	listed := false
	for _, ch := range list["channels"].([]any) {
		if m, _ := ch.(map[string]any); m["name"] == chanMattermost {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("the channel was not offered: %v", list)
	}

	marker := fmt.Sprintf("Acc 1723 portal post %d", time.Now().UnixNano())
	saved := c.call("save_asset", map[string]any{
		"name": marker, "content": "region,total\nwest,812\neast,640\n", "content_type": "text/csv",
		"description": "Acceptance #1723: an asset posted from its page",
	})
	assetID, _ := saved["asset_id"].(string)

	code, out := c.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/channel-shares",
		jsonBody(t, map[string]any{"channel": chanMattermost, "access_mode": "authenticated", "message": "From the asset page."}))
	if code != http.StatusOK || number(t, out, "queued") != 1 {
		t.Fatalf("publish: %d %v", code, out)
	}
	waitFor(t, "the posted asset", func() (bool, string) {
		_, ok := mattermostPostWith(t, env, marker)
		return ok, "waiting"
	})
	post, _ := mattermostPostWith(t, env, marker)
	for _, want := range []string{"From the asset page.", "| west | 812 |", "/portal/view/"} {
		if !containing([]string{post.message}, want) {
			t.Errorf("the post lacks %q: %q", want, post.message)
		}
	}

	// A channel the person cannot reach is refused in the tool's words.
	code, out = c.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/channel-shares",
		jsonBody(t, map[string]any{"channel": "no-such-channel", "access_mode": "authenticated"}))
	if code != http.StatusBadRequest {
		t.Errorf("an unknown channel: %d %v", code, out)
	}
}

// TestIssue1723_TheRunPageListsWhatTheRunPosted: a script that posts, and the
// run's page reading back the post with its delivery status.
func TestIssue1723_TheRunPageListsWhatTheRunPosted(t *testing.T) {
	env := readMattermostEnv(t)
	c := connect(t)
	putChannel(t, c, chanMattermost, map[string]any{
		"kind": "mattermost", "connection": "mattermost-dev", "target": env.channelID,
	})
	t.Cleanup(func() { deleteChannel(t, c, chanMattermost) })

	marker := fmt.Sprintf("acc-1723 run post %d", time.Now().UnixNano())
	name := fmt.Sprintf("acc-1723-poster-%d", time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name, "display_name": "Acceptance 1723 poster",
		"source": fmt.Sprintf(`
def main():
    """Posts one finding."""
    platform.notify(channel = %q, title = %q, body = "One finding.")
`, chanMattermost, marker),
	}, nil)
	t.Cleanup(func() { c.call("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	scriptID, _ := got["id"].(string)

	run := c.call("run_script", map[string]any{"name": name})
	runID, _ := run["run_id"].(string)
	if s, _ := run["status"].(string); s != "succeeded" || runID == "" {
		t.Fatalf("run: %v", run)
	}

	path := "/api/v1/portal/scripts/" + scriptID + "/runs/" + runID + "/posts"
	var posts []any
	waitFor(t, "the run's post to be delivered", func() (bool, string) {
		code, out := c.rest(http.MethodGet, path, nil)
		if code != http.StatusOK {
			return false, fmt.Sprint(code, out)
		}
		posts, _ = out["posts"].([]any)
		if len(posts) != 1 {
			return false, fmt.Sprint(len(posts), " posts")
		}
		p, _ := posts[0].(map[string]any)
		return p["status"] == "sent", fmt.Sprint(p)
	})
	p, _ := posts[0].(map[string]any)
	if p["channel"] != chanMattermost || p["title"] != marker {
		t.Errorf("post = %v", p)
	}
}
