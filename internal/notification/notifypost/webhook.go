package notifypost

import (
	"context"
	"encoding/json"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// WebhookMaxTextBytes is the most a webhook channel sends in one message. An
// incoming webhook is consumed by whichever server the URL belongs to, most
// often Slack or Mattermost, so the cap is the lower of the two.
const WebhookMaxTextBytes = 12000

// WebhookSender posts to a webhook in the channel's format: one
// {"text": ...} body for a chat client's incoming webhook (the default), or
// one JSON envelope for a system receiving events (#1997).
//
// The whole address is the connection's base_url, including the secret path
// segment an incoming webhook URL carries, so the kind sends to the base
// itself and names no path. That secret is the connection's to hold: a
// webhook URL is a bearer credential written as a URL, which is why the kind
// names a connection like the two token kinds rather than storing a URL of
// its own. A connection can hold the segment as path_secret, encrypted,
// rather than in its base_url.
//
// A JSON delivery is signed when the connection's auth_mode is hmac: the
// channel holds no secret and no signing settings of its own.
type WebhookSender struct {
	upstream UpstreamFunc
	// deployment names this deployment in an envelope.
	deployment string
}

// NewWebhookSender builds the webhook transport over upstream.
func NewWebhookSender(upstream UpstreamFunc, deployment string) *WebhookSender {
	return &WebhookSender{upstream: upstream, deployment: deployment}
}

// webhookRequest is the incoming-webhook body.
type webhookRequest struct {
	Text string `json:"text"`
}

// Envelope is the body a JSON webhook channel posts for one notification.
type Envelope struct {
	// ID is the queue row's id, the same on every retry of that row, so a
	// receiver deduplicates on it.
	ID string `json:"id"`
	// Type is what produced the notification, one of the
	// notification.Document* types; a receiver routes on it.
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
	Deployment string    `json:"deployment,omitempty"`
	Channel    string    `json:"channel"`
	Title      string    `json:"title"`
	// Body is the markdown body as sent, whole: a system reads fields, and a
	// chat client's display cap does not apply to it.
	Body   string                       `json:"body,omitempty"`
	Link   string                       `json:"link,omitempty"`
	Source *notification.DocumentSource `json:"source,omitempty"`
	// Data is the sender's structured payload, verbatim.
	Data json.RawMessage `json:"data,omitempty"`
}

// Send posts the delivery to the webhook.
func (s *WebhookSender) Send(ctx context.Context, ch notification.Channel, d notification.Delivery) error {
	u, err := resolve(s.upstream, ch)
	if err != nil {
		return err
	}
	if ch.PayloadFormat() != notification.ChannelFormatJSON {
		return postJSON(ctx, u, "", webhookRequest{Text: plainText(d.Document, WebhookMaxTextBytes)})
	}
	var headers map[string]string
	if carrier, ok := u.(DeliveryIDCarrier); ok && carrier.DeliveryIDHeader() != "" {
		headers = map[string]string{carrier.DeliveryIDHeader(): d.ID}
	}
	return postJSONWith(ctx, u, "", s.envelope(ch, d), headers)
}

// envelope builds the JSON body for one delivery.
func (s *WebhookSender) envelope(ch notification.Channel, d notification.Delivery) Envelope {
	typ := d.Type
	if typ == "" {
		typ = notification.DocumentNotifySent
	}
	return Envelope{
		ID:         d.ID,
		Type:       typ,
		OccurredAt: d.OccurredAt.UTC(),
		Deployment: s.deployment,
		Channel:    ch.Name,
		Title:      d.Title,
		Body:       d.Body,
		Link:       d.Link,
		Source:     d.Source,
		Data:       d.Data,
	}
}

// Verify interface compliance.
var _ Sender = (*WebhookSender)(nil)
