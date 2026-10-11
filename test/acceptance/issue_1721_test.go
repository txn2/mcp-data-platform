//go:build integration

package acceptance

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Issue #1721: what a channel carries. A document's markdown renders as the
// kind can show it, attachments travel as files, and what arrives is read back
// from the destination itself -- the Mattermost API and the mailpit API --
// never from the platform's record of having sent it.
//
// Wire forms. notify's attachments parameter is a list whose entries the
// schema admits as strings or as objects: the Mattermost criterion sends a
// string (the asset id) and an object ({"resource_id": ...}) in one list, and
// the email criterion sends the reverse ({"asset_id": ...} and the resource's
// mcp:resource: reference string). title and body are typed strings.

// pngBytes is a small PNG the attachment criteria send as a file.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := range 4 {
		for y := range 4 {
			img.Set(x, y, color.RGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// reportMarkdown is the document both criteria send: a heading, a table, a
// list, a code block and a link.
const reportMarkdown = "## Revenue by region\n\n" +
	"| Region | Revenue |\n| --- | ---: |\n| West | 1,204,000 |\n| East | 988,500 |\n\n" +
	"- West led for the third week\n\n" +
	"```sql\nSELECT region, SUM(amount) FROM sales GROUP BY 1\n```\n\n" +
	"See the [dashboard](https://example.com/dash)."

// attachmentFixtures saves a markdown asset and a PNG resource the caller can
// read, returning their ids.
func attachmentFixtures(t *testing.T, c *client, label string) (assetID, resourceID string) {
	t.Helper()
	saved := c.call("save_asset", map[string]any{
		"name":         "acc-1721 " + label + " notes",
		"content":      "# Notes\n\nAttached to a channel post.\n",
		"content_type": "text/markdown",
		"description":  "Acceptance #1721: an asset attached to a post",
	})
	assetID, _ = saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	res := c.call("manage_resource", map[string]any{
		"action":         "create",
		"filename":       "chart-" + label + ".png",
		"display_name":   "acc-1721 " + label + " chart",
		"path":           "acceptance-1721",
		"description":    "Acceptance #1721: a PNG attached to a post",
		"content_base64": base64.StdEncoding.EncodeToString(pngBytes(t)),
		"content_type":   "image/png",
		"if_exists":      "replace",
	})
	resourceID, _ = res["resource_id"].(string)
	if resourceID == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", res)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody) })
	return assetID, resourceID
}

// mattermostPost is a post as the Mattermost API answers it.
type mattermostPost struct {
	message string
	fileIDs []string
}

// mattermostPostWith returns the fixture channel's post containing marker.
func mattermostPostWith(t *testing.T, env mattermostEnv, marker string) (mattermostPost, bool) {
	t.Helper()
	out := getJSON(t, fmt.Sprintf("%s/api/v4/channels/%s/posts?per_page=50", env.url, env.channelID), env.token)
	posts, _ := out["posts"].(map[string]any)
	for _, p := range posts {
		post, _ := p.(map[string]any)
		msg, _ := post["message"].(string)
		if !strings.Contains(msg, marker) {
			continue
		}
		found := mattermostPost{message: msg}
		ids, _ := post["file_ids"].([]any)
		for _, id := range ids {
			if s, ok := id.(string); ok {
				found.fileIDs = append(found.fileIDs, s)
			}
		}
		return found, true
	}
	return mattermostPost{}, false
}

// TestIssue1721_MattermostCarriesTheRenderedBodyAndTheFiles: the markdown
// arrives as markdown under the title as a heading, and each attachment is a
// file on the post, uploaded to the channel.
func TestIssue1721_MattermostCarriesTheRenderedBodyAndTheFiles(t *testing.T) {
	env := readMattermostEnv(t)
	c := connect(t)
	putChannel(t, c, chanMattermost, map[string]any{
		"kind": "mattermost", "connection": "mattermost-dev", "target": env.channelID,
	})
	t.Cleanup(func() { deleteChannel(t, c, chanMattermost) })
	assetID, resourceID := attachmentFixtures(t, c, "mm")

	marker := fmt.Sprintf("acc-1721 weekly %d", time.Now().UnixNano())
	res := c.call("notify", map[string]any{
		"action": "send", "channel": chanMattermost, "title": marker, "body": reportMarkdown,
		"attachments": []any{assetID, map[string]any{"resource_id": resourceID}},
	})
	if number(t, res, "queued") != 1 {
		t.Fatalf("send: %v", res)
	}

	var post mattermostPost
	waitFor(t, "the post with its files", func() (bool, string) {
		p, ok := mattermostPostWith(t, env, marker)
		post = p
		return ok, fmt.Sprintf("found=%v files=%d", ok, len(p.fileIDs))
	})
	if !strings.HasPrefix(post.message, "#### "+marker) {
		t.Errorf("the title is not the post's heading: %q", post.message)
	}
	for _, want := range []string{"| West | 1,204,000 |", "```sql", "[dashboard](https://example.com/dash)"} {
		if !strings.Contains(post.message, want) {
			t.Errorf("the post lost %q from the markdown: %q", want, post.message)
		}
	}
	if len(post.fileIDs) != 2 {
		t.Fatalf("the post carries %d files, want the asset and the resource", len(post.fileIDs))
	}
	names := []string{}
	for _, id := range post.fileIDs {
		info := getJSON(t, env.url+"/api/v4/files/"+id+"/info", env.token)
		name, _ := info["name"].(string)
		names = append(names, name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "chart-mm.png") || !strings.Contains(joined, ".md") {
		t.Errorf("file names = %v, want the PNG resource and the markdown asset", names)
	}
}

// mailpitMessageWith returns mailpit's id for the message whose subject holds
// marker.
func mailpitMessageWith(t *testing.T, marker string) (string, bool) {
	t.Helper()
	out := getJSON(t, mailpitBase()+"/api/v1/search?query="+url.QueryEscape("subject:\""+marker+"\""), "")
	items, _ := out["messages"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if s, _ := m["Subject"].(string); strings.Contains(s, marker) {
			id, _ := m["ID"].(string)
			return id, id != ""
		}
	}
	return "", false
}

// TestIssue1721_EmailRendersTheBodyAndAttachesTheFiles: the HTML part is the
// markdown rendered (a real table, a heading, a link), the text part keeps the
// markdown as written, a script tag in the body reaches neither as markup,
// and the attachments are MIME parts.
func TestIssue1721_EmailRendersTheBodyAndAttachesTheFiles(t *testing.T) {
	c := connect(t)
	putChannel(t, c, chanEmail, map[string]any{
		"kind": "email", "recipients": []string{"acc-1721@example.com"},
	})
	t.Cleanup(func() { deleteChannel(t, c, chanEmail) })
	assetID, resourceID := attachmentFixtures(t, c, "mail")

	marker := fmt.Sprintf("acc-1721 emailed report %d", time.Now().UnixNano())
	res := c.call("notify", map[string]any{
		"action": "send", "channel": chanEmail, "title": marker,
		"body":        reportMarkdown + "\n\n<script>alert('x')</script>",
		"attachments": []any{map[string]any{"asset_id": assetID}, "mcp:resource:" + resourceID},
	})
	if number(t, res, "queued") != 1 {
		t.Fatalf("send: %v", res)
	}

	var id string
	waitFor(t, "the email", func() (bool, string) {
		got, ok := mailpitMessageWith(t, marker)
		id = got
		return ok, "searching mailpit"
	})
	msg := getJSON(t, mailpitBase()+"/api/v1/message/"+id, "")
	html, _ := msg["HTML"].(string)
	text, _ := msg["Text"].(string)
	for _, want := range []string{"<table>", "<h2>Revenue by region</h2>", `href="https://example.com/dash"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the HTML part lacks %q", want)
		}
	}
	if strings.Contains(strings.ToLower(html), "<script") {
		t.Error("a script tag in the body reached the HTML part as markup")
	}
	if !strings.Contains(text, "| West | 1,204,000 |") {
		t.Error("the text part does not carry the markdown as written")
	}
	atts, _ := msg["Attachments"].([]any)
	names := []string{}
	for _, a := range atts {
		m, _ := a.(map[string]any)
		name, _ := m["FileName"].(string)
		names = append(names, name)
	}
	if len(atts) != 2 || !strings.Contains(strings.Join(names, ","), "chart-mail.png") {
		t.Errorf("attachments = %v, want the PNG resource and the markdown asset", names)
	}
}
