package notifypost

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// MattermostMaxTextBytes is the most a mattermost channel sends in one
// message. A Mattermost post is capped at 16,383 characters by the server; the
// platform cuts below that and keeps the link, so the server never refuses a
// post for its length.
const MattermostMaxTextBytes = 16000

// mattermostPostPath is the REST route a post is created through, appended to
// the connection's base_url (the Mattermost site URL).
const mattermostPostPath = "/api/v4/posts"

// MattermostSender posts to a Mattermost channel through the v4 REST API.
//
// The bot token is the connection's credential, applied by the connection's
// authenticator as a bearer token, so this sender never sees it.
type MattermostSender struct {
	upstream UpstreamFunc
}

// NewMattermostSender builds the mattermost transport over upstream.
func NewMattermostSender(upstream UpstreamFunc) *MattermostSender {
	return &MattermostSender{upstream: upstream}
}

// mattermostPostRequest is the create-post body.
type mattermostPostRequest struct {
	ChannelID string `json:"channel_id"`
	Message   string `json:"message"`
}

// Send posts the document to the channel's target.
//
// Mattermost answers an application-level refusal with the matching HTTP
// status rather than with a 200 and a flag, so the shared status
// classification is the whole verdict and there is no envelope to read.
func (s *MattermostSender) Send(ctx context.Context, ch notification.Channel, doc notification.Document) error {
	u, err := resolve(s.upstream, ch)
	if err != nil {
		return err
	}
	return postJSON(ctx, u, mattermostPostPath, mattermostPostRequest{
		ChannelID: ch.Target,
		Message:   plainText(doc, MattermostMaxTextBytes),
	})
}

// Verify interface compliance.
var _ Sender = (*MattermostSender)(nil)
