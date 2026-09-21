// Package notifypost is the transport half of notification channels: one
// Sender per chat or webhook kind, turning a document into a post on its
// upstream.
//
// It is separate from notifychannel, which persists the operator's channel
// records, because the two share nothing: a record is read and written by the
// admin surface, a post is made by the send worker, and neither needs the
// other's machinery. What they share is the Channel value, which lives in the
// domain both depend on.
//
// No sender holds a credential. The three kinds deliver through an Upstream
// the api gateway materializes for the connection the channel names, so a bot
// token lives exactly where every other upstream credential lives.
package notifypost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// ErrTerminal marks a send failure that retrying cannot fix: the upstream
// refused the message itself rather than failing to receive it. A channel
// posting to a chat channel that was deleted, or through a bot that was
// uninstalled, fails this way on every attempt, so burning five of them
// delays nothing and fills the history with the same line five times.
//
// The worker matches it with errors.Is and fails the row at once. Everything
// else -- a timeout, a refused connection, a 5xx, a rate limit -- retries on
// the existing backoff.
var ErrTerminal = errors.New("notifychannel: upstream refused the message")

// maxResponseBytes bounds what a sender reads of an upstream's answer. The
// answer is a small JSON status in every kind; the cap is what keeps a
// misconfigured base_url pointing at a large document from being read into
// the worker.
const maxResponseBytes = 64 * 1024

// Upstream is the authorized transport a channel delivers through: the
// connection's base URL and its ability to make an authorized call.
//
// It is an interface here, satisfied by *apigateway.Upstream, so this package
// depends on the idea of an authorized upstream rather than on the api
// gateway. That keeps the senders testable against an httptest server and
// keeps the credential on the gateway's side of the seam.
type Upstream interface {
	// BaseURL is the connection's upstream root.
	BaseURL() string
	// Do applies the connection's authentication and static headers and
	// sends the request.
	Do(req *http.Request) (*http.Response, error)
}

// UpstreamFunc resolves a connection name to its authorized transport. The
// platform wires it to the api gateway; a test wires it to an httptest
// server.
type UpstreamFunc func(connection string) (Upstream, error)

// Sender delivers one document to one channel of its kind.
type Sender interface {
	// Send posts doc to ch, returning ErrTerminal for a refusal that
	// retrying cannot fix.
	Send(ctx context.Context, ch notification.Channel, doc notification.Document) error
}

// Senders dispatches a document to the sender for its channel's kind.
//
// The email kind is absent: an email channel fans out at enqueue to one
// ordinary queue row per recipient, which the existing renderer and SMTP
// sender deliver. There is nothing for a channel transport to do with it, and
// a second mail path would be a second place for the deployment's mail
// settings to be read.
type Senders struct {
	byKind map[string]Sender
}

// NewSenders builds the dispatcher for the HTTP kinds over upstream.
func NewSenders(upstream UpstreamFunc) *Senders {
	return &Senders{byKind: map[string]Sender{
		notification.ChannelKindMattermost: &MattermostSender{upstream: upstream},
		notification.ChannelKindWebhook:    &WebhookSender{upstream: upstream},
	}}
}

// For returns the sender for a kind, and whether this dispatcher delivers it.
func (s *Senders) For(kind string) (Sender, bool) {
	if s == nil {
		return nil, false
	}
	snd, ok := s.byKind[kind]
	return snd, ok
}

// Send delivers doc through the sender for ch's kind.
func (s *Senders) Send(ctx context.Context, ch notification.Channel, doc notification.Document) error {
	snd, ok := s.For(ch.Kind)
	if !ok {
		return fmt.Errorf("channel kind %q has no transport: %w", ch.Kind, ErrTerminal)
	}
	return snd.Send(ctx, ch, doc) //nolint:wrapcheck // the kind's own message is what the operator reads
}

// resolve obtains the channel's transport. A channel naming a connection this
// process does not serve is terminal rather than retryable: the connection was
// deleted or renamed, and no number of attempts brings it back.
func resolve(up UpstreamFunc, ch notification.Channel) (Upstream, error) {
	if up == nil {
		return nil, fmt.Errorf("no api gateway is wired, so channel %q cannot deliver: %w", ch.Name, ErrTerminal)
	}
	u, err := up(ch.Connection)
	if err != nil {
		return nil, fmt.Errorf("channel %q delivers through connection %q, which is unavailable (%s): %w",
			ch.Name, ch.Connection, err.Error(), ErrTerminal)
	}
	return u, nil
}

// postJSON sends body as JSON to path under the upstream's base URL and
// reports whether the upstream took it.
//
// The HTTP status is classified here, once, for every kind: a 5xx or a 429 is
// the upstream failing to take the message and retries; any other 4xx is the
// upstream refusing it and is terminal. The answer's body is read only to
// quote it in that refusal -- no kind here reads a field out of it, so none is
// returned.
func postJSON(ctx context.Context, u Upstream, path string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding the message failed (%s): %w", err.Error(), ErrTerminal)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(u.BaseURL(), path), bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("building the request failed (%s): %w", err.Error(), ErrTerminal)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	return do(u, req)
}

// do sends a prepared request and classifies what came back.
func do(u Upstream, req *http.Request) error {
	resp, err := u.Do(req)
	if err != nil {
		// A transport error is the upstream not being reached, which is
		// exactly what the retry budget is for.
		return fmt.Errorf("reaching the upstream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// The body is read before the status is judged so a refusal can quote
	// what the upstream said, which is usually the only thing that tells an
	// operator what to fix.
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err := classify(resp.StatusCode, payload); err != nil {
		return err
	}
	if readErr != nil {
		return fmt.Errorf("reading the upstream's answer: %w", readErr)
	}
	return nil
}

// The two status boundaries the classification turns on.
const (
	// statusOKFloor and statusOKCeil bound the 2xx class, which every kind
	// answers a delivery with.
	statusOKFloor = 200
	statusOKCeil  = 300
	// statusServerErrorFloor is where a status stops being the upstream
	// refusing the message and starts being the upstream failing to take it.
	statusServerErrorFloor = 500
)

// classify turns an HTTP status into the error a status of that class
// deserves, or nil for a success.
func classify(status int, payload []byte) error {
	switch {
	case status >= statusOKFloor && status < statusOKCeil:
		return nil
	case status == http.StatusTooManyRequests || status >= statusServerErrorFloor:
		return fmt.Errorf("upstream answered HTTP %d: %s", status, excerpt(payload))
	default:
		return fmt.Errorf("upstream answered HTTP %d: %s: %w", status, excerpt(payload), ErrTerminal)
	}
}

// excerptBytes bounds how much of an upstream's answer reaches an error
// message, which is stored on the queue row and shown in the admin history.
const excerptBytes = 400

// excerpt renders an upstream's answer for an error message: bounded, on one
// line, and never empty, so a reader is told what came back rather than being
// shown a blank.
func excerpt(payload []byte) string {
	text := strings.TrimSpace(string(payload))
	if text == "" {
		return "(no body)"
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > excerptBytes {
		text = text[:excerptBytes] + "..."
	}
	return text
}

// pathSeparator is the one separator a URL path is joined on.
const pathSeparator = "/"

// joinURL appends a path to a base URL without doubling or dropping the
// separator. The base is the operator's configured base_url, which they may
// or may not have ended with a slash.
//
// An empty path returns the base UNCHANGED, which is what the webhook kind
// needs: its whole address is the connection's base_url, including the secret
// path segment an incoming webhook carries, and appending a separator to
// https://hooks.slack.com/services/T00/B00/XXX asks for a different resource
// than the one the operator configured.
func joinURL(base, path string) string {
	if path == "" || path == pathSeparator {
		return base
	}
	return strings.TrimRight(base, pathSeparator) + pathSeparator + strings.TrimLeft(path, pathSeparator)
}

// plainText renders a document for a kind that shows no markup: the title,
// the body, and the link on its own last line. It is what the webhook kind
// sends, and what every kind falls back to before the richer rendering of
// stage two exists.
func plainText(doc notification.Document, limit int) string {
	// strings.Builder's writes cannot fail, which is why their errors are
	// discarded explicitly rather than checked.
	var b strings.Builder
	_, _ = b.WriteString(doc.Title)
	if doc.Body != "" {
		_, _ = b.WriteString("\n\n")
		_, _ = b.WriteString(doc.Body)
	}
	text := b.String()
	// The link is appended after the cut rather than before it, so the one
	// line that lets a reader reach the whole thing is the line that always
	// survives a document too long for the kind.
	if doc.Link != "" {
		room := limit - (len(doc.Link) + 1)
		text = truncate(text, room)
		text += "\n" + doc.Link
		return text
	}
	return truncate(text, limit)
}

// cutBoundaryFraction is the reciprocal of how much of a cut message may be
// given up to end it on a paragraph boundary: a fifth.
const cutBoundaryFraction = 5

// truncate cuts text to at most limit bytes, preferring the last paragraph
// boundary within the last fifth of what is kept so a cut message ends on a
// thought rather than mid-word.
func truncate(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	const ellipsis = "\n\n[...]"
	room := limit - len(ellipsis)
	if room <= 0 {
		return text[:limit]
	}
	cut := text[:room]
	// Walk back to a paragraph boundary, but only within the last
	// cutBoundaryFraction of what is kept: past that the message loses more
	// than the ragged ending was worth.
	if at := strings.LastIndex(cut, "\n\n"); at > room-room/cutBoundaryFraction {
		cut = cut[:at]
	}
	return cut + ellipsis
}
