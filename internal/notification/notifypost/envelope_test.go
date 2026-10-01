package notifypost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// idUpstream is a testUpstream that names a delivery id header, as an hmac
// connection with hmac_id_header does.
type idUpstream struct {
	testUpstream
	header string
}

func (u idUpstream) DeliveryIDHeader() string { return u.header }

// received is one request a receiver got.
type received struct {
	header http.Header
	body   []byte
}

func receiver(t *testing.T, status int, header http.Header, response string) (string, *[]received) {
	t.Helper()
	var got []received
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = append(got, received{header: r.Header.Clone(), body: raw})
		maps.Copy(w.Header(), header)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &got
}

func jsonChannel() notification.Channel {
	return notification.Channel{
		Name: "ops-events", Kind: notification.ChannelKindWebhook, Connection: "hook",
		Enabled: true, Format: notification.ChannelFormatJSON,
	}
}

func finding() notification.Delivery {
	return notification.Delivery{
		ID:         "ntf_42",
		OccurredAt: time.Date(2026, 9, 30, 14, 2, 11, 0, time.FixedZone("x", 3600)),
		Document: notification.Document{
			Title: "Restock delayed", Body: "**12** stores", Link: "https://portal.example.com/runs/r1",
			Type: notification.DocumentScriptFinding,
			Source: &notification.DocumentSource{
				Kind: notification.DocumentSourceScript, Script: "mcp:script:s1", RunID: "dpx_1",
			},
			Data: json.RawMessage(`{"stores":[3,7],"threshold":0.2}`),
		},
	}
}

// TestJSONEnvelope is what a format json webhook channel posts: one envelope
// carrying the row's id, the type, the source, the body whole and the data
// verbatim, with the id in the connection's id header.
func TestJSONEnvelope(t *testing.T) {
	base, got := receiver(t, http.StatusAccepted, nil, `{"accepted":1}`)
	up := func(string) (Upstream, error) {
		return idUpstream{testUpstream: testUpstream{base: base}, header: "webhook-id"}, nil
	}
	d := finding()
	d.Body = strings.Repeat("b", WebhookMaxTextBytes+10)
	require.NoError(t, NewWebhookSender(up, "ACME Data Platform").Send(context.Background(), jsonChannel(), d))
	require.Len(t, *got, 1)
	r := (*got)[0]
	assert.Equal(t, "ntf_42", r.header.Get("webhook-id"), "the id rides in the header the signature covers")

	var env map[string]any
	require.NoError(t, json.Unmarshal(r.body, &env))
	assert.Equal(t, "ntf_42", env["id"])
	assert.Equal(t, "script.finding", env["type"])
	assert.Equal(t, "2026-09-30T13:02:11Z", env["occurred_at"], "occurred_at is UTC")
	assert.Equal(t, "ACME Data Platform", env["deployment"])
	assert.Equal(t, "ops-events", env["channel"])
	assert.Equal(t, "Restock delayed", env["title"])
	assert.Len(t, env["body"], WebhookMaxTextBytes+10, "a system receives the body whole")
	assert.Equal(t, map[string]any{"kind": "script", "script": "mcp:script:s1", "run_id": "dpx_1"}, env["source"])
	assert.Contains(t, string(r.body), `"data":{"stores":[3,7],"threshold":0.2}`, "data is delivered verbatim")
}

func TestJSONEnvelopeWithoutAnIDHeaderOrType(t *testing.T) {
	base, got := receiver(t, http.StatusOK, nil, ``)
	up := func(string) (Upstream, error) { return testUpstream{base: base}, nil }
	d := notification.Delivery{ID: "ntf_7", Document: notification.Document{Title: "t"}}
	require.NoError(t, NewWebhookSender(up, "").Send(context.Background(), jsonChannel(), d))
	var env map[string]any
	require.NoError(t, json.Unmarshal((*got)[0].body, &env))
	assert.Equal(t, "notify.sent", env["type"], "a document queued before types reads as a send")
	assert.NotContains(t, env, "data")
	assert.NotContains(t, env, "deployment")
	assert.Empty(t, (*got)[0].header.Get("webhook-id"))
}

// TestTextChannelIgnoresData holds that a channel with no format delivers as
// it did before formats existed, byte for byte, whatever the document carries.
func TestTextChannelIgnoresData(t *testing.T) {
	base, got := receiver(t, http.StatusOK, nil, `ok`)
	up := func(string) (Upstream, error) { return testUpstream{base: base}, nil }
	ch := jsonChannel()
	ch.Format = ""
	d := finding()
	require.NoError(t, NewWebhookSender(up, "x").Send(context.Background(), ch, d))
	want, _ := json.Marshal(webhookRequest{Text: plainText(d.Document, WebhookMaxTextBytes)})
	assert.Equal(t, string(want), string((*got)[0].body))
}

func TestRetryAfterAndGone(t *testing.T) {
	cases := []struct {
		status   int
		header   http.Header
		after    time.Duration
		terminal bool
	}{
		{http.StatusTooManyRequests, http.Header{"Retry-After": {"120"}}, 2 * time.Minute, false},
		{http.StatusServiceUnavailable, http.Header{"Retry-After": {"7"}}, 7 * time.Second, false},
		{http.StatusServiceUnavailable, nil, 0, false},
		{http.StatusBadGateway, http.Header{"Retry-After": {"7"}}, 0, false},
		{http.StatusGone, nil, 0, true},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			base, _ := receiver(t, tc.status, tc.header, `stop`)
			up := func(string) (Upstream, error) { return testUpstream{base: base}, nil }
			err := NewWebhookSender(up, "").Send(context.Background(), jsonChannel(), finding())
			require.Error(t, err)
			assert.Equal(t, tc.terminal, errors.Is(err, ErrTerminal))
			var ra *RetryAfterError
			if tc.after == 0 {
				assert.False(t, errors.As(err, &ra))
				return
			}
			require.ErrorAs(t, err, &ra)
			assert.Equal(t, tc.after, ra.After)
			assert.Contains(t, ra.Error(), "HTTP")
		})
	}
	base, _ := receiver(t, http.StatusGone, nil, ``)
	up := func(string) (Upstream, error) { return testUpstream{base: base}, nil }
	err := NewWebhookSender(up, "").Send(context.Background(), jsonChannel(), finding())
	assert.Contains(t, err.Error(), "asking for no more deliveries")
}
