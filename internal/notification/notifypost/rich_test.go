package notifypost

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// update rewrites the golden files from the current output.
var update = flag.Bool("update", false, "rewrite golden files")

// reportBody is the document each kind's golden test renders: a heading, a
// table, a list, a code block and a link.
const reportBody = "## Revenue by region\n\n" +
	"| Region | Revenue |\n| --- | ---: |\n| West | 1,204,000 |\n| East | 988,500 |\n\n" +
	"- West led for the third week\n- East recovered\n\n" +
	"```sql\nSELECT region, SUM(amount) FROM sales GROUP BY 1\n```\n\n" +
	"See the [full report](https://portal.example.com/portal/assets/a1)."

// reportDelivery is the report with one file read and one sent as a link.
func reportDelivery() notification.Delivery {
	return notification.Delivery{
		ID: "ntf_7",
		Document: notification.Document{
			Title: "Weekly revenue",
			Body:  reportBody,
			Link:  "https://portal.example.com/portal/assets/a1",
			Attachments: []notification.Attachment{
				{AssetID: "a1", Name: "chart.png", Link: "https://portal.example.com/portal/assets/a1"},
				{ResourceID: "r1", Name: "raw.csv", Link: "https://portal.example.com/portal/resources/r1"},
			},
		},
		Files:  []notification.File{{Name: "chart.png", ContentType: "image/png", Data: []byte("\x89PNG")}},
		Linked: []notification.Attachment{{ResourceID: "r1", Name: "raw.csv", Link: "https://portal.example.com/portal/resources/r1"}},
	}
}

func goldenFile(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) // #nosec G304 -- a fixed name under testdata
	if err != nil {
		t.Fatalf("reading golden %s (run with -update to create it): %v", name, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from its golden file:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestMarkdownMessage_Golden(t *testing.T) {
	goldenFile(t, "report.mattermost.md", markdownMessage(reportDelivery(), MattermostMaxTextBytes))
}

func TestPlainText_Golden(t *testing.T) {
	goldenFile(t, "report.webhook.txt", plainText(webhookDelivery(reportDelivery()), WebhookMaxTextBytes))
}

func TestMarkdownMessage_TheCutKeepsTheLinkAndTheAttachmentLines(t *testing.T) {
	d := reportDelivery()
	d.Body = strings.Repeat("a paragraph of the report\n\n", 2000)
	out := markdownMessage(d, MattermostMaxTextBytes)
	if len(out) > MattermostMaxTextBytes {
		t.Fatalf("message is %d bytes, over the %d cap", len(out), MattermostMaxTextBytes)
	}
	if !strings.HasSuffix(out, "Attachment: raw.csv https://portal.example.com/portal/resources/r1\nhttps://portal.example.com/portal/assets/a1") {
		t.Errorf("the attachment line and the link did not survive the cut; message ends %q", out[len(out)-200:])
	}
	if !strings.HasPrefix(out, "#### Weekly revenue") {
		t.Error("the title heading did not survive the cut")
	}
}

func TestPlainText_TheCutKeepsTheLink(t *testing.T) {
	d := reportDelivery()
	d.Body = strings.Repeat("x", 3*WebhookMaxTextBytes)
	out := plainText(webhookDelivery(d), WebhookMaxTextBytes)
	if len(out) > WebhookMaxTextBytes || !strings.HasSuffix(out, d.Link) {
		t.Errorf("len %d, ends %q; want at most %d bytes ending in the link", len(out), out[len(out)-80:], WebhookMaxTextBytes)
	}
}

func TestMarkdownMessage_StatesTheSuppressedCount(t *testing.T) {
	since := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	d := notification.Delivery{Document: notification.Document{Title: "Feed stale", Suppressed: 1, SuppressedSince: &since}}
	if got := markdownMessage(d, 1000); !strings.Contains(got, "_1 repeat of this alert since 2026-10-10 09:00 UTC was not posted._") {
		t.Errorf("message = %q, want the suppressed count", got)
	}
}

func TestWebhookDelivery_EveryAttachmentTravelsAsALink(t *testing.T) {
	d := webhookDelivery(reportDelivery())
	if len(d.Files) != 0 {
		t.Errorf("a webhook delivery carries %d files; it carries none", len(d.Files))
	}
	names := make([]string, 0, len(d.Linked))
	for _, a := range d.Linked {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "raw.csv,chart.png" {
		t.Errorf("linked = %v, want both attachments, the unread one first", names)
	}
}

// mattermostCall is one request the Mattermost double received.
type mattermostCall struct {
	path, auth, ctype string
	form              map[string]string
	fileName, file    string
	post              mattermostPostRequest
}

// mattermostDouble answers the two routes a post with files uses, recording
// each request in order. uploadStatus is what the files route answers.
func mattermostDouble(t *testing.T, uploadStatus int) (up UpstreamFunc, calls func() []mattermostCall) {
	t.Helper()
	var (
		mu       sync.Mutex
		recorded []mattermostCall
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := mattermostCall{path: r.URL.Path, auth: r.Header.Get("Authorization"), ctype: r.Header.Get("Content-Type")}
		switch r.URL.Path {
		case mattermostFilePath:
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("upload is not multipart: %v", err)
			}
			c.form = map[string]string{"channel_id": r.FormValue("channel_id")}
			f, hdr, err := r.FormFile("files")
			if err == nil {
				raw, _ := io.ReadAll(f)
				c.fileName, c.file = hdr.Filename, string(raw)
			}
			w.WriteHeader(uploadStatus)
			_, _ = w.Write([]byte(`{"file_infos":[{"id":"file-1"}],"client_ids":[]}`))
		case mattermostPostPath:
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &c.post)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"post-1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
		mu.Lock()
		recorded = append(recorded, c)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	up = func(string) (Upstream, error) { return testUpstream{base: srv.URL, token: "bot-token"}, nil }
	return up, func() []mattermostCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]mattermostCall(nil), recorded...)
	}
}

func mattermostChannel() notification.Channel {
	return notification.Channel{Name: "ops", Kind: notification.ChannelKindMattermost, Connection: "mm", Target: "chan-9", Enabled: true, Mode: notification.ChannelModeImmediate}
}

func TestMattermost_UploadsEachFileThenPostsWithTheirIDs(t *testing.T) {
	up, calls := mattermostDouble(t, http.StatusCreated)
	if err := NewMattermostSender(up).Send(context.Background(), mattermostChannel(), reportDelivery()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := calls()
	if len(got) != 2 || got[0].path != mattermostFilePath || got[1].path != mattermostPostPath {
		t.Fatalf("calls = %+v, want an upload then a post", got)
	}
	upload, post := got[0], got[1].post
	if upload.auth != "Bearer bot-token" || upload.form["channel_id"] != "chan-9" {
		t.Errorf("upload carried auth %q and channel %q", upload.auth, upload.form["channel_id"])
	}
	if upload.fileName != "chart.png" || upload.file != "\x89PNG" {
		t.Errorf("uploaded %q with %q, want chart.png and its bytes", upload.fileName, upload.file)
	}
	if strings.Join(post.FileIDs, ",") != "file-1" || post.ChannelID != "chan-9" {
		t.Errorf("post = %+v, want file-1 on chan-9", post)
	}
	if !strings.Contains(post.Message, "| West | 1,204,000 |") || !strings.Contains(post.Message, "Attachment: raw.csv") {
		t.Errorf("the post did not carry the markdown body and the linked attachment: %q", post.Message)
	}
}

func TestMattermost_ARefusedUploadFailsTheSendBeforeAnyPost(t *testing.T) {
	up, calls := mattermostDouble(t, http.StatusForbidden)
	err := NewMattermostSender(up).Send(context.Background(), mattermostChannel(), reportDelivery())
	if !errors.Is(err, ErrTerminal) {
		t.Fatalf("err = %v, want a terminal refusal", err)
	}
	for _, c := range calls() {
		if c.path == mattermostPostPath {
			t.Error("a post was made after its upload was refused")
		}
	}
}

func TestMattermost_AnUploadAnswerNamingNoFileIsTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>not mattermost</html>`))
	}))
	t.Cleanup(srv.Close)
	up := func(string) (Upstream, error) { return testUpstream{base: srv.URL}, nil }
	err := NewMattermostSender(up).Send(context.Background(), mattermostChannel(), reportDelivery())
	if !errors.Is(err, ErrTerminal) || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("err = %v, want a terminal error naming the base_url", err)
	}
}

func TestMattermost_ARateLimitedUploadRetries(t *testing.T) {
	up, _ := mattermostDouble(t, http.StatusTooManyRequests)
	err := NewMattermostSender(up).Send(context.Background(), mattermostChannel(), reportDelivery())
	if err == nil || errors.Is(err, ErrTerminal) {
		t.Fatalf("err = %v, want a retryable failure", err)
	}
}

func TestFileName_RemovesPathSeparators(t *testing.T) {
	cases := []struct{ in, want string }{{"", "attachment"}, {"a/b\\c.png", "a_b_c.png"}, {" r.csv ", "r.csv"}}
	for _, tc := range cases {
		in, want := tc.in, tc.want
		if got := fileName(notification.File{Name: in}); got != want {
			t.Errorf("fileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBulletin_OneDocumentListingEachItem(t *testing.T) {
	ch := notification.Channel{Name: "reports"}
	ds := []notification.Delivery{
		{ID: "ntf_1", Document: notification.Document{
			Title: "Feed stale", Body: "Last row 41m ago.\n\nDetail.", Link: "https://p/1", Type: notification.DocumentScriptFinding,
			Attachments: []notification.Attachment{{AssetID: "a"}},
		}},
		{ID: "ntf_2", Document: notification.Document{Title: "Report ready", Body: "Revenue rose 4%.", Link: "https://p/2"}},
	}
	b := Bulletin(ch, ds)
	if b.ID != "ntf_1" || b.Title != "reports: 2 updates today" {
		t.Errorf("bulletin id %q title %q", b.ID, b.Title)
	}
	want := "**Feed stale**\nLast row 41m ago.\nhttps://p/1\n\n**Report ready**\nRevenue rose 4%.\nhttps://p/2"
	if b.Body != want {
		t.Errorf("body = %q, want %q", b.Body, want)
	}
	if len(b.Attachments) != 0 {
		t.Error("a bulletin carried attachments")
	}
	if one := Bulletin(ch, ds[:1]); one.Title != "reports: 1 update today" {
		t.Errorf("single-item title %q", one.Title)
	}
	if empty := Bulletin(ch, nil); empty.Title != "" {
		t.Errorf("empty bulletin title %q", empty.Title)
	}
}

func TestEnvelope_NamesTheAttachments(t *testing.T) {
	env := (&WebhookSender{deployment: "d"}).envelope(notification.Channel{Name: "hook"}, reportDelivery())
	if len(env.Attachments) != 2 || env.Attachments[1].Link == "" {
		t.Errorf("envelope attachments = %+v, want both with links", env.Attachments)
	}
}
