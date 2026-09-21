package notifypost

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// WebhookMaxTextBytes is the most a webhook channel sends in one message. An
// incoming webhook is consumed by whichever server the URL belongs to, most
// often Slack or Mattermost, so the cap is the lower of the two.
const WebhookMaxTextBytes = 12000

// WebhookSender posts one {"text": ...} body to an incoming-webhook URL, the
// shape Slack and Mattermost both accept and the shape most other receivers
// of a "post this line somewhere" webhook understand.
//
// The whole address is the connection's base_url, including the secret path
// segment an incoming webhook URL carries, so the kind sends to the base
// itself and names no path. That secret is the connection's to hold: a
// webhook URL is a bearer credential written as a URL, which is why the kind
// names a connection like the two token kinds rather than storing a URL of
// its own.
type WebhookSender struct {
	upstream UpstreamFunc
}

// NewWebhookSender builds the webhook transport over upstream.
func NewWebhookSender(upstream UpstreamFunc) *WebhookSender {
	return &WebhookSender{upstream: upstream}
}

// webhookRequest is the incoming-webhook body.
type webhookRequest struct {
	Text string `json:"text"`
}

// Send posts the document to the webhook.
func (s *WebhookSender) Send(ctx context.Context, ch notification.Channel, doc notification.Document) error {
	u, err := resolve(s.upstream, ch)
	if err != nil {
		return err
	}
	return postJSON(ctx, u, "", webhookRequest{Text: plainText(doc, WebhookMaxTextBytes)})
}

// Verify interface compliance.
var _ Sender = (*WebhookSender)(nil)
