package notifypost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// Slack's bounds. The markdown block's cap is cumulative across a payload's
// markdown blocks; the header's is its plain text.
const (
	// SlackMaxMarkdownBytes is the most a slack channel sends in its
	// markdown block. The platform cuts below it and keeps the link.
	SlackMaxMarkdownBytes = 12000
	// slackMaxHeaderChars bounds the header block's plain text.
	slackMaxHeaderChars = 150
)

// The Web API methods a post is made through, appended to the connection's
// base_url (https://slack.com/api).
const (
	slackPostMessage     = "/chat.postMessage"
	slackGetUploadURL    = "/files.getUploadURLExternal"
	slackCompleteUploads = "/files.completeUploadExternal"
)

// SlackSender posts to a Slack channel through the Web API with the bot token
// the connection holds as its bearer credential.
//
// Slack answers an application-level refusal with HTTP 200 and ok: false, so
// the shared status classification is not the whole verdict: every answer's
// envelope is read, and its error code decides whether a retry could help.
type SlackSender struct {
	upstream UpstreamFunc
}

// NewSlackSender builds the slack transport over upstream.
func NewSlackSender(upstream UpstreamFunc) *SlackSender {
	return &SlackSender{upstream: upstream}
}

// Send uploads the delivery's files, posts the document, then shares the files
// into the post's thread.
//
// The upload's first two steps come before the post: a file Slack has
// received but nobody has shared is seen by nobody, so a failure there costs a
// retry nothing. The share comes after, so the files sit with the message they
// belong to. A share that fails after the post is terminal and says the
// message was posted: retrying would post the message a second time.
func (s *SlackSender) Send(ctx context.Context, ch notification.Channel, d notification.Delivery) error {
	u, err := resolve(s.upstream, ch)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(d.Files))
	for _, f := range d.Files {
		id, err := uploadSlackFile(ctx, u, f)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	posted, err := slackCall(ctx, u, slackPostMessage, slackMessage(ch.Target, d))
	if err != nil {
		return err
	}
	ts, _ := posted["ts"].(string)
	inChannel, inThread := splitPreview(ids, d.Files)
	// The preview is shared into the channel itself, the message's next
	// line, so everyone in the channel sees what the asset looks like
	// without opening a thread (#2241). Every other file goes to the thread.
	for _, share := range []slackFiles{inChannel, inThread.withThread(ts)} {
		if len(share.ids) == 0 {
			continue
		}
		if _, err := slackCall(ctx, u, slackCompleteUploads, slackShare(share.ids, share.files, ch.Target, share.thread)); err != nil {
			return fmt.Errorf("the message was posted, but its files could not be attached to it (%s): %w", err.Error(), ErrTerminal)
		}
	}
	return nil
}

// slackFiles is one completeUploadExternal share: the uploaded ids, the files
// they were uploaded from, and the thread they go to (empty: the channel).
type slackFiles struct {
	ids    []string
	files  []notification.File
	thread string
}

// withThread is the share addressed to a post's thread.
func (s slackFiles) withThread(ts string) slackFiles {
	s.thread = ts
	return s
}

// splitPreview separates the preview images, shared into the channel, from
// the files that go to the post's thread.
func splitPreview(ids []string, files []notification.File) (inChannel, inThread slackFiles) {
	for i, id := range ids {
		if files[i].Preview {
			inChannel.ids, inChannel.files = append(inChannel.ids, id), append(inChannel.files, files[i])
			continue
		}
		inThread.ids, inThread.files = append(inThread.ids, id), append(inThread.files, files[i])
	}
	return inChannel, inThread
}

// slackMessage builds the chat.postMessage body: a header block with the
// title, a markdown block with the body and the attachments sent as links, and
// a context block with the link. text is the notification's own fallback.
func slackMessage(channelID string, d notification.Delivery) map[string]any {
	blocks := []map[string]any{{
		"type": "header",
		"text": map[string]any{"type": "plain_text", "text": cutRunes(oneLine(d.Title), slackMaxHeaderChars)},
	}}
	if text := slackMarkdown(d); text != "" {
		blocks = append(blocks, map[string]any{"type": "markdown", "text": text})
	}
	if d.Link != "" {
		blocks = append(blocks, map[string]any{
			"type":     "context",
			"elements": []map[string]any{{"type": "mrkdwn", "text": "<" + d.Link + "|Open>"}},
		})
	}
	return map[string]any{"channel": channelID, "text": oneLine(d.Title), "blocks": blocks}
}

// slackMarkdown is the markdown block's text: the suppression line, the body,
// and the attachments sent as links, cut to the block's cap with the link
// lines kept.
func slackMarkdown(d notification.Delivery) string {
	var b strings.Builder
	if note := d.SuppressionNote(); note != "" {
		_, _ = b.WriteString("_" + note + "_")
	}
	if d.Body != "" {
		if b.Len() > 0 {
			_, _ = b.WriteString("\n\n")
		}
		_, _ = b.WriteString(d.Body)
	}
	tail := linkLines(d)
	if len(tail) == 0 {
		return truncate(b.String(), SlackMaxMarkdownBytes)
	}
	end := strings.Join(tail, "\n")
	if b.Len() == 0 {
		return end
	}
	end = "\n\n" + end
	return truncate(b.String(), SlackMaxMarkdownBytes-len(end)) + end
}

// slackShare builds the completeUploadExternal body sharing the files into
// the channel, or into the post's thread when threadTS names one.
func slackShare(ids []string, files []notification.File, channelID, threadTS string) map[string]any {
	list := make([]map[string]any, 0, len(ids))
	for i, id := range ids {
		list = append(list, map[string]any{"id": id, "title": fileName(files[i])})
	}
	body := map[string]any{"files": list, "channel_id": channelID}
	if threadTS != "" {
		body["thread_ts"] = threadTS
	}
	return body
}

// uploadSlackFile runs the upload's first two steps: ask for an upload URL,
// then send the bytes to it. It returns the file's id.
func uploadSlackFile(ctx context.Context, u Upstream, f notification.File) (string, error) {
	form := url.Values{}
	form.Set("filename", fileName(f))
	form.Set("length", strconv.Itoa(len(f.Data)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(u.BaseURL(), slackGetUploadURL),
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building the upload request failed (%s): %w", err.Error(), ErrTerminal)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	answer, err := slackRead(u, req)
	if err != nil {
		return "", fmt.Errorf("uploading %s: %w", fileName(f), err)
	}
	uploadURL, _ := answer["upload_url"].(string)
	id, _ := answer["file_id"].(string)
	if id == "" || !sameDomain(u.BaseURL(), uploadURL) {
		return "", fmt.Errorf("uploading %s: Slack answered with no upload address on the connection's own domain: %w",
			fileName(f), ErrTerminal)
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(f.Data))
	if err != nil {
		return "", fmt.Errorf("building the upload failed (%s): %w", err.Error(), ErrTerminal)
	}
	put.Header.Set("Content-Type", "application/octet-stream")
	if _, err := doRead(u, put); err != nil {
		return "", fmt.Errorf("uploading %s: %w", fileName(f), err)
	}
	return id, nil
}

// sameDomain reports whether target is an https URL on base's host or a
// subdomain of base's parent domain: files.slack.com for https://slack.com/api.
// The connection's credential travels with every request it makes, so the
// upload address an answer names is held to the domain the operator chose.
func sameDomain(base, target string) bool {
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	t, err := url.Parse(target)
	if err != nil || t.Scheme != "https" {
		return false
	}
	bh, th := strings.ToLower(b.Hostname()), strings.ToLower(t.Hostname())
	if bh == "" || th == "" {
		return false
	}
	if net.ParseIP(bh) != nil {
		return th == bh
	}
	domain := bh
	if parts := strings.Split(bh, "."); len(parts) > 2 {
		domain = strings.Join(parts[len(parts)-2:], ".")
	}
	return th == domain || strings.HasSuffix(th, "."+domain)
}

// slackCall posts body as JSON to a Web API method and returns the envelope.
func slackCall(ctx context.Context, u Upstream, method string, body any) (map[string]any, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding the message failed (%s): %w", err.Error(), ErrTerminal)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(u.BaseURL(), method), bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("building the request failed (%s): %w", err.Error(), ErrTerminal)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	return slackRead(u, req)
}

// slackRead sends a request and reads Slack's envelope: the HTTP status by
// the shared classification, then ok and error.
func slackRead(u Upstream, req *http.Request) (map[string]any, error) {
	payload, err := doRead(u, req)
	if err != nil {
		return nil, err
	}
	var env map[string]any
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("the answer is not a Slack Web API envelope (%s); the connection's base_url should be https://slack.com/api: %w",
			excerpt(payload), ErrTerminal)
	}
	ok, isBool := env["ok"].(bool)
	if !isBool {
		return nil, fmt.Errorf("the answer is not a Slack Web API envelope (%s); the connection's base_url should be https://slack.com/api: %w",
			excerpt(payload), ErrTerminal)
	}
	if ok {
		return env, nil
	}
	code, _ := env["error"].(string)
	return nil, slackRefusal(code)
}

// slackRetryable are the error codes that describe a moment rather than the
// message: a retry could succeed.
var slackRetryable = map[string]bool{
	"ratelimited": true, "rate_limited": true, "service_unavailable": true,
	"internal_error": true, "fatal_error": true, "request_timeout": true,
}

// slackHints are the next step an operator takes for the refusals they can
// fix.
var slackHints = map[string]string{ // #nosec G101 -- operator guidance text naming where a token goes; it holds none
	"not_in_channel":    "the bot is not in that channel; invite it with /invite in Slack",
	"channel_not_found": "no channel with that id is visible to the bot; check the channel's target id",
	"missing_scope":     "the bot lacks a scope it needs (chat:write to post, files:write for attachments); add it and reinstall the app",
	"invalid_auth":      "the connection's credential is not a valid Slack token; update the connection's bot token",
	"token_revoked":     "the bot token was revoked; reinstall the app and update the connection's credential",
	"account_inactive":  "the bot's account is inactive; reinstall the app and update the connection's credential",
	"not_authed":        "no token was sent; set the connection's auth_mode to bearer with the bot token as its credential",
}

// slackRefusal turns an ok: false error code into the error a send returns.
func slackRefusal(code string) error {
	if code == "" {
		code = "unknown_error"
	}
	if slackRetryable[code] {
		return fmt.Errorf("the Slack Web API answered %s", code)
	}
	if hint, ok := slackHints[code]; ok {
		return fmt.Errorf("the Slack Web API refused the message (%s): %s: %w", code, hint, ErrTerminal)
	}
	return fmt.Errorf("the Slack Web API refused the message (%s): %w", code, ErrTerminal)
}

// cutRunes cuts s to at most n characters, on a rune boundary.
func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Verify interface compliance.
var _ Sender = (*SlackSender)(nil)
