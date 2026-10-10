package notifypost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// MattermostMaxTextBytes is the most a mattermost channel sends in one
// message. A Mattermost post is capped at 16,383 characters by the server; the
// platform cuts below that and keeps the link, so the server never refuses a
// post for its length.
const MattermostMaxTextBytes = 16000

// The REST routes a post is made through, appended to the connection's
// base_url (the Mattermost site URL).
const (
	mattermostPostPath = "/api/v4/posts"
	mattermostFilePath = "/api/v4/files"
)

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
	ChannelID string   `json:"channel_id"`
	Message   string   `json:"message"`
	FileIDs   []string `json:"file_ids,omitempty"`
}

// mattermostFileResponse is the part of the upload answer a post needs.
type mattermostFileResponse struct {
	FileInfos []struct {
		ID string `json:"id"`
	} `json:"file_infos"`
}

// Send uploads the delivery's files to the channel, then posts the document
// with them.
//
// Mattermost answers an application-level refusal with the matching HTTP
// status rather than with a 200 and a flag, so the shared status
// classification is the whole verdict and there is no envelope to read.
//
// An upload that fails fails the send, retried or terminal by the same
// classification as the post: a message that names files it does not carry
// would read as the report having been sent when it was not. A retry uploads
// again; an upload is not a post, so a file Mattermost holds from an attempt
// that went no further is shown to nobody.
func (s *MattermostSender) Send(ctx context.Context, ch notification.Channel, d notification.Delivery) error {
	u, err := resolve(s.upstream, ch)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(d.Files))
	for _, f := range d.Files {
		id, err := uploadMattermostFile(ctx, u, ch.Target, f)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	return postJSON(ctx, u, mattermostPostPath, mattermostPostRequest{
		ChannelID: ch.Target,
		Message:   markdownMessage(d, MattermostMaxTextBytes),
		FileIDs:   ids,
	})
}

// uploadMattermostFile uploads one file to the channel and returns its id.
func uploadMattermostFile(ctx context.Context, u Upstream, channelID string, f notification.File) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("channel_id", channelID); err != nil {
		return "", fmt.Errorf("building the upload failed (%s): %w", err.Error(), ErrTerminal)
	}
	part, err := form.CreateFormFile("files", fileName(f))
	if err != nil {
		return "", fmt.Errorf("building the upload failed (%s): %w", err.Error(), ErrTerminal)
	}
	_, _ = part.Write(f.Data) // a multipart part over a bytes.Buffer cannot fail
	_ = form.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(u.BaseURL(), mattermostFilePath), &body)
	if err != nil {
		return "", fmt.Errorf("building the upload failed (%s): %w", err.Error(), ErrTerminal)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	payload, err := doRead(u, req)
	if err != nil {
		return "", fmt.Errorf("uploading %s: %w", fileName(f), err)
	}
	var answer mattermostFileResponse
	if err := json.Unmarshal(payload, &answer); err != nil || len(answer.FileInfos) == 0 || answer.FileInfos[0].ID == "" {
		return "", fmt.Errorf("uploading %s: the answer named no file (%s); the connection's base_url may not be the Mattermost site: %w",
			fileName(f), excerpt(payload), ErrTerminal)
	}
	return answer.FileInfos[0].ID, nil
}

// fileName is the name a file is uploaded under.
func fileName(f notification.File) string {
	name := strings.TrimSpace(strings.NewReplacer("/", "_", "\\", "_").Replace(f.Name))
	if name == "" {
		return "attachment"
	}
	return name
}

// doRead sends a prepared request and returns the answer's body when the
// upstream took it, classifying a refusal as do does.
func doRead(u Upstream, req *http.Request) ([]byte, error) {
	resp, err := u.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reaching the upstream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err := classify(resp.StatusCode, payload); err != nil {
		return nil, withRetryAfter(err, resp, time.Now())
	}
	if readErr != nil {
		return nil, fmt.Errorf("reading the upstream's answer: %w", readErr)
	}
	return payload, nil
}

// markdownMessage renders a document for a kind that shows markdown: the
// title as a heading, the suppression line, the body, the attachments sent as
// links, and the link on its own last line. The cut is made before the link is
// added, so the one line that reaches the whole thing always survives.
func markdownMessage(d notification.Delivery, limit int) string {
	var b strings.Builder
	_, _ = b.WriteString("#### " + oneLine(d.Title))
	if note := d.SuppressionNote(); note != "" {
		_, _ = b.WriteString("\n\n_" + note + "_")
	}
	if d.Body != "" {
		_, _ = b.WriteString("\n\n" + d.Body)
	}
	tail := linkLines(d)
	if d.Link != "" {
		tail = append(tail, d.Link)
	}
	if len(tail) == 0 {
		return truncate(b.String(), limit)
	}
	end := "\n\n" + strings.Join(tail, "\n")
	return truncate(b.String(), limit-len(end)) + end
}

// linkLines names each attachment sent as a link, one line each.
func linkLines(d notification.Delivery) []string {
	lines := make([]string, 0, len(d.Linked))
	for _, a := range d.Linked {
		name := a.Name
		if name == "" {
			name = "attachment"
		}
		if a.Link == "" {
			lines = append(lines, "Attachment: "+name)
			continue
		}
		lines = append(lines, "Attachment: "+name+" "+a.Link)
	}
	return lines
}

// oneLine folds a title onto one line, since a heading ends at a newline.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Verify interface compliance.
var _ Sender = (*MattermostSender)(nil)
