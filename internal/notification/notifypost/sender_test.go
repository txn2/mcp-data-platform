package notifypost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// testUpstream is an httptest server presented as the authorized transport a
// channel delivers through. It models the real Upstream faithfully in the one
// respect that matters here: Do is where the credential would be applied, so
// the header it sets is what a sender's request actually carries.
type testUpstream struct {
	base  string
	token string
	// err, when set, is returned instead of making the request, standing in
	// for a connection this process does not serve.
	err error
}

func (u testUpstream) BaseURL() string { return u.base }

func (u testUpstream) Do(req *http.Request) (*http.Response, error) {
	if u.err != nil {
		return nil, u.err
	}
	if u.token != "" {
		req.Header.Set("Authorization", "Bearer "+u.token)
	}
	// #nosec G704 -- the request targets this test's own httptest server,
	// whose address is not caller-controlled.
	return http.DefaultClient.Do(req) //nolint:wrapcheck // test transport
}

// capture records what an upstream received.
type capture struct {
	path   string
	auth   string
	ctype  string
	body   map[string]any
	rawErr error
}

// upstreamFor starts a server answering status with body, and returns the
// transport reaching it plus the capture it fills.
func upstreamFor(t *testing.T, status int, response string) (UpstreamFunc, *capture) {
	t.Helper()
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		got.ctype = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		got.rawErr = json.Unmarshal(raw, &got.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return func(string) (Upstream, error) {
		return testUpstream{base: srv.URL, token: "xoxb-test"}, nil
	}, got
}

func TestMattermostSender_PostsToV4Posts(t *testing.T) {
	up, got := upstreamFor(t, http.StatusCreated, `{"id":"post123"}`)
	ch := notification.Channel{
		Name: "ops", Kind: notification.ChannelKindMattermost,
		Connection: "mattermost-dev", Target: "ch_abc", Enabled: true,
	}
	if err := NewMattermostSender(up).Send(context.Background(), ch,
		notification.Document{Title: "Weekly report", Body: "| a | b |"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.path != "/api/v4/posts" {
		t.Errorf("path = %q, want /api/v4/posts", got.path)
	}
	if got.body["channel_id"] != "ch_abc" {
		t.Errorf("channel_id = %v, want the channel's target", got.body["channel_id"])
	}
	if msg, _ := got.body["message"].(string); !strings.Contains(msg, "| a | b |") {
		t.Errorf("markdown did not pass through; message = %q", msg)
	}
}

func TestWebhookSender_PostsTextToTheBase(t *testing.T) {
	up, got := upstreamFor(t, http.StatusOK, `ok`)
	ch := notification.Channel{
		Name: "hook", Kind: notification.ChannelKindWebhook,
		Connection: "hook-conn", Enabled: true,
	}
	if err := NewWebhookSender(up).Send(context.Background(), ch,
		notification.Document{Title: "Done", Link: "https://example.com/x"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// The whole address is the connection's base_url, including the secret
	// path segment an incoming webhook carries, so the sender names no path
	// and appends no separator: an httptest base URL has an empty path, and
	// the request reaches the server's root exactly as configured.
	if got.path != "" && got.path != "/" {
		t.Errorf("path = %q; a webhook posts to its base and names no path", got.path)
	}
	text, _ := got.body["text"].(string)
	if !strings.Contains(text, "Done") || !strings.Contains(text, "https://example.com/x") {
		t.Errorf("text = %q, want the title and the link", text)
	}
}

func TestStatusClassification(t *testing.T) {
	tests := []struct {
		status   int
		terminal bool
		delivers bool
	}{
		{http.StatusOK, false, true},
		{http.StatusCreated, false, true},
		{http.StatusTooManyRequests, false, false},
		{http.StatusInternalServerError, false, false},
		{http.StatusBadGateway, false, false},
		{http.StatusBadRequest, true, false},
		{http.StatusUnauthorized, true, false},
		{http.StatusForbidden, true, false},
		{http.StatusNotFound, true, false},
	}
	for _, tc := range tests {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			up, _ := upstreamFor(t, tc.status, `{"id":"x"}`)
			err := NewMattermostSender(up).Send(context.Background(), notification.Channel{
				Name: "ops", Kind: notification.ChannelKindMattermost,
				Connection: "c", Target: "t", Enabled: true,
			}, notification.Document{Title: "t"})
			if tc.delivers {
				if err != nil {
					t.Fatalf("Send: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("HTTP %d was treated as a delivery", tc.status)
			}
			if errors.Is(err, ErrTerminal) != tc.terminal {
				t.Errorf("terminal = %v, want %v", errors.Is(err, ErrTerminal), tc.terminal)
			}
		})
	}
}

func TestSend_UnreachableConnectionIsTerminal(t *testing.T) {
	// A channel naming a connection nothing serves can never deliver, so
	// retrying it five times only delays the report to the operator.
	up := UpstreamFunc(func(string) (Upstream, error) {
		return nil, errors.New("no api connection named ghost is served here")
	})
	err := NewMattermostSender(up).Send(context.Background(), notification.Channel{
		Name: "ops", Kind: notification.ChannelKindMattermost, Connection: "ghost", Target: "C1",
	}, notification.Document{Title: "t"})
	if !errors.Is(err, ErrTerminal) {
		t.Errorf("err = %v, want terminal", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q does not name the missing connection", err)
	}
}

func TestSend_TransportFailureRetries(t *testing.T) {
	// Not reaching the upstream is exactly what the retry budget is for.
	up := UpstreamFunc(func(string) (Upstream, error) {
		return testUpstream{base: "http://127.0.0.1:1", err: errors.New("connection refused")}, nil
	})
	err := NewWebhookSender(up).Send(context.Background(), notification.Channel{
		Name: "h", Kind: notification.ChannelKindWebhook, Connection: "c",
	}, notification.Document{Title: "t"})
	if err == nil {
		t.Fatal("Send succeeded against an unreachable upstream")
	}
	if errors.Is(err, ErrTerminal) {
		t.Errorf("err = %v, want retryable: an unreachable upstream may be reachable later", err)
	}
}

func TestSenders_DispatchesByKind(t *testing.T) {
	up, got := upstreamFor(t, http.StatusOK, `{"ok":true}`)
	senders := NewSenders(up)
	for _, kind := range []string{
		notification.ChannelKindMattermost,
		notification.ChannelKindWebhook,
	} {
		if _, ok := senders.For(kind); !ok {
			t.Errorf("no sender for kind %q", kind)
		}
	}
	// The email kind is deliberately absent: its rows are ordinary queue rows
	// delivered by the existing SMTP path.
	if _, ok := senders.For(notification.ChannelKindEmail); ok {
		t.Error("the email kind has a channel transport; its rows go through the mail path")
	}
	err := senders.Send(context.Background(), notification.Channel{
		Name: "x", Kind: notification.ChannelKindEmail,
	}, notification.Document{Title: "t"})
	if !errors.Is(err, ErrTerminal) {
		t.Errorf("err = %v, want a terminal refusal for a kind with no transport", err)
	}
	if got.path != "" {
		t.Errorf("an email channel reached an HTTP upstream at %q", got.path)
	}
}

func TestPlainText_KeepsTheLinkWhenTheBodyIsCut(t *testing.T) {
	// The cut exists so a long report arrives as a summary and a pointer. A
	// cut that dropped the pointer would leave the reader with neither.
	link := "https://portal.example.com/portal/assets/a1"
	doc := notification.Document{
		Title: "Long report",
		Body:  strings.Repeat("paragraph of text\n\n", 500),
		Link:  link,
	}
	out := plainText(doc, 400)
	if len(out) > 400 {
		t.Errorf("plainText returned %d bytes, over the 400-byte cap", len(out))
	}
	if !strings.HasSuffix(out, link) {
		t.Errorf("the link did not survive the cut; got %q", out)
	}
	if !strings.Contains(out, "Long report") {
		t.Error("the title did not survive the cut")
	}
}

func TestPlainText_ShortDocumentIsUnchanged(t *testing.T) {
	doc := notification.Document{Title: "Hi", Body: "there"}
	if got, want := plainText(doc, 1000), "Hi\n\nthere"; got != want {
		t.Errorf("plainText = %q, want %q", got, want)
	}
}

func TestExcerpt_BoundsAndFlattensAnUpstreamAnswer(t *testing.T) {
	// The excerpt is stored on the queue row and shown in the admin history,
	// so an upstream that answers with a page must not become the error.
	got := excerpt([]byte(strings.Repeat("a", 5000)))
	if len(got) > excerptBytes+3 {
		t.Errorf("excerpt is %d bytes, over the bound", len(got))
	}
	if got := excerpt([]byte("  line one\n\n  line two  ")); got != "line one line two" {
		t.Errorf("excerpt = %q, want the answer on one line", got)
	}
	if got := excerpt(nil); got != "(no body)" {
		t.Errorf("excerpt(nil) = %q, want a statement that there was no body", got)
	}
}

func TestJoinURL(t *testing.T) {
	tests := []struct{ base, path, want string }{
		{"https://slack.com/api", "/chat.postMessage", "https://slack.com/api/chat.postMessage"},
		{"https://slack.com/api/", "/chat.postMessage", "https://slack.com/api/chat.postMessage"},
		{"https://slack.com/api", "chat.postMessage", "https://slack.com/api/chat.postMessage"},
		// The webhook kind's whole address is the base, including the secret
		// path segment: appending a separator asks the upstream for a
		// different resource than the one configured.
		{"https://hooks.example.com/T/B/xyz", "", "https://hooks.example.com/T/B/xyz"},
		{"https://hooks.example.com/T/B/xyz", "/", "https://hooks.example.com/T/B/xyz"},
	}
	for _, tc := range tests {
		if got := joinURL(tc.base, tc.path); got != tc.want {
			t.Errorf("joinURL(%q, %q) = %q, want %q", tc.base, tc.path, got, tc.want)
		}
	}
}
