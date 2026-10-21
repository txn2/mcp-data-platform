package notifypost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// routes starts a server answering each path (query excluded) with its body,
// recording every request URI it was sent.
func routes(t *testing.T, answers map[string]string) (up UpstreamFunc, requests func() []string) {
	t.Helper()
	var (
		mu  sync.Mutex
		got []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.URL.RequestURI())
		mu.Unlock()
		key := r.URL.Path
		if q := r.URL.Query().Get("types"); q != "" {
			key += "?types=" + q
		}
		answer, ok := answers[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return func(string) (Upstream, error) { return testUpstream{base: srv.URL, token: "t"}, nil },
		func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
}

func TestTargets_SlackListsTheBotsChannelsByNameAcrossPages(t *testing.T) {
	up, calls := routes(t, map[string]string{
		"/users.conversations?types=public_channel,private_channel": `{"ok":true,"channels":[{"id":"C2","name":"ops"}],"response_metadata":{"next_cursor":"p2"}}`,
	})
	// The second page is told apart by its cursor, which routes does not key
	// on, so the double answers it from a wrapped handler instead.
	page := 0
	wrapped := func(name string) (Upstream, error) {
		u, err := up(name)
		return pagedUpstream{Upstream: u, page: &page}, err
	}
	got, err := NewSenders(wrapped, "").Targets(context.Background(),
		notification.Channel{Name: "slack", Kind: notification.ChannelKindSlack, Connection: "slack"})
	if err != nil {
		t.Fatal(err)
	}
	want := []notification.ChatTarget{{ID: "C1", Name: "General"}, {ID: "C2", Name: "ops"}, {ID: "G1", Name: "secret", Private: true}}
	if mustJSON(t, got) != mustJSON(t, want) {
		t.Errorf("targets = %s, want %s", mustJSON(t, got), mustJSON(t, want))
	}
	if n := len(calls()); n != 1 {
		t.Errorf("the first page was read %d times", n)
	}
}

// pagedUpstream answers a request carrying a cursor with the listing's last
// page, and every other request through the wrapped upstream.
type pagedUpstream struct {
	Upstream
	page *int
}

func (p pagedUpstream) Do(req *http.Request) (*http.Response, error) {
	if req.URL.Query().Get("cursor") == "p2" {
		*p.page++
		body := `{"ok":true,"channels":[{"id":"C1","name":"General"},{"id":"G1","name":"secret","is_private":true}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	}
	return p.Upstream.Do(req) //nolint:wrapcheck // test transport
}

func TestTargets_SlackWithoutGroupsReadListsThePublicChannels(t *testing.T) {
	up, calls := routes(t, map[string]string{
		"/users.conversations?types=public_channel,private_channel": `{"ok":false,"error":"missing_scope","needed":"groups:read"}`,
		"/users.conversations?types=public_channel":                 `{"ok":true,"channels":[{"id":"C1","name":"general"}]}`,
	})
	got, err := NewSenders(up, "").Targets(context.Background(),
		notification.Channel{Name: "slack", Kind: notification.ChannelKindSlack, Connection: "slack"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "C1" {
		t.Errorf("targets = %v", got)
	}
	if n := len(calls()); n != 2 {
		t.Errorf("made %d requests, want the refused one and the public-only one", n)
	}
}

func TestTargets_SlackRefusalIsReported(t *testing.T) {
	up, _ := routes(t, map[string]string{
		"/users.conversations?types=public_channel,private_channel": `{"ok":false,"error":"invalid_auth"}`,
	})
	_, err := NewSenders(up, "").Targets(context.Background(),
		notification.Channel{Name: "slack", Kind: notification.ChannelKindSlack, Connection: "slack"})
	if err == nil || !strings.Contains(err.Error(), "invalid_auth") {
		t.Errorf("err = %v, want the Slack error code", err)
	}
}

func TestTargets_MattermostListsOpenAndPrivateChannelsOfEveryTeam(t *testing.T) {
	up, _ := routes(t, map[string]string{
		"/api/v4/users/me/teams": `[{"id":"t1","display_name":"Acme"},{"id":"t2","display_name":"Ops"}]`,
		"/api/v4/users/me/teams/t1/channels": `[{"id":"m1","name":"town-square","display_name":"Town Square","type":"O"},` +
			`{"id":"m2","name":"dm","type":"D"},{"id":"m3","name":"gone","display_name":"Gone","type":"O","delete_at":5}]`,
		"/api/v4/users/me/teams/t2/channels": `[{"id":"m4","name":"oncall","display_name":"","type":"P"}]`,
	})
	got, err := NewSenders(up, "").Targets(context.Background(),
		notification.Channel{Name: "mm", Kind: notification.ChannelKindMattermost, Connection: "mm"})
	if err != nil {
		t.Fatal(err)
	}
	want := []notification.ChatTarget{{ID: "m1", Name: "Acme / Town Square"}, {ID: "m4", Name: "Ops / oncall", Private: true}}
	if mustJSON(t, got) != mustJSON(t, want) {
		t.Errorf("targets = %s, want %s", mustJSON(t, got), mustJSON(t, want))
	}
}

func TestTargets_MattermostOnOneTeamNamesNoTeam(t *testing.T) {
	up, _ := routes(t, map[string]string{
		"/api/v4/users/me/teams":             `[{"id":"t1","display_name":"Acme"}]`,
		"/api/v4/users/me/teams/t1/channels": `[{"id":"m1","name":"town-square","display_name":"Town Square","type":"O"}]`,
	})
	got, err := NewSenders(up, "").Targets(context.Background(),
		notification.Channel{Name: "mm", Kind: notification.ChannelKindMattermost, Connection: "mm"})
	if err != nil || len(got) != 1 || got[0].Name != "Town Square" {
		t.Errorf("targets = %v, err %v", got, err)
	}
}

func TestTargets_FailuresAreReported(t *testing.T) {
	up, _ := routes(t, map[string]string{"/api/v4/users/me/teams": `not json`})
	mm := notification.Channel{Name: "mm", Kind: notification.ChannelKindMattermost, Connection: "mm"}
	if _, err := NewSenders(up, "").Targets(context.Background(), mm); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("an unreadable answer: err = %v", err)
	}
	empty, _ := routes(t, map[string]string{})
	if _, err := NewSenders(empty, "").Targets(context.Background(), mm); err == nil {
		t.Error("a 404 listing was accepted")
	}
	webhook := notification.Channel{Name: "hook", Kind: notification.ChannelKindWebhook, Connection: "hook"}
	if _, err := NewSenders(up, "").Targets(context.Background(), webhook); err == nil {
		t.Error("a webhook channel was listed")
	}
	var nilSenders *Senders
	if _, err := nilSenders.Targets(context.Background(), mm); err == nil {
		t.Error("a nil dispatcher listed channels")
	}
	if _, err := NewSenders(nil, "").Targets(context.Background(), mm); err == nil {
		t.Error("a dispatcher with no upstream listed channels")
	}
}

func TestSenders_ADocumentTargetRedirectsAChatPostOnly(t *testing.T) {
	up, got := upstreamFor(t, http.StatusCreated, `{"id":"p1"}`)
	s := NewSenders(up, "")
	ch := notification.Channel{Name: "mm", Kind: notification.ChannelKindMattermost, Connection: "mm", Target: "fixed"}
	var d notification.Delivery
	d.Title, d.Target = "t", "chosen"
	if err := s.Send(context.Background(), ch, d); err != nil {
		t.Fatal(err)
	}
	if got.body["channel_id"] != "chosen" {
		t.Errorf("posted to %v, want the document's target", got.body["channel_id"])
	}
	d.Target = ""
	if err := s.Send(context.Background(), ch, d); err != nil {
		t.Fatal(err)
	}
	if got.body["channel_id"] != "fixed" {
		t.Errorf("posted to %v, want the channel's own target", got.body["channel_id"])
	}
}

func TestSlack_APreviewIsSharedIntoTheChannelAndOtherFilesIntoTheThread(t *testing.T) {
	up, calls := slackDouble(t, map[string]string{
		"/api/files.getUploadURLExternal": `{"ok":true,"upload_url":"SERVER/upload/f1","file_id":"F1"}`,
		"/api/chat.postMessage":           `{"ok":true,"ts":"1700.1"}`,
	})
	d := reportDelivery()
	d.Files = []notification.File{
		{Name: "preview.png", ContentType: "image/png", Data: []byte("\x89PNG"), Preview: true},
		{Name: "raw.csv", ContentType: "text/csv", Data: []byte("a,b")},
	}
	if err := NewSlackSender(up).Send(context.Background(), slackChannel(), d); err != nil {
		t.Fatal(err)
	}
	var shares []map[string]any
	for _, c := range calls() {
		if c.path != "/api/files.completeUploadExternal" {
			continue
		}
		var share map[string]any
		if err := json.Unmarshal(c.body, &share); err != nil {
			t.Fatal(err)
		}
		shares = append(shares, share)
	}
	if len(shares) != 2 {
		t.Fatalf("made %d shares, want the preview's and the thread's", len(shares))
	}
	if _, threaded := shares[0]["thread_ts"]; threaded || shares[0]["channel_id"] != "C123" {
		t.Errorf("the preview was shared as %v, want into the channel", shares[0])
	}
	if shares[1]["thread_ts"] != "1700.1" {
		t.Errorf("the other file was shared as %v, want into the thread", shares[1])
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
