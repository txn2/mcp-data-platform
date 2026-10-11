package notifypost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// tlsUpstream reaches an httptest TLS server with the server's own client,
// applying the bot token the way the connection's authenticator does.
type tlsUpstream struct {
	srv   *httptest.Server
	token string
}

func (u tlsUpstream) BaseURL() string { return u.srv.URL + "/api" }

func (u tlsUpstream) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+u.token)
	// #nosec G704 -- the request targets this test's own httptest server.
	return u.srv.Client().Do(req) //nolint:wrapcheck // test transport
}

// slackCall is one request the Slack double received.
type slackRequest struct {
	path, auth, ctype string
	body              []byte
}

// slackDouble answers the Web API routes a post with files uses. answers maps
// a route to the envelope it answers with; the upload route answers HTTP 200.
func slackDouble(t *testing.T, answers map[string]string) (up UpstreamFunc, calls func() []slackRequest) {
	t.Helper()
	var (
		mu  sync.Mutex
		got []slackRequest
	)
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		mu.Lock()
		got = append(got, slackRequest{path: r.URL.Path, auth: r.Header.Get("Authorization"), ctype: r.Header.Get("Content-Type"), body: raw})
		mu.Unlock()
		if r.URL.Path == "/upload/f1" || r.URL.Path == "/upload/f2" {
			return
		}
		answer, ok := answers[r.URL.Path]
		if !ok {
			answer = `{"ok":true}`
		}
		answer = strings.ReplaceAll(answer, "SERVER", srv.URL)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	up = func(string) (Upstream, error) { return tlsUpstream{srv: srv, token: "xoxb-test"}, nil }
	return up, func() []slackRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]slackRequest(nil), got...)
	}
}

func slackChannel() notification.Channel {
	return notification.Channel{
		Name: "ops", Kind: notification.ChannelKindSlack, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: "slack", Target: "C123",
	}
}

func TestSlack_PostsTheBlocksWithTheBotToken(t *testing.T) {
	up, calls := slackDouble(t, map[string]string{"/api/chat.postMessage": `{"ok":true,"ts":"1.2"}`})
	d := reportDelivery()
	d.Files = nil
	if err := NewSlackSender(up).Send(context.Background(), slackChannel(), d); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 1 || got[0].path != "/api/chat.postMessage" || got[0].auth != "Bearer xoxb-test" {
		t.Fatalf("calls = %+v", got)
	}
	var body map[string]any
	if err := json.Unmarshal(got[0].body, &body); err != nil {
		t.Fatal(err)
	}
	if body["channel"] != "C123" || body["text"] != "Weekly revenue" {
		t.Errorf("body = %v", body)
	}
	goldenFile(t, "report.slack.json", string(mustIndent(t, body)))
}

func mustIndent(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSlack_UploadsThenPostsThenSharesIntoTheThread(t *testing.T) {
	up, calls := slackDouble(t, map[string]string{
		"/api/files.getUploadURLExternal": `{"ok":true,"upload_url":"SERVER/upload/f1","file_id":"F1"}`,
		"/api/chat.postMessage":           `{"ok":true,"ts":"1700.1"}`,
	})
	if err := NewSlackSender(up).Send(context.Background(), slackChannel(), reportDelivery()); err != nil {
		t.Fatal(err)
	}
	got := calls()
	paths := make([]string, 0, len(got))
	for _, c := range got {
		paths = append(paths, c.path)
	}
	want := "/api/files.getUploadURLExternal,/upload/f1,/api/chat.postMessage,/api/files.completeUploadExternal"
	if strings.Join(paths, ",") != want {
		t.Fatalf("order = %v, want %s", paths, want)
	}
	if !strings.Contains(string(got[0].body), "filename=chart.png") || string(got[1].body) != "\x89PNG" {
		t.Errorf("upload: %q then %q", got[0].body, got[1].body)
	}
	var share map[string]any
	if err := json.Unmarshal(got[3].body, &share); err != nil {
		t.Fatal(err)
	}
	if share["channel_id"] != "C123" || share["thread_ts"] != "1700.1" {
		t.Errorf("share = %v", share)
	}
}

func TestSlack_AnUploadAddressOffTheConnectionsDomainIsRefused(t *testing.T) {
	up, calls := slackDouble(t, map[string]string{
		"/api/files.getUploadURLExternal": `{"ok":true,"upload_url":"https://elsewhere.example.com/u","file_id":"F1"}`,
	})
	err := NewSlackSender(up).Send(context.Background(), slackChannel(), reportDelivery())
	if !errors.Is(err, ErrTerminal) || !strings.Contains(err.Error(), "own domain") {
		t.Fatalf("err = %v", err)
	}
	if len(calls()) != 1 {
		t.Error("the bytes were sent to an address off the connection's domain")
	}
}

func TestSlack_AShareThatFailsAfterThePostSaysTheMessageWasPosted(t *testing.T) {
	up, _ := slackDouble(t, map[string]string{
		"/api/files.getUploadURLExternal":   `{"ok":true,"upload_url":"SERVER/upload/f1","file_id":"F1"}`,
		"/api/files.completeUploadExternal": `{"ok":false,"error":"internal_error"}`,
	})
	err := NewSlackSender(up).Send(context.Background(), slackChannel(), reportDelivery())
	if !errors.Is(err, ErrTerminal) || !strings.Contains(err.Error(), "the message was posted") {
		t.Fatalf("err = %v", err)
	}
}

func TestSlack_ReadsTheEnvelopeNotOnlyTheStatus(t *testing.T) {
	cases := []struct {
		answer   string
		terminal bool
		says     string
	}{
		{`{"ok":false,"error":"not_in_channel"}`, true, "/invite"},
		{`{"ok":false,"error":"missing_scope"}`, true, "chat:write"},
		{`{"ok":false,"error":"token_revoked"}`, true, "reinstall"},
		{`{"ok":false,"error":"invalid_auth"}`, true, "bot token"},
		{`{"ok":false,"error":"account_inactive"}`, true, "inactive"},
		{`{"ok":false,"error":"channel_not_found"}`, true, "target"},
		{`{"ok":false,"error":"not_authed"}`, true, "bearer"},
		{`{"ok":false,"error":"is_archived"}`, true, "is_archived"},
		{`{"ok":false}`, true, "unknown_error"},
		{`{"ok":false,"error":"ratelimited"}`, false, "ratelimited"},
		{`{"ok":false,"error":"internal_error"}`, false, "internal_error"},
		{`<html>not slack</html>`, true, "base_url"},
		{`{"channel":"C1"}`, true, "base_url"},
	}
	for _, tc := range cases {
		up, _ := slackDouble(t, map[string]string{"/api/chat.postMessage": tc.answer})
		d := reportDelivery()
		d.Files = nil
		err := NewSlackSender(up).Send(context.Background(), slackChannel(), d)
		if err == nil {
			t.Errorf("%s: an ok: false answer read as a delivery", tc.answer)
			continue
		}
		if errors.Is(err, ErrTerminal) != tc.terminal || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: err = %v (terminal %v), want terminal=%v mentioning %q", tc.answer, err, errors.Is(err, ErrTerminal), tc.terminal, tc.says)
		}
	}
}

func TestSlack_ARefusedUploadFailsBeforeAnyPost(t *testing.T) {
	up, calls := slackDouble(t, map[string]string{"/api/files.getUploadURLExternal": `{"ok":false,"error":"missing_scope"}`})
	if err := NewSlackSender(up).Send(context.Background(), slackChannel(), reportDelivery()); !errors.Is(err, ErrTerminal) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range calls() {
		if c.path == "/api/chat.postMessage" {
			t.Error("a message was posted after its upload was refused")
		}
	}
}

func TestSlackMessage_CutsAndKeepsTheLinks(t *testing.T) {
	d := reportDelivery()
	d.Body = strings.Repeat("a paragraph of the report\n\n", 2000)
	d.Title = strings.Repeat("Long title ", 30)
	msg := slackMessage("C1", d)
	blocks, _ := msg["blocks"].([]map[string]any)
	header, _ := blocks[0]["text"].(map[string]any)
	headerText, _ := header["text"].(string)
	if n := len([]rune(headerText)); n > slackMaxHeaderChars {
		t.Errorf("header is %d characters", n)
	}
	md, _ := blocks[1]["text"].(string)
	if len(md) > SlackMaxMarkdownBytes || !strings.HasSuffix(md, "Attachment: raw.csv https://portal.example.com/portal/resources/r1") {
		t.Errorf("markdown is %d bytes, ends %q", len(md), md[len(md)-80:])
	}
	if blocks[2]["type"] != "context" {
		t.Errorf("no context block with the link: %v", blocks[2])
	}
	bare := slackMessage("C1", notification.Delivery{Document: notification.Document{Title: "t"}})
	if b, _ := bare["blocks"].([]map[string]any); len(b) != 1 {
		t.Errorf("a title-only document carries %d blocks, want the header alone", len(b))
	}
	onlyLinks := notification.Delivery{Linked: []notification.Attachment{{Name: "x.csv"}}}
	if got := slackMarkdown(onlyLinks); got != "Attachment: x.csv" {
		t.Errorf("links alone = %q", got)
	}
}

func TestSameDomain(t *testing.T) {
	cases := []struct {
		base, target string
		want         bool
	}{
		{"https://slack.com/api", "https://files.slack.com/upload/v1/x", true},
		{"https://slack.com/api", "https://slack.com/x", true},
		{"https://slack.com/api", "http://files.slack.com/x", false},
		{"https://slack.com/api", "https://evil.com/slack.com", false},
		{"https://slack.com/api", "https://notslack.com/x", false},
		{"https://api.slack.com/api", "https://files.slack.com/x", true},
		{"https://127.0.0.1:9/api", "https://127.0.0.1:9/u", true},
		{"https://127.0.0.1:9/api", "https://127.0.0.2:9/u", false},
		{"https://slack.com/api", "", false},
		{"::", "https://slack.com", false},
	}
	for _, tc := range cases {
		if got := sameDomain(tc.base, tc.target); got != tc.want {
			t.Errorf("sameDomain(%q, %q) = %v", tc.base, tc.target, got)
		}
	}
}

func TestSendersDispatchSlack(t *testing.T) {
	if _, ok := NewSenders(nil, "").For(notification.ChannelKindSlack); !ok {
		t.Error("the slack kind has no sender")
	}
}
